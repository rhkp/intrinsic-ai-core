import unittest

from validate_rendered_manifests import ValidationError, validate_documents


class RenderedManifestValidationTest(unittest.TestCase):
    namespace = "arhkp-intrinsic"
    image = "quay.io/rhkp/intrinsic/demo@sha256:" + "a" * 64

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
                        "route": [{"destination": {"host": "demo-service", "port": {"number": 8080}}}],
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
                        "route": [{"destination": {"host": "demo-service", "port": {"number": 8080}}}],
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
                "egress": [{
                    "to": [{
                        "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": self.namespace}},
                        "podSelector": {"matchLabels": {"app.kubernetes.io/name": "intrinsic-grpc-gateway"}},
                    }],
                    "ports": [{"protocol": "TCP", "port": 8080}],
                }],
            },
        }
        kinds, _ = validate_documents([self.deployment(), network_policy], self.namespace, {self.image})
        self.assertEqual(kinds["NetworkPolicy"], 1)

    def test_rejects_network_policy_with_flat_pod_selector_labels(self):
        network_policy = {
            "apiVersion": "networking.k8s.io/v1",
            "kind": "NetworkPolicy",
            "metadata": {"name": "invalid-egress", "namespace": self.namespace},
            "spec": {
                "podSelector": {"matchLabels": {"app": "skill"}},
                "policyTypes": ["Egress"],
                "egress": [{"to": [{"podSelector": {"app": "intrinsic-grpc-gateway"}}]}],
            },
        }
        with self.assertRaises(ValidationError):
            validate_documents([self.deployment(), network_policy], self.namespace, {self.image})

    def test_rejects_external_mesh_virtual_service_destination(self):
        virtual_service = {
            "apiVersion": "networking.istio.io/v1beta1",
            "kind": "VirtualService",
            "metadata": {"name": "external-route", "namespace": self.namespace},
            "spec": {
                "hosts": ["*"],
                "gateways": ["mesh-system/gateway"],
                "exportTo": [".", "mesh-system"],
                "http": [{"route": [{"destination": {"host": "outside.example.invalid"}}]}],
            },
        }
        with self.assertRaises(ValidationError):
            validate_documents(
                [self.deployment(), virtual_service], self.namespace, {self.image}
            )

    def test_rejects_host_access_fixed_uid_and_old_dns(self):
        hostpath = self.deployment()
        hostpath["spec"]["template"]["spec"]["volumes"] = [
            {"name": "host", "hostPath": {"path": "/tmp"}}
        ]
        fixed_uid = self.deployment()
        fixed_uid["spec"]["template"]["spec"]["securityContext"] = {
            "runAsUser": 1000
        }
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
