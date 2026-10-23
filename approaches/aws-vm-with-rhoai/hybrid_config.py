# This project was developed with assistance from AI tools.
"""Explicit, isolated configuration contract for the working hybrid approach."""

import argparse
from dataclasses import dataclass
from pathlib import Path
import re
import shlex
import ssl


class ConfigError(ValueError):
    """Configuration errors identify fields, never their private values."""


FIELDS = {
    "HYBRID_CONFIG_VERSION": "version",
    "HYBRID_OPENSHIFT_CONTEXT": "context",
    "HYBRID_RHOAI_NAMESPACE": "namespace",
    "HYBRID_RHOAI_ROUTE": "route",
    "HYBRID_RHOAI_ENDPOINT": "endpoint",
    "HYBRID_RHOAI_CA_FILE": "ca_file",
    "HYBRID_RHOAI_CLIENT_ACCOUNT": "account",
    "HYBRID_RHOAI_SEGMENTATION_MODEL": "segmentation_model",
    "HYBRID_RHOAI_POSE_MODEL": "pose_model",
    "HYBRID_VM_SSH_HOST": "ssh_host",
    "HYBRID_VM_SSH_KEY": "ssh_key",
}


@dataclass(frozen=True)
class HybridConfig:
    version: str
    context: str
    namespace: str
    route: str
    endpoint: str
    ca_file: Path
    account: str
    segmentation_model: str
    pose_model: str
    ssh_host: str
    ssh_key: Path


def load(path: Path, *, validate_files=True) -> HybridConfig:
    """Read only the explicitly selected file; never source it or alter os.environ."""
    path = path.expanduser().resolve()
    try:
        text = path.read_text()
    except OSError:
        raise ConfigError("Cannot read the selected hybrid environment file") from None
    values = {}
    for number, line in enumerate(text.splitlines(), 1):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:].lstrip()
        key, separator, raw = line.partition("=")
        key = key.strip()
        if not separator or key not in FIELDS:
            raise ConfigError(f"Unknown hybrid field on line {number}")
        if key in values:
            raise ConfigError(f"Duplicate field: {key}")
        try:
            parts = shlex.split(raw, comments=True)
        except ValueError:
            raise ConfigError(f"Invalid quoting: {key}") from None
        if len(parts) != 1 or not parts[0] or any(c in parts[0] for c in "\r\n\x00"):
            raise ConfigError(f"Expected one nonempty value: {key}")
        values[key] = parts[0]
    missing = FIELDS.keys() - values.keys()
    if missing:
        raise ConfigError("Missing hybrid fields: " + ", ".join(sorted(missing)))
    if values["HYBRID_CONFIG_VERSION"] != "1":
        raise ConfigError("Unsupported HYBRID_CONFIG_VERSION")
    context = values["HYBRID_OPENSHIFT_CONTEXT"]
    if context.startswith("-") or any(c.isspace() for c in context):
        raise ConfigError("Invalid HYBRID_OPENSHIFT_CONTEXT")
    label = r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?"
    for key in (
        "HYBRID_RHOAI_NAMESPACE",
        "HYBRID_RHOAI_ROUTE",
        "HYBRID_RHOAI_CLIENT_ACCOUNT",
    ):
        if not re.fullmatch(label, values[key]):
            raise ConfigError(f"Invalid application resource name: {key}")
    if values["HYBRID_RHOAI_NAMESPACE"].startswith(("openshift-", "kube-")):
        raise ConfigError("HYBRID_RHOAI_NAMESPACE must be an application namespace")
    if not re.fullmatch(rf"{label}(?:\.{label})+:443", values["HYBRID_RHOAI_ENDPOINT"]):
        raise ConfigError(
            "HYBRID_RHOAI_ENDPOINT must be a DNS hostname:443, without an HTTPS URL or REST path"
        )
    for key in ("HYBRID_RHOAI_SEGMENTATION_MODEL", "HYBRID_RHOAI_POSE_MODEL"):
        if not re.fullmatch(r"[A-Za-z0-9_.-]+", values[key]):
            raise ConfigError(f"Invalid model name: {key}")
    if values["HYBRID_RHOAI_SEGMENTATION_MODEL"] == values["HYBRID_RHOAI_POSE_MODEL"]:
        raise ConfigError("Segmentation and pose models must be distinct")
    if not re.fullmatch(
        r"[A-Za-z0-9_.-]+@[A-Za-z0-9.-]+", values["HYBRID_VM_SSH_HOST"]
    ):
        raise ConfigError("HYBRID_VM_SSH_HOST must be one user@host destination")
    fields = {attribute: values[key] for key, attribute in FIELDS.items()}
    for attribute in ("ca_file", "ssh_key"):
        file = Path(fields[attribute]).expanduser()
        fields[attribute] = (
            (path.parent / file).resolve() if not file.is_absolute() else file
        )
        if validate_files and not fields[attribute].is_file():
            raise ConfigError(f"Missing file for {attribute}")
    if validate_files:
        if fields["ssh_key"].stat().st_mode & 0o077:
            raise ConfigError("ssh_key must be accessible only to its owner")
        try:
            ssl.create_default_context(cafile=str(fields["ca_file"]))
        except (OSError, ssl.SSLError):
            raise ConfigError("ca_file must contain a usable CA certificate") from None
    return HybridConfig(**fields)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", type=Path, required=True)
    args = parser.parse_args()
    try:
        load(args.env_file)
    except ConfigError as exc:
        parser.exit(2, str(exc) + "\n")
    print(
        "Hybrid configuration and local CA/key files validated; no resources changed."
    )
