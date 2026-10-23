# This project was developed with assistance from AI tools.
"""Canonical hybrid config isolation, target guards, credentials and RPC regressions."""

import contextlib
import io
import json
import os
import shlex
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from jsonschema import Draft202012Validator

from hybrid_config import (
    ConfigError,
    EXAMPLE_PATH,
    SCHEMA_PATH,
    load,
    verify_context,
    render_env,
    write_env,
)
import hybrid_triton
import refresh_vm_token


class HybridConfigTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name) / "config.local.json"
        self.document = {
            "schema_version": 1,
            "local": {
                "context": "hybrid-test-context",
                "api_server_url": "https://api.hybrid.test:6443",
                "rhoai_ca_file": "certs/ca.crt",
                "vm_ssh_key": "keys/vm.pem",
            },
            "target": {
                "namespace": "hybrid-test",
                "rhoai": {
                    "route": "hybrid-triton-grpc",
                    "endpoint": "hybrid.cluster.test:443",
                    "client_account": "model-client",
                    "models": {
                        "segmentation": "segmentor.rfdetr",
                        "pose": "pose_estimator.foundationpose",
                    },
                },
                "vm": {"ssh_host": "ubuntu@vm.cluster.test"},
            },
        }

    def write(self):
        self.path.write_text(json.dumps(self.document))

    def test_schema_and_example_are_valid_but_placeholders_are_not_runtime_values(self):
        Draft202012Validator.check_schema(json.loads(SCHEMA_PATH.read_text()))
        config = load(EXAMPLE_PATH, allow_placeholders=True, validate_files=False)
        self.assertEqual(config.schema_version, 1)
        with self.assertRaisesRegex(ConfigError, "placeholder"):
            load(EXAMPLE_PATH, validate_files=False)

    def test_other_topology_configuration_is_rejected(self):
        other = Path(__file__).resolve().parents[2] / "openshift/config.example.json"
        with self.assertRaises(ConfigError):
            load(other, allow_placeholders=True, validate_files=False)

    def test_canonical_file_ignores_environment_and_resolves_its_own_paths(self):
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
        self.assertNotIn("hybrid-test-context", repr(config))

    def test_unknown_fields_are_rejected_without_private_values(self):
        for section in (
            self.document,
            self.document["local"],
            self.document["target"]["rhoai"],
        ):
            section["unexpected"] = "private-value-that-must-not-be-printed"
            self.write()
            with self.assertRaises(ConfigError) as caught:
                load(self.path, validate_files=False)
            self.assertNotIn("private-value", str(caught.exception))
            del section["unexpected"]

    def test_dotenv_duplicate_missing_and_unsupported_schema_fail(self):
        self.path.write_text("HYBRID_RHOAI_ENDPOINT=private-value\n")
        with self.assertRaises(ConfigError):
            load(self.path, validate_files=False)
        self.path.write_text('{"schema_version":1,"schema_version":2}')
        with self.assertRaisesRegex(ConfigError, "Duplicate"):
            load(self.path, validate_files=False)
        del self.document["target"]["rhoai"]["client_account"]
        self.write()
        with self.assertRaises(ConfigError):
            load(self.path, validate_files=False)
        self.document["target"]["rhoai"]["client_account"] = "model-client"
        for version in (2, True, "1"):
            self.document["schema_version"] = version
            self.write()
            with self.assertRaises(ConfigError):
                load(self.path, validate_files=False)

    def test_rest_urls_wrong_ports_system_namespaces_and_duplicate_models_fail(self):
        rhoai = self.document["target"]["rhoai"]
        for endpoint in (
            "https://hybrid.cluster.test",
            "hybrid.cluster.test/v2/models/cifar10",
            "hybrid.cluster.test:80",
        ):
            rhoai["endpoint"] = endpoint
            self.write()
            with self.assertRaises(ConfigError):
                load(self.path, validate_files=False)
        rhoai["endpoint"] = "hybrid.cluster.test:443"
        self.document["target"]["namespace"] = "openshift-system"
        self.write()
        with self.assertRaises(ConfigError):
            load(self.path, validate_files=False)
        self.document["target"]["namespace"] = "hybrid-test"
        rhoai["models"]["pose"] = rhoai["models"]["segmentation"]
        self.write()
        with self.assertRaises(ConfigError):
            load(self.path, validate_files=False)

    def test_api_server_must_be_https_and_matches_selected_context(self):
        self.document["local"]["api_server_url"] = "http://api.hybrid.test:6443"
        self.write()
        with self.assertRaises(ConfigError):
            load(self.path, validate_files=False)
        self.document["local"]["api_server_url"] = "https://api.hybrid.test:6443"
        self.write()
        config = load(self.path, validate_files=False)
        with patch(
            "hybrid_config.subprocess.run",
            return_value=SimpleNamespace(returncode=0, stdout=config.api_server_url),
        ) as run:
            verify_context(config)
            self.assertEqual(
                run.call_args.args[0][:3], ["oc", "--context", config.context]
            )
        with patch(
            "hybrid_config.subprocess.run",
            return_value=SimpleNamespace(
                returncode=0, stdout="https://other-api.test:6443"
            ),
        ):
            with self.assertRaises(ConfigError) as caught:
                verify_context(config)
            self.assertNotIn("other-api", str(caught.exception))

    def test_target_flags_cannot_override_canonical_config(self):
        tool = Path(__file__).with_name("refresh_vm_token.py")
        result = subprocess.run(
            [
                sys.executable,
                str(tool),
                "--config",
                str(self.path),
                "--namespace",
                "other-target",
            ],
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("unrecognized arguments", result.stderr)

    def test_ingress_can_be_rendered_before_ca_exists_and_is_deterministic(self):
        self.write()
        tool = Path(__file__).with_name("prepare_grpc_ingress.py")
        outputs = []
        for name in ("first", "second"):
            output = self.path.parent / name
            result = subprocess.run(
                [
                    sys.executable,
                    str(tool),
                    "--config",
                    str(self.path),
                    "--output",
                    str(output),
                ],
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                (output / "endpoint").read_text().strip(),
                self.document["target"]["rhoai"]["endpoint"],
            )
            outputs.append((output / "ingress.yaml").read_bytes())
        self.assertEqual(outputs[0], outputs[1])

    def test_generated_env_is_quoted_deterministic_private_and_checked_for_drift(self):
        self.document["local"]["vm_ssh_key"] = "keys/key with spaces.pem"
        self.write()
        config = load(self.path, validate_files=False)
        output = self.path.parent / "generated"
        write_env(config, output)
        expected = render_env(config)
        self.assertEqual((output / ".env").read_text(), expected)
        self.assertEqual((output / ".env").stat().st_mode & 0o777, 0o600)
        values = dict(
            line.split("=", 1)
            for line in expected.splitlines()
            if not line.startswith("#")
        )
        self.assertEqual(
            shlex.split(values["HYBRID_VM_SSH_KEY"]), [str(config.ssh_key)]
        )
        write_env(config, output, check=True)
        (output / ".env").write_text("manual drift")
        with self.assertRaisesRegex(ConfigError, "drifted"):
            write_env(config, output, check=True)
        self.assertEqual((output / ".env").read_text(), "manual drift")
        write_env(config, output)
        self.assertEqual((output / ".env").read_text(), expected)

    def test_runtime_checks_require_local_files_and_reject_invalid_ca(self):
        self.write()
        with self.assertRaisesRegex(ConfigError, "Missing local file"):
            load(self.path)
        for name in ("certs/ca.crt", "keys/vm.pem"):
            file = self.path.parent / name
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_text("")
            file.chmod(0o600)
        with self.assertRaisesRegex(ConfigError, "certificate"):
            load(self.path)

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


class HybridTransportTests(unittest.TestCase):
    def test_rotated_token_is_read_for_every_rpc(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "token"
            path.write_text("first")
            values = []
            with patch.dict(os.environ, {"RHOAI_TOKEN_FILE": str(path)}):
                plugin = hybrid_triton.TokenCredentials()
                plugin(None, lambda metadata, error: values.append((metadata, error)))
                replacement = path.with_name("new-token")
                replacement.write_text("second")
                replacement.replace(path)
                plugin(None, lambda metadata, error: values.append((metadata, error)))
                path.unlink()
                plugin(None, lambda metadata, error: values.append((metadata, error)))
            self.assertEqual(values[0], ((("authorization", "Bearer first"),), None))
            self.assertEqual(values[1], ((("authorization", "Bearer second"),), None))
            self.assertEqual(values[2][0], ())
            self.assertIsInstance(values[2][1], RuntimeError)

    def test_deadlines_are_bounded_and_shorter_caller_deadline_is_preserved(self):
        for method, timeout, expected in [
            ("/inference.GRPCInferenceService/ServerReady", None, 15),
            ("/inference.GRPCInferenceService/ServerReady", 3, 3),
            ("/inference.GRPCInferenceService/ModelInfer", 900, 120),
        ]:
            details = SimpleNamespace(
                method=method, timeout=timeout, metadata=(), credentials=None
            )
            captured = []
            result = SimpleNamespace(add_done_callback=lambda callback: None)
            hybrid_triton.BoundedCalls().intercept_unary_unary(
                lambda outgoing, request: captured.append(outgoing) or result,
                details,
                SimpleNamespace(model_name="test"),
            )
            self.assertEqual(captured[0].timeout, expected)
            self.assertFalse(captured[0].wait_for_ready)


if __name__ == "__main__":
    unittest.main()
