#!/usr/bin/env python3
"""Sanitized readiness snapshot for deploying Intrinsic on dev01."""

from __future__ import annotations

import sys
from common import PilotError, TARGET_PROJECT, can_i, get_json, resolve_context


def main() -> int:
    try:
        access = resolve_context()
        namespace = get_json(access, "get", "namespace", TARGET_PROJECT, missing_ok=True)
        if not namespace:
            raise PilotError("Target project is absent; no deployment checks were run.")
        labels = namespace.get("metadata", {}).get("labels", {})
        dashboard = labels.get("opendatahub.io/dashboard") == "true"
        quotas = get_json(access, "get", "resourcequotas", "-n", TARGET_PROJECT) or {"items": []}
        limits = get_json(access, "get", "limitranges", "-n", TARGET_PROJECT) or {"items": []}
        policies = get_json(
            access, "get", "networkpolicies.networking.k8s.io", "-n", TARGET_PROJECT
        ) or {"items": []}
        pvs = get_json(access, "get", "persistentvolumeclaims", "-n", TARGET_PROJECT) or {"items": []}
        pods = get_json(access, "get", "pods", "-n", TARGET_PROJECT) or {"items": []}
        deployments = get_json(access, "get", "deployments.apps", "-n", TARGET_PROJECT) or {"items": []}
        jobs = get_json(access, "get", "jobs.batch", "-n", TARGET_PROJECT) or {"items": []}
        services = get_json(access, "get", "services", "-n", TARGET_PROJECT) or {"items": []}
        service_accounts = get_json(access, "get", "serviceaccounts", "-n", TARGET_PROJECT) or {"items": []}
        default_sa = next(
            (item for item in service_accounts.get("items", [])
             if item.get("metadata", {}).get("name") == "default"),
            {},
        )
        crds = get_json(access, "get", "customresourcedefinitions", missing_ok=True) or {"items": []}
        crd_names = {item.get("metadata", {}).get("name", "") for item in crds.get("items", [])}
        chartassignment_crd = any(
            name.startswith("chartassignments.") and name.endswith("cloudrobotics.com")
            for name in crd_names
        )
        api_version = get_json(access, "get", "clusterversion", "version")
        ocp_version = api_version.get("status", {}).get("desired", {}).get("version", "unknown")

        namespaced_checks = {
            "create pods": ("create", "pods"),
            "create deployments": ("create", "deployments.apps"),
            "create jobs": ("create", "jobs.batch"),
            "create services": ("create", "services"),
            "create PVCs": ("create", "persistentvolumeclaims"),
            "create NetworkPolicies": ("create", "networkpolicies.networking.k8s.io"),
            "create Routes": ("create", "routes.route.openshift.io"),
            "create RoleBindings": ("create", "rolebindings.rbac.authorization.k8s.io"),
        }
        cluster_checks = {
            "create CRDs": ("create", "customresourcedefinitions.apiextensions.k8s.io"),
            "create ClusterRoles": ("create", "clusterroles.rbac.authorization.k8s.io"),
            "create ClusterRoleBindings": ("create", "clusterrolebindings.rbac.authorization.k8s.io"),
            "create SCCs": ("create", "securitycontextconstraints.security.openshift.io"),
        }
        api_permissions = {
            label: can_i(access, verb, resource, TARGET_PROJECT)
            for label, (verb, resource) in namespaced_checks.items()
        }
        cluster_permissions = {
            label: can_i(access, verb, resource)
            for label, (verb, resource) in cluster_checks.items()
        }

        print(f"OpenShift version: {ocp_version}")
        print(f"Target project present: yes; RHOAI dashboard marker: {'yes' if dashboard else 'no'}")
        print(
            "Project policy objects: "
            f"quotas={len(quotas.get('items', []))}, "
            f"limit-ranges={len(limits.get('items', []))}, "
            f"network-policies={len(policies.get('items', []))}, "
            f"PVCs={len(pvs.get('items', []))}"
        )
        print(
            "Project workloads: "
            f"pods={len(pods.get('items', []))}, "
            f"deployments={len(deployments.get('items', []))}, "
            f"jobs={len(jobs.get('items', []))}, "
            f"services={len(services.get('items', []))}"
        )
        pull_secrets = default_sa.get("imagePullSecrets", [])
        print(f"Default ServiceAccount image-pull secret references: {len(pull_secrets)}")
        print(f"ChartAssignment CRD already installed: {'yes' if chartassignment_crd else 'no'}")
        print("Project-scoped create permissions:")
        for label, allowed in api_permissions.items():
            print(f"  {label}: {'yes' if allowed else 'no'}")
        print("Cluster-scoped create permissions (capability only; not approval):")
        for label, allowed in cluster_permissions.items():
            print(f"  {label}: {'yes' if allowed else 'no'}")
        print("No resources were created or changed. Context, identity, API address, secrets, and raw resources were not printed.")
        return 0
    except (PilotError, OSError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
