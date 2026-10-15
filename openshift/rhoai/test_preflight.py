# This project was developed with assistance from AI tools.
"""Verify discovery's target guard, incomplete-evidence behavior, and privacy."""

import json
import subprocess
import unittest
from unittest.mock import patch

from preflight import DiscoveryError, Reader, Settings, discover, triton_candidates


SETTINGS = Settings("test-context", "test-app", "test-serving", "test-platform")


class FakeReader:
    def __init__(self, forbidden=(), ready=True, overrides=None):
        self.forbidden = forbidden
        self.ready = ready
        self.overrides = overrides or {}

    def get(self, resource, namespace=None, name=None):
        if resource in self.forbidden:
            raise DiscoveryError(
                "Discovery access denied; platform state is unverified."
            )
        if resource in self.overrides:
            return self.overrides[resource]
        if resource == "namespaces":
            return {"metadata": {"labels": {"opendatahub.io/dashboard": "true"}}}
        if resource == "statefulsets":
            return {
                "metadata": {"uid": "test-controller-uid", "generation": 2},
                "spec": {"replicas": 1},
                "status": {
                    "observedGeneration": 2,
                    "currentRevision": "test-revision",
                    "updateRevision": "test-revision",
                    "readyReplicas": 1,
                },
            }
        if resource == "pods":
            return {
                "items": [
                    {
                        "metadata": {
                            "name": "rs-inference-service-0",
                            "ownerReferences": [
                                {
                                    "kind": "StatefulSet",
                                    "controller": True,
                                    "uid": "test-controller-uid",
                                }
                            ],
                        },
                        "spec": {
                            "containers": [
                                {"image": "example.invalid/inference_service_image"},
                                {"image": "example.invalid/tritonserver"},
                            ]
                        },
                        "status": {
                            "phase": "Running",
                            "conditions": [
                                {
                                    "type": "Ready",
                                    "status": "True" if self.ready else "False",
                                }
                            ],
                        },
                    }
                ]
            }
        if resource == "customresourcedefinitions":
            return {
                "spec": {"versions": [{"name": "v1beta1", "served": True}]},
                "status": {"conditions": [{"type": "Established", "status": "True"}]},
            }
        if resource == "clusterversions":
            return {"status": {"desired": {"version": "0.0.0"}}}
        if resource == "clusterserviceversions":
            return {
                "items": [
                    {
                        "spec": {
                            "displayName": "Red Hat OpenShift AI",
                            "version": "0.0.0",
                        },
                        "status": {"phase": "Succeeded"},
                    }
                ]
            }
        if resource == "datascienceclusters":
            return {
                "items": [
                    {"spec": {"components": {"kserve": {"managementState": "Managed"}}}}
                ]
            }
        if resource == "templates":
            return {
                "items": [
                    {
                        "objects": [
                            {
                                "kind": "ServingRuntime",
                                "spec": {
                                    "supportedModelFormats": [{"name": "triton"}],
                                    "protocolVersions": ["grpc-v2"],
                                    "containers": [
                                        {
                                            "name": "kserve-container",
                                            "image": "example.invalid/triton@sha256:"
                                            + "a" * 64,
                                        }
                                    ],
                                },
                            }
                        ]
                    }
                ]
            }
        return {"items": []}

    def permission(self, verb, resource):
        return "allowed"


