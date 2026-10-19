#!/usr/bin/env python3
# This project was developed with assistance from AI tools.
"""Execute sanitized, dependency-ordered acceptance gates for the existing demo."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
from dataclasses import asdict, dataclass, field
from datetime import datetime, timezone
from pathlib import Path

from common import PilotError
from configure_routing import verified_ingress_selector
from demo_probe import CELL, RESET_FILES, parse_result
from render_config import DEFAULT_CONFIG_PATH, load_image_lock
from runtime_config import RuntimeTarget, load_runtime_target
from validate_rendered_manifests import locked_references, walk

ROOT = Path(__file__).resolve().parent
PHASES = {
    1: "workspace",
    2: "artifacts",
    3: "core",
    4: "solution",
    5: "runtime",
    6: "application",
}
CORE_SERVICES = {
    "content-addressable-storage": (9747,),
    "hot-shared-state": (9777,),
    "operations": (8080,),
    "resource-registry": (8080,),
    "skill-registry": (8080,),
    "runtime-db": (8080,),
    "proto-registry": (8080,),
    "solution-service": (8082,),
    "solution-deployment-v1": (8080,),
    "workcell-cluster-service": (9957,),
}
RUNTIME_WORKLOADS = (
    "rs-gazebo-simulator",
    "rs-icon",
    "rs-ur-module",
    "rs-hande-gripper",
    "rs-gripper",
    "rs-motion-planner-service",
    "rs-inference-service",
)


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def foundationpose_completed(log: str) -> bool:
    """Recognize the deployed model's positive completion record, never raw logs."""
    return any(
        re.search(r"foundationpose.*\bcompleted\b", line, re.IGNORECASE)
        and re.search(
            r"(?:outputs\s*[:=]\s*[0-9]+|\b[0-9]+\s+outputs\b)", line, re.IGNORECASE
        )
        and not re.search(r"\b(?:not|failed|error)\b", line, re.IGNORECASE)
        for line in log.splitlines()
    )


def ready(resource: dict) -> bool:
    return not resource.get("metadata", {}).get("deletionTimestamp") and any(
        value.get("type") == "Ready" and value.get("status") == "True"
        for value in resource.get("status", {}).get("conditions", [])
    )


def pod_ready(pod: dict) -> bool:
    containers = pod.get("spec", {}).get("containers", [])
    statuses = {
        value.get("name"): value
        for value in pod.get("status", {}).get("containerStatuses", [])
    }
    return (
        bool(containers)
        and pod.get("status", {}).get("phase") == "Running"
        and ready(pod)
        and all(
            statuses.get(value.get("name"), {}).get("ready") is True
            and bool(statuses[value["name"]].get("state", {}).get("running"))
            for value in containers
        )
    )


def condition_age(resource: dict | None) -> int | None:
    if not resource:
        return None
    dates = [
        c.get("lastTransitionTime")
        for c in resource.get("status", {}).get("conditions", [])
        if c.get("status") == "True"
    ]
    dates = [date for date in dates if date]
    if not dates:
        return None
    try:
        return max(
            0,
            int(
                (
                    datetime.now(timezone.utc)
                    - max(
                        datetime.fromisoformat(date.replace("Z", "+00:00"))
                        for date in dates
                    )
                ).total_seconds()
            ),
        )
    except (TypeError, ValueError):
        return None


@dataclass
class Gate:
    phase: int
    name: str
    status: str
    condition: str
    reason: str
    age_seconds: int | None = None
    observed_at: str = field(default_factory=utc_now)
    evidence: dict[str, bool | int] = field(default_factory=dict)


