#!/usr/bin/env python3
# This project was developed with assistance from AI tools.
"""Verify or apply routing settings from validated canonical configuration."""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

from common import PilotError, get_json
from runtime_config import DEFAULT_CONFIG_PATH, load_runtime_target

CONFIG_MAP = "intrinsic-routing-config"


def main_error(exc: PilotError) -> int:
    """Emit only the intentionally sanitized operator-facing error."""
    print(f"ERROR: {exc}", file=sys.stderr)
    return 2


def safe_selector(selector: object) -> dict[str, str]:
    if not isinstance(selector, dict) or not selector:
        raise PilotError(
            "The configured ingress Service/Gateway has no simple pod selector."
        )
    result: dict[str, str] = {}
    for key, value in selector.items():
        if (
            not isinstance(key, str)
            or not re.fullmatch(r"[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)?", key)
            or not isinstance(value, str)
            or not re.fullmatch(r"[A-Za-z0-9_.-]+", value)
        ):
            raise PilotError("The ingress pod selector contains an unsupported label.")
        result[key] = value
    return result


def ready_pods(access, namespace: str, selector: dict[str, str]) -> list[dict]:
    label_selector = ",".join(f"{key}={value}" for key, value in selector.items())
    result = get_json(
        access, "get", "pods", "--namespace", namespace, "-l", label_selector
    )
    return [
        pod
        for pod in result.get("items", [])
        if pod.get("status", {}).get("phase") == "Running"
        and any(
            condition.get("type") == "Ready" and condition.get("status") == "True"
            for condition in pod.get("status", {}).get("conditions", [])
        )
    ]


def verify_project_mesh_membership(
    access, mesh_namespace: str, *, project: str
) -> None:
    namespace = get_json(access, "get", "namespace", project)
    labels = namespace.get("metadata", {}).get("labels", {})
    member = get_json(
        access,
        "get",
        "servicemeshmembers.maistra.io",
        "default",
        "--namespace",
        project,
        missing_ok=True,
    )
    member_selected = False
    if member:
        control_plane = member.get("spec", {}).get("controlPlaneRef", {})
        ready = any(
            condition.get("type") == "Ready" and condition.get("status") == "True"
            for condition in member.get("status", {}).get("conditions", [])
        )
        control_plane_namespace = control_plane.get("namespace")
        if not ready or not control_plane_namespace or not control_plane.get("name"):
            raise PilotError("The target project's ServiceMeshMember is not Ready.")
        mesh_namespace = control_plane_namespace
        member_selected = True
    rolls = get_json(
        access,
        "get",
        "servicemeshmemberrolls.maistra.io",
        "--namespace",
        mesh_namespace,
    ).get("items", [])
    selected = False
    for roll in rolls:
        members = roll.get("spec", {}).get("members", [])
        if project in members:
            selected = True
            break
        for selector in roll.get("spec", {}).get("memberSelectors", []):
            match_labels = selector.get("matchLabels", {})
            if match_labels and all(
                labels.get(key) == value for key, value in match_labels.items()
            ):
                selected = True
                break
        if selected:
            break
    selected = selected or member_selected
    if not selected:
        raise PilotError(
            "The target project is not enrolled in this Service Mesh; have the mesh owner add it before routing."
        )


