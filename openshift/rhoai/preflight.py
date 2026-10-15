#!/usr/bin/env python3
# This project was developed with assistance from AI tools.
"""Read-only, sanitized discovery for the Intrinsic-to-RHOAI migration."""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
from dataclasses import dataclass
from typing import Any


class DiscoveryError(RuntimeError):
    """An error whose message is safe to display without CLI response data."""

    def __init__(self, message: str, category: str = "unverified"):
        super().__init__(message)
        self.category = category


@dataclass(frozen=True)
class Settings:
    context: str
    application_namespace: str
    serving_namespace: str
    platform_namespace: str
    operator_namespace: str = "redhat-ods-operator"

    def validate(self) -> None:
        if not self.context.strip() or self.context.startswith(("your-", "replace-")):
            raise DiscoveryError("Set an explicit OPENSHIFT_CONTEXT before discovery.")
        for namespace in (
            self.application_namespace,
            self.serving_namespace,
            self.platform_namespace,
            self.operator_namespace,
        ):
            if not re.fullmatch(
                r"[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?", namespace
            ) or namespace.startswith(("your-", "replace-")):
                raise DiscoveryError("Set valid, non-placeholder namespace settings.")
        for namespace in (self.application_namespace, self.serving_namespace):
            if namespace.startswith(("kube-", "openshift-")) or namespace in {
                "default",
                "redhat-ods-operator",
                "redhat-ods-applications",
                self.operator_namespace,
                self.platform_namespace,
            }:
                raise DiscoveryError(
                    "Use user projects for application and model workloads."
                )


