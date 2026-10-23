# This project was developed with assistance from AI tools.
"""TLS, rotating bearer credentials and bounded RPCs for the VM's Triton wrapper."""

from collections import namedtuple
import logging
import os
from pathlib import Path
import time
import uuid

import grpc


class TokenCredentials(grpc.AuthMetadataPlugin):
    def __call__(self, context, callback):
        try:
            token = Path(os.environ["RHOAI_TOKEN_FILE"]).read_text().strip()
            if not token or any(character.isspace() for character in token):
                raise ValueError("Invalid token file")
        except (OSError, ValueError, KeyError):
            callback((), RuntimeError("RHOAI token unavailable"))
            return
        callback((("authorization", "Bearer " + token),), None)


CallDetails = namedtuple(
    "CallDetails", "method timeout metadata credentials wait_for_ready compression"
)


class BoundedCalls(grpc.UnaryUnaryClientInterceptor):
    def intercept_unary_unary(self, continuation, details, request):
        inference = details.method.endswith("/ModelInfer")
        bound = 120 if inference else 15
        timeout = min(details.timeout, bound) if details.timeout is not None else bound
        outgoing = CallDetails(
            details.method,
            timeout,
            details.metadata,
            details.credentials,
            False,
            getattr(details, "compression", None),
        )
        started = time.monotonic()
        request_id = uuid.uuid4().hex if inference else None
        result = continuation(outgoing, request)
        if inference:
            model = (
                "foundationpose"
                if request.model_name.endswith("foundationpose")
                else "rfdetr"
                if request.model_name.endswith("rfdetr")
                else "other"
            )

            def complete(call):
                logging.warning(
                    "HYBRID_RHOAI_INFER request=%s model=%s status=%s seconds=%.3f",
                    request_id,
                    model,
                    call.code().name,
                    time.monotonic() - started,
                )

            result.add_done_callback(complete)
        return result


def channel(endpoint, options):
    """No insecure transport or native inference fallback is available here."""
    roots = Path(os.environ["RHOAI_CA_FILE"]).read_bytes()
    credentials = grpc.composite_channel_credentials(
        grpc.ssl_channel_credentials(root_certificates=roots),
        grpc.metadata_call_credentials(TokenCredentials()),
    )
    bounded = dict(options)
    bounded.update(
        {
            "grpc.max_send_message_length": 128 * 1024 * 1024,
            "grpc.max_receive_message_length": 128 * 1024 * 1024,
        }
    )
    return grpc.intercept_channel(
        grpc.secure_channel(endpoint, credentials, options=list(bounded.items())),
        BoundedCalls(),
    )


def managed_model_action(controller, model, action):
    """Maintain the Intrinsic catalog while KServe owns backend load/unload."""
    from tritonclient.grpc import service_pb2

    if action.value == "unload":
        return
    if action.value not in {"load", "reload"}:
        raise ValueError("Unknown model lifecycle action")
    controller._write_triton_config(model)
    response = controller._triton_stub.ModelReady(
        service_pb2.ModelReadyRequest(name=model.model_config.name), timeout=15
    )
    if not response.ready:
        raise RuntimeError("RHOAI model is not ready")


def patch_source(source, expected_digest):
    """Fail before changing a wrapper whose upstream source has drifted."""
    import hashlib

    if hashlib.sha256(source.encode()).hexdigest() != expected_digest:
        raise RuntimeError("Unexpected upstream wrapper source")
    replacements = [
        (
            '_INFERENCE_SERVER_URL = "unix:///dev/shm/triton.sock:0"',
            '_INFERENCE_SERVER_URL = os.environ["INTRINSIC_RHOAI_TRITON_ENDPOINT"]',
        ),
        (
            "import grpc\n",
            "import grpc\nimport sys\nsys.path.insert(0, '/etc/intrinsic-hybrid')\nimport hybrid_triton as _hybrid_triton\n",
        ),
        (
            "triton_channel = grpc.insecure_channel(\n      _INFERENCE_SERVER_URL,\n      options=inference_service.GRPC_OPTIONS,\n  )",
            "triton_channel = _hybrid_triton.channel(_INFERENCE_SERVER_URL, inference_service.GRPC_OPTIONS)",
        ),
        (
            "  inference_runner = inference_runner_lib.InferenceRunner(",
            "  model_controller._run_triton_action = lambda model, action: _hybrid_triton.managed_model_action(model_controller, model, action)\n\n  inference_runner = inference_runner_lib.InferenceRunner(",
        ),
        ("use_shm=True", "use_shm=False"),
        ("_TRITON_CHANNEL_READY_TIMEOUT = 1200", "_TRITON_CHANNEL_READY_TIMEOUT = 60"),
    ]
    for old, new in replacements:
        if source.count(old) != 1:
            raise RuntimeError("Unexpected wrapper patch context")
        source = source.replace(old, new)
    return "# This project was developed with assistance from AI tools.\n" + source
