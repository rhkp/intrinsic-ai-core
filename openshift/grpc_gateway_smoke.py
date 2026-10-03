#!/usr/bin/env python3
"""Run a bounded, temporary gRPC ping through the project-owned Istio gateway."""

from __future__ import annotations

import secrets
import sys
import time

from common import PilotError, get_json, resolve_context

PROJECT = "arhkp-intrinsic"
GATEWAY = "intrinsic-grpc-gateway"
GATEWAY_RESOURCE = "intrinsic-grpc-internal"
# Fortio's official multi-architecture image, pinned to its linux/amd64 manifest.
# https://github.com/fortio/fortio
FORTIO_IMAGE = (
    "docker.io/fortio/fortio@"
    "sha256:f8d5b98308fcc39de71de6551d408819c27e923d235f356c64c0fe269594c223"
)
TIMEOUT_SECONDS = 180


def pod_ready(access, name: str) -> bool:
    pod = get_json(access, "get", "pod", name, "-n", PROJECT, missing_ok=True)
    if not pod:
        return False
    conditions = pod.get("status", {}).get("conditions", [])
    return pod.get("status", {}).get("phase") == "Running" and any(
        condition.get("type") == "Ready" and condition.get("status") == "True"
        for condition in conditions
    )


def wait_for_pod(access, name: str, terminal: set[str]) -> dict:
    deadline = time.monotonic() + TIMEOUT_SECONDS
    while time.monotonic() < deadline:
        pod = get_json(access, "get", "pod", name, "-n", PROJECT, missing_ok=True)
        if pod:
            phase = pod.get("status", {}).get("phase", "Unknown")
            if phase in terminal:
                return pod
            if phase == "Running" and pod_ready(access, name):
                return pod
        time.sleep(3)
    raise PilotError("Timed out waiting for a smoke pod; raw cluster output was suppressed.")


def safe_client_failure(access, name: str) -> str:
    pod = get_json(access, "get", "pod", name, "-n", PROJECT, missing_ok=True) or {}
    status = pod.get("status", {})
    containers = status.get("containerStatuses", [])
    terminated = containers[0].get("state", {}).get("terminated", {}) if containers else {}
    logs = access.oc("logs", name, "-n", PROJECT, check=False).stdout.casefold()
    classifications = (
        ("virtualservice_no_route_404", ("status code 404", "http status 404", "not found")),
        ("gateway_backend_unavailable_503", ("status code 503", "http status 503", "no healthy upstream")),
        ("http2_protocol_or_listener_error", ("frame too large", "error reading server preface", "server preface", "first record does not look like a tls handshake")),
        ("gateway_upstream_connect_error", ("upstream connect error", "connection refused", "connect: no route to host")),
        ("fortio_cli_error", ("unknown command", "unknown flag", "flag provided but not defined")),
        ("grpc_method_rejected", ("unimplemented", "unknown service", "unknown method")),
        (
            "grpc_transport_or_route_failure",
            ("unavailable", "transport: error while dialing", "rpc error"),
        ),
        ("auth_or_policy_failure", ("unauthenticated", "permission denied", "forbidden")),
        ("grpc_timeout", ("deadline exceeded", "timed out", "timeout")),
    )
    failure_class = next(
        (label for label, markers in classifications if any(marker in logs for marker in markers)),
        "client_or_backend_failure",
    )
    exit_code = terminated.get("exitCode")
    return f"phase={status.get('phase', 'unknown')}, exit_code={exit_code}, class={failure_class}"


def manifests(suffix: str) -> str:
    backend = f"intrinsic-grpc-smoke-backend-{suffix}"
    server_pod = backend
    virtual_service = f"intrinsic-grpc-smoke-{suffix}"
    instance = f"smoke-{suffix}"
    return f"""apiVersion: v1
kind: Pod
metadata:
  name: {server_pod}
  namespace: {PROJECT}
  labels:
    app.kubernetes.io/name: {backend}
  annotations:
    sidecar.istio.io/inject: "true"
spec:
  automountServiceAccountToken: false
  restartPolicy: Never
  containers:
    - name: fortio
      image: {FORTIO_IMAGE}
      args: ["server", "-http-port", "disabled", "-grpc-port", "8079", "-data-dir", "/tmp"]
      ports:
        - name: grpc-smoke
          containerPort: 8079
          protocol: TCP
      resources:
        requests:
          cpu: 25m
          memory: 64Mi
        limits:
          cpu: 250m
          memory: 256Mi
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: ["ALL"]
        seccompProfile:
          type: RuntimeDefault
---
apiVersion: v1
kind: Service
metadata:
  name: {backend}
  namespace: {PROJECT}
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: {backend}
  ports:
    - name: grpc-smoke
      protocol: TCP
      port: 8079
      targetPort: grpc-smoke
---
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: {virtual_service}
  namespace: {PROJECT}
spec:
  hosts: ["*"]
  gateways:
    - {PROJECT}/{GATEWAY_RESOURCE}
  http:
    - match:
        - uri:
            prefix: /
          headers:
            x-resource-instance-name:
              exact: {instance}
      route:
        - destination:
            host: {backend}.{PROJECT}.svc.cluster.local
            port:
              number: 8079
"""


