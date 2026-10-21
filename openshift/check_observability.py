# This project was developed with assistance from AI tools.
"""Read-only E8 acceptance: live sources, Prometheus scrape, and rule evaluation."""

import argparse
import json
import re
import select
import ssl
import subprocess
import urllib.parse
import urllib.request
from pathlib import Path

from common import PilotError
from observability import NAME, rules
from render_config import DEFAULT_CONFIG_PATH
from runtime_config import load_runtime_target

SIGNALS = {
    "controller_reconcile_latency",
    "controller_reconcile_errors",
    "gateway_grpc_latency",
    "gateway_grpc_errors",
    "inference_authorization_failures",
    "inference_request_latency",
    "inference_request_errors",
    "gpu_memory_utilization",
    "simulator_ipc_connectivity",
}
LIMIT = 8_000_000
NATIVE_METRICS = {
    "controller_reconcile_duration_seconds_sum",
    "controller_reconcile_duration_seconds_count",
    "controller_reconciles_total",
    "controller_reconcile_errors_total",
    "gateway_grpc_requests_total",
    "gateway_grpc_errors_total",
    "gateway_grpc_unexpected_errors_total",
    "gateway_grpc_duration_seconds_sum",
    "gateway_grpc_duration_seconds_count",
    "inference_authorization_failures_total",
    "inference_request_duration_seconds_sum",
    "inference_request_duration_seconds_count",
    "inference_request_errors_total",
    "inference_request_success_total",
    "inference_gpu_memory_used_bytes",
    "inference_gpu_memory_total_bytes",
    "simulator_ipc_connected",
}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def request_json(url, *, context=None, token=None):
    handlers = [NoRedirect]
    if url.startswith("http://127.0.0.1:"):
        handlers.append(urllib.request.ProxyHandler({}))
    if context:
        handlers.append(urllib.request.HTTPSHandler(context=context))
    opener = urllib.request.build_opener(*handlers)
    request = urllib.request.Request(
        url, headers={"Authorization": "Bearer " + token} if token else {}
    )
    with opener.open(request, timeout=15) as response:
        raw = response.read(LIMIT + 1)
    if len(raw) > LIMIT:
        raise ValueError("response_too_large")
    return json.loads(raw)


def snapshot(target):
    command = [
        "oc",
        "--context",
        target.document["local"]["context"],
        "-n",
        target.namespace,
        "port-forward",
        "service/" + NAME,
        ":9099",
        "--address=127.0.0.1",
    ]
    process = subprocess.Popen(
        command, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True
    )
    try:
        if not select.select([process.stdout], [], [], 15)[0]:
            raise ValueError("forward_timeout")
        match = re.search(r"127\.0\.0\.1:(\d+)", process.stdout.readline())
        if not match:
            raise ValueError("forward_unavailable")
        return request_json("http://127.0.0.1:" + match[1] + "/api/status")
    finally:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)


def snapshot_checks(data):
    observation = data.get("data") or {}
    components = observation.get("components", {})
    assignments = observation.get("assignments", {})
    signals = observation.get("native", {}).get("signals", {})
    fresh = (
        data.get("collection_success") is True
        and 0 <= data.get("age_seconds", 999) < 90
    )
    return {
        "collection_fresh": fresh,
        "components_ready": fresh
        and len(components) == 10
        and all(c.get("ready") is True for c in components.values()),
        "assignments_ready": fresh
        and len(assignments) == 4
        and all(a.get("ready") is True for a in assignments.values()),
        "native_sources_available": fresh
        and SIGNALS <= signals.keys()
        and not observation.get("unavailable_signals"),
        "simulator_ipc_enabled": fresh
        and signals.get("simulator_ipc_connectivity", {}).get("value") == 1,
    }


def normalized_rule_query(expression, namespace):
    """Ignore formatting and only the monitoring stack's injected project label."""

    def selector(match):
        matchers = re.findall(
            r'([a-zA-Z_][a-zA-Z0-9_]*)\s*(=~|!~|!=|=)\s*("(?:[^"\\]|\\.)*")',
            match[0][1:-1],
        )
        retained = [
            name + operator + value
            for name, operator, value in matchers
            if not (
                name == "namespace"
                and operator == "="
                and json.loads(value) == namespace
            )
        ]
        return "{" + ",".join(sorted(retained)) + "}" if retained else ""

    expression = re.sub(r'\{(?:[^"{}]|"(?:[^"\\]|\\.)*")*\}', selector, expression)
    return re.sub(
        r'"(?:[^"\\]|\\.)*"|\s+',
        lambda match: match[0] if match[0].startswith('"') else "",
        expression,
    )


