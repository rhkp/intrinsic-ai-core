# This project was developed with assistance from AI tools.
import json
import unittest
from pathlib import Path

from render_quay_image_values import render_values


class RenderQuayImageValuesTest(unittest.TestCase):
    def test_output_is_deterministic_and_lock_derived(self):
        lock_path = Path(__file__).resolve().parent / "image-lock.json"
        lock = json.loads(lock_path.read_text(encoding="utf-8"))
        first = render_values(lock)
        second = render_values(lock)
        self.assertEqual(first, second)
        self.assertIn(
            "  resource_registry: \"/resource_registry_mz6oamw4xrhc5j4b"
            "@sha256:289da3d7464048dbe74a7e9d15f5054cce7ba1ab730eb61db83172593cf441f6\"\n",
            first,
        )

    def test_committed_output_matches_renderer(self):
        lock = json.loads(
            (Path(__file__).resolve().parent / "image-lock.json").read_text(
                encoding="utf-8"
            )
        )
        generated = (
            Path(__file__).resolve().parent
            / "deployment"
            / "quay-image-values.yaml"
        ).read_text(encoding="utf-8")
        self.assertEqual(render_values(lock), generated)


if __name__ == "__main__":
    unittest.main()
