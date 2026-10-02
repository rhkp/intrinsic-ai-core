#!/usr/bin/env python3
"""Check RHOAI model-route TLS and bearer-token behavior without printing secrets."""

from __future__ import annotations

import json
import re
import ssl
import stat
import sys
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit, urlunsplit
from urllib.request import Request, urlopen


ENV_FILE = Path(__file__).resolve().parents[2] / ".env"


def load_env(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:].lstrip()
        key, separator, value = line.partition("=")
        if not separator:
            continue
        key = key.strip()
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        values[key] = value
    return values


def safe_inference_url(raw_url: str, model: str) -> str:
    parsed = urlsplit(raw_url)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password:
        raise ValueError("endpoint must be HTTPS and contain no embedded credentials")
    if parsed.query or parsed.fragment:
        raise ValueError("endpoint URL must not include a query or fragment")
    path = parsed.path.rstrip("/")
    if path.endswith("/infer"):
        pass
    elif path.endswith(f"/v2/models/{model}"):
        path += "/infer"
    else:
        path += f"/v2/models/{model}/infer"
    return urlunsplit((parsed.scheme, parsed.netloc, path, "", ""))


def post_inference(url: str, payload: bytes, context: ssl.SSLContext, token: str | None) -> tuple[int, dict | None]:
    headers = {"Content-Type": "application/json"}
    if token is not None:
        headers["Authorization"] = f"Bearer {token}"
    request = Request(url, data=payload, headers=headers, method="POST")
    try:
        with urlopen(request, context=context, timeout=15) as response:
            status = response.status
            body = response.read()
    except HTTPError as exc:
        return exc.code, None
    except (URLError, TimeoutError, OSError) as exc:
        reason = getattr(exc, "reason", None)
        if isinstance(reason, ssl.SSLCertVerificationError) or isinstance(exc, ssl.SSLCertVerificationError):
            raise RuntimeError("TLS certificate validation failed") from None
        raise RuntimeError("endpoint connection failed") from None
    if not 200 <= status < 300:
        return status, None
    try:
        return status, json.loads(body)
    except (UnicodeDecodeError, json.JSONDecodeError):
        return status, None


def main() -> int:
    try:
        env = load_env(ENV_FILE)
        raw_url = env["RHOAI_INFERENCE_URL"]
        model = env.get("RHOAI_MODEL_NAME", "cifar10")
        token_path = Path(env["RHOAI_BEARER_TOKEN_FILE"]).expanduser()
        ca_path = env.get("RHOAI_CA_BUNDLE_FILE", "").strip()
        if not re.fullmatch(r"[A-Za-z0-9_.-]+", model):
            raise ValueError("model name contains unsupported characters")
        url = safe_inference_url(raw_url, model)
        token_stat = token_path.stat()
        if not stat.S_ISREG(token_stat.st_mode) or (token_stat.st_mode & 0o077):
            raise ValueError("token file must be a regular file accessible only to its owner")
        token = token_path.read_text(encoding="utf-8").strip()
        if not token or "\n" in token:
            raise ValueError("token file must contain one non-empty token")
        context = ssl.create_default_context(cafile=ca_path or None)
    except (KeyError, OSError, ValueError) as exc:
        # Keep endpoint names and local secret paths out of command output.
        print(f"configuration=failed ({type(exc).__name__})")
        return 2

    payload = json.dumps(
        {
            "inputs": [
                {
                    "name": "INPUT__0",
                    "shape": [1, 3, 32, 32],
                    "datatype": "FP32",
                    "data": [0.0] * 3072,
                }
            ]
        }
    ).encode("utf-8")

    checks: list[tuple[str, int, dict | None]] = []
    try:
        checks.append(("missing_token", *post_inference(url, payload, context, None)))
        checks.append(("invalid_token", *post_inference(url, payload, context, "invalid-token-for-smoke-test")))
        checks.append(("valid_token", *post_inference(url, payload, context, token)))
    except RuntimeError as exc:
        print(f"connectivity=failed ({exc})")
        return 1

    ok = True
    for name, status, _ in checks[:2]:
        passed = status in (401, 403)
        ok &= passed
        print(f"{name}=HTTP_{status} {'PASS' if passed else 'FAIL'}")

    name, status, response = checks[2]
    outputs = response.get("outputs", []) if isinstance(response, dict) else []
    shape = outputs[0].get("shape") if outputs and isinstance(outputs[0], dict) else None
    passed = (
        status == 200
        and isinstance(response, dict)
        and response.get("model_name") == model
        and shape == [1, 10]
        and isinstance(outputs[0].get("data"), list)
        and len(outputs[0]["data"]) == 10
    )
    ok &= passed
    if passed:
        print(f"{name}=HTTP_{status} model_match=yes output_shape=1x10 PASS")
    else:
        print(f"{name}=HTTP_{status} response_check=FAIL")
    print("tls_verification=enabled")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
