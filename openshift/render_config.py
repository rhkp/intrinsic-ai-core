#!/usr/bin/env python3
# This project was developed with assistance from AI tools.
"""Validate canonical OpenShift configuration and render compatibility artifacts."""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import re
import shlex
import sys
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

import yaml
from jsonschema import Draft202012Validator

from render_quay_image_values import render_values
from image_model import normalize_lock


ROOT = Path(__file__).resolve().parent
SCHEMA_PATH = ROOT / "config.schema.json"
EXAMPLE_PATH = ROOT / "config.example.json"
DEFAULT_CONFIG_PATH = ROOT / "config.local.json"

OUTPUT_PATHS = {
    "controller_deployment": Path(
        "deployment/chartassignment-controller/deploy/controller-deployment.yaml"
    ),
    "controller_rbac": Path(
        "deployment/chartassignment-controller/deploy/controller-rbac.yaml"
    ),
    "workspace": Path("deployment/omts-workspace/workspace.yaml"),
    "env": Path(".env"),
    "target_config": Path("target-config.json"),
    "configmap": Path(
        "deployment/chartassignment-controller/deploy/target-config.configmap.yaml"
    ),
    "quay_image_values": Path("deployment/quay-image-values.yaml"),
    "controller_patch": Path(
        "deployment/chartassignment-controller/deploy/controller-deployment-patch.yaml"
    ),
}

DNS_LABEL = re.compile(r"[a-z0-9](?:[-a-z0-9]*[a-z0-9])?")
CONTEXT = re.compile(r"[A-Za-z0-9@:._/-]+")
HOST = re.compile(r"[a-z0-9.-]+(?::[0-9]+)?")
quay_prefix_pattern = re.compile(r"quay\.io/[a-z0-9-]+/[a-z0-9._/-]+")
QUANTITY = re.compile(r"[0-9]+(?:\.[0-9]+)?(?:[EPTGMk]i?|m)?")
LABEL_KEY = re.compile(r"[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)?")
LABEL_VALUE = re.compile(r"[A-Za-z0-9_.-]+")
LOCK_PATH = re.compile(r"[A-Za-z0-9._/-]+")
DIGEST = re.compile(r"sha256:[0-9a-f]{64}")
INGRESS_ADDRESS = re.compile(
    r"([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)"
    r"\.([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)"
    r"\.svc\.cluster\.local:80"
)
INGRESS_GATEWAY = re.compile(
    r"([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)/"
    r"([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)"
)

PLACEHOLDER_MARKERS = ("your-", "replace-with-", "example.invalid")


class LiteralDumper(yaml.SafeDumper):
    """YAML dumper that preserves multi-line strings as readable block scalars."""


def represent_string(dumper: yaml.Dumper, data: str) -> yaml.Node:
    if "\n" in data:
        return dumper.represent_scalar("tag:yaml.org,2002:str", data, style="|")
    return dumper.represent_scalar("tag:yaml.org,2002:str", data)


LiteralDumper.add_representer(str, represent_string)


class ConfigError(ValueError):
    """A safe-to-print canonical configuration validation failure."""


def load_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ConfigError(f"could not read {path.name}") from exc
    if not isinstance(value, dict):
        raise ConfigError(f"{path.name} must contain a JSON object")
    return value


def validate_schema(config: dict[str, Any]) -> None:
    schema = load_json(SCHEMA_PATH)
    validator = Draft202012Validator(schema)
    errors = sorted(validator.iter_errors(config), key=lambda error: error.json_path)
    if errors:
        details = "; ".join(
            f"{error.json_path}: {error.message}" for error in errors[:5]
        )
        raise ConfigError(f"configuration schema validation failed: {details}")


