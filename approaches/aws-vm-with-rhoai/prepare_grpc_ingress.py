# This project was developed with assistance from AI tools.
"""Render isolated gRPC ingress resources; cert-manager generates keys in-cluster."""

import argparse
import os
from pathlib import Path
import re
import string

from hybrid_config import ConfigError, DEFAULT_CONFIG_PATH, load


def prepare(namespace: str, host: str, output: Path) -> None:
    label = r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?"
    if not re.fullmatch(label, namespace) or namespace.startswith(
        ("openshift-", "kube-")
    ):
        raise ValueError("Use a valid application namespace.")
    if len(host) > 253 or not re.fullmatch(rf"{label}(?:\.{label})+", host):
        raise ValueError(
            "Use a concrete DNS hostname; wildcard certificates are excluded."
        )
    # Keep live hostnames in a private, ignored output directory.
    output = output.resolve()
    output.mkdir(parents=True, exist_ok=False, mode=0o700)
    os.chmod(output, 0o700)
    old_umask = os.umask(0o077)
    try:
        template = Path(__file__).with_name("manifests") / "grpc-ingress.yaml"
        rendered = string.Template(template.read_text()).substitute(
            RHOAI_NAMESPACE=namespace, RHOAI_GRPC_HOST=host
        )
        (output / "ingress.yaml").write_text(rendered)
        (output / "endpoint").write_text(f"{host}:443\n")
    finally:
        os.umask(old_umask)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        # Certificate provisioning precedes the availability of client CA files.
        config = load(args.config, validate_files=False)
    except ConfigError as exc:
        parser.error(str(exc))
    prepare(config.namespace, config.endpoint.rsplit(":", 1)[0], args.output)
    print(
        "Prepared ingress manifest; no cluster resources changed or private keys generated."
    )


if __name__ == "__main__":
    main()
