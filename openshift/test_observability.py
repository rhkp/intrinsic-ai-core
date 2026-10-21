# This project was developed with assistance from AI tools.
import importlib.util
import json
import sys
import unittest
from pathlib import Path

import observability
from render_config import load_config, load_image_lock, EXAMPLE_PATH
from runtime_config import RuntimeTarget
import check_observability
from unittest.mock import patch
import io
import contextlib
from common import PilotError
from types import SimpleNamespace
from unittest.mock import Mock

DIRECTORY = Path(__file__).parent / "observability"
sys.path.insert(0, str(DIRECTORY))
import native  # noqa: E402

spec = importlib.util.spec_from_file_location(
    "operational_observer", DIRECTORY / "observer.py"
)
observer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(observer)


class NativeTelemetryTest(unittest.TestCase):
    def test_controller_filters_other_controllers_and_drops_private_labels(self):
        values, exported = native.controller("""
controller_runtime_reconcile_total{controller="chartassignment",result="success",private="secret-value"} 8
controller_runtime_reconcile_total{controller="chartassignment",result="error"} 2
controller_runtime_reconcile_time_seconds_count{controller="chartassignment"} 10
controller_runtime_reconcile_time_seconds_sum{controller="chartassignment"} 5
controller_runtime_reconcile_total{controller="unrelated",result="error"} 999
""")
        self.assertEqual(values["controller_reconcile_latency"]["value"], 0.5)
        self.assertEqual(dict(exported)["controller_reconcile_errors_total"], 2)
        self.assertNotIn("secret-value", json.dumps([values, exported]))

    def test_gateway_counts_grpc_failures_and_converts_milliseconds(self):
        values, exported = native.mesh(
            """
istio_requests_total{reporter="source",request_protocol="grpc",response_code="200",grpc_response_status="0"} 7
istio_requests_total{reporter="source",request_protocol="grpc",response_code="200",grpc_response_status="14"} 2
istio_requests_total{reporter="destination",request_protocol="grpc",response_code="500"} 100
istio_request_duration_milliseconds_count{reporter="source",request_protocol="grpc"} 9
istio_request_duration_milliseconds_sum{reporter="source",request_protocol="grpc"} 4500
""",
            "gateway",
        )
        self.assertEqual(values["gateway_grpc_latency"]["value"], 0.5)
        self.assertEqual(dict(exported)["gateway_grpc_requests_total"], 9)
        self.assertEqual(dict(exported)["gateway_grpc_errors_total"], 2)

    def test_authorization_denials_are_not_server_errors(self):
        values, _ = native.mesh(
            """
istio_requests_total{reporter="destination",response_code="403",grpc_response_status="7"} 2
istio_requests_total{reporter="destination",response_code="200",grpc_response_status="16"} 3
istio_requests_total{reporter="destination",response_code="500"} 99
""",
            "authorization",
        )
        self.assertEqual(values["inference_authorization_failures"]["value"], 5)

    def test_expected_sdk_discovery_errors_remain_visible_without_paging(self):
        _, exported = native.mesh(
            """
istio_requests_total{reporter="source",request_protocol="grpc",response_code="200",grpc_response_status="12"} 5
istio_requests_total{reporter="source",request_protocol="grpc",response_code="200",grpc_response_status="1"} 2
istio_requests_total{reporter="source",request_protocol="grpc",response_code="200",grpc_response_status="14"} 1
""",
            "gateway",
        )
        self.assertEqual(dict(exported)["gateway_grpc_errors_total"], 8)
        self.assertEqual(dict(exported)["gateway_grpc_unexpected_errors_total"], 1)

    def test_missing_metrics_and_no_traffic_are_distinct(self):
        self.assertEqual(native.mesh("", "gateway"), ({}, []))
        values, _ = native.controller("""
controller_runtime_reconcile_time_seconds_count{controller="chartassignment"} 0
controller_runtime_reconcile_time_seconds_sum{controller="chartassignment"} 0
""")
        self.assertIsNone(values["controller_reconcile_latency"]["value"])

    def test_invalid_or_duplicate_samples_fail_closed(self):
        for raw in [
            'metric{label="unterminated} 1',
            'metric{a="one",a="two"} 1',
            "metric 1\nmetric 2",
        ]:
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                native.samples(raw)
        self.assertEqual(native.samples("metric NaN\nmetric +Inf\nmetric -1"), [])
        with self.assertRaises(ValueError):
            native.samples("#" * (native.LIMIT + 1))

    def test_triton_requires_expected_models_and_valid_device_capacity(self):
        raw = "\n".join(
            f'{metric}{{model="{model}",version="1"}} {value}'
            for model in native.MODELS
            for metric, value in [
                ("nv_inference_request_success", 2),
                ("nv_inference_request_failure", 1),
                ("nv_inference_request_duration_us", 4_000_000),
            ]
        )
        raw += '\nnv_gpu_memory_used_bytes{gpu_uuid="private-device"} 40\nnv_gpu_memory_total_bytes{gpu_uuid="private-device"} 100'
        values, exported = native.triton(raw)
        self.assertEqual(values["inference_request_latency"]["value"], 2)
        self.assertEqual(values["gpu_memory_utilization"]["value"], 0.4)
        self.assertNotIn("private-device", json.dumps([values, exported]))
        values, _ = native.triton(raw.replace("} 40", "} 101"))
        self.assertNotIn("gpu_memory_utilization", values)
        self.assertNotIn(
            "inference_request_latency",
            native.triton(raw.replace(next(iter(native.MODELS)), "unrelated"))[0],
        )
        for metric, signal in [
            ("nv_inference_request_duration_us", "inference_request_latency"),
            ("nv_inference_request_failure", "inference_request_errors"),
        ]:
            partial = "\n".join(
                line
                for line in raw.splitlines()
                if not (
                    line.startswith(metric + "{") and next(iter(native.MODELS)) in line
                )
            )
            with self.subTest(missing=metric):
                self.assertNotIn(signal, native.triton(partial)[0])
                self.assertIn("gpu_memory_utilization", native.triton(partial)[0])

    def test_icon_disconnected_and_unreachable_are_distinct(self):
        values, _ = native.icon(
            '{"operationalStatus":{"state":"ENABLED"},"cellControlHardwareStatus":{"state":"FAULTED"}}'
        )
        self.assertEqual(values["simulator_ipc_connectivity"]["value"], 0)
        with self.assertRaises(ValueError):
            native.icon("{}")

    def test_failed_endpoint_does_not_remove_other_signals(self):
        def read(url):
            if "/v1/operationalStatus" in url:
                return '{"operationalStatus":{"state":"ENABLED"},"cellControlHardwareStatus":{"state":"ENABLED"}}'
            raise TimeoutError("private endpoint")

        result = native.collect(
            {"controller": [], "gateway": [], "rhoai": []}, "example", read
        )
        self.assertEqual(set(result["signals"]), {"simulator_ipc_connectivity"})
        self.assertNotIn("private", json.dumps(result))

    def test_stale_native_metrics_are_suppressed(self):
        data = {
            "components": {},
            "assignments": {},
            "warnings": {},
            "native": {
                "signals": {"simulator_ipc_connectivity": {}},
                "metrics": [("simulator_ipc_connected", 1)],
            },
        }
        fresh = observer.metrics(data, True, 10)
        stale = observer.metrics(data, False, 100)
        self.assertIn('signal="simulator_ipc_connectivity"} 1', fresh)
        self.assertIn('signal="simulator_ipc_connectivity"} 0', stale)
        self.assertNotIn("intrinsic_simulator_ipc_connected", stale)