def reject_placeholders(value: Any, path: str = "config") -> None:
    if isinstance(value, str):
        if any(marker in value for marker in PLACEHOLDER_MARKERS):
            raise ConfigError(f"{path} contains a placeholder value")
    elif isinstance(value, dict):
        for key, nested in value.items():
            reject_placeholders(nested, f"{path}.{key}")
    elif isinstance(value, list):
        for index, nested in enumerate(value):
            reject_placeholders(nested, f"{path}[{index}]")


def validate_host_port(value: str, field: str) -> None:
    host, separator, port = value.rpartition(":")
    if not separator or not host or not port.isdigit() or not 1 <= int(port) <= 65535:
        raise ConfigError(f"{field} must use host:port syntax")
    if not HOST.fullmatch(host):
        raise ConfigError(f"{field} host is invalid")


def validate_semantics(config: dict[str, Any], lock: dict[str, Any]) -> None:
    local = config["local"]
    target = config["target"]

    if not CONTEXT.fullmatch(local["context"]):
        raise ConfigError("local.context is invalid")
    parsed_url = urlsplit(local["api_server_url"])
    try:
        parsed_url.port
    except ValueError as exc:
        raise ConfigError("local.api_server_url has an invalid port") from exc
    if (
        parsed_url.scheme != "https"
        or not parsed_url.hostname
        or parsed_url.username is not None
        or parsed_url.password is not None
        or parsed_url.query
        or parsed_url.fragment
    ):
        raise ConfigError(
            "local.api_server_url must be an HTTPS URL without credentials"
        )

    namespace = target["namespace"]
    if not DNS_LABEL.fullmatch(namespace):
        raise ConfigError("target.namespace must be a Kubernetes DNS label")

    registry = target["registry"]
    if not quay_prefix_pattern.fullmatch(registry["quay_prefix"]):
        raise ConfigError("target.registry.quay_prefix is invalid")
    if not registry["quay_prefix"].startswith(f"quay.io/{registry['quay_org']}/"):
        raise ConfigError("target.registry.quay_prefix does not match quay_org")
    if not HOST.fullmatch(registry["internal_host"]):
        raise ConfigError("target.registry.internal_host is invalid")
    if lock.get("schema_version") != 2:
        raise ConfigError("image lock schema_version must be 2")

    storage = target["storage"]
    for field in ("block_class", "shared_filesystem_class"):
        if not DNS_LABEL.fullmatch(storage[field]):
            raise ConfigError(f"target.storage.{field} must be a Kubernetes DNS label")
    if not QUANTITY.fullmatch(storage["pvc_size"]):
        raise ConfigError("target.storage.pvc_size must be a Kubernetes quantity")

    scheduling = target["scheduling"]
    if not LABEL_KEY.fullmatch(scheduling["gpu_taint_key"]):
        raise ConfigError("target.scheduling.gpu_taint_key is invalid")

    networking = target["networking"]
    address_match = INGRESS_ADDRESS.fullmatch(networking["ingress_address"])
    if not address_match:
        raise ConfigError(
            "target.networking.ingress_address must be service.namespace.svc.cluster.local:80"
        )
    if address_match.group(2) != namespace:
        raise ConfigError(
            "target.networking.ingress_address is outside the target namespace"
        )
    gateway_match = INGRESS_GATEWAY.fullmatch(networking["ingress_gateway"])
    if not gateway_match:
        raise ConfigError(
            "target.networking.ingress_gateway must use namespace/name form"
        )
    if gateway_match.group(1) != namespace:
        raise ConfigError(
            "target.networking.ingress_gateway is outside the target namespace"
        )

    selector = networking["ingress_pod_selector"]
    if not selector:
        raise ConfigError("target.networking.ingress_pod_selector must not be empty")
    for key, value in selector.items():
        if not LABEL_KEY.fullmatch(key) or not LABEL_VALUE.fullmatch(value):
            raise ConfigError(
                "target.networking.ingress_pod_selector contains an invalid label"
            )

    validate_host_port(
        networking["zenoh_router_endpoint"],
        "target.networking.zenoh_router_endpoint",
    )
    validate_host_port(
        networking["rhoai_triton_endpoint"],
        "target.networking.rhoai_triton_endpoint",
    )

    lock_reference = target["images"]["lock"]
    if not LOCK_PATH.fullmatch(lock_reference):
        raise ConfigError("target.images.lock must be a relative file path")

    for field in ("mesh_namespace", "mesh_revision"):
        if not DNS_LABEL.fullmatch(networking[field]):
            raise ConfigError(f"target.networking.{field} must be a DNS label")
    try:
        normalize_lock(lock, target)
    except (ValueError, KeyError, TypeError) as exc:
        raise ConfigError("portable image lock is invalid") from exc


