# This project was developed with assistance from AI tools.
import unittest
import json
import tempfile
from pathlib import Path
from unittest.mock import patch

from . import audit_config
from .audit_config import collect_audit, has_target_default


class ConfigAuditTest(unittest.TestCase):
    def test_adapter_guard_covers_extracted_files(self):
        cases = (
            ("openshift.go", 'var endpoint = os.Getenv("SOME_SETTING")', True),
            (
                "openshift_images.go",
                "var defaultImageOverrides = map[string]string{}",
                True,
            ),
            ("openshift_routing.go", 'var endpoint = os.Getenv("SOME_SETTING")', True),
            (
                "openshift_inference.go",
                'var endpoint = os.Getenv("SOME_SETTING")',
                True,
            ),
            ("openshift_storage.go", 'const name = "fixed-resource"', False),
            ("config.go", 'var endpoint = os.Getenv("SOME_SETTING")', False),
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            deployment = root / "deployment"
            deployment.mkdir()
            (deployment / "SOURCE_MANIFEST.json").write_text(json.dumps({"files": []}))
            for name, source, rejected in cases:
                with self.subTest(file=name):
                    path = root / name
                    path.write_text(source)
                    with (
                        patch.object(audit_config, "ROOT", root),
                        patch.object(audit_config, "source_files", return_value=[path]),
                    ):
                        violations = audit_config.executable_target_violations()
                    self.assertEqual(bool(violations), rejected)

    def test_reports_environment_and_target_surfaces(self):
        audit = collect_audit()
        for key in (
            "OPENSHIFT_NAMESPACE",
            "OPENSHIFT_STORAGE_CLASS",
            "OPENSHIFT_SHARED_STORAGE_CLASS",
            "INTRINSIC_INGRESS_ADDRESS",
            "INTRINSIC_INGRESS_GATEWAY",
        ):
            self.assertIn(key, audit["environment_variables"])
        for label in (
            "namespace",
            "registry_org",
            "block_storage_class",
            "shared_storage_class",
            "gpu_taint_key",
        ):
            self.assertIn(label, audit["target_placeholders"])
            self.assertIsInstance(audit["target_placeholders"][label], dict)

    def test_reports_controller_and_image_lock_surfaces(self):
        audit = collect_audit()
        self.assertTrue(audit["controller_deployment"]["image"])
        self.assertEqual(
            audit["controller_deployment"]["watch_namespace"],
            "your-project-namespace",
        )
        self.assertEqual(len(audit["image_lock_references"]["quay"]), 24)
        self.assertEqual(len(audit["image_lock_references"]["openshift"]), 1)
        self.assertEqual(len(audit["image_lock_references"]["project"]), 38)
        self.assertEqual(audit["controller_image_constant_mismatches"], {})
        self.assertEqual(audit["executable_target_violations"], [])

    def test_rejects_concrete_target_defaults(self):
        for text, suffix in (
            (
                'namespace = os.environ.get("OPENSHIFT_NAMESPACE", "fixed-project")',
                ".py",
            ),
            ('namespace="${OPENSHIFT_NAMESPACE:-fixed-project}"', ".sh"),
            ('const endpoint="gateway.fixed-project.svc.cluster.local:80"', ".go"),
        ):
            self.assertTrue(has_target_default(text, suffix))
        self.assertFalse(
            has_target_default(
                'namespace="${OPENSHIFT_NAMESPACE:?Required canonical namespace}"',
                ".sh",
            )
        )

    def test_generated_image_values_match_image_lock(self):
        audit = collect_audit()
        self.assertEqual(audit["generated_image_value_mismatches"], [])


if __name__ == "__main__":
    unittest.main()