class ObservabilityCheckTest(unittest.TestCase):
    def healthy(self):
        return {
            "collection_success": True,
            "age_seconds": 10,
            "data": {
                "components": {str(i): {"ready": True} for i in range(10)},
                "assignments": {str(i): {"ready": True} for i in range(4)},
                "unavailable_signals": [],
                "native": {
                    "signals": {
                        name: {"value": 1} for name in check_observability.SIGNALS
                    }
                },
            },
        }

    def test_stale_missing_source_and_disconnected_ipc_fail_acceptance(self):
        healthy = self.healthy()
        self.assertTrue(all(check_observability.snapshot_checks(healthy).values()))
        healthy["age_seconds"] = 100
        self.assertFalse(any(check_observability.snapshot_checks(healthy).values()))
        healthy = self.healthy()
        del healthy["data"]["native"]["signals"]["gpu_memory_utilization"]
        self.assertFalse(
            check_observability.snapshot_checks(healthy)["native_sources_available"]
        )
        healthy["data"]["native"]["signals"]["simulator_ipc_connectivity"]["value"] = 0
        self.assertFalse(
            check_observability.snapshot_checks(healthy)["simulator_ipc_enabled"]
        )

    def test_cli_connection_failure_never_prints_context_or_endpoint(self):
        output = io.StringIO()
        with (
            patch(
                "check_observability.load_runtime_target",
                side_effect=PilotError("private-context private-endpoint"),
            ),
            contextlib.redirect_stdout(output),
        ):
            result = check_observability.main([])
        self.assertEqual(result, 1)
        self.assertNotIn("private", output.getvalue())
        self.assertFalse(json.loads(output.getvalue())["passed"])

    def test_monitoring_requires_current_rule_expressions_and_real_ingestion(self):
        def command(*args, **kwargs):
            if args[0] == "whoami":
                return SimpleNamespace(stdout="test-token", returncode=0)
            if args[1] == "route":
                data = {"spec": {"host": "monitoring.example.invalid"}}
            else:
                data = {"data": {}}
            return SimpleNamespace(stdout=json.dumps(data), returncode=0)

        access = Mock()
        access.oc.side_effect = command
        loaded = [
            {
                "name": r["alert"],
                "query": r["expr"],
                "health": "ok",
                "duration": int(r["for"][:-1]) * 60,
                "labels": r["labels"],
                "annotations": r["annotations"],
            }
            for r in observability.rules()
        ]
        response = {
            "status": "success",
            "data": {
                "groups": [
                    {
                        "name": "intrinsic-operational-health",
                        "file": "/rules/demo-project-intrinsic-observability-test.yaml",
                        "rules": loaded,
                    }
                ]
            },
        }

        def read(*args, **kwargs):
            if "/rules?" in args[0]:
                return response
            if "count+by" in args[0]:
                return {
                    "status": "success",
                    "data": {
                        "result": [
                            {
                                "metric": {"__name__": "intrinsic_" + name},
                                "value": [0, "1"],
                            }
                            for name in check_observability.NATIVE_METRICS
                        ]
                    },
                }
            value = "9" if "count%28" in args[0] else "1"
            return {"status": "success", "data": {"result": [{"value": [0, value]}]}}

        with patch("check_observability.request_json", side_effect=read):
            target = SimpleNamespace(namespace="demo-project")
            self.assertTrue(
                all(check_observability.monitoring_checks(target, access).values())
            )
            gateway = next(
                r for r in loaded if r["name"] == "IntrinsicGatewayGRPCErrors"
            )
            gateway["query"] = gateway["query"].replace("unexpected_errors", "errors")
            self.assertFalse(
                check_observability.monitoring_checks(target, access)[
                    "alert_rules_healthy"
                ]
            )
            gateway["query"] = next(
                r["expr"]
                for r in observability.rules()
                if r["alert"] == gateway["name"]
            )
            gateway["query"] = gateway["query"].replace("0.05", "0.5")
            self.assertFalse(
                check_observability.monitoring_checks(target, access)[
                    "alert_rules_healthy"
                ]
            )
            gateway["query"] = next(
                r["expr"]
                for r in observability.rules()
                if r["alert"] == gateway["name"]
            )
            gateway["duration"] = 0
            self.assertFalse(
                check_observability.monitoring_checks(target, access)[
                    "alert_rules_healthy"
                ]
            )

    def test_rule_normalization_preserves_matchers_and_quoted_whitespace(self):
        normalize = check_observability.normalized_rule_query
        self.assertEqual(
            normalize(
                'up{service="observer",namespace="demo-project"} == 0', "demo-project"
            ),
            normalize('up{service="observer"}==0', "demo-project"),
        )
        self.assertNotEqual(
            normalize('up{namespace="other",service="observer"}', "demo-project"),
            normalize('up{service="observer"}', "demo-project"),
        )
        self.assertNotEqual(
            normalize('up{service="two words"}', "demo-project"),
            normalize('up{service="twowords"}', "demo-project"),
        )