def client_manifest(name: str, target: str, instance: str) -> str:
    return f"""apiVersion: v1
kind: Pod
metadata:
  name: {name}
  namespace: {PROJECT}
  annotations:
    sidecar.istio.io/inject: "false"
spec:
  automountServiceAccountToken: false
  restartPolicy: Never
  containers:
    - name: fortio
      image: {FORTIO_IMAGE}
      args:
        - grpcping
        - -n
        - "5"
        - -H
        - x-resource-instance-name:{instance}
        - {target}
      resources:
        requests:
          cpu: 25m
          memory: 64Mi
        limits:
          cpu: 250m
          memory: 256Mi
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: ["ALL"]
        seccompProfile:
          type: RuntimeDefault
"""


def run_client(access, name: str, target: str, instance: str) -> None:
    access.oc(
        "apply",
        "-f",
        "-",
        input_data=client_manifest(name, target, instance),
    )
    pod = wait_for_pod(access, name, {"Succeeded", "Failed", "Unknown"})
    if pod.get("status", {}).get("phase") != "Succeeded":
        raise PilotError(
            "The gateway gRPC check failed ("
            + safe_client_failure(access, name)
            + ")."
        )
    logs = access.oc("logs", name, "-n", PROJECT, check=False)
    if logs.returncode or logs.stdout.count("Ping RTT") != 5:
        raise PilotError("The gateway gRPC check did not report five successful ping responses.")


def main() -> int:
    suffix = secrets.token_hex(4)
    backend = f"intrinsic-grpc-smoke-backend-{suffix}"
    virtual_service = f"intrinsic-grpc-smoke-{suffix}"
    client = f"intrinsic-grpc-smoke-client-{suffix}"
    applied = False
    access = None
    passed = False
    cleanup_ok = True
    try:
        access = resolve_context("dev01")
        gateway_service = get_json(
            access, "get", "service", GATEWAY, "-n", PROJECT, missing_ok=True
        )
        gateway = get_json(
            access,
            "get",
            "gateways.networking.istio.io",
            GATEWAY_RESOURCE,
            "-n",
            PROJECT,
            missing_ok=True,
        )
        if not gateway_service or gateway_service.get("spec", {}).get("type") != "ClusterIP":
            raise PilotError("The project-owned internal gateway Service is not ready for the smoke.")
        if not gateway:
            raise PilotError("The project-owned HTTP/2 Gateway is absent; no smoke resources were created.")
        expected_host = f"{GATEWAY}.{PROJECT}.svc.cluster.local"
        listeners = gateway.get("spec", {}).get("servers", [])
        if not any(
            item.get("port", {}).get("number") == 80
            and item.get("port", {}).get("protocol") == "HTTP2"
            and expected_host in item.get("hosts", [])
            for item in listeners
        ):
            raise PilotError("The project Gateway does not expose the expected internal HTTP/2 listener.")

        applied = True
        access.oc("apply", "-f", "-", input_data=manifests(suffix))
        server_pod = wait_for_pod(access, backend, {"Failed", "Unknown", "Succeeded"})
        if server_pod.get("status", {}).get("phase") != "Running" or not pod_ready(access, backend):
            raise PilotError("The temporary gRPC backend did not become ready.")
        container_names = {
            item.get("name")
            for item in server_pod.get("status", {}).get("containerStatuses", [])
        }
        if "istio-proxy" not in container_names:
            raise PilotError("The temporary backend did not receive the sidecar required by STRICT mTLS.")
        time.sleep(3)  # Allow the Service endpoint and mesh route configuration to converge.
        run_client(
            access,
            client,
            f"{GATEWAY}.{PROJECT}.svc.cluster.local:80",
            f"smoke-{suffix}",
        )
        passed = True
    except PilotError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
    finally:
        if access and applied:
            # Include all names even if client creation failed partway through.
            deleted = access.oc(
                "delete",
                "-f",
                "-",
                "--ignore-not-found=true",
                "--wait=true",
                check=False,
                input_data=(
                    f"apiVersion: networking.istio.io/v1beta1\nkind: VirtualService\nmetadata:\n  name: {virtual_service}\n  namespace: {PROJECT}\n"
                    f"---\napiVersion: v1\nkind: Pod\nmetadata:\n  name: {client}\n  namespace: {PROJECT}\n"
                    f"---\napiVersion: v1\nkind: Service\nmetadata:\n  name: {backend}\n  namespace: {PROJECT}\n"
                    f"---\napiVersion: v1\nkind: Pod\nmetadata:\n  name: {backend}\n  namespace: {PROJECT}\n"
                ),
            )
            cleanup_ok = deleted.returncode == 0
            for kind, name in (
                ("pod", client),
                ("pod", backend),
                ("service", backend),
                ("virtualservice.networking.istio.io", virtual_service),
            ):
                try:
                    remaining = get_json(
                        access, "get", kind, name, "-n", PROJECT, missing_ok=True
                    )
                except PilotError:
                    cleanup_ok = False
                    continue
                if remaining:
                    cleanup_ok = False
    if passed:
        if cleanup_ok:
            print("gRPC gateway smoke passed: five h2c pings traversed the project Gateway and header-based VirtualService to a STRICT-mTLS backend.")
            print("Temporary pods, Service, and VirtualService were deleted and verified absent.")
        else:
            print("gRPC gateway smoke passed, but cleanup could not be fully verified.", file=sys.stderr)
            return 1
        return 0
    print("Smoke resources were cleaned up where they had been created.", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
