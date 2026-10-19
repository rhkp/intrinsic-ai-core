# This project was developed with assistance from AI tools.
import copy
import hashlib
import json
import tempfile
import unittest
from pathlib import Path

import yaml

from render_config import (
    EXAMPLE_PATH,
    ConfigError,
    check_outputs,
    load_config,
    load_image_lock,
    render_outputs,
    validate_config,
    validate_schema,
    write_outputs,
)


GOLDEN_DIR = Path(__file__).resolve().parent / "testdata" / "config-render"


def example_config() -> dict:
    config = load_config(EXAMPLE_PATH, allow_placeholders=True)
    lock = load_image_lock(config, EXAMPLE_PATH)
    validate_config(config, lock, allow_placeholders=True)
    return config


def realistic_config() -> dict:
    return {
        "schema_version": 1,
        "local": {
            "context": "example-context",
            "api_server_url": "https://api.example.com:6443",
        },
        "target": {
            "namespace": "example-namespace",
            "registry": {
                "quay_prefix": "quay.io/example-org/intrinsic",
                "quay_org": "example-org",
                "internal_host": "registry.example.com:5000",
            },
            "storage": {
                "block_class": "example-block",
                "shared_filesystem_class": "example-shared",
                "pvc_size": "20Gi",
            },
            "scheduling": {"gpu_taint_key": "example-gpu"},
            "networking": {
                "mesh_namespace": "mesh-system",
                "mesh_revision": "mesh",
                "ingress_address": (
                    "intrinsic-grpc-gateway.example-namespace.svc.cluster.local:80"
                ),
                "ingress_gateway": "example-namespace/intrinsic-grpc-internal",
                "ingress_pod_selector": {
                    "app.kubernetes.io/name": "intrinsic-grpc-gateway",
                    "istio": "intrinsic-grpc-gateway",
                },
                "zenoh_router_endpoint": (
                    "zenoh-router.example-namespace.svc.cluster.local:7447"
                ),
                "rhoai_triton_endpoint": (
                    "triton.example-namespace.svc.cluster.local:8000"
                ),
            },
            "images": {"lock": "image-lock.json"},
        },
    }


def realistic_lock() -> dict:
    return {
        "schema_version": 2,
        "images": [],
        "project_images": [
            {
                "name": "chartassignment-controller",
                "digest": "sha256:" + "a" * 64,
            }
        ],
    }


def set_case(config: dict, path: tuple, value) -> None:
    target = config
    for key in path[:-2]:
        target = target[key]
    target[path[-2]] = value