class ObservabilityResourcesTest(unittest.TestCase):
    def setUp(self):
        document = load_config(EXAMPLE_PATH, allow_placeholders=True)
        self.target = RuntimeTarget(document)
        self.lock = load_image_lock(document, EXAMPLE_PATH)

    def test_resources_are_owned_scoped_and_do_not_mutate_app_workloads(self):
        objects = observability.render(
            self.target, self.lock, ["192.0.2.1"], {"app": "example-predictor"}
        )
        for obj in objects:
            self.assertEqual(obj["metadata"]["namespace"], self.target.namespace)
            self.assertEqual(
                obj["metadata"]["labels"]["app.kubernetes.io/part-of"],
                observability.NAME,
            )
            if obj["kind"] == "Deployment":
                self.assertEqual(obj["metadata"]["name"], observability.NAME)
        role = next(obj for obj in objects if obj["kind"] == "Role")
        for rule in role["rules"]:
            self.assertLessEqual(set(rule["verbs"]), {"get", "list"})
            self.assertFalse(
                {"secrets", "pods/exec", "pods/log"} & set(rule["resources"])
            )
        filters = [obj for obj in objects if obj["kind"] == "EnvoyFilter"]
        self.assertEqual(len(filters), 2)
        self.assertTrue(
            all(obj["spec"]["workloadSelector"]["labels"] for obj in filters)
        )

    def test_empty_predictor_selector_cannot_create_namespace_wide_policy(self):
        with self.assertRaises(ValueError):
            observability.render(self.target, self.lock, ["192.0.2.1"], {})

    def test_rules_cover_native_telemetry_and_have_runbooks(self):
        rules = observability.rules()
        self.assertGreater(len(rules), 9)
        for rule in rules:
            self.assertIn("#", rule["annotations"]["runbook_url"])
        names = {r["alert"] for r in rules}
        self.assertIn("IntrinsicNativeTelemetryMissing", names)
        self.assertIn("IntrinsicSimulatorIPCDisconnected", names)


if __name__ == "__main__":
    unittest.main()
