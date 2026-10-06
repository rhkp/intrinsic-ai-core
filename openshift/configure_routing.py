#!/usr/bin/env python3
"""Apply only the non-secret OpenShift gRPC routing values from ignored .env."""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

from common import PilotError, get_json, resolve_context

ENV_FILE = Path(__file__).resolve().parent / ".env"
PROJECT = "arhkp-intrinsic"
CONFIG_MAP = "intrinsic-routing-config"
GATEWAY_SERVICE = "intrinsic-grpc-gateway"
GATEWAY_RESOURCE = "intrinsic-grpc-internal"
GATEWAY_SELECTOR = {
    "app.kubernetes.io/name": GATEWAY_SERVICE,
    "istio": GATEWAY_SERVICE,
}
REQUIRED = {"INTRINSIC_INGRESS_ADDRESS", "INTRINSIC_INGRESS_GATEWAY"}


def main_error(exc: PilotError) -> int:
    """Emit only the intentionally sanitized operator-facing error."""
    print(f"ERROR: {exc}", file=sys.stderr)
    return 2


def read_routing_values() -> dict[str, str]:
    try:
        lines = ENV_FILE.read_text().splitlines()
    except OSError as exc:
        raise PilotError("openshift/.env is required; copy .env.sample and set the verified routing values.") from exc

    values: dict[str, str] = {}
    for line in lines:
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        key, separator, value = line.partition("=")
        if not separator or key not in REQUIRED:
            continue
        value = value.strip()
        if value.startswith(("'", '"')) and value.endswith(value[0]):
            value = value[1:-1]
        if not value or any(char in value for char in "`$;\n\r"):
            raise PilotError("routing values must be literal DNS names without shell syntax.")
        if key in values:
            raise PilotError(f"openshift/.env contains duplicate {key} entries.")
        values[key] = value

    if set(values) != REQUIRED:
        raise PilotError("openshift/.env must define both required ingress routing values.")

    address = values["INTRINSIC_INGRESS_ADDRESS"]
    address_match = re.fullmatch(
        r"([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)\.([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)\.svc\.cluster\.local:80",
        address,
    )
    if not address_match:
        raise PilotError("INTRINSIC_INGRESS_ADDRESS must be service.namespace.svc.cluster.local:80.")
    gateway = values["INTRINSIC_INGRESS_GATEWAY"]
    if not re.fullmatch(r"[a-z0-9](?:[-a-z0-9]*[a-z0-9])?/[a-z0-9](?:[-a-z0-9]*[a-z0-9])?", gateway):
        raise PilotError("INTRINSIC_INGRESS_GATEWAY must use namespace/name form.")
    return values


def safe_selector(selector: object) -> dict[str, str]:
    if not isinstance(selector, dict) or not selector:
        raise PilotError("The configured ingress Service/Gateway has no simple pod selector.")
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
    result = get_json(access, "get", "pods", "--namespace", namespace, "-l", label_selector)
    return [
        pod
        for pod in result.get("items", [])
        if pod.get("status", {}).get("phase") == "Running"
        and any(
            condition.get("type") == "Ready" and condition.get("status") == "True"
            for condition in pod.get("status", {}).get("conditions", [])
        )
    ]


