# This project was developed with assistance from AI tools.
"""Credential rotation and finite-deadline behavior independent of a live cluster."""

import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import hybrid_triton


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