class Reader:
    def __init__(self, settings: Settings):
        self.settings = settings

    def _run(
        self, *args: str, allow_denied: bool = False
    ) -> subprocess.CompletedProcess[str]:
        try:
            result = subprocess.run(
                [
                    "oc",
                    "--context",
                    self.settings.context,
                    *args,
                    "--request-timeout=15s",
                ],
                capture_output=True,
                text=True,
                check=False,
                timeout=25,
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise DiscoveryError(
                "Cluster command unavailable or timed out; details suppressed.",
                "connection",
            ) from exc
        if result.returncode:
            message = result.stderr.lower()
            if any(
                term in message
                for term in (
                    "unauthorized",
                    "must be logged",
                    "token has expired",
                    "token is expired",
                )
            ):
                raise DiscoveryError(
                    "Cluster authentication failed; renew the local oc login.",
                    "authentication",
                )
            if (
                allow_denied
                and result.returncode == 1
                and result.stdout.strip().lower() == "no"
            ):
                return result
            if "forbidden" in message:
                raise DiscoveryError(
                    "Discovery access denied; platform state is unverified.",
                    "authorization",
                )
            if (
                "notfound" in message
                or "not found" in message
                or "doesn't have a resource type" in message
            ):
                raise DiscoveryError("Required discovery resource was not found.")
            raise DiscoveryError(
                "Cluster query failed; raw connection details suppressed.", "connection"
            )
        return result

    def get(
        self, resource: str, namespace: str | None = None, name: str | None = None
    ) -> dict:
        args = ["get", resource]
        if name:
            args.append(name)
        if namespace:
            args.extend(["--namespace", namespace])
        args.extend(["-o", "json"])
        try:
            value = json.loads(self._run(*args).stdout)
        except json.JSONDecodeError as exc:
            raise DiscoveryError("Cluster returned unreadable discovery data.") from exc
        if not isinstance(value, dict):
            raise DiscoveryError("Cluster returned an unexpected discovery object.")
        return value

    def permission(self, verb: str, resource: str) -> str:
        """Distinguish a denied permission from a failed permission query."""
        try:
            result = self._run(
                "auth",
                "can-i",
                verb,
                resource,
                "--namespace",
                self.settings.serving_namespace,
                allow_denied=True,
            )
        except DiscoveryError as exc:
            if exc.category in {"authentication", "connection"}:
                raise
            return "unverified"
        return {"yes": "allowed", "no": "denied"}.get(
            result.stdout.strip().lower(), "unverified"
        )


def is_ready(resource: dict) -> bool:
    return any(
        c.get("type") == "Ready" and c.get("status") == "True"
        for c in resource.get("status", {}).get("conditions", [])
    )


def native_baseline(statefulset: dict, pods: dict) -> dict[str, bool]:
    """Require current controller ownership and rollout, not a matching pod name."""
    metadata = statefulset.get("metadata", {})
    status = statefulset.get("status", {})
    desired = statefulset.get("spec", {}).get("replicas", 1)
    owned = [
        p
        for p in pods.get("items", [])
        if any(
            o.get("kind") == "StatefulSet"
            and o.get("controller") is True
            and o.get("uid") == metadata.get("uid")
            and bool(metadata.get("uid"))
            for o in p.get("metadata", {}).get("ownerReferences", [])
        )
    ]
    rollout_current = bool(
        metadata.get("uid")
        and not metadata.get("deletionTimestamp")
        and status.get("observedGeneration", -1) >= metadata.get("generation", 0)
        and status.get("currentRevision")
        and status.get("currentRevision") == status.get("updateRevision")
    )
    pods_ready = bool(owned) and all(
        not p.get("metadata", {}).get("deletionTimestamp")
        and p.get("status", {}).get("phase") == "Running"
        and is_ready(p)
        for p in owned
    )
    return {
        "controller_found": bool(metadata.get("uid")),
        "rollout_current": rollout_current,
        "owned_pods_ready": pods_ready,
        "baseline_ready": bool(
            rollout_current
            and desired > 0
            and len(owned) == desired
            and status.get("readyReplicas", 0) == desired
            and pods_ready
        ),
    }


def triton_candidates(templates: dict, runtimes: dict) -> list[dict[str, Any]]:
    candidates = list(runtimes.get("items", []))
    for template in templates.get("items", []):
        candidates.extend(
            obj
            for obj in template.get("objects", [])
            if obj.get("kind") == "ServingRuntime"
        )
    reports = []
    for candidate in candidates:
        spec = candidate.get("spec", {})
        if (
            spec.get("disabled")
            or spec.get("multiModel")
            or "grpc-v2" not in spec.get("protocolVersions", [])
        ):
            continue
        if not any(
            f.get("name") == "triton" for f in spec.get("supportedModelFormats", [])
        ):
            continue
        containers = spec.get("containers", [])
        if not containers or not any(
            c.get("name") == "kserve-container" for c in containers
        ):
            continue
        reports.append(
            {
                "protocols": sorted(
                    p
                    for p in spec.get("protocolVersions", [])
                    if p in {"v2", "grpc-v2"}
                ),
                "images_digest_pinned": bool(containers)
                and all(
                    re.search(r"@sha256:[0-9a-f]{64}$", c.get("image", "")) is not None
                    for c in containers
                ),
            }
        )
    return reports


def discover(reader: Reader, settings: Settings) -> dict[str, Any]:
    report: dict[str, Any] = {"read_only": True, "checks": {}, "blockers": []}
    checks = report["checks"]

    def query(
        label: str, resource: str, namespace: str | None = None, name: str | None = None
    ) -> dict:
        try:
            return reader.get(resource, namespace, name)
        except DiscoveryError as exc:
            if exc.category in {"authentication", "connection"}:
                raise
            report["blockers"].append(f"{label}: {exc}")
            return {}

    # Stop immediately on invalid authentication/target access before inventory.
    reader.get("namespaces", name=settings.application_namespace)
    serving = query("serving project", "namespaces", name=settings.serving_namespace)
    checks["same_user_project"] = (
        settings.application_namespace == settings.serving_namespace
    )
    checks["serving_project_dashboard_marker"] = (
        serving.get("metadata", {}).get("labels", {}).get("opendatahub.io/dashboard")
        == "true"
    )
    if not checks["serving_project_dashboard_marker"]:
        report["blockers"].append(
            "Confirm serving project eligibility; dashboard marker is absent or unreadable."
        )

    statefulset = query(
        "native controller",
        "statefulsets",
        settings.application_namespace,
        "rs-inference-service",
    )
    pods = query("native inference", "pods", settings.application_namespace)
    checks["native_baseline"] = native_baseline(statefulset, pods)
    if not checks["native_baseline"]["baseline_ready"]:
        report["blockers"].append(
            "Native inference baseline is absent, unreadable, or not Ready."
        )

    checks["kserve_api_versions"] = {}
    for resource in (
        "inferenceservices.serving.kserve.io",
        "servingruntimes.serving.kserve.io",
    ):
        crd = query("KServe API", "customresourcedefinitions", name=resource)
        checks["kserve_api_versions"][resource] = [
            v["name"]
            for v in crd.get("spec", {}).get("versions", [])
            if v.get("served")
            and re.fullmatch(r"v\d+(?:(?:alpha|beta)\d+)?", v.get("name", ""))
        ]
        established = any(
            c.get("type") == "Established" and c.get("status") == "True"
            for c in crd.get("status", {}).get("conditions", [])
        )
        if not established or not checks["kserve_api_versions"][resource]:
            report["blockers"].append(
                "Required KServe CRD is not verified Established with a served API version."
            )

    version = query("OpenShift version", "clusterversions", name="version")
    ocp_version = version.get("status", {}).get("desired", {}).get("version", "")
    checks["openshift_version"] = (
        ocp_version
        if re.fullmatch(r"\d+\.\d+\.\d+(?:[-+][\w.-]+)?", ocp_version)
        else "unverified"
    )
    csvs = query("RHOAI version", "clusterserviceversions", settings.operator_namespace)
    checks["rhoai_versions"] = sorted(
        {
            csv.get("spec", {}).get("version", "")
            for csv in csvs.get("items", [])
            if csv.get("status", {}).get("phase") == "Succeeded"
            and any(
                term in csv.get("spec", {}).get("displayName", "").lower()
                for term in ("openshift ai", "openshift data science")
            )
            and re.fullmatch(
                r"\d+\.\d+\.\d+(?:[-+][\w.-]+)?", csv.get("spec", {}).get("version", "")
            )
        }
    )
    if (
        checks["openshift_version"] == "unverified"
        or len(checks["rhoai_versions"]) != 1
    ):
        report["blockers"].append(
            "A single successful RHOAI version and exact OpenShift version are unverified; resolve operator state before choosing serving procedures."
        )
    dsc = query("RHOAI configuration", "datascienceclusters")
    checks["kserve_managed_requested"] = any(
        item.get("spec", {})
        .get("components", {})
        .get("kserve", {})
        .get("managementState")
        == "Managed"
        for item in dsc.get("items", [])
    )
    if not checks["kserve_managed_requested"]:
        report["blockers"].append(
            "Managed KServe configuration was not verified; this setting alone would not establish controller or serving readiness."
        )

    templates = query("runtime templates", "templates", settings.platform_namespace)
    runtimes = query(
        "project runtimes",
        "servingruntimes.serving.kserve.io",
        settings.serving_namespace,
    )
    checks["triton_candidates"] = triton_candidates(templates, runtimes)
    if not checks["triton_candidates"]:
        report["blockers"].append(
            "No enabled single-model grpc-v2 Triton runtime with a recognized container shape was discovered; inspect installed runtime support."
        )

    required = (
        "inferenceservices.serving.kserve.io",
        "servingruntimes.serving.kserve.io",
        "jobs.batch",
    )
    checks["candidate_management_permissions"] = {
        resource: {
            verb: reader.permission(verb, resource)
            for verb in ("get", "create", "patch", "delete")
        }
        for resource in required
    }
    if any(
        value != "allowed"
        for verbs in checks["candidate_management_permissions"].values()
        for value in verbs.values()
    ):
        report["blockers"].append(
            "Candidate creation, inspection, update, or cleanup permissions are denied or unverified. These checks concern the operator identity, not serving/caller service-account access."
        )
    report["required_evidence"] = [
        "Installed controller images/version-matched runtime, admission, and generated pod/probe behavior",
        "Native runtime-context, image digests, CAS patch, ingress/mesh configuration, and actual model readiness",
        "Actual caller protobufs, TLS/authentication, credential rotation, request sizes, and bounded RPC failures",
        "RHOAI internal serving path tested without port-forwarding from the intended caller identity",
        "Actual model/backend artifacts, GPU execution/headroom, cold-start budgets, and output parity",
        "Reconciliation ownership, model-source isolation, partial-container recovery, and rollback",
    ]
    report["phase_0_complete"] = False
    report["candidate_deployment_ready"] = False
    report["discovery_complete"] = not report["blockers"]
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", default=os.environ.get("OPENSHIFT_CONTEXT", ""))
    parser.add_argument(
        "--namespace", default=os.environ.get("OPENSHIFT_NAMESPACE", "")
    )
    parser.add_argument(
        "--serving-namespace", default=os.environ.get("RHOAI_SERVING_NAMESPACE", "")
    )
    parser.add_argument(
        "--platform-namespace",
        default=os.environ.get("RHOAI_PLATFORM_NAMESPACE", "redhat-ods-applications"),
    )
    parser.add_argument(
        "--operator-namespace",
        default=os.environ.get("RHOAI_OPERATOR_NAMESPACE", "redhat-ods-operator"),
    )
    args = parser.parse_args()
    try:
        settings = Settings(
            args.context,
            args.namespace,
            args.serving_namespace or args.namespace,
            args.platform_namespace,
            args.operator_namespace,
        )
        settings.validate()
        if not shutil.which("oc"):
            raise DiscoveryError("The OpenShift CLI is required.")
        report = discover(Reader(settings), settings)
    except DiscoveryError as exc:
        print(json.dumps({"read_only": True, "error": str(exc)}))
        return 2
    print(json.dumps(report, indent=2))
    return 0 if report["discovery_complete"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