def load_config(path: Path, allow_placeholders: bool = False) -> dict[str, Any]:
    config = load_json(path)
    validate_schema(config)
    if not allow_placeholders:
        reject_placeholders(config)
    return expand_config(config)


def load_image_lock(config: dict[str, Any], config_path: Path) -> dict[str, Any]:
    lock_reference = config["target"]["images"]["lock"]
    lock_path = Path(lock_reference)
    if not lock_path.is_absolute():
        lock_path = config_path.parent / lock_path
    return load_json(lock_path)


def validate_config(
    config: dict[str, Any],
    lock: dict[str, Any],
    allow_placeholders: bool = False,
) -> None:
    config = expand_config(config)
    validate_schema(config)
    if not allow_placeholders:
        reject_placeholders(config)
        validate_semantics(config, lock)
    try:
        normalize_lock(lock, config["target"])
    except (ValueError, KeyError, TypeError) as exc:
        raise ConfigError("portable image lock is invalid") from exc


def expand_config(config: dict[str, Any]) -> dict[str, Any]:
    config = copy.deepcopy(config)
    target = config["target"]
    for field in (
        "ingress_address",
        "ingress_gateway",
        "zenoh_router_endpoint",
        "rhoai_triton_endpoint",
    ):
        target["networking"][field] = target["networking"][field].replace(
            "{namespace}", target["namespace"]
        )
    return config


def target_document(config: dict[str, Any]) -> dict[str, Any]:
    return {
        "schema_version": config["schema_version"],
        "target": config["target"],
    }


def render_target_config(config: dict[str, Any]) -> str:
    return json.dumps(target_document(config), sort_keys=True, indent=2) + "\n"


def checksum(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def canonical_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, indent=2) + "\n"


def env_value(value: str) -> str:
    if re.fullmatch(r"[A-Za-z0-9_./:@=-]+", value):
        return value
    return shlex.quote(value)


def render_env(config: dict[str, Any]) -> str:
    local = config["local"]
    target = config["target"]
    values = {
        "OPENSHIFT_CONTEXT": local["context"],
        "OPENSHIFT_NAMESPACE": target["namespace"],
        "OPENSHIFT_API_SERVER_URL": local["api_server_url"],
        "OPENSHIFT_GPU_TAINT_KEY": target["scheduling"]["gpu_taint_key"],
        "QUAY_REGISTRY_ORG": target["registry"]["quay_org"],
        "QUAY_REGISTRY_PREFIX": target["registry"]["quay_prefix"],
        "INTRINSIC_REGISTRY_HOST": target["registry"]["internal_host"],
        "INTRINSIC_GATEWAY_ADDRESS": target["networking"]["ingress_address"],
        "OPENSHIFT_STORAGE_CLASS": target["storage"]["block_class"],
        "OPENSHIFT_SHARED_STORAGE_CLASS": target["storage"]["shared_filesystem_class"],
        "PVC_SIZE": target["storage"]["pvc_size"],
        "INTRINSIC_INGRESS_ADDRESS": target["networking"]["ingress_address"],
        "INTRINSIC_INGRESS_GATEWAY": target["networking"]["ingress_gateway"],
        "INTRINSIC_INGRESS_POD_SELECTOR": json.dumps(
            target["networking"]["ingress_pod_selector"],
            sort_keys=True,
            separators=(",", ":"),
        ),
        "INTRINSIC_ZENOH_ROUTER_ENDPOINT": target["networking"][
            "zenoh_router_endpoint"
        ],
        "INTRINSIC_RHOAI_TRITON_ENDPOINT": target["networking"][
            "rhoai_triton_endpoint"
        ],
    }
    lines = ["# Generated by openshift/render_config.py. Do not edit."]
    lines.extend(f"{key}={env_value(value)}" for key, value in values.items())
    return "\n".join(lines) + "\n"