class RenderConfigTest(unittest.TestCase):
    def setUp(self):
        self.config = example_config()
        self.lock = load_image_lock(self.config, EXAMPLE_PATH)
        self.outputs = render_outputs(self.config, self.lock)

    def test_schema_accepts_example(self):
        validate_schema(self.config)

    def test_schema_rejects_unknown_fields(self):
        config = copy.deepcopy(self.config)
        config["unexpected"] = True
        with self.assertRaises(ConfigError):
            validate_schema(config)

    def test_runtime_mode_rejects_placeholders(self):
        with self.assertRaises(ConfigError):
            load_config(EXAMPLE_PATH)

    def test_semantic_validation_accepts_realistic_config(self):
        validate_config(realistic_config(), realistic_lock())

    def test_semantic_validation_rejects_invalid_values(self):
        cases = {
            "namespace": ("target", "namespace", "invalid_namespace"),
            "api_url": ("local", "api_server_url", "http://api.example.com"),
            "quay_prefix": (
                "target",
                "registry",
                "quay_prefix",
                "docker.io/example/intrinsic",
            ),
            "storage_class": (
                "target",
                "storage",
                "block_class",
                "invalid/class",
            ),
            "pvc_size": ("target", "storage", "pvc_size", "20X"),
            "gpu_taint": ("target", "scheduling", "gpu_taint_key", "invalid key"),
            "ingress_address": (
                "target",
                "networking",
                "ingress_address",
                "gateway.other-namespace.svc.cluster.local:80",
            ),
            "ingress_gateway": (
                "target",
                "networking",
                "ingress_gateway",
                "other-namespace/gateway",
            ),
            "selector": (
                "target",
                "networking",
                "ingress_pod_selector",
                {"invalid key": "value"},
            ),
            "zenoh": (
                "target",
                "networking",
                "zenoh_router_endpoint",
                "not a host",
            ),
            "rhoai": (
                "target",
                "networking",
                "rhoai_triton_endpoint",
                "missing-port",
            ),
        }
        for label, path in cases.items():
            with self.subTest(label=label):
                config = realistic_config()
                set_case(config, path, path[-1])
                with self.assertRaises(ConfigError):
                    validate_config(config, realistic_lock())

    def test_image_lock_must_match_registry_prefix(self):
        lock = realistic_lock()
        lock["registry_prefix"] = "quay.io/other-org/intrinsic"
        with self.assertRaises(ConfigError):
            validate_config(realistic_config(), lock)

    def test_project_images_must_use_target_namespace(self):
        lock = realistic_lock()
        lock["project_images"][0]["repository"] = (
            "registry.example.com:5000/other-namespace/demo"
        )
        with self.assertRaises(ConfigError):
            validate_config(realistic_config(), lock)

    def test_image_lock_repositories_must_use_normalized_composition(self):
        cases = {
            "chart": (
                "images",
                "quay_repository",
                "quay.io/example-org/intrinsic/other",
            ),
            "project": (
                "project_images",
                "repository",
                "registry.example.com:5000/example-namespace/other",
            ),
        }
        for label, (collection, field, value) in cases.items():
            with self.subTest(label=label):
                lock = realistic_lock()
                if collection == "images":
                    lock["images"] = [
                        {
                            "chart_repository_basename": "demo",
                            "quay_repository": value,
                            "quay_digest": "sha256:" + "a" * 64,
                        }
                    ]
                else:
                    lock[collection][0][field] = value
                with self.assertRaises(ConfigError):
                    validate_config(realistic_config(), lock)

    def test_controller_overrides_require_source_images(self):
        lock = realistic_lock()
        lock["project_images"][0]["controller_override"] = True
        with self.assertRaises(ConfigError):
            validate_config(realistic_config(), lock)

    def test_namespace_only_change_updates_all_generated_references(self):
        config = realistic_config()
        original = config["target"]["namespace"]
        for field in (
            "ingress_address",
            "ingress_gateway",
            "zenoh_router_endpoint",
            "rhoai_triton_endpoint",
        ):
            config["target"]["networking"][field] = config["target"]["networking"][
                field
            ].replace(original, "{namespace}")
        lock = realistic_lock()
        first = render_outputs(config, lock)
        config["target"]["namespace"] = "relocated-namespace"
        validate_config(config, lock)
        second = render_outputs(config, lock)
        for key in first:
            with self.subTest(key=key):
                self.assertNotIn(original, second[key])
        deployment = yaml.safe_load(second["controller_deployment"])
        container = deployment["spec"]["template"]["spec"]["containers"][0]
        self.assertIn("/relocated-namespace/", container["image"])
        self.assertIn("--watch-namespace=relocated-namespace", container["args"])
        for doc in yaml.safe_load_all(second["controller_rbac"]):
            self.assertEqual(doc["metadata"]["namespace"], "relocated-namespace")
            for subject in doc.get("subjects", []):
                self.assertEqual(subject["namespace"], "relocated-namespace")
        self.assertIn(
            "gateway.relocated-namespace.svc.cluster.local:80",
            second["target_config"].replace("intrinsic-grpc-gateway", "gateway"),
        )
        self.assertNotEqual(first["controller_patch"], second["controller_patch"])

    def test_generated_controller_is_single_reconciler_and_has_no_env_fallback(self):
        deployment = yaml.safe_load(self.outputs["controller_deployment"])
        self.assertEqual(
            deployment["spec"]["strategy"]["rollingUpdate"],
            {"maxSurge": 0, "maxUnavailable": 1},
        )
        container = deployment["spec"]["template"]["spec"]["containers"][0]
        self.assertNotIn("envFrom", container)
        self.assertIn("@sha256:", container["image"])

    def test_outputs_are_deterministic(self):
        second = render_outputs(self.config, self.lock)
        self.assertEqual(self.outputs, second)

    def test_outputs_match_golden_files(self):
        for key, output in self.outputs.items():
            with self.subTest(key=key):
                golden = (GOLDEN_DIR / f"{key}.golden").read_text(encoding="utf-8")
                self.assertEqual(output, golden)

    def test_target_config_excludes_local_values(self):
        document = json.loads(self.outputs["target_config"])
        self.assertNotIn("local", document)
        self.assertNotIn("context", json.dumps(document))
        self.assertNotIn("api_server_url", json.dumps(document))

    def test_env_contains_expected_keys(self):
        lines = self.outputs["env"].splitlines()
        keys = [line.partition("=")[0] for line in lines if "=" in line]
        self.assertIn("OPENSHIFT_CONTEXT", keys)
        self.assertIn("OPENSHIFT_NAMESPACE", keys)
        self.assertIn("INTRINSIC_INGRESS_POD_SELECTOR", keys)
        self.assertIn("INTRINSIC_ZENOH_ROUTER_ENDPOINT", keys)
        self.assertIn("INTRINSIC_RHOAI_TRITON_ENDPOINT", keys)

    def test_configmap_contains_only_deployment_values(self):
        document = yaml.safe_load(self.outputs["configmap"])
        self.assertEqual(document["kind"], "ConfigMap")
        self.assertEqual(
            document["metadata"]["namespace"],
            self.config["target"]["namespace"],
        )
        self.assertIn("target-config.json", document["data"])
        self.assertIn("target-config.sha256", document["data"])
        self.assertIn("image-lock.json", document["data"])
        self.assertIn("image-lock.sha256", document["data"])
        embedded = json.loads(document["data"]["target-config.json"])
        self.assertNotIn("local", embedded)
        self.assertEqual(json.loads(document["data"]["image-lock.json"]), self.lock)
        self.assertEqual(
            document["data"]["target-config.sha256"],
            hashlib.sha256(self.outputs["target_config"].encode()).hexdigest(),
        )
        self.assertEqual(
            document["data"]["image-lock.sha256"],
            hashlib.sha256(document["data"]["image-lock.json"].encode()).hexdigest(),
        )

    def test_controller_patch_mounts_read_only_config_and_lock(self):
        document = yaml.safe_load(self.outputs["controller_patch"])
        self.assertEqual(document["kind"], "Deployment")
        self.assertEqual(
            document["metadata"]["namespace"],
            self.config["target"]["namespace"],
        )
        annotations = document["metadata"]["annotations"]
        self.assertIn("intrinsic.ai/controller-config-checksum", annotations)
        pod_spec = document["spec"]["template"]["spec"]
        self.assertEqual(
            pod_spec["volumes"],
            [
                {
                    "name": "intrinsic-controller-config",
                    "configMap": {"name": "intrinsic-target-config"},
                }
            ],
        )
        container = pod_spec["containers"][0]
        self.assertEqual(container["volumeMounts"][0]["readOnly"], True)
        self.assertIn(
            "--target-config=/etc/intrinsic/controller-config/target-config.json",
            container["args"],
        )
        self.assertIn(
            "--image-lock=/etc/intrinsic/controller-config/image-lock.json",
            container["args"],
        )

    def test_quay_image_values_match_current_renderer(self):
        current = (
            Path(__file__).resolve().parent / "deployment" / "quay-image-values.yaml"
        ).read_text(encoding="utf-8")
        self.assertEqual(self.outputs["quay_image_values"], current)

    def test_check_outputs_detects_drift(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            write_outputs(self.outputs, root)
            self.assertEqual(check_outputs(self.outputs, root), [])
            target = root / "target-config.json"
            target.write_text("{}\n", encoding="utf-8")
            mismatches = check_outputs(self.outputs, root)
            self.assertTrue(mismatches)
            self.assertTrue(any("target-config.json" in item for item in mismatches))


if __name__ == "__main__":
    unittest.main()
