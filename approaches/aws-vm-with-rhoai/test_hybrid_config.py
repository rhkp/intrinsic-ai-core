# This project was developed with assistance from AI tools.
"""Keep hybrid target selection explicit and separate from other demo profiles."""

import contextlib
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from hybrid_config import ConfigError, load
import refresh_vm_token


class HybridConfigTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name) / ".env"
        self.values = {
            "HYBRID_CONFIG_VERSION": "1",
            "HYBRID_OPENSHIFT_CONTEXT": "hybrid-test-context",
            "HYBRID_RHOAI_NAMESPACE": "hybrid-test",
            "HYBRID_RHOAI_ROUTE": "hybrid-triton-grpc",
            "HYBRID_RHOAI_ENDPOINT": "hybrid.example.invalid:443",
            "HYBRID_RHOAI_CA_FILE": "certs/ca.crt",
            "HYBRID_RHOAI_CLIENT_ACCOUNT": "model-client",
            "HYBRID_RHOAI_SEGMENTATION_MODEL": "segmentor.rfdetr",
            "HYBRID_RHOAI_POSE_MODEL": "pose_estimator.foundationpose",
            "HYBRID_VM_SSH_HOST": "ubuntu@vm.example.invalid",
            "HYBRID_VM_SSH_KEY": "keys/vm.pem",
        }

    def write(self, extra=""):
        self.path.write_text(
            "".join(f"{k}={v}\n" for k, v in self.values.items()) + extra
        )

    def test_selected_file_ignores_shell_targets_and_resolves_its_own_paths(self):
        self.write()
        with patch.dict(
            os.environ,
            {
                "OPENSHIFT_CONTEXT": "other-topology",
                "HYBRID_OPENSHIFT_CONTEXT": "other-hybrid",
            },
        ):
            before = dict(os.environ)
            config = load(self.path, validate_files=False)
            self.assertEqual(dict(os.environ), before)
        self.assertEqual(config.context, "hybrid-test-context")
        self.assertEqual(config.ca_file, (self.path.parent / "certs/ca.crt").resolve())
        self.assertEqual(config.ssh_key, (self.path.parent / "keys/vm.pem").resolve())

    def test_legacy_rest_and_unrecognized_fields_are_rejected_without_values(self):
        self.path.write_text("RHOAI_INFERENCE_URL=https://private-value.invalid\n")
        with self.assertRaises(ConfigError) as caught:
            load(self.path, validate_files=False)
        self.assertNotIn("private-value", str(caught.exception))
        self.assertNotIn("https://", str(caught.exception))

    def test_duplicate_missing_and_unsupported_version_fail(self):
        self.write("HYBRID_RHOAI_NAMESPACE=another-project\n")
        with self.assertRaisesRegex(ConfigError, "Duplicate"):
            load(self.path, validate_files=False)
        self.values.pop("HYBRID_RHOAI_CLIENT_ACCOUNT")
        self.write()
        with self.assertRaisesRegex(ConfigError, "Missing"):
            load(self.path, validate_files=False)
        self.values["HYBRID_RHOAI_CLIENT_ACCOUNT"] = "model-client"
        self.values["HYBRID_CONFIG_VERSION"] = "2"
        self.write()
        with self.assertRaisesRegex(ConfigError, "Unsupported"):
            load(self.path, validate_files=False)

    def test_rest_urls_wrong_ports_and_system_namespaces_fail(self):
        for endpoint in (
            "https://hybrid.example.invalid",
            "hybrid.example.invalid/v2/models/cifar10",
            "hybrid.example.invalid:80",
        ):
            with self.subTest(endpoint=endpoint):
                self.values["HYBRID_RHOAI_ENDPOINT"] = endpoint
                self.write()
                with self.assertRaises(ConfigError):
                    load(self.path, validate_files=False)
        self.values["HYBRID_RHOAI_ENDPOINT"] = "hybrid.example.invalid:443"
        self.values["HYBRID_RHOAI_NAMESPACE"] = "openshift-system"
        self.write()
        with self.assertRaises(ConfigError):
            load(self.path, validate_files=False)

    def test_env_profile_cannot_be_mixed_with_another_target(self):
        tool = Path(__file__).with_name("refresh_vm_token.py")
        result = subprocess.run(
            [
                sys.executable,
                str(tool),
                "--env-file",
                str(self.path),
                "--namespace",
                "other-target",
            ],
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("without mixing", result.stderr)

    def test_refresh_pins_context_and_keeps_token_out_of_arguments(self):
        issued = SimpleNamespace(returncode=0, stdout="synthetic-token-value\n")
        applied = SimpleNamespace(returncode=0, stdout='{"model_token_refreshed":true}')
        with (
            patch.object(
                refresh_vm_token.subprocess, "run", side_effect=[issued, applied]
            ) as run,
            contextlib.redirect_stdout(io.StringIO()),
        ):
            refresh_vm_token.refresh(
                "ubuntu@vm.example.invalid",
                Path("key.pem"),
                "hybrid-test",
                "model-client",
                context="hybrid-test-context",
            )
        first, second = run.call_args_list
        self.assertEqual(first.args[0][:3], ["oc", "--context", "hybrid-test-context"])
        self.assertIn("hybrid-test", first.args[0])
        self.assertIn("ubuntu@vm.example.invalid", second.args[0])
        self.assertNotIn("synthetic-token-value", " ".join(second.args[0]))
        self.assertIn("synthetic-token-value", second.kwargs["input"])


if __name__ == "__main__":
    unittest.main()
