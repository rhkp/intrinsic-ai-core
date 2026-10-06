#!/usr/bin/env python3
"""Create short-lived Docker auth and CA files from a mounted service account."""

from __future__ import annotations

import base64
import json
import os
import re
import stat
import tempfile
from pathlib import Path


def _required_path(name: str, default: str) -> Path:
    path = Path(os.environ.get(name, default))
    if not path.is_file() or not os.access(path, os.R_OK):
        raise SystemExit(f"required file is not readable: {name}")
    return path


def main() -> None:
    namespace = os.environ.get("OPENSHIFT_NAMESPACE", "arhkp-intrinsic")
    registry_host = os.environ.get(
        "INTRINSIC_REGISTRY_HOST", "image-registry.openshift-image-registry.svc:5000"
    )
    if not re.fullmatch(r"[a-zA-Z0-9.-]+:[0-9]{1,5}", registry_host):
        raise SystemExit("INTRINSIC_REGISTRY_HOST must be a host and port")
    if not re.fullmatch(r"[a-z0-9](?:[-a-z0-9.]*[a-z0-9])?", namespace):
        raise SystemExit("OPENSHIFT_NAMESPACE is invalid")
    token_path = _required_path(
        "SERVICE_ACCOUNT_TOKEN_FILE",
        "/var/run/secrets/kubernetes.io/serviceaccount/token",
    )
    namespace_path = Path("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
    if namespace_path.is_file():
        mounted_namespace = namespace_path.read_text(encoding="utf-8").strip()
        if mounted_namespace != namespace:
            raise SystemExit("mounted service account namespace does not match configuration")

    token = token_path.read_text(encoding="utf-8").strip()
    if not token or any(char.isspace() for char in token):
        raise SystemExit("mounted service account token is empty or malformed")

    system_ca = _required_path(
        "SYSTEM_CA_BUNDLE", "/etc/pki/tls/certs/ca-bundle.crt"
    ).read_bytes()
    service_ca = _required_path(
        "OPENSHIFT_SERVICE_CA_FILE", "/var/run/registry-ca/service-ca.crt"
    ).read_bytes()
    marker = b"-----BEGIN CERTIFICATE-----"
    if marker not in system_ca or marker not in service_ca:
        raise SystemExit("system or OpenShift service CA bundle is not a PEM certificate")

    docker_config_dir = Path(
        os.environ.get("DOCKER_CONFIG", str(Path.home() / ".docker"))
    )
    docker_config_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    docker_config_dir.chmod(0o700)
    # OpenShift authenticates the token as the Basic-auth password; the
    # username is arbitrary and must be colon-free for Docker's auth format.
    username = "openshift-token"
    auth = base64.b64encode(f"{username}:{token}".encode("utf-8")).decode("ascii")
    config = {"auths": {registry_host: {"auth": auth}}}
    fd, config_tmp_name = tempfile.mkstemp(
        prefix=".config-", suffix=".json", dir=docker_config_dir
    )
    try:
        os.fchmod(fd, stat.S_IRUSR | stat.S_IWUSR)
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(config, stream, separators=(",", ":"))
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(config_tmp_name, docker_config_dir / "config.json")
    finally:
        if os.path.exists(config_tmp_name):
            os.unlink(config_tmp_name)

    tmp_dir = Path(os.environ.get("TMPDIR", "/tmp"))
    tmp_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, ca_tmp_name = tempfile.mkstemp(
        prefix="intrinsic-registry-ca-", suffix=".pem", dir=tmp_dir
    )
    try:
        os.fchmod(fd, stat.S_IRUSR | stat.S_IWUSR | stat.S_IRGRP | stat.S_IROTH)
        with os.fdopen(fd, "wb") as stream:
            for bundle in (system_ca, service_ca):
                stream.write(bundle)
                if not bundle.endswith(b"\n"):
                    stream.write(b"\n")
            stream.flush()
            os.fsync(stream.fileno())
    except BaseException:
        os.unlink(ca_tmp_name)
        raise

    # Only the temporary CA bundle path is printed. The service account token
    # is never included in diagnostics or command-line arguments.
    print(ca_tmp_name)


if __name__ == "__main__":
    main()