def verify_project_mesh_membership(access, mesh_namespace: str) -> None:
    namespace = get_json(access, "get", "namespace", PROJECT)
    labels = namespace.get("metadata", {}).get("labels", {})
    member = get_json(
        access,
        "get",
        "servicemeshmembers.maistra.io",
        "default",
        "--namespace",
        PROJECT,
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
        if PROJECT in members:
            selected = True
            break
        for selector in roll.get("spec", {}).get("memberSelectors", []):
            match_labels = selector.get("matchLabels", {})
            if match_labels and all(labels.get(key) == value for key, value in match_labels.items()):
                selected = True
                break
        if selected:
            break
    selected = selected or member_selected
    if not selected:
        raise PilotError("The target project is not enrolled in this Service Mesh; have the mesh owner add it before routing.")


def verified_ingress_selector(access, values: dict[str, str]) -> dict[str, str]:
    address = values["INTRINSIC_INGRESS_ADDRESS"]
    match = re.fullmatch(
        r"([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)\.([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)\.svc\.cluster\.local:80",
        address,
    )
    if not match:
        raise PilotError("INTRINSIC_INGRESS_ADDRESS must be service.namespace.svc.cluster.local:80.")
    service_name, namespace = match.groups()
    service_host = f"{service_name}.{namespace}.svc.cluster.local"
    gateway_namespace, gateway_name = values["INTRINSIC_INGRESS_GATEWAY"].split("/", 1)
    if service_name != GATEWAY_SERVICE or namespace != PROJECT:
        raise PilotError("The configured ingress Service must be the dedicated gateway Service in the target project.")
    if gateway_namespace != PROJECT or gateway_name != GATEWAY_RESOURCE:
        raise PilotError("The configured Gateway must be the dedicated project-scoped Gateway.")
    verify_project_mesh_membership(access, namespace)

    service = get_json(access, "get", "service", service_name, "--namespace", namespace)
    spec = service.get("spec", {})
    selector = safe_selector(spec.get("selector"))
    if any(selector.get(key) != value for key, value in GATEWAY_SELECTOR.items()):
        raise PilotError("The ingress Service selector must target only the dedicated gateway workload.")
    service_ports = {
        port.get("port")
        for port in spec.get("ports", [])
        if port.get("protocol", "TCP") == "TCP"
    }
    if spec.get("type", "ClusterIP") != "ClusterIP" or not {80, 443}.issubset(service_ports):
        raise PilotError("The configured internal Gateway Service must expose both the tunnel and mesh ports.")

    endpoints = get_json(access, "get", "endpoints", service_name, "--namespace", namespace)
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
        raise PilotError("The Gateway selector must exactly match the dedicated ingress Service selector.")
    listeners = gateway_spec.get("servers", [])
    tunnel_listener = any(
        server.get("port", {}).get("number") == 80
        and server.get("port", {}).get("protocol") == "HTTP2"
        and service_host in server.get("hosts", [])
        and not server.get("tls")
        for server in listeners
    )
    mesh_listener = any(
        server.get("port", {}).get("number") == 443
        and server.get("port", {}).get("protocol") == "HTTPS"
        and {service_host, "*"}.issubset(set(server.get("hosts", [])))
        and server.get("tls") == {"mode": "ISTIO_MUTUAL"}
        for server in listeners
    )
    if not tunnel_listener or not mesh_listener:
        raise PilotError("The dedicated Gateway must expose its loopback-tunnel HTTP/2 listener and Istio mTLS mesh listener.")

    pods = ready_pods(access, PROJECT, gateway_selector)
    matched = {
        pod.get("metadata", {}).get("name")
        for pod in pods
        if pod.get("metadata", {}).get("name") in endpoint_names
        and pod.get("status", {}).get("podIP") in endpoint_ips
    }
    if not matched:
        raise PilotError("The dedicated Gateway selector does not select a ready Service endpoint.")
    return selector


def apply() -> None:
    values = read_routing_values()
    access = resolve_context()
    selector = verified_ingress_selector(access, values)
    args = [
        "create",
        "configmap",
        CONFIG_MAP,
        "--namespace",
        PROJECT,
        f"--from-literal=INTRINSIC_INGRESS_ADDRESS={values['INTRINSIC_INGRESS_ADDRESS']}",
        f"--from-literal=INTRINSIC_INGRESS_GATEWAY={values['INTRINSIC_INGRESS_GATEWAY']}",
        "--from-literal=INTRINSIC_INGRESS_POD_SELECTOR="
        + json.dumps(selector, sort_keys=True, separators=(",", ":")),
        "--dry-run=client",
        "-o",
        "json",
    ]
    generated = access.oc(*args, check=False)
    if generated.returncode:
        raise PilotError("Could not generate the routing ConfigMap; raw CLI output was suppressed.")
    applied = access.oc("apply", "-f", "-", check=False, input_data=generated.stdout)
    if applied.returncode:
        raise PilotError("Could not apply the routing ConfigMap; raw CLI output was suppressed.")
    print("OpenShift gRPC routing ConfigMap applied to the target project.")


if __name__ == "__main__":
    try:
        apply()
    except PilotError as exc:
        raise SystemExit(main_error(exc))
