# This project was developed with assistance from AI tools.
"""Validate canonical hybrid JSON without importing another topology's configuration."""

import argparse
from dataclasses import dataclass
import json
import os
from pathlib import Path
import shlex
import ssl
import subprocess
import tempfile
from urllib.parse import urlsplit

from jsonschema import Draft202012Validator

ROOT = Path(__file__).resolve().parent
SCHEMA_PATH = ROOT / "config.schema.json"
EXAMPLE_PATH = ROOT / "config.example.json"
DEFAULT_CONFIG_PATH = ROOT / "config.local.json"
PLACEHOLDERS = (
    "replace-me",
    "replace-with-",
    "your-",
    ".invalid",
    ".example",
    "{namespace}",
)


class ConfigError(ValueError):
    """Safe-to-print errors never include private configuration values."""


@dataclass(frozen=True, repr=False)
class HybridConfig:
    schema_version: int
    context: str
    api_server_url: str
    namespace: str
    route: str
    endpoint: str
    ca_file: Path
    account: str
    segmentation_model: str
    pose_model: str
    ssh_host: str
    ssh_key: Path


def unique_fields(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ConfigError("Duplicate JSON configuration field")
        result[key] = value
    return result


def read_json(path):
    try:
        return json.loads(path.read_text(), object_pairs_hook=unique_fields)
    except (OSError, UnicodeError, json.JSONDecodeError):
        raise ConfigError(
            "Could not read the selected hybrid JSON configuration"
        ) from None


def validate_schema(document):
    schema = read_json(SCHEMA_PATH)
    validator = Draft202012Validator(schema)
    error = next(validator.iter_errors(document), None)
    if error is not None:
        field = ".".join(str(part) for part in error.absolute_path) or "config"
        raise ConfigError(
            f"Hybrid schema validation failed at {field} ({error.validator})"
        )


def reject_placeholders(value):
    if isinstance(value, str) and any(marker in value for marker in PLACEHOLDERS):
        raise ConfigError("Hybrid runtime configuration contains placeholder values")
    if isinstance(value, dict):
        for nested in value.values():
            reject_placeholders(nested)


def load(path=DEFAULT_CONFIG_PATH, *, allow_placeholders=False, validate_files=True):
    """Use one explicitly selected JSON source; shell env and dotenv never select targets."""
    path = path.expanduser().resolve()
    document = read_json(path)
    validate_schema(document)
    if not allow_placeholders:
        reject_placeholders(document)
    local, target = document["local"], document["target"]
    rhoai = target["rhoai"]
    if target["namespace"].startswith(("openshift-", "kube-")):
        raise ConfigError("target.namespace must be an application namespace")
    if rhoai["models"]["segmentation"] == rhoai["models"]["pose"]:
        raise ConfigError("Segmentation and pose models must be distinct")
    try:
        api = urlsplit(local["api_server_url"])
        valid = (
            api.scheme == "https"
            and api.hostname
            and not api.username
            and not api.password
            and api.path in ("", "/")
            and not api.query
            and not api.fragment
            and not any(c.isspace() for c in local["api_server_url"])
        )
        if api.port is not None and not 1 <= api.port <= 65535:
            valid = False
    except ValueError:
        valid = False
    if not valid:
        raise ConfigError(
            "local.api_server_url must be an HTTPS API address without credentials"
        )
    paths = {}
    for field in ("rhoai_ca_file", "vm_ssh_key"):
        file = Path(local[field]).expanduser()
        paths[field] = (
            (path.parent / file).resolve() if not file.is_absolute() else file
        )
        if validate_files and not paths[field].is_file():
            raise ConfigError(f"Missing local file for {field}")
    if validate_files:
        try:
            if paths["vm_ssh_key"].stat().st_mode & 0o077:
                raise ConfigError(
                    "local.vm_ssh_key must be accessible only to its owner"
                )
            ssl.create_default_context(cafile=str(paths["rhoai_ca_file"]))
        except (OSError, ssl.SSLError):
            raise ConfigError(
                "Local SSH key or CA certificate is unavailable or invalid"
            ) from None
    return HybridConfig(
        schema_version=document["schema_version"],
        context=local["context"],
        api_server_url=local["api_server_url"],
        namespace=target["namespace"],
        route=rhoai["route"],
        endpoint=rhoai["endpoint"],
        ca_file=paths["rhoai_ca_file"],
        account=rhoai["client_account"],
        segmentation_model=rhoai["models"]["segmentation"],
        pose_model=rhoai["models"]["pose"],
        ssh_host=target["vm"]["ssh_host"],
        ssh_key=paths["vm_ssh_key"],
    )


def verify_context(config):
    """Bind credential issuance to both the canonical context and its approved API server."""
    try:
        result = subprocess.run(
            [
                "oc",
                "--context",
                config.context,
                "config",
                "view",
                "--minify",
                "-o",
                "jsonpath={.clusters[0].cluster.server}",
            ],
            capture_output=True,
            text=True,
            timeout=20,
        )
    except (OSError, subprocess.SubprocessError):
        raise ConfigError("Cannot verify the configured OpenShift context") from None
    if result.returncode or result.stdout.strip() != config.api_server_url:
        raise ConfigError(
            "Configured OpenShift context does not match its approved API server"
        )


def render_env(config):
    """Compatibility output is derived from canonical JSON, never read as a source."""
    values = {
        "HYBRID_CONFIG_VERSION": str(config.schema_version),
        "HYBRID_OPENSHIFT_CONTEXT": config.context,
        "HYBRID_OPENSHIFT_API_SERVER_URL": config.api_server_url,
        "HYBRID_RHOAI_NAMESPACE": config.namespace,
        "HYBRID_RHOAI_ROUTE": config.route,
        "HYBRID_RHOAI_ENDPOINT": config.endpoint,
        "HYBRID_RHOAI_CA_FILE": str(config.ca_file),
        "HYBRID_RHOAI_CLIENT_ACCOUNT": config.account,
        "HYBRID_RHOAI_SEGMENTATION_MODEL": config.segmentation_model,
        "HYBRID_RHOAI_POSE_MODEL": config.pose_model,
        "HYBRID_VM_SSH_HOST": config.ssh_host,
        "HYBRID_VM_SSH_KEY": str(config.ssh_key),
    }
    return (
        "# Generated by hybrid_config.py from canonical JSON. Do not edit.\n"
        + "".join(f"{key}={shlex.quote(value)}\n" for key, value in values.items())
    )


def write_env(config, output_root, *, check=False):
    output_root = output_root.expanduser().resolve()
    if output_root in (ROOT, ROOT.parents[1]):
        raise ConfigError("Use a separate private generated-output directory")
    path = output_root / ".env"
    content = render_env(config)
    if check:
        try:
            matches = path.read_text() == content
        except OSError:
            matches = False
        if not matches:
            raise ConfigError(
                "Generated hybrid env is missing or drifted from canonical JSON"
            )
        return
    output_root.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, temporary = tempfile.mkstemp(prefix=".env.", dir=output_root)
    try:
        with os.fdopen(fd, "w") as output:
            output.write(content)
        os.replace(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    parser.add_argument("--validate-only", action="store_true")
    parser.add_argument("--output-root", type=Path)
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--verify-context", action="store_true")
    parser.add_argument(
        "--allow-placeholders",
        action="store_true",
        help="Validate the published example only; runtime helpers reject placeholders",
    )
    args = parser.parse_args()
    if args.check and not args.output_root:
        parser.error("--check requires --output-root")
    if args.validate_only and args.output_root:
        parser.error("--validate-only does not write generated output")
    if args.allow_placeholders and not args.validate_only:
        parser.error("--allow-placeholders requires --validate-only")
    try:
        config = load(
            args.config,
            allow_placeholders=args.allow_placeholders,
            validate_files=False,
        )
        if args.verify_context:
            verify_context(config)
        if args.output_root:
            write_env(config, args.output_root, check=args.check)
    except ConfigError as exc:
        parser.exit(2, str(exc) + "\n")
    print("Hybrid JSON configuration validated; no resources changed.")
