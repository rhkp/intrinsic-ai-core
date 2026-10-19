# This project was developed with assistance from AI tools.
"""Regressions for canonical smoke and shell command target selection."""

import ast
import json
import tempfile
import unittest
from pathlib import Path

from runtime_config import RuntimeTarget
from smoke_quay_image import image_for
from test_render_config import realistic_config, realistic_lock

ROOT = Path(__file__).resolve().parent


class OperationalConsumersTest(unittest.TestCase):
    def test_smoke_queries_keep_explicit_namespace_arguments(self):
        for name in (
            "gpu_smoke.py",
            "smoke_quay_image.py",
            "smoke_restricted_pod.py",
            "grpc_gateway_smoke.py",
        ):
            tree = ast.parse((ROOT / name).read_text())
            for node in ast.walk(tree):
                if not isinstance(node, ast.Call):
                    continue
                for index, argument in enumerate(node.args):
                    if isinstance(argument, ast.Constant) and argument.value == "-n":
                        self.assertLess(index + 1, len(node.args), name)
                        self.assertIsInstance(node.args[index + 1], ast.Name, name)

    def test_image_pull_helper_uses_the_configured_lock_and_registry(self):
        config = realistic_config()
        lock = realistic_lock()
        lock["images"] = [
            {
                "chart_image_key": "demo",
                "chart_repository_basename": "demo",
                "quay_digest": "sha256:" + "a" * 64,
            }
        ]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config.json"
            (path.parent / "image-lock.json").write_text(json.dumps(lock))
            self.assertEqual(
                image_for("demo", RuntimeTarget(config), path),
                "quay.io/example-org/intrinsic/demo@sha256:" + "a" * 64,
            )

    def test_cluster_shell_consumers_bootstrap_canonical_validation(self):
        for name in (
            "omts-workspace/apply-workspace.sh",
            "omts-workspace/build-workspace-image.sh",
            "resource-registry/build-image.sh",
            "omts-runner/build-tool-image.sh",
            "viewer/build-viewer-image.sh",
            "omts-runner/build-image.sh",
            "apply-runtime-secret.sh",
        ):
            text = (ROOT / "deployment" / name).read_text()
            self.assertIn('/canonical-env.sh"', text, name)
            self.assertNotIn("your-project-namespace", text, name)


if __name__ == "__main__":
    unittest.main()
