#!/usr/bin/env python3
"""Create or verify only the pilot project; never writes kubeconfig."""

from __future__ import annotations

import argparse
import sys

from common import PilotError, TARGET_PROJECT, get_json, main_error, resolve_context


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--context-fragment",
        default="dev01",
        help="Local context-name hint; the selected context is never printed.",
    )
    parser.add_argument(
        "--apply",
        action="store_true",
        help="Create the project when absent. Without this flag, the script is read-only.",
    )
    args = parser.parse_args()
    try:
        access = resolve_context(args.context_fragment)
        if not get_json(access, "get", "clusterversion", "version"):
            raise PilotError("Could not verify the selected cluster before project bootstrap.")

        existing = get_json(access, "get", "project", TARGET_PROJECT, missing_ok=True)
        namespace = get_json(access, "get", "namespace", TARGET_PROJECT, missing_ok=True)
        dashboard_marked = (
            (namespace or {}).get("metadata", {}).get("labels", {}).get("opendatahub.io/dashboard")
            == "true"
        )
        if existing and dashboard_marked:
            print(f"Project {TARGET_PROJECT} exists with the RHOAI dashboard marker; no changes made.")
            return 0
        if not args.apply:
            if existing:
                print(f"Project {TARGET_PROJECT} exists without the RHOAI dashboard marker; re-run with --apply to add it.")
            else:
                print(f"Project {TARGET_PROJECT} is absent. Re-run with --apply to create only this project.")
            return 0

        if namespace and "opendatahub.io/dashboard" in namespace.get("metadata", {}).get("labels", {}):
            raise PilotError("The existing project has a conflicting dashboard marker; it was left unchanged.")

        label_permission = access.oc("auth", "can-i", "patch", "namespaces", check=False)
        if label_permission.returncode != 0 or label_permission.stdout.strip().casefold() != "yes":
            raise PilotError("The selected identity cannot add the project dashboard marker.")
        if not existing:
            create_permission = access.oc(
                "auth", "can-i", "create", "projectrequests.project.openshift.io", check=False
            )
            if create_permission.returncode != 0 or create_permission.stdout.strip().casefold() != "yes":
                raise PilotError("The selected identity cannot create an OpenShift project.")

        if not existing:
            result = access.oc(
                "new-project",
                TARGET_PROJECT,
                "--skip-config-write=true",
                "--display-name=Intrinsic RHOAI hybrid pilot",
                "--description=Project-scoped resources for the Intrinsic AWS VM and RHOAI pilot",
                check=False,
            )
            if result.returncode != 0:
                raise PilotError("OpenShift did not create the pilot project; raw CLI output was suppressed.")
        # This is the namespace marker observed on existing RHOAI dashboard
        # projects in the selected cluster. Do not overwrite a conflicting value.
        result = access.oc(
            "label", "namespace", TARGET_PROJECT, "opendatahub.io/dashboard=true", check=False
        )
        if result.returncode != 0:
            raise PilotError("Could not mark the project for RHOAI dashboard discovery; raw output was suppressed.")
        verified = get_json(access, "get", "project", TARGET_PROJECT, missing_ok=True)
        verified_namespace = get_json(access, "get", "namespace", TARGET_PROJECT, missing_ok=True)
        marked = (
            (verified_namespace or {}).get("metadata", {}).get("labels", {}).get("opendatahub.io/dashboard")
            == "true"
        )
        if not verified or not marked:
            raise PilotError("Project creation returned success but verification did not find the project.")
        action = "Created" if not existing else "Updated"
        print(f"{action} and verified project {TARGET_PROJECT} with the RHOAI dashboard marker; kubeconfig was not changed.")
        print("No quota, policy, ServingRuntime, InferenceService, route, or endpoint was created.")
        return 0
    except (PilotError, OSError) as exc:
        return main_error(exc)


if __name__ == "__main__":
    sys.exit(main())