def render_configmap(
    config: dict[str, Any],
    target_config: str,
    target_config_checksum: str,
    image_lock: str,
    image_lock_checksum: str,
) -> str:
    document = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {
            "name": "intrinsic-target-config",
            "namespace": config["target"]["namespace"],
        },
        "data": {
            "target-config.json": target_config,
            "target-config.sha256": target_config_checksum,
            "image-lock.json": image_lock,
            "image-lock.sha256": image_lock_checksum,
        },
    }
    return yaml.dump(
        document,
        Dumper=LiteralDumper,
        sort_keys=True,
        default_flow_style=False,
    )


def render_controller_patch(
    config: dict[str, Any], controller_config_checksum: str
) -> str:
    namespace = config["target"]["namespace"]
    document = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {
            "name": "chartassignment-controller",
            "namespace": namespace,
            "annotations": {
                "intrinsic.ai/controller-config-checksum": controller_config_checksum,
            },
        },
        "spec": {
            "strategy": {
                "type": "RollingUpdate",
                "rollingUpdate": {"maxSurge": 0, "maxUnavailable": 1},
            },
            "template": {
                "metadata": {
                    "annotations": {
                        "intrinsic.ai/controller-config-checksum": controller_config_checksum,
                    }
                },
                "spec": {
                    "volumes": [
                        {
                            "name": "intrinsic-controller-config",
                            "configMap": {
                                "name": "intrinsic-target-config",
                            },
                        }
                    ],
                    "containers": [
                        {
                            "name": "controller",
                            "volumeMounts": [
                                {
                                    "name": "intrinsic-controller-config",
                                    "mountPath": "/etc/intrinsic/controller-config",
                                    "readOnly": True,
                                }
                            ],
                            "args": [
                                f"--watch-namespace={namespace}",
                                "--cloud-cluster=true",
                                "--webhook-enabled=false",
                                "--healthz-port=8080",
                                "--target-config=/etc/intrinsic/controller-config/target-config.json",
                                "--image-lock=/etc/intrinsic/controller-config/image-lock.json",
                            ],
                        }
                    ],
                },
            },
        },
    }
    return yaml.safe_dump(document, sort_keys=True, default_flow_style=False)


def render_template(text: str, config: dict, lock: dict) -> str:
    config = expand_config(config)
    values = dict(
        item.split("=", 1) for item in shlex.split(render_env(config), comments=True)
    )
    model = normalize_lock(lock, config["target"])
    for image in model["project_images"]:
        if image["name"] == "chartassignment-controller":
            values["CONTROLLER_IMAGE"] = f"{image['repository']}@{image['digest']}"

    def replace(match):
        if match[1] not in values:
            raise ConfigError("template requires an unknown canonical setting")
        return values[match[1]]

    return re.sub(r"\$\{([A-Z][A-Z0-9_]*)\}", replace, text)


def render_controller_deployment(config: dict, lock: dict, config_checksum: str) -> str:
    source = (
        ROOT / "deployment/chartassignment-controller/deploy/controller-deployment.yaml"
    )
    document = yaml.safe_load(render_template(source.read_text(), config, lock))
    patch = yaml.safe_load(render_controller_patch(config, config_checksum))
    document["metadata"].update(patch["metadata"])
    document["spec"]["strategy"] = patch["spec"]["strategy"]
    pod = document["spec"]["template"]
    pod["metadata"].update(patch["spec"]["template"]["metadata"])
    patch_pod = patch["spec"]["template"]["spec"]
    pod["spec"]["volumes"].extend(patch_pod["volumes"])
    container = pod["spec"]["containers"][0]
    container.pop("envFrom", None)
    settings = patch_pod["containers"][0]
    container["args"] = settings["args"]
    container["volumeMounts"].extend(settings["volumeMounts"])
    return yaml.safe_dump(document, sort_keys=True)