class Reader:
    """All cluster I/O is bounded, captured and scoped to the approved context."""

    def __init__(self, target: RuntimeTarget):
        self.target = target

    def oc(self, *args: str, check: bool = True, input_data: str | None = None):
        try:
            result = subprocess.run(
                [
                    "oc",
                    "--context",
                    self.target.document["local"]["context"],
                    "--request-timeout=15s",
                    *args,
                ],
                input=input_data,
                text=True,
                capture_output=True,
                timeout=25,
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise PilotError("cluster_query_unavailable") from exc
        if check and result.returncode:
            raise PilotError("cluster_query_failed")
        return result

    def get(
        self, resource: str, name: str | None = None, namespace: str | None = None
    ) -> dict:
        args = ["get", resource, "-n", namespace or self.target.namespace, "-o", "json"]
        if name:
            args.insert(2, name)
        result = self.oc(*args)
        try:
            value = json.loads(result.stdout)
            if not isinstance(value, dict):
                raise ValueError()
            return value
        except (TypeError, ValueError) as exc:
            raise PilotError("cluster_response_invalid") from exc

    def inventory(self) -> list[dict]:
        return self.get(
            "deployments,statefulsets,replicasets,pods,services,endpoints,chartassignments,resourcesets,persistentvolumeclaims"
        )["items"]

    def probe(self, action: str, payload: dict, *, wrapper: bool = False) -> dict:
        source = (ROOT / "demo_probe.py").read_text()
        request = {**payload, "action": action}
        if action in {"gateway", "solution_health", "idle", "scene"}:
            request["probe_source"] = source
        script = source + "\nrun_request(" + repr(request) + ")\n"
        pod = "rs-inference-service-0" if wrapper else "deployment/omts-build-workspace"
        container = "rs-inference-service" if wrapper else "workspace"
        python = (
            "/intrinsic_inference/assets/inference_service/inference_service_main.runfiles/rules_python++python+python_3_11_x86_64-unknown-linux-gnu/bin/python3"
            if wrapper
            else "python3"
        )
        limit = (
            payload.get("cycle_timeout", 180) + 15
            if action == "cycle"
            else (100 if action == "reset" else 55)
        )
        try:
            result = subprocess.run(
                [
                    "oc",
                    "--context",
                    self.target.document["local"]["context"],
                    "exec",
                    "-i",
                    pod,
                    "-n",
                    self.target.namespace,
                    "-c",
                    container,
                    "--",
                    python,
                    "-",
                ],
                input=script,
                text=True,
                capture_output=True,
                timeout=limit,
            )
            return (
                parse_result(result.stdout) if result.returncode == 0 else {"ok": False}
            )
        except subprocess.TimeoutExpired:
            return {"ok": False, "timed_out": True}


class Inventory:
    def __init__(self, items: list[dict]):
        self.items = items
        self.by_key = {(item["kind"], item["metadata"]["name"]): item for item in items}

    def get(self, kind: str, name: str) -> dict:
        return self.by_key.get((kind, name), {})

    def owner_current(self, pod: dict) -> bool:
        for owner in pod.get("metadata", {}).get("ownerReferences", []):
            if owner.get("controller") is not True:
                continue
            resource = self.get(owner.get("kind", ""), owner.get("name", ""))
            if not resource or resource["metadata"].get("uid") != owner.get("uid"):
                return False
            if owner["kind"] == "StatefulSet":
                return self.workload_ready("StatefulSet", owner["name"])
            if owner["kind"] == "ReplicaSet":
                return any(
                    parent.get("kind") == "Deployment"
                    and parent.get("controller") is True
                    and self.get("Deployment", parent.get("name", ""))
                    .get("metadata", {})
                    .get("uid")
                    == parent.get("uid")
                    and self.workload_ready("Deployment", parent["name"])
                    for parent in resource["metadata"].get("ownerReferences", [])
                )
        return False

    def workload_ready(self, kind: str, name: str) -> bool:
        workload = self.get(kind, name)
        metadata, status, spec = (
            workload.get(key, {}) for key in ("metadata", "status", "spec")
        )
        desired = spec.get("replicas", 1)
        if (
            not metadata.get("uid")
            or metadata.get("deletionTimestamp")
            or desired < 1
            or status.get("observedGeneration", -1) < metadata.get("generation", 0)
        ):
            return False
        if any(
            status.get(key, 0) != desired
            for key in ("replicas", "readyReplicas", "updatedReplicas")
        ):
            return False
        if kind == "Deployment" and status.get("availableReplicas", 0) != desired:
            return False
        if kind == "StatefulSet" and (
            not status.get("currentRevision")
            or status.get("currentRevision") != status.get("updateRevision")
        ):
            return False
        owners = {metadata["uid"]}
        if kind == "Deployment":
            owners = {
                item["metadata"]["uid"]
                for item in self.items
                if item["kind"] == "ReplicaSet"
                and any(
                    owner.get("uid") == metadata["uid"]
                    and owner.get("controller") is True
                    for owner in item["metadata"].get("ownerReferences", [])
                )
            }
        pods = [
            item
            for item in self.items
            if item["kind"] == "Pod"
            and not item["metadata"].get("deletionTimestamp")
            and any(
                owner.get("uid") in owners and owner.get("controller") is True
                for owner in item["metadata"].get("ownerReferences", [])
            )
        ]
        return len(pods) == desired and all(pod_ready(pod) for pod in pods)

    def service_pods(self, name: str, ports: tuple[int, ...]) -> list[dict]:
        service = self.get("Service", name)
        selector = service.get("spec", {}).get("selector", {})
        service_ports = {
            item.get("port"): item
            for item in service.get("spec", {}).get("ports", [])
            if item.get("protocol", "TCP") == "TCP"
        }
        if not selector or not set(ports) <= service_ports.keys():
            return []
        endpoint = self.get("Endpoints", name)
        selected = {}
        for subset in endpoint.get("subsets", []):
            endpoint_ports = {
                item.get("port")
                for item in subset.get("ports", [])
                if item.get("protocol", "TCP") == "TCP"
            }
            for address in subset.get("addresses", []):
                reference = address.get("targetRef", {})
                pod = self.get("Pod", reference.get("name", ""))
                labels = pod.get("metadata", {}).get("labels", {})
                if (
                    reference.get("kind") != "Pod"
                    or reference.get("uid") != pod.get("metadata", {}).get("uid")
                    or not pod_ready(pod)
                    or not self.owner_current(pod)
                    or address.get("ip") != pod.get("status", {}).get("podIP")
                    or not all(
                        labels.get(key) == value for key, value in selector.items()
                    )
                ):
                    continue
                named = {
                    port.get("name"): port["containerPort"]
                    for container in pod.get("spec", {}).get("containers", [])
                    for port in container.get("ports", [])
                }
                targets = [
                    service_ports[port].get("targetPort", port) for port in ports
                ]
                if all(
                    (named.get(target) if isinstance(target, str) else target)
                    in endpoint_ports
                    for target in targets
                ):
                    selected[pod["metadata"]["uid"]] = pod
        return list(selected.values())

    def assignment_ready(self, name: str) -> bool:
        assignment = self.get("ChartAssignment", name)
        status, metadata = assignment.get("status", {}), assignment.get("metadata", {})
        if (
            metadata.get("deletionTimestamp")
            or not metadata.get("uid")
            or status.get("phase") != "Ready"
            or status.get("observedGeneration", -1) < metadata.get("generation", 0)
        ):
            return False
        if not all(
            any(
                c.get("type") == kind and c.get("status") == "True"
                for c in status.get("conditions", [])
            )
            for kind in ("Ready", "Settled")
        ):
            return False
        sets = [
            item
            for item in self.items
            if item["kind"] == "ResourceSet"
            and item["metadata"].get("labels", {}).get("name") == name
        ]
        if not sets:
            return False
        try:
            latest = max(
                sets, key=lambda item: int(item["metadata"]["name"].rsplit(".v", 1)[1])
            )
        except (IndexError, ValueError):
            return False
        state = latest.get("status", {})
        settled = (
            not latest["metadata"].get("deletionTimestamp")
            and state.get("phase") == "Settled"
            and bool(state.get("finishedAt"))
            and not state.get("failed")
            and bool(state.get("applied"))
        )
        if not settled:
            return False
        for group in state["applied"]:
            kind = group.get("kind")
            if kind not in {
                "Deployment",
                "StatefulSet",
                "Service",
                "PersistentVolumeClaim",
            }:
                continue
            for applied in group.get("items", []):
                resource = self.get(kind, applied.get("name", ""))
                metadata = resource.get("metadata", {})
                if (
                    not metadata
                    or metadata.get("uid") != applied.get("uid")
                    or metadata.get("generation", 0) != applied.get("generation", 0)
                ):
                    return False
        return True

    def storage_signature(self) -> dict:
        return {
            item["metadata"]["name"]: (
                item["metadata"].get("uid"),
                item.get("spec"),
                item.get("status", {}).get("phase"),
            )
            for item in self.items
            if item["kind"] == "PersistentVolumeClaim"
        }


class Validator:
    def __init__(
        self,
        target: RuntimeTarget,
        reader: Reader,
        lock: dict,
        cycle_timeout: int = 180,
    ):
        self.target, self.reader, self.lock = target, reader, lock
        self.inventory = Inventory(reader.inventory())
        self.results: list[Gate] = []
        self.actions_executed = False
        self.predictors: list[tuple[str, str]] = []
        self.cycle_timeout = cycle_timeout
        self.payload = {
            "namespace": target.namespace,
            "gateway": target.document["target"]["networking"]["ingress_address"],
        }

    def gate(
        self,
        phase: int,
        name: str,
        passed: bool,
        condition: str,
        reason: str,
        resource: dict | None = None,
        evidence: dict | None = None,
    ) -> bool:
        self.results.append(
            Gate(
                phase,
                name,
                "passed" if passed else "failed",
                condition,
                "verified" if passed else reason,
                condition_age(resource),
                evidence=evidence or {},
            )
        )
        return passed

    def probe_gate(
        self,
        phase: int,
        name: str,
        action: str,
        condition: str,
        *,
        payload: dict | None = None,
        wrapper: bool = False,
    ) -> bool:
        result = self.reader.probe(
            action, {**self.payload, **(payload or {})}, wrapper=wrapper
        )
        reason = (
            "known_collision"
            if result.get("known_collision")
            else "probe_timed_out"
            if result.get("timed_out")
            else "probe_failed"
        )
        return self.gate(
            phase,
            name,
            result.get("ok") is True,
            condition,
            reason,
            evidence={key: value for key, value in result.items() if key != "ok"},
        )

    def service_gate(self, phase: int, name: str, ports: tuple[int, ...]) -> bool:
        return self.gate(
            phase,
            name,
            bool(self.inventory.service_pods(name, ports)),
            "ReadyPodEndpointsAndPorts",
            "service_endpoints_not_ready",
            self.inventory.get("Service", name),
        )

    def assignment_gate(self, phase: int, name: str) -> bool:
        return self.gate(
            phase,
            name,
            self.inventory.assignment_ready(name),
            "CurrentGenerationReadyAndLatestResourceSetSettled",
            "assignment_or_resourceset_not_current",
            self.inventory.get("ChartAssignment", name),
        )

    def workspace(self) -> None:
        if not self.gate(
            1,
            "workspace_rollout",
            self.inventory.workload_ready("Deployment", "omts-build-workspace"),
            "CurrentRolloutAndOwnedPodsReady",
            "workspace_not_ready",
            self.inventory.get("Deployment", "omts-build-workspace"),
        ):
            return
        try:
            verified_ingress_selector(
                self.reader,
                self.target.routing_values(),
                project=self.target.namespace,
                expected_selector=self.target.ingress_selector,
            )
            routed = True
        except PilotError:
            routed = False
        if not self.gate(
            1,
            "gateway_configuration",
            routed,
            "DedicatedGatewayAndMeshVerified",
            "gateway_not_verified",
        ):
            return
        self.probe_gate(
            1, "workspace_gateway_rpc", "gateway", "SolutionStatusRPCAnswered"
        )
        image = next(
            (
                item
                for item in self.lock["project_images"]
                if item["name"].startswith("ai.intrinsic.")
            ),
            None,
        )
        if not image:
            self.gate(
                1,
                "registry_authentication",
                False,
                "AuthenticatedManifestHead",
                "solution_image_missing_from_lock",
            )
            return
        self.probe_gate(
            1,
            "registry_authentication",
            "registry",
            "AuthenticatedManifestHead",
            payload={
                "registry": self.target.document["target"]["registry"]["internal_host"],
                "image_name": image["name"],
                "digest": image["digest"],
            },
        )

    def artifacts(self) -> None:
        self.service_gate(2, "artifacts-deployment", (8080,))
        self.gate(
            2,
            "artifact_upload_visible",
            any(
                g.name == "registry_authentication" and g.status == "passed"
                for g in self.results
            )
            or self.registry_visible(),
            "ExistingSolutionImageDigestVerified",
            "uploaded_image_not_verified",
        )

    def registry_visible(self) -> bool:
        image = next(
            (
                item
                for item in self.lock["project_images"]
                if item["name"].startswith("ai.intrinsic.")
            ),
            None,
        )
        return (
            bool(image)
            and self.reader.probe(
                "registry",
                {
                    **self.payload,
                    "registry": self.target.document["target"]["registry"][
                        "internal_host"
                    ],
                    "image_name": image["name"],
                    "digest": image["digest"],
                },
            ).get("ok")
            is True
        )

    def core(self) -> None:
        self.assignment_gate(3, "intrinsic-base")
        for name, ports in CORE_SERVICES.items():
            self.service_gate(3, name, ports)

    def solution(self) -> None:
        for name in ("intrinsic-app-chart", "resources", "skills"):
            self.assignment_gate(4, name)
        for name in ("asset-artifacts-v1", "asset-deployment", "asset-instances-v1"):
            self.service_gate(4, name, (8080,))
        approved = locked_references(self.lock, self.target.document["target"])
        refs = [
            value
            for item in self.inventory.items
            if item["kind"] in {"Deployment", "StatefulSet"}
            and any(
                owner.get("kind") == "ResourceSet"
                for owner in item["metadata"].get("ownerReferences", [])
            )
            for key, value in walk(item.get("spec", {}).get("template", {}))
            if key == "image"
        ]
        self.gate(
            4,
            "solution_image_identity",
            bool(refs) and all(ref in approved for ref in refs),
            "AllSolutionImagesMatchDigestLock",
            "solution_image_not_locked",
            evidence={"image_references_checked": len(refs)},
        )
        self.probe_gate(4, "solution_backend", "solution_health", "SolutionStatusReady")

    def runtime(self) -> None:
        for name in RUNTIME_WORKLOADS:
            self.gate(
                5,
                name,
                self.inventory.workload_ready("StatefulSet", name),
                "CurrentRolloutAndOwnedPodsReady",
                "runtime_workload_not_ready",
                self.inventory.get("StatefulSet", name),
            )
        for name, ports in {
            "simulation-service": (8088,),
            "simulation-server": (12377, 12393, 12394, 12395, 12396, 12476, 15333),
            "rs-icon": (9090, 9091),
            "rs-ur-module": (9090,),
            "rs-gripper": (9090,),
        }.items():
            self.service_gate(5, name, ports)
        self.probe_gate(
            5, "icon_hardware_state", "icon", "OperationalAndCellHardwareEnabled"
        )
        endpoint = self.target.document["target"]["networking"]["rhoai_triton_endpoint"]
        host, port = endpoint.rsplit(":", 1)
        parts = host.split(".")
        if len(parts) == 1:
            namespace = self.target.namespace
        elif len(parts) == 5 and parts[2:] == ["svc", "cluster", "local"]:
            namespace = parts[1]
        else:
            self.gate(
                5,
                "rhoai_predictor",
                False,
                "KServePredictorReadyWithGPU",
                "rhoai_service_not_internal",
            )
            return
        serving = (
            self.inventory
            if namespace == self.target.namespace
            else Inventory(
                sum(
                    (
                        self.reader.get(kind, namespace=namespace).get("items", [])
                        for kind in (
                            "services",
                            "endpoints",
                            "pods",
                            "deployments",
                            "replicasets",
                        )
                    ),
                    [],
                )
            )
        )
        pods = serving.service_pods(parts[0], (int(port),))
        self.predictors = [(namespace, pod["metadata"]["name"]) for pod in pods]
        predictor_ready = bool(pods)
        for pod in pods:
            name = (
                pod.get("metadata", {})
                .get("labels", {})
                .get("serving.kserve.io/inferenceservice")
            )
            if not name:
                predictor_ready = False
                continue
            predictor_ready = predictor_ready and ready(
                self.reader.get("inferenceservices.serving.kserve.io", name, namespace)
            )
            predictor_ready = predictor_ready and any(
                float(
                    container.get("resources", {})
                    .get("limits", {})
                    .get("nvidia.com/gpu", 0)
                )
                > 0
                for container in pod["spec"]["containers"]
                if container["name"] == "kserve-container"
            )
        self.gate(
            5,
            "rhoai_predictor",
            predictor_ready,
            "KServePredictorReadyWithGPU",
            "rhoai_predictor_not_ready",
        )
        if predictor_ready:
            result = self.reader.oc(
                "exec",
                pods[0]["metadata"]["name"],
                "-n",
                namespace,
                "-c",
                "kserve-container",
                "--",
                "nvidia-smi",
                "-L",
                check=False,
            )
            self.gate(
                5,
                "gpu_device_access",
                result.returncode == 0
                and any(line.startswith("GPU ") for line in result.stdout.splitlines()),
                "NvidiaDeviceQuerySucceeded",
                "gpu_device_unavailable",
            )
        wrapper = (
            self.inventory.get("StatefulSet", "rs-inference-service")
            .get("spec", {})
            .get("template", {})
            .get("spec", {})
        )
        containers = wrapper.get("containers", [])
        native = any(
            "triton_inference_server" in c.get("image", "")
            or c.get("name") == "rs-inference-service-1"
            for c in containers
        )
        wired = any(
            value.get("name") == "INTRINSIC_RHOAI_TRITON_ENDPOINT"
            and value.get("value") == endpoint
            for container in containers
            for value in container.get("env", [])
        )
        self.gate(
            5,
            "wrapper_remote_topology",
            wired and not native,
            "CanonicalRHOAIEndpointAndNoNativeTriton",
            "wrapper_topology_mismatch",
        )
        self.probe_gate(
            5,
            "rhoai_and_wrapper_models",
            "rhoai",
            "AuthenticatedModelReadinessAndWrapperMetadata",
            payload={"rhoai_endpoint": endpoint},
            wrapper=True,
        )

    def pre_action_ready(self) -> bool:
        return (
            all(
                self.inventory.assignment_ready(name)
                for name in (
                    "intrinsic-base",
                    "intrinsic-app-chart",
                    "resources",
                    "skills",
                )
            )
            and all(
                self.inventory.workload_ready("StatefulSet", name)
                for name in RUNTIME_WORKLOADS
            )
            and bool(self.inventory.storage_signature())
            and all(
                value[2] == "Bound"
                for value in self.inventory.storage_signature().values()
            )
        )

    def inference_trace(self, since: str) -> None:
        completed = False
        for namespace, name in self.predictors:
            result = self.reader.oc(
                "logs",
                name,
                "-n",
                namespace,
                "-c",
                "kserve-container",
                "--since-time=" + since,
                check=False,
            )
            if result.returncode:
                continue
            completed = completed or foundationpose_completed(result.stdout)
        self.gate(
            6,
            "demo_rhoai_inference",
            completed,
            "FoundationPoseCompletedDuringCycle",
            "inference_completion_unverified",
        )

    def application(self) -> None:
        self.inventory = Inventory(self.reader.inventory())
        if not self.gate(
            6,
            "pre_action_readiness",
            self.pre_action_ready(),
            "FreshAssignmentsRuntimeAndStorageReady",
            "pre_action_readiness_failed",
        ):
            return
        # Recheck idleness immediately before acting; never cancel another run.
        if not self.probe_gate(6, "executive_idle", "idle", "NoActiveOperation"):
            return
        simulated = {
            "rs-ur-module": "ai.intrinsic.ur3e_hardware_module_core_service.gazebo_hwm_stub_image",
            "rs-hande-gripper": "ai.intrinsic.hande_gripper_aquarium_hande_gripper_launch_xml.hande-gripper-sim-driver-hande-gripper.launch.xml",
            "rs-gazebo-simulator": "ai.intrinsic.gazebo_simulator.asset-gzserver-image",
        }
        safe = all(
            any(
                container.get("image", "").split("@", 1)[0].rsplit("/", 1)[-1] == marker
                for container in self.inventory.get("StatefulSet", name)
                .get("spec", {})
                .get("template", {})
                .get("spec", {})
                .get("containers", [])
            )
            for name, marker in simulated.items()
        )
        if not self.gate(
            6,
            "simulated_hardware",
            safe,
            "KnownSimulationImages",
            "simulation_topology_not_verified",
        ):
            return
        before = self.inventory.storage_signature()
        source = ROOT / "deployment/intrinsic-omts"
        payload = {
            "allow_app_actions": True,
            "cycle_timeout": self.cycle_timeout,
            "file_hashes": {
                name: hashlib.sha256((source / name).read_bytes()).hexdigest()
                for name in [*RESET_FILES, CELL + "app_config.yaml"]
            },
        }
        self.actions_executed = True
        try:
            if not self.probe_gate(
                6,
                "four_file_scene_reset",
                "reset",
                "ExplicitCellFilesApplied",
                payload=payload,
            ):
                return
            self.inventory = Inventory(self.reader.inventory())
            runtime_ready = all(
                self.inventory.workload_ready("StatefulSet", name)
                for name in RUNTIME_WORKLOADS
            )
            if not self.gate(
                6,
                "post_reset_runtime",
                runtime_ready,
                "RuntimeReadyAfterReset",
                "post_reset_runtime_not_ready",
            ):
                return
            if not self.probe_gate(
                6, "post_reset_icon", "icon", "HardwareReconnectedAfterReset"
            ):
                return
            if not self.probe_gate(
                6,
                "scene_alignment",
                "scene",
                "RobotTransformAttachmentsAndFramesVerified",
            ):
                return
            started_at = utc_now()
            self.probe_gate(
                6, "bounded_cycle", "cycle", "OneCycleCompleted", payload=payload
            )
            self.inference_trace(started_at)
        finally:
            after = Inventory(self.reader.inventory()).storage_signature()
            self.gate(
                6,
                "persistent_storage_preserved",
                bool(before)
                and before == after
                and all(value[2] == "Bound" for value in after.values()),
                "PVCIdentitiesSpecificationsAndBoundStatusUnchanged",
                "persistent_storage_changed",
            )

    def run(self, phases: list[int]) -> dict:
        blocked = False
        for phase in phases:
            if blocked:
                self.results.append(
                    Gate(
                        phase,
                        PHASES[phase],
                        "skipped",
                        "PrerequisitesRequired",
                        "prerequisite_failed",
                    )
                )
                continue
            start = len(self.results)
            try:
                getattr(self, PHASES[phase])()
            except (PilotError, OSError, ValueError, TypeError, KeyError):
                self.gate(
                    phase,
                    PHASES[phase] + "_query",
                    False,
                    "BoundedQuerySucceeded",
                    "query_or_probe_unavailable",
                )
            blocked = any(gate.status != "passed" for gate in self.results[start:])
        complete = any(
            gate.name == "bounded_cycle" and gate.status == "passed"
            for gate in self.results
        )
        return {
            "schema_version": 1,
            "observed_at": utc_now(),
            "read_only": not self.actions_executed,
            "passed": bool(self.results)
            and all(gate.status == "passed" for gate in self.results),
            "application_cycle_complete": complete,
            "rhoai_demo_inference_observed": any(
                gate.name == "demo_rhoai_inference" and gate.status == "passed"
                for gate in self.results
            ),
            "gates": [asdict(gate) for gate in self.results],
        }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    selection = parser.add_mutually_exclusive_group()
    selection.add_argument("--phase", type=int, choices=PHASES)
    selection.add_argument("--through-phase", type=int, choices=PHASES, default=5)
    parser.add_argument(
        "--allow-app-actions",
        action="store_true",
        help="Allow phase 6 to reset the simulated scene and run one cached cycle",
    )
    parser.add_argument(
        "--cycle-timeout",
        type=int,
        default=180,
        help="Bound the application process to 30–600 seconds",
    )
    parser.add_argument(
        "--json", action="store_true", help="Emit sanitized structured evidence"
    )
    args = parser.parse_args(argv)
    selected = args.phase or args.through_phase
    if selected == 6 and not args.allow_app_actions:
        parser.error("phase 6 requires --allow-app-actions; no cluster calls were made")
    if not 30 <= args.cycle_timeout <= 600:
        parser.error("--cycle-timeout must be between 30 and 600 seconds")
    phases = (
        [selected] if args.phase and selected != 6 else list(range(1, selected + 1))
    )
    try:
        target = load_runtime_target(args.config)
        target.connect()
        report = Validator(
            target,
            Reader(target),
            load_image_lock(target.document, args.config),
            args.cycle_timeout,
        ).run(phases)
    except (PilotError, OSError, ValueError, TypeError, KeyError):
        report = {
            "schema_version": 1,
            "observed_at": utc_now(),
            "read_only": True,
            "passed": False,
            "application_cycle_complete": False,
            "rhoai_demo_inference_observed": False,
            "gates": [
                asdict(
                    Gate(
                        0,
                        "configuration_or_access",
                        "failed",
                        "ValidatedCanonicalTarget",
                        "configuration_or_cluster_unavailable",
                    )
                )
            ],
        }
    if args.json:
        print(json.dumps(report, indent=2, sort_keys=True))
    else:
        for gate in report["gates"]:
            print(
                f"{gate['phase']} {gate['name']}: {gate['status']} ({gate['reason']})"
            )
        print("Acceptance: " + ("passed" if report["passed"] else "failed"))
        print(
            "Application cycle complete: "
            + ("yes" if report["application_cycle_complete"] else "not established")
        )
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
