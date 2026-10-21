# This project was developed with assistance from AI tools.
import importlib.util
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import grafana
from render_config import EXAMPLE_PATH, load_config, load_image_lock
from runtime_config import RuntimeTarget

spec = importlib.util.spec_from_file_location(
    "grafana_credentials", Path(__file__).parent / "observability/grafana_provision.py"
)
credentials = importlib.util.module_from_spec(spec)
spec.loader.exec_module(credentials)


class GrafanaTest(unittest.TestCase):
    def setUp(self):
        self.target = RuntimeTarget(load_config(EXAMPLE_PATH, allow_placeholders=True))
        self.lock = load_image_lock(self.target.document, EXAMPLE_PATH)

    def test_trial_is_loopback_only_nonroot_scoped_and_uses_immutable_images(self):
        objects = grafana.render(self.target, self.lock, 100001)
        self.assertTrue(
            all(o["metadata"]["namespace"] == self.target.namespace for o in objects)
        )
        self.assertFalse(
            any(
                o["kind"]
                in {"Route", "ClusterRoleBinding", "PersistentVolumeClaim", "Secret"}
                for o in objects
            )
        )
        deployment = next(o for o in objects if o["kind"] == "Deployment")
        pod = deployment["spec"]["template"]["spec"]
        self.assertFalse(pod["automountServiceAccountToken"])
        self.assertEqual(pod["securityContext"]["runAsUser"], 100001)
        for container in pod["containers"] + pod["initContainers"]:
            self.assertRegex(container["image"], r"@sha256:[a-f0-9]{64}$")
            self.assertTrue(container["securityContext"]["readOnlyRootFilesystem"])
        main = pod["containers"][0]
        env = {e["name"]: e["value"] for e in main["env"]}
        self.assertEqual(env["GF_SERVER_HTTP_ADDR"], "127.0.0.1")
        self.assertEqual(env["GF_AUTH_ANONYMOUS_ORG_ROLE"], "Viewer")
        self.assertIn(
            "__FILE", next(e["name"] for e in main["env"] if "PASSWORD" in e["name"])
        )
        binding = next(o for o in objects if o["kind"] == "RoleBinding")
        self.assertEqual(binding["roleRef"]["name"], "view")

    def test_every_panel_has_project_scoping_and_a_recovery_pointer(self):
        dashboard = grafana.model.dashboard("test-project")
        self.assertFalse(dashboard["editable"])
        self.assertGreaterEqual(len(dashboard["panels"]), 20)
        identifiers = set()
        for panel in dashboard["panels"]:
            self.assertNotIn(panel["id"], identifiers)
            identifiers.add(panel["id"])
            self.assertTrue(panel["links"][0]["url"].startswith("https://"))
            for target in panel["targets"]:
                self.assertIn('namespace="$namespace"', target["expr"])
                self.assertEqual(target["datasource"]["uid"], "intrinsic-prometheus")
        self.assertEqual(dashboard["refresh"], "30s")

    def test_datasource_uses_project_tenancy_and_verified_tls(self):
        self.assertIn(credentials.OUTPUT.suffix, {".yaml", ".yml"})
        config = credentials.datasource(
            "test-project",
            "test-token",
            "-----BEGIN CERTIFICATE-----\nexample\n-----END CERTIFICATE-----",
        )
        source = config["datasources"][0]
        self.assertEqual(source["jsonData"]["httpMethod"], "GET")
        self.assertTrue(source["url"].endswith(":9092"))
        self.assertEqual(
            source["jsonData"]["customQueryParameters"], "namespace=test-project"
        )
        self.assertFalse(source["jsonData"]["tlsSkipVerify"])
        self.assertEqual(
            source["secureJsonData"]["httpHeaderValue1"], "Bearer test-token"
        )
        self.assertFalse(source["editable"])
        with self.assertRaises(ValueError):
            credentials.datasource(
                "other&namespace=target", "test-token", "-----BEGIN CERTIFICATE-----"
            )

    def test_rotated_credentials_are_written_atomically_with_restricted_permissions(
        self,
    ):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "namespace").write_text("test-project")
            (root / "token").write_text("first-test-token")
            (root / "service-ca.crt").write_text(
                "-----BEGIN CERTIFICATE-----\nexample\n-----END CERTIFICATE-----"
            )
            output = root / "datasource.yaml"
            with (
                patch.object(credentials, "MOUNT", root),
                patch.object(credentials, "OUTPUT", output),
            ):
                first = credentials.write_config()
                (root / "token").write_text("rotated-test-token")
                second = credentials.write_config()
            self.assertNotEqual(first, second)
            text = output.read_text()
            self.assertNotIn("first-test-token", text)
            self.assertIn("Bearer rotated-test-token", text)
            self.assertEqual(output.stat().st_mode & 0o777, 0o640)
            self.assertFalse(output.with_suffix(".pending").exists())

    def test_missing_image_identity_and_invalid_uid_fail_closed(self):
        with self.assertRaises(ValueError):
            grafana.render(self.target, self.lock, 0)
        lock = {
            **self.lock,
            "project_images": [
                i
                for i in self.lock["project_images"]
                if i["name"] != "intrinsic-grafana"
            ],
        }
        with self.assertRaises(KeyError):
            grafana.render(self.target, lock, 100001)


if __name__ == "__main__":
    unittest.main()
