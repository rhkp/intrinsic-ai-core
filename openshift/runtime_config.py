# This project was developed with assistance from AI tools.
"""Validated canonical settings for local operational commands."""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from common import ClusterAccess, PilotError
from render_config import (
    DEFAULT_CONFIG_PATH,
    ConfigError,
    load_config,
    load_image_lock,
    validate_config,
)


@dataclass(frozen=True)
class RuntimeTarget:
    document: dict[str, Any] = field(repr=False)

    @property
    def namespace(self) -> str:
        return self.document["target"]["namespace"]

    @property
    def ingress_selector(self) -> dict[str, str]:
        return dict(self.document["target"]["networking"]["ingress_pod_selector"])

    def connect(self) -> ClusterAccess:
        """Bind to the exact configured context and verify its API address."""
        local = self.document["local"]
        access = ClusterAccess(local["context"])
        try:
            result = access.oc(
                "config",
                "view",
                "--minify",
                "-o",
                "jsonpath={.clusters[0].cluster.server}",
                check=False,
            )
        except OSError as exc:
            raise PilotError(
                "The OpenShift CLI could not be executed; details suppressed."
            ) from exc
        if result.returncode:
            raise PilotError(
                "The canonical cluster context is unavailable; details suppressed."
            )
        if result.stdout.strip() != local["api_server_url"]:
            raise PilotError(
                "The canonical context does not match the approved API address."
            )
        return access

    def routing_values(self) -> dict[str, str]:
        target = self.document["target"]
        return {
            "INTRINSIC_INGRESS_ADDRESS": target["networking"]["ingress_address"],
            "INTRINSIC_INGRESS_GATEWAY": target["networking"]["ingress_gateway"],
            "OPENSHIFT_STORAGE_CLASS": target["storage"]["block_class"],
            "OPENSHIFT_SHARED_STORAGE_CLASS": target["storage"][
                "shared_filesystem_class"
            ],
        }

    def routing_configmap(self, selector: dict[str, str]) -> dict[str, Any]:
        return {
            "apiVersion": "v1",
            "kind": "ConfigMap",
            "metadata": {
                "name": "intrinsic-routing-config",
                "namespace": self.namespace,
            },
            "data": {
                **self.routing_values(),
                "INTRINSIC_INGRESS_POD_SELECTOR": json.dumps(
                    selector,
                    sort_keys=True,
                    separators=(",", ":"),
                ),
            },
        }


def load_runtime_target(path: Path = DEFAULT_CONFIG_PATH) -> RuntimeTarget:
    """Validate the complete target and lock before any cluster command runs."""
    try:
        document = load_config(path)
        lock = load_image_lock(document, path)
        validate_config(document, lock)
    except (ConfigError, OSError) as exc:
        raise PilotError(
            "Canonical configuration or image lock is missing or invalid; "
            "validate the ignored local configuration before continuing."
        ) from exc
    return RuntimeTarget(document)
