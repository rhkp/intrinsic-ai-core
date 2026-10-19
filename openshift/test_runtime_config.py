# This project was developed with assistance from AI tools.
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from common import PilotError
from runtime_config import load_runtime_target
from test_render_config import realistic_config, realistic_lock


def write_target(directory: str, namespace: str = "example-namespace") -> Path:
    config = realistic_config()
    lock = realistic_lock()
    target = config["target"]
    old_namespace = target["namespace"]
    # Relocate the sanitized fixture coherently; production loading validates it.
    config = json.loads(json.dumps(config).replace(old_namespace, namespace))
    lock = json.loads(json.dumps(lock).replace(old_namespace, namespace))
    path = Path(directory) / "config.json"
    path.write_text(json.dumps(config))
    (path.parent / "image-lock.json").write_text(json.dumps(lock))
    return path


class RuntimeConfigTest(unittest.TestCase):
    def test_loads_target_without_environment_defaults(self):
        with tempfile.TemporaryDirectory() as directory:
            path = write_target(directory)
            with patch.dict(
                "os.environ",
                {
                    "OPENSHIFT_NAMESPACE": "stale-project",
                    "OPENSHIFT_CONTEXT": "stale-context",
                    "OPENSHIFT_API_SERVER_URL": "https://stale.example.com:6443",
                },
            ):
                target = load_runtime_target(path)
            self.assertEqual(target.namespace, "example-namespace")
            self.assertEqual(target.document["local"]["context"], "example-context")
            self.assertNotIn("example-context", repr(target))

    def test_missing_configuration_fails_before_cli_access(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch("runtime_config.ClusterAccess.oc") as command:
                with self.assertRaises(PilotError):
                    load_runtime_target(Path(directory) / "missing.json")
            command.assert_not_called()

    def test_invalid_configuration_errors_hide_values(self):
        with tempfile.TemporaryDirectory() as directory:
            path = write_target(directory)
            config = json.loads(path.read_text())
            config["unexpected"] = "sensitive-test-sentinel"
            path.write_text(json.dumps(config))
            with patch("runtime_config.ClusterAccess.oc") as command:
                with self.assertRaises(PilotError) as result:
                    load_runtime_target(path)
            self.assertNotIn("sensitive-test-sentinel", str(result.exception))
            command.assert_not_called()

    def test_placeholder_and_lock_drift_fail_before_cli_access(self):
        with tempfile.TemporaryDirectory() as directory:
            path = write_target(directory)
            config = json.loads(path.read_text())
            config["local"]["context"] = "replace-with-your-context-name"
            path.write_text(json.dumps(config))
            with self.assertRaises(PilotError):
                load_runtime_target(path)
            path = write_target(directory)
            lock_path = path.parent / "image-lock.json"
            lock = json.loads(lock_path.read_text())
            lock["registry_prefix"] = "quay.io/different-org/intrinsic"
            lock_path.write_text(json.dumps(lock))
            with patch("runtime_config.ClusterAccess.oc") as command:
                with self.assertRaises(PilotError):
                    load_runtime_target(path)
            command.assert_not_called()

    def test_connect_binds_exact_context_and_api(self):
        with tempfile.TemporaryDirectory() as directory:
            target = load_runtime_target(write_target(directory))
        result = subprocess.CompletedProcess(
            [], 0, "https://api.example.com:6443\n", ""
        )
        with patch("runtime_config.ClusterAccess.oc", return_value=result) as command:
            access = target.connect()
        self.assertEqual(access.context, "example-context")
        self.assertEqual(command.call_args.args[:3], ("config", "view", "--minify"))

    def test_wrong_or_unavailable_context_fails_without_disclosing_urls(self):
        with tempfile.TemporaryDirectory() as directory:
            target = load_runtime_target(write_target(directory))
        for result in [
            subprocess.CompletedProcess([], 0, "https://wrong.example.com:6443", ""),
            subprocess.CompletedProcess([], 1, "", "private response"),
        ]:
            with self.subTest(returncode=result.returncode):
                with patch("runtime_config.ClusterAccess.oc", return_value=result):
                    with self.assertRaises(PilotError) as error:
                        target.connect()
                for value in [
                    "api.example.com",
                    "wrong.example.com",
                    "private response",
                ]:
                    self.assertNotIn(value, str(error.exception))

    def test_namespace_relocation_reaches_all_routing_fields(self):
        for namespace in ["first-project", "second-project"]:
            with (
                self.subTest(namespace=namespace),
                tempfile.TemporaryDirectory() as directory,
            ):
                target = load_runtime_target(write_target(directory, namespace))
                manifest = target.routing_configmap(target.ingress_selector)
            self.assertEqual(manifest["metadata"]["namespace"], namespace)
            self.assertIn(
                f".{namespace}.svc.cluster.local:80",
                manifest["data"]["INTRINSIC_INGRESS_ADDRESS"],
            )
            self.assertTrue(
                manifest["data"]["INTRINSIC_INGRESS_GATEWAY"].startswith(
                    namespace + "/"
                )
            )
            self.assertEqual(
                manifest["data"]["OPENSHIFT_STORAGE_CLASS"], "example-block"
            )
            self.assertEqual(len(manifest["data"]), 5)
            self.assertNotIn("api_server_url", json.dumps(manifest))


if __name__ == "__main__":
    unittest.main()
