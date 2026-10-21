# This project was developed with assistance from AI tools.
"""Execute alert behavior fixtures when the optional Prometheus promtool exists."""

import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

import yaml

import grafana
from observability import rules


def behavior_fixture():
    test = yaml.safe_load(
        (Path(__file__).parent / "testdata/observability-rules.test.yaml").read_text()
    )
    expression = grafana.model.dashboard("demo-project")["panels"][0]["targets"][0][
        "expr"
    ]
    expression = expression.replace("$namespace", "demo-project")
    for name, collected, scraped, expected in [
        ("fresh collection is healthy", "1+0x6", "1+0x6", 1),
        ("failed collection is unhealthy", "0+0x6", "1+0x6", 0),
        (
            "failed scrape cannot show a healthy cached collection",
            "1+0x6",
            "1 0+0x5",
            0,
        ),
        ("old samples cannot show a fresh collection", "1 _ _ _ _ _ _", "1+0x6", 0),
    ]:
        test["tests"].append(
            {
                "name": name,
                "interval": "1m",
                "input_series": [
                    {
                        "series": 'intrinsic_observability_collection_success{namespace="demo-project"}',
                        "values": collected,
                    },
                    {
                        "series": 'up{namespace="demo-project",service="intrinsic-observability"}',
                        "values": scraped,
                    },
                ],
                "promql_expr_test": [
                    {
                        "expr": expression,
                        "eval_time": "2m",
                        "exp_samples": [{"labels": "{}", "value": expected}],
                    }
                ],
            }
        )
    return test


@unittest.skipUnless(
    shutil.which("promtool"),
    "promtool is optional; live E8 validation runs these fixtures with the platform binary",
)
class AlertBehaviorTest(unittest.TestCase):
    def test_alert_thresholds_no_traffic_discovery_failures_and_resolution(self):
        with tempfile.TemporaryDirectory(prefix="intrinsic-e8-rules-") as directory:
            root = Path(directory)
            rule_file = root / "rules.yaml"
            rule_file.write_text(
                yaml.safe_dump(
                    {
                        "groups": [
                            {"name": "intrinsic-operational-health", "rules": rules()}
                        ]
                    }
                )
            )
            test = behavior_fixture()
            test["rule_files"] = [str(rule_file)]
            test_file = root / "tests.yaml"
            test_file.write_text(yaml.safe_dump(test))
            result = subprocess.run(
                ["promtool", "test", "rules", str(test_file)],
                capture_output=True,
                text=True,
                timeout=30,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
