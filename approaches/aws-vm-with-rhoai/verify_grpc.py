# This project was developed with assistance from AI tools.
"""Verify TLS, ALPN, authorization and a synthetic RF-DETR gRPC request."""

import argparse
import json
from pathlib import Path
import socket
import ssl
import time

import numpy as np
import tritonclient.grpc as triton
from tritonclient.utils import InferenceServerException


def denied(
    client, headers: dict[str, str], label: str, model: str, inputs: list
) -> None:
    for method in ("ServerReady", "ModelInfer"):
        try:
            if method == "ServerReady":
                client.is_server_ready(headers=headers, client_timeout=15)
            else:
                client.infer(model, inputs, headers=headers, client_timeout=15)
        except InferenceServerException as exc:
            status = exc.status()
            if status not in {
                "StatusCode.UNAUTHENTICATED",
                "StatusCode.PERMISSION_DENIED",
            }:
                raise RuntimeError(
                    f"{label}: unexpected gRPC status {status}"
                ) from None
            print(
                json.dumps(
                    {
                        "check": label,
                        "method": method,
                        "result": "PASS",
                        "status": status,
                    }
                )
            )
            continue
        raise RuntimeError(f"{label}: unauthorized {method} request reached Triton")


def verify(endpoint: str, ca: Path, token: str, model: str, unauthorized: str) -> None:
    host, port = endpoint.rsplit(":", 1)
    context = ssl.create_default_context(cafile=str(ca))
    context.set_alpn_protocols(["h2"])
    with socket.create_connection((host, int(port)), timeout=15) as raw:
        with context.wrap_socket(raw, server_hostname=host) as connection:
            if connection.selected_alpn_protocol() != "h2":
                raise RuntimeError("TLS succeeded but ALPN did not select h2")
    print(
        json.dumps({"check": "tls_hostname_chain_alpn", "result": "PASS", "alpn": "h2"})
    )
    client = triton.InferenceServerClient(
        endpoint,
        ssl=True,
        root_certificates=str(ca),
        channel_args=[("grpc.max_receive_message_length", 64 * 1024 * 1024)],
    )
    try:
        image = triton.InferInput("input", [1, 3, 224, 224], "UINT8")
        image.set_data_from_numpy(np.zeros([1, 3, 224, 224], dtype=np.uint8))
        thresholds = triton.InferInput("thresholds", [1, 2], "FP32")
        thresholds.set_data_from_numpy(np.array([[0.5, 0.5]], dtype=np.float32))
        inputs = [image, thresholds]
        denied(client, {}, "missing_token", model, inputs)
        denied(
            client,
            {"authorization": "Bearer invalid-pilot-token"},
            "invalid_token",
            model,
            inputs,
        )
        if unauthorized:
            denied(
                client,
                {"authorization": f"Bearer {unauthorized}"},
                "unauthorized_identity",
                model,
                inputs,
            )
        headers = {"authorization": f"Bearer {token}"}
        if not client.is_server_ready(headers=headers, client_timeout=15):
            raise RuntimeError("Authorized server readiness returned false")
        if not client.is_model_ready(model, headers=headers, client_timeout=15):
            raise RuntimeError("Authorized model readiness returned false")
        metadata = client.get_model_metadata(model, headers=headers, client_timeout=15)
        metadata_inputs = {
            item.name: (item.datatype, list(item.shape)) for item in metadata.inputs
        }
        if (
            set(metadata_inputs) != {"input", "thresholds"}
            or metadata_inputs["input"][0] != "UINT8"
            or metadata_inputs["thresholds"] != ("FP32", [1, 2])
        ):
            raise RuntimeError(
                "Selected model does not match the RF-DETR input contract"
            )
        expected = {
            "boxes": (np.float32, (0, 4)),
            "scores": (np.float32, (0,)),
            "masks": (np.bool_, (0, 224, 224)),
            "visibility": (np.float32, (0,)),
        }
        started = time.monotonic()
        result = client.infer(
            model,
            inputs,
            outputs=[triton.InferRequestedOutput(name) for name in expected],
            headers=headers,
            client_timeout=120,
        )
        response = result.get_response()
        if (
            response.model_name != model
            or response.model_version not in metadata.versions
        ):
            raise RuntimeError("Response model identity/version mismatch")
        for name, (dtype, shape) in expected.items():
            value = result.as_numpy(name)
            if value is None or value.dtype != dtype or value.shape != shape:
                raise RuntimeError(f"Unexpected synthetic output contract: {name}")
        print(
            json.dumps(
                {
                    "check": "authorized_readiness_metadata_inference",
                    "result": "PASS",
                    "seconds": round(time.monotonic() - started, 3),
                    "outputs": {
                        name: list(shape) for name, (_, shape) in expected.items()
                    },
                }
            )
        )
        # Model lifecycle operations must never be reachable through this ingress.
        try:
            client.load_model(
                "nonexistent-pilot-model", headers=headers, client_timeout=15
            )
        except InferenceServerException as exc:
            if exc.status() != "StatusCode.PERMISSION_DENIED":
                raise RuntimeError(
                    "Administrative RPC was not blocked at the gateway"
                ) from None
        else:
            raise RuntimeError("Administrative RPC unexpectedly succeeded")
        print(json.dumps({"check": "administrative_rpc_blocked", "result": "PASS"}))
    finally:
        client.close()


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--endpoint-file", type=Path, required=True)
    parser.add_argument("--ca", type=Path, required=True)
    parser.add_argument("--token-file", type=Path, required=True)
    parser.add_argument("--model-file", type=Path, required=True)
    parser.add_argument("--unauthorized-token-file", type=Path)
    args = parser.parse_args()
    try:
        verify(
            args.endpoint_file.read_text().strip(),
            args.ca,
            args.token_file.read_text().strip(),
            args.model_file.read_text().strip(),
            args.unauthorized_token_file.read_text().strip()
            if args.unauthorized_token_file
            else "",
        )
    except (OSError, ValueError, RuntimeError, InferenceServerException) as exc:
        # gRPC exception details can contain endpoint addresses; never print them.
        message = str(exc) if isinstance(exc, RuntimeError) else type(exc).__name__
        print(json.dumps({"result": "FAIL", "category": message}))
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
