# This project was developed with assistance from AI tools.
import contextlib
import io
import tempfile
import unittest
from pathlib import Path
from unittest.mock import Mock, patch

import preflight
from common import PilotError
from runtime_config import RuntimeTarget
from test_runtime_config import write_target


class PreflightTest(unittest.TestCase):
    def test_queries_canonical_namespace_and_preserves_sanitized_output(self):
        access = Mock()

        def query(_access, *args, **kwargs):
            if args[:2] == ("get", "namespace"):
                return {"metadata": {"labels": {"opendatahub.io/dashboard": "true"}}}
            if args[:2] == ("get", "clusterversion"):
                return {"status": {"desired": {"version": "test-version"}}}
            return {"items": []}

        with tempfile.TemporaryDirectory() as directory:
            path = write_target(directory, "relocated-project")
            output = io.StringIO()
            with (
                patch.object(RuntimeTarget, "connect", return_value=access),
                patch("preflight.get_json", side_effect=query) as queries,
                patch("preflight.can_i", return_value=True),
                patch.dict("os.environ", {"OPENSHIFT_NAMESPACE": "stale-project"}),
                contextlib.redirect_stdout(output),
            ):
                result = preflight.main(["--config", str(path)])
        self.assertEqual(result, 0)
        self.assertIn("RHOAI dashboard marker: yes", output.getvalue())
        for value in [
            "relocated-project",
            "stale-project",
            "example-context",
            "api.example.com",
        ]:
            self.assertNotIn(value, output.getvalue())
        for call in queries.call_args_list:
            if "-n" in call.args:
                index = call.args.index("-n")
                self.assertEqual(call.args[index + 1], "relocated-project")
        access.oc.assert_not_called()

    def test_missing_configuration_stops_before_cluster_queries(self):
        with tempfile.TemporaryDirectory() as directory:
            with (
                patch("preflight.get_json") as query,
                patch.object(RuntimeTarget, "connect") as connect,
                contextlib.redirect_stderr(io.StringIO()),
            ):
                result = preflight.main(
                    ["--config", str(Path(directory) / "missing.json")]
                )
        self.assertEqual(result, 2)
        query.assert_not_called()
        connect.assert_not_called()

    def test_context_mismatch_stops_before_inventory(self):
        with tempfile.TemporaryDirectory() as directory:
            path = write_target(directory)
            with (
                patch.object(
                    RuntimeTarget, "connect", side_effect=PilotError("Context mismatch")
                ),
                patch("preflight.get_json") as query,
                contextlib.redirect_stderr(io.StringIO()),
            ):
                result = preflight.main(["--config", str(path)])
        self.assertEqual(result, 2)
        query.assert_not_called()


if __name__ == "__main__":
    unittest.main()
