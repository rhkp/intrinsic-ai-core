#!/usr/bin/env python3
"""Read-only, low-output inventory for the AWS VM + RHOAI pilot."""

from __future__ import annotations

import argparse
import sys
from collections import defaultdict
from typing import Any

from common import PilotError, TARGET_PROJECT, get_json, main_error, resolve_context


def quantity(value: Any) -> int:
    try:
        return int(value or 0)
    except (TypeError, ValueError):
        return 0


def gpu_request(pod: dict[str, Any]) -> int:
    """Estimate effective integer GPU requests across regular/init containers."""
    spec = pod.get("spec", {})

    def container_request(container: dict[str, Any]) -> int:
        resources = container.get("resources", {})
        return quantity(
            resources.get("requests", {}).get("nvidia.com/gpu")
            or resources.get("limits", {}).get("nvidia.com/gpu")
        )

    regular = sum(container_request(item) for item in spec.get("containers", []))
    init = max((container_request(item) for item in spec.get("initContainers", [])), default=0)
    return max(regular, init)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--context-fragment",
        default="dev01",
        help="Local context-name hint; the selected context is never printed.",
    )
    args = parser.parse_args()
    try:
        access = resolve_context(args.context_fragment)
        version = get_json(access, "get", "clusterversion", "version")
        if not version:
            raise PilotError("Could not read the OpenShift cluster version.")
        ocp_version = version.get("status", {}).get("desired", {}).get("version", "unknown")

        dsc = get_json(access, "get", "datascienceclusters", missing_ok=True) or {"items": []}
        dsc_items = dsc.get("items", [])
        kserve_state = "not detected"
        if dsc_items:
            kserve_state = dsc_items[0].get("spec", {}).get("components", {}).get("kserve", {}).get(
                "managementState", "not specified"
            )

        rhoai_versions: set[str] = set()
        for ns in ("redhat-ods-operator", "redhat-ods-applications"):
            csvs = get_json(access, "get", "clusterserviceversions", "-n", ns, missing_ok=True)
            for csv in (csvs or {}).get("items", []):
                name = csv.get("metadata", {}).get("name", "").casefold()
                value = csv.get("spec", {}).get("version", "")
                if value and any(token in name for token in ("rhods", "openshift-ai", "rhoai")):
                    rhoai_versions.add(value)

        runtime_list = get_json(
            access, "get", "servingruntimes.serving.kserve.io", "-A", missing_ok=True
        ) or {"items": []}
        runtimes = runtime_list.get("items", [])
        has_triton = any("triton" in item.get("metadata", {}).get("name", "").casefold() for item in runtimes)

        project = get_json(access, "get", "project", TARGET_PROJECT, missing_ok=True)
        project_namespace = get_json(access, "get", "namespace", TARGET_PROJECT, missing_ok=True)
        dashboard_marked = (
            (project_namespace or {}).get("metadata", {}).get("labels", {}).get("opendatahub.io/dashboard")
            == "true"
        )
        quotas = (
            get_json(access, "get", "resourcequotas", "-n", TARGET_PROJECT, missing_ok=True)
            if project
            else None
        )
        gpu_quota: list[str] = []
        for quota in (quotas or {}).get("items", []):
            hard = quota.get("status", {}).get("hard", {})
            used = quota.get("status", {}).get("used", {})
            for key in ("requests.nvidia.com/gpu", "limits.nvidia.com/gpu"):
                if key in hard:
                    gpu_quota.append(f"{key}: {used.get(key, '0')} / {hard[key]}")

        nodes = get_json(access, "get", "nodes") or {"items": []}
        allocatable: dict[str, int] = defaultdict(int)
        node_products: dict[str, str] = {}
        for node in nodes.get("items", []):
            metadata = node.get("metadata", {})
            labels = metadata.get("labels", {})
            product = labels.get("nvidia.com/gpu.product", "unspecified-product")
            node_products[metadata.get("name", "")] = product
            allocatable[product] += quantity(
                node.get("status", {}).get("allocatable", {}).get("nvidia.com/gpu")
            )

        # Keep the complete Pod API response in memory only. Extract scheduling
        # fields and GPU quantities; never print or persist raw Pod specs.
        pods = get_json(access, "get", "pods", "-A") or {"items": []}
        requested: dict[str, int] = defaultdict(int)
        for pod in pods.get("items", []):
            node_name = pod.get("spec", {}).get("nodeName")
            phase = pod.get("status", {}).get("phase")
            if node_name and phase not in ("Succeeded", "Failed"):
                requested[node_products.get(node_name, "unspecified-product")] += gpu_request(pod)

        classes = get_json(access, "get", "storageclasses") or {"items": []}
        storage_classes = sorted(
            item.get("metadata", {}).get("name", "")
            for item in classes.get("items", [])
            if item.get("metadata", {}).get("name")
        )

        rbac: dict[str, str] = {}
        if project:
            for resource in (
                "servingruntimes.serving.kserve.io",
                "inferenceservices.serving.kserve.io",
                "persistentvolumeclaims",
                "resourcequotas",
                "networkpolicies.networking.k8s.io",
            ):
                result = access.oc(
                    "auth", "can-i", "create", resource, "-n", TARGET_PROJECT, check=False
                )
                rbac[resource] = (
                    "yes"
                    if result.returncode == 0 and result.stdout.strip().casefold() == "yes"
                    else "no/unknown"
                )

        print(f"OpenShift version: {ocp_version}")
        print(f"RHOAI CSV version(s): {', '.join(sorted(rhoai_versions)) if rhoai_versions else 'not detected'}")
        print(f"RHOAI KServe management state: {kserve_state}")
        print(f"ServingRuntime resources visible: {len(runtimes)}")
        print(f"Triton ServingRuntime detected: {'yes' if has_triton else 'no'}")
        print(f"Project {TARGET_PROJECT}: {'present' if project else 'absent'}")
        print(f"RHOAI dashboard project marker: {'present' if dashboard_marked else 'absent'}")
        print(f"Project GPU quota: {'; '.join(gpu_quota) if gpu_quota else 'none detected'}")
        if rbac:
            print("Namespace-scoped create permissions:")
            for resource, allowed in rbac.items():
                print(f"  {resource}: {allowed}")
        print("GPU inventory (allocatable / scheduled request / estimated free):")
        for product in sorted(
            key for key in (set(allocatable) | set(requested)) if allocatable[key] or requested[key]
        ):
            total, used = allocatable[product], requested[product]
            print(f"  {product}: {total} / {used} / {max(0, total - used)}")
        print(f"StorageClasses: {', '.join(storage_classes) if storage_classes else 'none detected'}")
        print("Read-only: no cluster objects changed; context, identity, API address, and raw resources were not printed.")
        return 0
    except (PilotError, OSError) as exc:
        return main_error(exc)


if __name__ == "__main__":
    sys.exit(main())
