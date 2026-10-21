# This project was developed with assistance from AI tools.
"""Bounded native telemetry; no raw labels, identities, or payloads leave here."""

import ipaddress
import json
import math
import re
import urllib.request
from concurrent.futures import ThreadPoolExecutor

SIGNALS = (
    "controller_reconcile_latency",
    "controller_reconcile_errors",
    "gateway_grpc_latency",
    "gateway_grpc_errors",
    "inference_authorization_failures",
    "inference_request_latency",
    "inference_request_errors",
    "gpu_memory_utilization",
    "simulator_ipc_connectivity",
)
MODELS = {
    "ai.intrinsic.ioc_pose_estimation.segmentor.rfdetr": "rfdetr",
    "ai.intrinsic.ioc_pose_estimation.pose_estimator.foundationpose": "foundationpose",
}
LIMIT = 4_000_000
SAMPLE = re.compile(r"([a-zA-Z_:][a-zA-Z0-9_:]*)(\{.*\})?\s+(\S+)(?:\s+\S+)?")
LABEL = re.compile(r'([a-zA-Z_][a-zA-Z0-9_]*)=("(?:[^"\\]|\\.)*")(?:,|$)')


def samples(text):
    if len(text) > LIMIT:
        raise ValueError("metrics_too_large")
    result = []
    seen = set()
    for line in text.splitlines():
        if not line or line.startswith("#"):
            continue
        match = SAMPLE.fullmatch(line)
        if not match:
            raise ValueError("invalid_metric_sample")
        name, raw, value = match.groups()
        value = float(value)
        if not math.isfinite(value) or value < 0:
            continue
        labels = {}
        if raw:
            tail = raw[1:-1]
            while tail:
                label = LABEL.match(tail)
                if not label or label[1] in labels:
                    raise ValueError("invalid_metric_labels")
                labels[label[1]] = json.loads(label[2])
                tail = tail[label.end() :]
        key = (name, tuple(sorted(labels.items())))
        if key in seen:
            raise ValueError("duplicate_metric_sample")
        seen.add(key)
        result.append((name, labels, value))
    return result


def fetch(url):
    # Never follow redirects from a metrics target to another destination.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None

    with urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect).open(
        url, timeout=5
    ) as response:
        raw = response.read(LIMIT + 1)
    if len(raw) > LIMIT:
        raise ValueError("metrics_too_large")
    return raw.decode("utf-8")


def total(rows, name):
    values = [value for metric, _, value in rows if metric == name]
    return sum(values) if values else None


def measurement(value, unit, source):
    return {"value": value, "unit": unit, "source": source}


def controller(text):
    rows = [r for r in samples(text) if r[1].get("controller") == "chartassignment"]
    count = total(rows, "controller_runtime_reconcile_time_seconds_count")
    duration = total(rows, "controller_runtime_reconcile_time_seconds_sum")
    outcomes = [r for r in rows if r[0] == "controller_runtime_reconcile_total"]
    result, metrics = {}, []
    if count is not None and duration is not None:
        result["controller_reconcile_latency"] = measurement(
            duration / count if count else None, "seconds", "controller_runtime"
        )
        metrics += [
            ("controller_reconcile_duration_seconds_sum", duration),
            ("controller_reconcile_duration_seconds_count", count),
        ]
    if outcomes:
        errors = sum(v for _, labels, v in outcomes if labels.get("result") == "error")
        result["controller_reconcile_errors"] = measurement(
            errors, "total", "controller_runtime"
        )
        metrics += [
            ("controller_reconciles_total", sum(v for _, _, v in outcomes)),
            ("controller_reconcile_errors_total", errors),
        ]
    return result, metrics


def mesh(text, component):
    # These filters are installed only on inbound gateway/predictor listeners.
    reporter = "source" if component == "gateway" else "destination"
    rows = [r for r in samples(text) if r[1].get("reporter") == reporter]
    requests = [r for r in rows if r[0] == "istio_requests_total"]
    result, metrics = {}, []
    if not requests:
        return result, metrics
    if component == "gateway":
        requests = [r for r in requests if r[1].get("request_protocol") == "grpc"]
        if not requests:
            return result, metrics
        failed = sum(
            v
            for _, labels, v in requests
            if labels.get("grpc_response_status", "0") not in {"0", "-", ""}
            or int(labels.get("response_code", "0")) >= 400
        )
        unexpected = sum(
            v
            for _, labels, v in requests
            if (
                labels.get("grpc_response_status", "0") not in {"0", "1", "12", "-", ""}
                or int(labels.get("response_code", "0")) >= 400
            )
        )
        count = total(
            [r for r in rows if r[1].get("request_protocol") == "grpc"],
            "istio_request_duration_milliseconds_count",
        )
        duration = total(
            [r for r in rows if r[1].get("request_protocol") == "grpc"],
            "istio_request_duration_milliseconds_sum",
        )
        result["gateway_grpc_errors"] = measurement(failed, "total", "envoy_istio")
        metrics += [
            ("gateway_grpc_requests_total", sum(v for _, _, v in requests)),
            ("gateway_grpc_errors_total", failed),
            ("gateway_grpc_unexpected_errors_total", unexpected),
        ]
        if count is not None and duration is not None:
            result["gateway_grpc_latency"] = measurement(
                duration / count / 1000 if count else None, "seconds", "envoy_istio"
            )
            metrics += [
                ("gateway_grpc_duration_seconds_sum", duration / 1000),
                ("gateway_grpc_duration_seconds_count", count),
            ]
    else:
        denied = sum(
            v
            for _, labels, v in requests
            if labels.get("response_code") in {"401", "403"}
            or labels.get("grpc_response_status") in {"7", "16"}
        )
        result["inference_authorization_failures"] = measurement(
            denied, "total", "envoy_istio"
        )
        metrics += [("inference_authorization_failures_total", denied)]
    return result, metrics