def render_rbac(config: dict, lock: dict) -> str:
    source = ROOT / "deployment/chartassignment-controller/deploy/controller-rbac.yaml"
    docs = list(yaml.safe_load_all(render_template(source.read_text(), config, lock)))
    for doc in docs:
        doc["metadata"]["namespace"] = config["target"]["namespace"]
    return yaml.safe_dump_all(docs, sort_keys=True)


def render_outputs(config: dict[str, Any], lock: dict[str, Any]) -> dict[str, str]:
    config = expand_config(config)
    target_config = render_target_config(config)
    image_lock = canonical_json(lock)
    target_config_checksum = checksum(target_config)
    image_lock_checksum = checksum(image_lock)
    controller_config_checksum = checksum(target_config + image_lock)
    outputs = {
        "controller_deployment": render_controller_deployment(
            config, lock, controller_config_checksum
        ),
        "controller_rbac": render_rbac(config, lock),
        "workspace": render_template(
            (ROOT / "deployment/omts-workspace/workspace.yaml").read_text(),
            config,
            lock,
        ),
        "env": render_env(config),
        "target_config": target_config,
        "configmap": render_configmap(
            config,
            target_config,
            target_config_checksum,
            image_lock,
            image_lock_checksum,
        ),
        "quay_image_values": render_values(normalize_lock(lock, config["target"])),
        "controller_patch": render_controller_patch(config, controller_config_checksum),
    }

    attribution = "# This project was developed with assistance from AI tools.\n"
    for key in outputs:
        if key != "target_config" and not outputs[key].startswith(attribution):
            outputs[key] = attribution + outputs[key]
    return outputs


def write_outputs(outputs: dict[str, str], output_root: Path) -> None:
    for key, relative_path in OUTPUT_PATHS.items():
        destination = output_root / relative_path
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(outputs[key], encoding="utf-8")
        destination.chmod(0o600)


def check_outputs(outputs: dict[str, str], output_root: Path) -> list[str]:
    mismatches: list[str] = []
    for key, relative_path in OUTPUT_PATHS.items():
        path = output_root / relative_path
        try:
            actual = path.read_text(encoding="utf-8")
        except OSError:
            mismatches.append(f"{relative_path}: missing")
            continue
        if actual != outputs[key]:
            mismatches.append(f"{relative_path}: drift")
    return mismatches


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    parser.add_argument("--output-root", type=Path, default=ROOT / ".generated")
    parser.add_argument("--allow-placeholders", action="store_true")
    parser.add_argument("--validate-only", action="store_true")
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()

    try:
        if not args.config.exists():
            raise ConfigError(
                f"{args.config.name} is missing; copy config.example.json and replace placeholders"
            )
        config = load_config(args.config, args.allow_placeholders)
        lock = load_image_lock(config, args.config)
        validate_config(config, lock, args.allow_placeholders)
        if args.validate_only:
            print("Canonical configuration and image lock validated.")
            return 0
        outputs = render_outputs(config, lock)
        if args.check:
            mismatches = check_outputs(outputs, args.output_root)
            if mismatches:
                print("Canonical configuration output drift:", file=sys.stderr)
                for mismatch in mismatches:
                    print(f"  {mismatch}", file=sys.stderr)
                return 1
            print("Canonical configuration outputs match.")
            return 0
        write_outputs(outputs, args.output_root)
    except ConfigError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 2

    print(f"Rendered {len(outputs)} canonical configuration artifacts.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
