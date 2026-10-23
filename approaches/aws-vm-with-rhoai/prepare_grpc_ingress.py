# This project was developed with assistance from AI tools.
"""Render isolated gRPC ingress resources; cert-manager generates keys in-cluster."""

import argparse
import os
from pathlib import Path
import re
import string

from hybrid_config import ConfigError, load


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
    parser.add_argument("--env-file", type=Path)
    parser.add_argument("--namespace")
    parser.add_argument("--host")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.env_file:
        if args.namespace or args.host:
            parser.error("Use --env-file or --namespace/--host, without mixing them")
        try:
            config = load(args.env_file)
        except ConfigError as exc:
            parser.error(str(exc))
        args.namespace, args.host = config.namespace, config.endpoint.rsplit(":", 1)[0]
    if not args.namespace or not args.host:
        parser.error("Select --env-file or supply --namespace and --host")
    prepare(args.namespace, args.host, args.output)
    print(
        "Prepared ingress manifest; no cluster resources changed or private keys generated."
    )


if __name__ == "__main__":
    main()