def triton(text):
    rows = samples(text)
    result, metrics = {}, []
    selected = [r for r in rows if r[1].get("model") in MODELS]

    # Require both intended models rather than accepting an unrelated ready server.
    def complete(name):
        return {
            labels.get("model") for metric, labels, _ in selected if metric == name
        } == set(MODELS)

    if complete("nv_inference_request_success"):
        count = total(selected, "nv_inference_request_success")
        failed = total(selected, "nv_inference_request_failure")
        duration = total(selected, "nv_inference_request_duration_us")
        if complete("nv_inference_request_duration_us"):
            result["inference_request_latency"] = measurement(
                duration / count / 1_000_000 if count else None, "seconds", "triton"
            )
            metrics += [
                ("inference_request_duration_seconds_sum", duration / 1_000_000),
                ("inference_request_duration_seconds_count", count),
            ]
        if complete("nv_inference_request_failure"):
            result["inference_request_errors"] = measurement(failed, "total", "triton")
            metrics += [
                ("inference_request_errors_total", failed),
                ("inference_request_success_total", count),
            ]
    # Device memory is measured by Triton for its visible inference GPU(s).
    used = {
        labels.get("gpu_uuid"): v
        for n, labels, v in rows
        if n == "nv_gpu_memory_used_bytes"
    }
    capacity = {
        labels.get("gpu_uuid"): v
        for n, labels, v in rows
        if n == "nv_gpu_memory_total_bytes"
    }
    if (
        used
        and set(used) == set(capacity)
        and all(0 <= used[k] <= capacity[k] and capacity[k] > 0 for k in used)
    ):
        used_bytes, total_bytes = sum(used.values()), sum(capacity.values())
        result["gpu_memory_utilization"] = measurement(
            used_bytes / total_bytes, "ratio", "triton_visible_devices"
        )
        metrics += [
            ("inference_gpu_memory_used_bytes", used_bytes),
            ("inference_gpu_memory_total_bytes", total_bytes),
        ]
    return result, metrics


def icon(text):
    data = json.loads(text)
    states = [
        data.get(key, {}).get("state")
        for key in ("operationalStatus", "cellControlHardwareStatus")
    ]
    if not all(isinstance(s, str) and s for s in states):
        raise ValueError("invalid_icon_status")
    connected = int(states == ["ENABLED", "ENABLED"])
    return {
        "simulator_ipc_connectivity": measurement(
            connected, "enabled", "icon_operational_hardware"
        )
    }, [("simulator_ipc_connected", connected)]


def pod_address(pods, port, path):
    # Refuse ambiguity during rollouts. Read only a current owned pod's numeric IP.
    if len(pods) != 1:
        raise ValueError("telemetry_pod_not_unique")
    address = ipaddress.ip_address(pods[0]["status"]["podIP"])
    host = f"[{address}]" if address.version == 6 else str(address)
    return f"http://{host}:{port}{path}"


def collect(pods, namespace, read=fetch):
    tasks = {
        "controller": (
            lambda: pod_address(pods["controller"], 9092, "/metrics"),
            controller,
        ),
        "gateway": (
            lambda: pod_address(pods["gateway"], 15090, "/stats/prometheus"),
            lambda text: mesh(text, "gateway"),
        ),
        "authorization": (
            lambda: pod_address(pods["rhoai"], 15090, "/stats/prometheus"),
            lambda text: mesh(text, "authorization"),
        ),
        "triton": (
            lambda: (
                f"http://intrinsic-observability-triton.{namespace}.svc.cluster.local:8002/metrics"
            ),
            triton,
        ),
        "ipc": (
            lambda: (
                f"http://rs-icon.{namespace}.svc.cluster.local:9091/v1/operationalStatus"
            ),
            icon,
        ),
    }

    def run(task):
        address, parse = task
        try:
            return parse(read(address()))
        except Exception:
            # Endpoint errors are isolated; no raw exception or response is exported.
            return {}, []

    values, exported = {}, []
    with ThreadPoolExecutor(max_workers=5) as executor:
        for found, metrics in executor.map(run, tasks.values()):
            values.update(found)
            exported.extend(metrics)
    return {"signals": values, "metrics": exported}