def monitoring_checks(target, access):
    context = ssl.create_default_context()
    for name in ("default-ingress-cert", "trusted-ca-bundle"):
        response = access.oc(
            "get",
            "configmap",
            name,
            "-n",
            "openshift-config-managed",
            "-o",
            "json",
            check=False,
        )
        if response.returncode:
            continue
        for value in json.loads(response.stdout).get("data", {}).values():
            certs = re.findall(
                r"-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----", value, re.S
            )
            if certs:
                context.load_verify_locations(cadata="\n".join(certs))
    route = json.loads(
        access.oc(
            "get", "route", "thanos-querier", "-n", "openshift-monitoring", "-o", "json"
        ).stdout
    )
    host = route["spec"]["host"]
    if not re.fullmatch(r"[a-zA-Z0-9.-]+", host):
        raise ValueError("invalid_monitoring_host")
    base = "https://" + host
    token = access.oc("whoami", "-t").stdout.strip()
    if not token:
        raise ValueError("monitoring_identity_unavailable")
    query = f'up{{namespace="{target.namespace}",service="{NAME}"}}'
    scrape = request_json(
        base + "/api/v1/query?" + urllib.parse.urlencode({"query": query}),
        context=context,
        token=token,
    )
    samples = scrape.get("data", {}).get("result", [])
    # Verify every HA scrape series if the querier is configured without deduplication.
    healthy_scrape = (
        scrape.get("status") == "success"
        and bool(samples)
        and all(float(s["value"][1]) == 1 for s in samples)
    )
    response = request_json(
        base
        + "/api/v1/rules?"
        + urllib.parse.urlencode(
            {"type": "alert", "rule_group[]": "intrinsic-operational-health"}
        ),
        context=context,
        token=token,
    )
    expected = {rule["alert"]: rule for rule in rules()}
    found = [
        rule
        for group in response.get("data", {}).get("groups", [])
        if group.get("name") == "intrinsic-operational-health"
        and Path(group.get("file", "")).name.startswith(
            target.namespace + "-" + NAME + "-"
        )
        for rule in group.get("rules", [])
        if rule.get("name") in expected
        and normalized_rule_query(rule.get("query", ""), target.namespace)
        == normalized_rule_query(expected[rule["name"]]["expr"], target.namespace)
        and rule.get("duration") == int(expected[rule["name"]]["for"][:-1]) * 60
        and expected[rule["name"]]["labels"].items() <= rule.get("labels", {}).items()
        and expected[rule["name"]]["annotations"].items()
        <= rule.get("annotations", {}).items()
    ]
    healthy_rules = (
        response.get("status") == "success"
        and {rule["name"] for rule in found} == set(expected)
        and all(
            rule.get("health") == "ok" and not rule.get("lastError") for rule in found
        )
    )
    # Demand actual ingested native data, not only a working exporter endpoint.
    query = f'count(intrinsic_observability_signal_available{{namespace="{target.namespace}"}} == 1)'
    coverage = request_json(
        base + "/api/v1/query?" + urllib.parse.urlencode({"query": query}),
        context=context,
        token=token,
    )
    measured = coverage.get("data", {}).get("result", [])
    ingested = (
        coverage.get("status") == "success"
        and len(measured) == 1
        and float(measured[0]["value"][1]) == len(SIGNALS)
    )
    query = f'count by (__name__) ({{namespace="{target.namespace}",service="{NAME}",__name__=~"intrinsic_controller_.*|intrinsic_gateway_.*|intrinsic_inference_.*|intrinsic_simulator_ipc_connected"}})'
    families = request_json(
        base + "/api/v1/query?" + urllib.parse.urlencode({"query": query}),
        context=context,
        token=token,
    )
    ingested_names = {
        row.get("metric", {}).get("__name__")
        for row in families.get("data", {}).get("result", [])
    }
    metrics_ingested = (
        families.get("status") == "success"
        and {"intrinsic_" + name for name in NATIVE_METRICS} <= ingested_names
    )
    return {
        "prometheus_scrape_healthy": healthy_scrape,
        "alert_rules_healthy": healthy_rules,
        "native_signals_ingested": ingested,
        "native_metrics_ingested": metrics_ingested,
    }


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    args = parser.parse_args(argv)
    try:
        target = load_runtime_target(args.config)
        access = target.connect()
        checks = snapshot_checks(snapshot(target))
        checks.update(monitoring_checks(target, access))
        passed = all(checks.values())
        print(
            json.dumps(
                {"read_only": True, "passed": passed, "checks": checks}, sort_keys=True
            )
        )
        return 0 if passed else 1
    except (
        PilotError,
        OSError,
        ValueError,
        TypeError,
        KeyError,
        subprocess.SubprocessError,
    ):
        print(
            json.dumps(
                {
                    "read_only": True,
                    "passed": False,
                    "error": "observability_check_unavailable",
                    "raw_details_suppressed": True,
                }
            )
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
