# This project was developed with assistance from AI tools.
import unittest
import json
import tempfile
from pathlib import Path

from validate_rendered_manifests import (
    ValidationError,
    locked_references,
    validate_documents,
)


class RenderedManifestValidationTest(unittest.TestCase):
    namespace = "your-project-namespace"
    image = "quay.io/your-registry-org/intrinsic/demo@sha256:" + "a" * 64

    def deployment(self):
        return {
            "apiVersion": "apps/v1",
            "kind": "Deployment",
            "metadata": {"name": "demo", "namespace": self.namespace},
            "spec": {
                "template": {
                    "spec": {
                        "containers": [{"name": "demo", "image": self.image}],
                    }
                }
            },
        }

    def test_accepts_namespaced_digest_locked_workload(self):
        kinds, image_count = validate_documents(
            [self.deployment()], self.namespace, {self.image}
        )
        self.assertEqual(kinds["Deployment"], 1)
        self.assertEqual(image_count, 1)

    def test_rejects_cluster_scoped_and_secret_kinds(self):
        for kind in ("Namespace", "ClusterRole", "Secret"):
            with self.subTest(kind=kind):
                doc = {
                    "apiVersion": "v1",
                    "kind": kind,
                    "metadata": {"name": "unsafe"},
                }
                with self.assertRaises(ValidationError):
                    validate_documents([doc], self.namespace, {self.image})

    def test_rejects_wrong_project_and_unlocked_image(self):
        wrong_namespace = self.deployment()
        wrong_namespace["metadata"]["namespace"] = "another-project"
        with self.assertRaises(ValidationError):
            validate_documents([wrong_namespace], self.namespace, {self.image})

        with self.assertRaises(ValidationError):
            validate_documents([self.deployment()], self.namespace, set())

    def test_locked_references_include_openshift_destination(self):
        from render_config import EXAMPLE_PATH, load_config, load_image_lock

        config = load_config(EXAMPLE_PATH, True)
        lock = load_image_lock(config, EXAMPLE_PATH)
        refs = locked_references(lock, config["target"])
        image = next(
            image["openshift_image"]
            for image in lock["images"]
            if image.get("openshift_image")
        )
        expected = (
            config["target"]["registry"]["internal_host"]
            + "/"
            + self.namespace
            + "/"
            + image["name"]
            + "@"
            + image["digest"]
        )
        self.assertIn(expected, refs)

    def test_rejects_openshift_destination_outside_target_project(self):
        lock = {
            "registry_prefix": "quay.io/example/intrinsic",
            "images": [
                {
                    "quay_repository": "quay.io/example/intrinsic/demo",
                    "quay_digest": "sha256:" + "a" * 64,
                    "openshift_image_reference": (
                        "image-registry.openshift-image-registry.svc:5000/"
                        "another-project/demo@sha256:" + "a" * 64
                    ),
                }
            ],
            "project_images": [],
        }
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "image-lock.json"
            path.write_text(json.dumps(lock), encoding="utf-8")
            with self.assertRaises(ValidationError):
                locked_references(
                    lock,
                    {
                        "namespace": self.namespace,
                        "registry": {
                            "quay_prefix": "quay.io/example/intrinsic",
                            "internal_host": "registry.example.com:5000",
                        },
                    },
                )

    def test_locked_references_use_target_registry_composition(self):
        lock = {
            "schema_version": 2,
            "images": [
                {
                    "chart_image_key": "demo",
                    "chart_repository_basename": "demo",
                    "quay_digest": "sha256:" + "a" * 64,
                }
            ],
            "project_images": [{"name": "adapted", "digest": "sha256:" + "b" * 64}],
        }
        refs = locked_references(
            lock,
            {
                "namespace": self.namespace,
                "registry": {
                    "quay_prefix": "quay.io/example/intrinsic",
                    "internal_host": "registry.example.com:5000",
                },
            },
        )
        self.assertIn("quay.io/example/intrinsic/demo@sha256:" + "a" * 64, refs)
        self.assertIn(
            "registry.example.com:5000/"
            + self.namespace
            + "/adapted@sha256:"
            + "b" * 64,
            refs,
        )

    def test_rejects_repeated_repository_drift(self):
        base = {
            "registry_prefix": "quay.io/example/intrinsic",
            "images": [
                {
                    "chart_repository_basename": "demo",
                    "quay_repository": "quay.io/example/intrinsic/other",
                    "quay_digest": "sha256:" + "a" * 64,
                }
            ],
            "project_images": [],
        }
        cases = [
            ("chart", base),
            (
                "project",
                {
                    "registry_prefix": "quay.io/example/intrinsic",
                    "images": [],
                    "project_images": [
                        {
                            "name": "adapted",
                            "repository": "registry.example.com:5000/other-namespace/adapted",
                            "digest": "sha256:" + "b" * 64,
                        }
                    ],
                },
            ),
        ]
        for label, lock in cases:
            with self.subTest(label=label), tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / "image-lock.json"
                path.write_text(json.dumps(lock), encoding="utf-8")
                with self.assertRaises(ValidationError):
                    locked_references(
                        lock,
                        {
                            "namespace": self.namespace,
                            "registry": {
                                "quay_prefix": "quay.io/example/intrinsic",
                                "internal_host": "registry.example.com:5000",
                            },
                        },
                    )

    def test_accepts_project_scoped_mesh_virtual_service(self):
        virtual_service = {
            "apiVersion": "networking.istio.io/v1beta1",
            "kind": "VirtualService",
            "metadata": {"name": "resource-route", "namespace": self.namespace},
            "spec": {
                "hosts": ["*"],
                "gateways": ["mesh-system/gateway"],
                "exportTo": [".", "mesh-system"],
                "http": [
                    {
                        "match": [{"uri": {"prefix": "/demo.v1.Service/"}}],
                        "route": [
                            {
                                "destination": {
                                    "host": "demo-service",
                                    "port": {"number": 8080},
                                }
                            }
                        ],
                    }
                ],
            },
        }
        kinds, image_count = validate_documents(
            [self.deployment(), virtual_service], self.namespace, {self.image}
        )
        self.assertEqual(kinds["VirtualService"], 1)
        self.assertEqual(image_count, 1)

    def test_accepts_virtual_service_with_gateway_in_same_project(self):
        virtual_service = {
            "apiVersion": "networking.istio.io/v1beta1",
            "kind": "VirtualService",
            "metadata": {"name": "resource-route", "namespace": self.namespace},
            "spec": {
                "hosts": ["*"],
                "gateways": [f"{self.namespace}/intrinsic-grpc-internal"],
                "exportTo": ["."],
                "http": [
                    {
                        "match": [{"uri": {"prefix": "/demo.v1.Service/"}}],
                        "route": [
                            {
                                "destination": {
                                    "host": "demo-service",
                                    "port": {"number": 8080},
                                }
                            }
                        ],
                    }
                ],
            },
        }
        kinds, _ = validate_documents(
            [self.deployment(), virtual_service], self.namespace, {self.image}
        )
        self.assertEqual(kinds["VirtualService"], 1)

    def test_accepts_network_policy_with_valid_scoped_label_selectors(self):
        network_policy = {
            "apiVersion": "networking.k8s.io/v1",
            "kind": "NetworkPolicy",
            "metadata": {"name": "skill-egress", "namespace": self.namespace},
            "spec": {
                "podSelector": {"matchLabels": {"app": "skill"}},
                "policyTypes": ["Egress"],
                "egress": [
                    {
                        "to": [
                            {
                                "namespaceSelector": {
                                    "matchLabels": {
                                        "kubernetes.io/metadata.name": self.namespace
                                    }
                                },
                                "podSelector": {
                                    "matchLabels": {
                                        "app.kubernetes.io/name": "intrinsic-grpc-gateway"
                                    }
                                },
                            }
                        ],
                        "ports": [{"protocol": "TCP", "port": 8080}],
                    }
                ],
            },
        }
        kinds, _ = validate_documents(
            [self.deployment(), network_policy], self.namespace, {self.image}
        )
        self.assertEqual(kinds["NetworkPolicy"], 1)

    def test_rejects_network_policy_with_flat_pod_selector_labels(self):
        network_policy = {
            "apiVersion": "networking.k8s.io/v1",
            "kind": "NetworkPolicy",
            "metadata": {"name": "invalid-egress", "namespace": self.namespace},
            "spec": {
                "podSelector": {"matchLabels": {"app": "skill"}},
                "policyTypes": ["Egress"],
                "egress": [
                    {"to": [{"podSelector": {"app": "intrinsic-grpc-gateway"}}]}
                ],
            },
        }
        with self.assertRaises(ValidationError):
            validate_documents(
                [self.deployment(), network_policy], self.namespace, {self.image}
            )

    def test_rejects_external_mesh_virtual_service_destination(self):
        virtual_service = {
            "apiVersion": "networking.istio.io/v1beta1",
            "kind": "VirtualService",
            "metadata": {"name": "external-route", "namespace": self.namespace},
            "spec": {
                "hosts": ["*"],
                "gateways": ["mesh-system/gateway"],
                "exportTo": [".", "mesh-system"],
                "http": [
                    {"route": [{"destination": {"host": "outside.example.invalid"}}]}
                ],
            },
        }
        with self.assertRaises(ValidationError):
            validate_documents(
                [self.deployment(), virtual_service], self.namespace, {self.image}
            )

    def test_only_exact_simulated_realtime_container_may_run_as_root(self):
        doc = self.deployment()
        doc["kind"] = "StatefulSet"
        doc["metadata"]["name"] = "rs-ur-module"
        spec = doc["spec"]["template"]["spec"]
        spec.update(
            serviceAccountName="intrinsic-sim-realtime",
            automountServiceAccountToken=False,
        )
        container = spec["containers"][0]
        container.update(
            name="rs-ur-module",
            image="registry.example.com/project/ai.intrinsic.ur3e_hardware_module_core_service.gazebo_hwm_stub_image@sha256:"
            + "a" * 64,
            securityContext={
                "runAsUser": 0,
                "allowPrivilegeEscalation": False,
                "capabilities": {"add": ["IPC_LOCK"]},
            },
        )
        validate_documents([doc], self.namespace, {container["image"]})
        self.assertEqual(container["securityContext"]["runAsUser"], 0)
        for field, value in (
            ("serviceAccountName", "default"),
            ("automountServiceAccountToken", True),
        ):
            old = spec[field]
            spec[field] = value
            with self.assertRaises(ValidationError):
                validate_documents([doc], self.namespace, {container["image"]})
            spec[field] = old
        container["securityContext"]["capabilities"]["add"].append("SYS_ADMIN")
        with self.assertRaises(ValidationError):
            validate_documents([doc], self.namespace, {container["image"]})

    def test_rejects_host_access_fixed_uid_and_old_dns(self):
        hostpath = self.deployment()
        hostpath["spec"]["template"]["spec"]["volumes"] = [
            {"name": "host", "hostPath": {"path": "/tmp"}}
        ]
        fixed_uid = self.deployment()
        fixed_uid["spec"]["template"]["spec"]["securityContext"] = {"runAsUser": 1000}
        old_dns = self.deployment()
        old_dns["data"] = {
            "endpoint": "service.app-intrinsic-base.svc.cluster.local:80"
        }
        for doc in (hostpath, fixed_uid, old_dns):
            with self.subTest(name=doc["metadata"]["name"], data=doc.get("data")):
                with self.assertRaises(ValidationError):
                    validate_documents([doc], self.namespace, {self.image})


if __name__ == "__main__":
    unittest.main()