def verified_ingress_selector(
    access,
    values: dict[str, str],
    *,
    project: str,
    expected_selector: dict[str, str],
) -> dict[str, str]:
    address = values["INTRINSIC_INGRESS_ADDRESS"]
    match = re.fullmatch(
        r"([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)\.([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)\.svc\.cluster\.local:80",
        address,
    )
    if not match:
        raise PilotError(
            "INTRINSIC_INGRESS_ADDRESS must be service.namespace.svc.cluster.local:80."
        )
    service_name, namespace = match.groups()
    service_host = f"{service_name}.{namespace}.svc.cluster.local"
    gateway_namespace, gateway_name = values["INTRINSIC_INGRESS_GATEWAY"].split("/", 1)
    if service_name == "istio-ingressgateway" or namespace != project:
        raise PilotError(
            "The configured ingress Service must be the dedicated gateway Service in the target project."
        )
    if gateway_namespace != project:
        raise PilotError(
            "The configured Gateway must be the dedicated project-scoped Gateway."
        )
    verify_project_mesh_membership(access, namespace, project=project)

    service = get_json(access, "get", "service", service_name, "--namespace", namespace)
    spec = service.get("spec", {})
    selector = safe_selector(spec.get("selector"))
    if selector != safe_selector(expected_selector):
        raise PilotError(
            "The ingress Service selector must target only the dedicated gateway workload."
        )
    service_ports = {
        port.get("port")
        for port in spec.get("ports", [])
        if port.get("protocol", "TCP") == "TCP"
    }
    if spec.get("type", "ClusterIP") != "ClusterIP" or not {80, 443}.issubset(
        service_ports
    ):
        raise PilotError(
            "The configured internal Gateway Service must expose both client ports."
        )
    client_ports = {
        port["port"]: port.get("targetPort", port["port"])
        for port in spec.get("ports", [])
        if port.get("protocol", "TCP") == "TCP" and port.get("port") in {80, 443}
    }
    if client_ports != {80: 443, 443: 443}:
        raise PilotError(
            "Both Gateway Service client ports must target the mTLS listener on port 443."
        )

    endpoints = get_json(
        access, "get", "endpoints", service_name, "--namespace", namespace
    )
    endpoint_names = {
        address.get("targetRef", {}).get("name")
        for subset in endpoints.get("subsets", [])
        for address in subset.get("addresses", [])
        if address.get("targetRef", {}).get("kind") == "Pod"
    }
    endpoint_names.discard(None)
    endpoint_ips = {
        address.get("ip")
        for subset in endpoints.get("subsets", [])
        for address in subset.get("addresses", [])
        if address.get("ip")
    }
    if not endpoint_names:
        raise PilotError("The configured ingress Service has no ready Pod endpoints.")

    gateway = get_json(
        access,
        "get",
        "gateways.networking.istio.io",
        gateway_name,
        "--namespace",
        gateway_namespace,
    )
    gateway_spec = gateway.get("spec", {})
    gateway_selector = safe_selector(gateway_spec.get("selector"))
    if gateway_selector != selector:
        raise PilotError(
            "The Gateway selector must exactly match the dedicated ingress Service selector."
        )
    listeners = gateway_spec.get("servers", [])
    mesh_listener = any(
        server.get("port", {}).get("number") == 443
        and server.get("port", {}).get("protocol") == "HTTPS"
        and {service_host, "*"}.issubset(set(server.get("hosts", [])))
        and server.get("tls") == {"mode": "ISTIO_MUTUAL"}
        for server in listeners
    )
    if not mesh_listener:
        raise PilotError(
            "The dedicated Gateway must expose the configured host and its Istio mTLS listener."
        )

    pods = ready_pods(access, project, gateway_selector)
    matched = {
        pod.get("metadata", {}).get("name")
        for pod in pods
        if pod.get("metadata", {}).get("name") in endpoint_names
        and pod.get("status", {}).get("podIP") in endpoint_ips
    }
    if not matched:
        raise PilotError(
            "The dedicated Gateway selector does not select a ready Service endpoint."
        )
    return selector


def apply(config_path: Path = DEFAULT_CONFIG_PATH, *, check_only: bool = False) -> None:
    target = load_runtime_target(config_path)
    access = target.connect()
    selector = verified_ingress_selector(
        access,
        target.routing_values(),
        project=target.namespace,
        expected_selector=target.ingress_selector,
    )
    manifest = target.routing_configmap(selector)
    if check_only:
        current = get_json(
            access,
            "get",
            "configmap",
            CONFIG_MAP,
            "--namespace",
            target.namespace,
            missing_ok=True,
        )
        if not current or current.get("data") != manifest["data"]:
            raise PilotError(
                "The routing ConfigMap is missing or differs from canonical configuration."
            )
        print(
            "OpenShift gRPC routing matches canonical configuration; no resources changed."
        )
        return
    applied = access.oc(
        "apply",
        "-f",
        "-",
        check=False,
        input_data=json.dumps(manifest),
    )
    if applied.returncode:
        raise PilotError(
            "Could not apply the routing ConfigMap; raw CLI output was suppressed."
        )
    print("OpenShift gRPC routing ConfigMap applied to the target project.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    parser.add_argument(
        "--check",
        action="store_true",
        help="Verify live routing without applying changes",
    )
    args = parser.parse_args()
    try:
        apply(args.config, check_only=args.check)
    except PilotError as exc:
        raise SystemExit(main_error(exc))