class PreflightTests(unittest.TestCase):
    def test_requires_explicit_targets_and_user_projects(self):
        for settings in (
            Settings("", "test-app", "test-serving", "test-platform"),
            Settings(
                "your-cluster-context", "test-app", "test-serving", "test-platform"
            ),
            Settings("test-context", "your-project", "test-serving", "test-platform"),
            Settings("test-context", "test-app", "openshift-serving", "test-platform"),
            Settings("test-context", "test-app", "test-platform", "test-platform"),
        ):
            with self.subTest(settings=settings), self.assertRaises(DiscoveryError):
                settings.validate()
        SETTINGS.validate()

    def test_query_has_explicit_context_namespace_and_no_mutation(self):
        result = subprocess.CompletedProcess([], 0, '{"items": []}', "")
        with patch("preflight.subprocess.run", return_value=result) as run:
            Reader(SETTINGS).get("pods", SETTINGS.application_namespace)
        args = run.call_args.args[0]
        self.assertEqual(args[:5], ["oc", "--context", "test-context", "get", "pods"])
        self.assertIn("test-app", args)
        self.assertIn("--request-timeout=15s", args)
        self.assertTrue(run.call_args.kwargs["capture_output"])

    def test_errors_suppress_server_identity_and_credentials(self):
        for detail in (
            "Unauthorized: token=<mock-token> server=private-endpoint",
            "Forbidden: user=private-identity",
            "Unable to connect: private-endpoint",
        ):
            with self.subTest(detail=detail):
                result = subprocess.CompletedProcess([], 1, "", detail)
                with patch("preflight.subprocess.run", return_value=result):
                    with self.assertRaises(DiscoveryError) as caught:
                        Reader(SETTINGS).get("pods", "test-app")
                self.assertNotIn("private-", str(caught.exception))

    def test_forbidden_discovery_is_a_blocker_not_platform_absence(self):
        report = discover(FakeReader(forbidden={"templates"}), SETTINGS)
        self.assertFalse(report["discovery_complete"])
        self.assertTrue(any("access denied" in b for b in report["blockers"]))

    def test_platform_success_does_not_claim_integration_complete(self):
        report = discover(FakeReader(), SETTINGS)
        self.assertTrue(report["discovery_complete"], report["blockers"])
        self.assertFalse(report["phase_0_complete"])
        self.assertFalse(report["candidate_deployment_ready"])
        self.assertTrue(report["required_evidence"])
        self.assertEqual(report["checks"]["rhoai_versions"], ["0.0.0"])
        text = json.dumps(report)
        self.assertNotIn(SETTINGS.context, text)
        self.assertNotIn(SETTINGS.application_namespace, text)
        self.assertNotIn(SETTINGS.serving_namespace, text)

    def test_unhealthy_native_baseline_blocks_progress(self):
        report = discover(FakeReader(ready=False), SETTINGS)
        self.assertFalse(report["discovery_complete"])
        self.assertTrue(any("baseline" in b for b in report["blockers"]))

    def test_runtime_output_does_not_echo_custom_names_or_images(self):
        report = triton_candidates(
            {},
            {
                "items": [
                    {
                        "metadata": {"name": "private-runtime-name"},
                        "spec": {
                            "supportedModelFormats": [{"name": "triton"}],
                            "protocolVersions": ["grpc-v2", "private-protocol"],
                            "containers": [
                                {
                                    "name": "kserve-container",
                                    "image": "private-registry/image:latest",
                                }
                            ],
                        },
                    }
                ]
            },
        )
        self.assertEqual(
            report, [{"protocols": ["grpc-v2"], "images_digest_pinned": False}]
        )
        self.assertNotIn("private-", json.dumps(report))

    def test_matching_pod_name_without_controller_ownership_cannot_pass(self):
        pods = FakeReader().get("pods")
        pods["items"][0]["metadata"]["ownerReferences"][0]["uid"] = (
            "unrelated-controller"
        )
        report = discover(FakeReader(overrides={"pods": pods}), SETTINGS)
        self.assertFalse(report["discovery_complete"])
        self.assertFalse(report["checks"]["native_baseline"]["baseline_ready"])

    def test_terminating_ready_pod_cannot_pass(self):
        pods = FakeReader().get("pods")
        pods["items"][0]["metadata"]["deletionTimestamp"] = "test-timestamp"
        report = discover(FakeReader(overrides={"pods": pods}), SETTINGS)
        self.assertFalse(report["discovery_complete"])

    def test_stale_or_scaled_down_statefulset_cannot_pass(self):
        for field, value in (
            ("observedGeneration", 1),
            ("updateRevision", "new-revision"),
            ("readyReplicas", 0),
        ):
            sts = FakeReader().get("statefulsets")
            sts["status"][field] = value
            with self.subTest(field=field):
                report = discover(FakeReader(overrides={"statefulsets": sts}), SETTINGS)
                self.assertFalse(report["discovery_complete"])

    def test_unestablished_or_unserved_crd_cannot_pass(self):
        for crd in (
            {},
            {"spec": {"versions": [{"name": "v1beta1", "served": False}]}},
            {"spec": {"versions": [{"name": "v1beta1", "served": True}]}},
        ):
            with self.subTest(crd=crd):
                report = discover(
                    FakeReader(overrides={"customresourcedefinitions": crd}), SETTINGS
                )
                self.assertFalse(report["discovery_complete"])

    def test_failed_or_ambiguous_operator_version_cannot_pass(self):
        csvs = FakeReader().get("clusterserviceversions")
        csvs["items"][0]["status"]["phase"] = "Failed"
        report = discover(
            FakeReader(overrides={"clusterserviceversions": csvs}), SETTINGS
        )
        self.assertFalse(report["discovery_complete"])
        csvs = FakeReader().get("clusterserviceversions")
        csvs["items"].append(
            {
                "spec": {"displayName": "OpenShift AI", "version": "0.0.1"},
                "status": {"phase": "Succeeded"},
            }
        )
        report = discover(
            FakeReader(overrides={"clusterserviceversions": csvs}), SETTINGS
        )
        self.assertFalse(report["discovery_complete"])

    def test_disabled_modelmesh_rest_only_or_malformed_runtime_is_not_candidate(self):
        for field, value in (
            ("disabled", True),
            ("multiModel", True),
            ("protocolVersions", ["v2"]),
            ("containers", []),
        ):
            templates = FakeReader().get("templates")
            templates["items"][0]["objects"][0]["spec"][field] = value
            with self.subTest(field=field):
                self.assertEqual(triton_candidates(templates, {}), [])

    def test_cleanup_permission_is_required_and_all_permissions_are_checked(self):
        reader = FakeReader()
        with patch.object(
            reader,
            "permission",
            side_effect=lambda verb, resource: (
                "denied" if verb == "delete" else "allowed"
            ),
        ) as permission:
            report = discover(reader, SETTINGS)
        self.assertFalse(report["discovery_complete"])
        self.assertEqual(permission.call_count, 12)

    def test_permission_denial_and_failed_authorization_query_are_distinct(self):
        for result, expected in (
            (subprocess.CompletedProcess([], 1, "no\n", ""), "denied"),
            (
                subprocess.CompletedProcess([], 1, "", "Forbidden: private-identity"),
                "unverified",
            ),
        ):
            with (
                self.subTest(expected=expected),
                patch("preflight.subprocess.run", return_value=result),
            ):
                self.assertEqual(
                    Reader(SETTINGS).permission("delete", "jobs.batch"), expected
                )

    def test_mid_discovery_authentication_failure_aborts(self):
        reader = FakeReader()
        original_get = reader.get

        def get(resource, namespace=None, name=None):
            if resource == "templates":
                raise DiscoveryError(
                    "Cluster authentication failed; renew the local oc login.",
                    "authentication",
                )
            return original_get(resource, namespace, name)

        with (
            patch.object(reader, "get", side_effect=get),
            patch.object(reader, "permission") as permission,
        ):
            with self.assertRaises(DiscoveryError) as caught:
                discover(reader, SETTINGS)
        self.assertEqual(caught.exception.category, "authentication")
        permission.assert_not_called()

    def test_certificate_expiry_is_not_reported_as_expired_login(self):
        result = subprocess.CompletedProcess(
            [], 1, "", "x509: certificate has expired: private-endpoint"
        )
        with patch("preflight.subprocess.run", return_value=result):
            with self.assertRaises(DiscoveryError) as caught:
                Reader(SETTINGS).get("pods", "test-app")
        self.assertEqual(caught.exception.category, "connection")
        self.assertNotIn("private-", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
