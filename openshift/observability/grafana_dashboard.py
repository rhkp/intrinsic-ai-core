# This project was developed with assistance from AI tools.
"""A provisioned Grafana dashboard over the existing E8 Prometheus metrics."""

import re

UID = "intrinsic-e8"
DATASOURCE_UID = "intrinsic-prometheus"
RUNBOOK = "https://github.com/rhkp/intrinsic-ai-core/blob/main/openshift/observability/README.md"


def dashboard(namespace):
    if not re.fullmatch(r"[a-z0-9]([-a-z0-9]*[a-z0-9])?", namespace):
        raise ValueError("invalid_namespace")
    scope = '{namespace="$namespace"}'

    def metric(name):
        return "intrinsic_" + name + scope

    def rate(name):
        return f"rate({metric(name)}[$__rate_interval])"

    def mean(name):
        return rate(name + "_sum") + " / " + rate(name + "_count")

    panels = []
    y = 0

    def panel(
        title,
        queries,
        *,
        kind="timeseries",
        unit="short",
        legend="",
        anchor="collection",
        description="",
        width=12,
        height=8,
        x=0,
    ):
        targets = [
            {
                "refId": chr(65 + index),
                "expr": expression,
                "legendFormat": label,
                "range": kind != "stat",
                "instant": kind == "stat",
                "datasource": {"type": "prometheus", "uid": DATASOURCE_UID},
            }
            for index, (expression, label) in enumerate(queries)
        ]
        result = {
            "id": len(panels) + 1,
            "title": title,
            "type": kind,
            "datasource": {"type": "prometheus", "uid": DATASOURCE_UID},
            "gridPos": {"x": x, "y": y, "w": width, "h": height},
            "targets": targets,
            "description": description,
            "fieldConfig": {
                "defaults": {"unit": unit, "noValue": "No observations"},
                "overrides": [],
            },
            "options": {
                "legend": {"displayMode": "list", "placement": "bottom"},
                "tooltip": {"mode": "multi", "sort": "none"},
            },
            "links": [
                {
                    "title": "Recovery runbook",
                    "url": RUNBOOK + "#" + anchor,
                    "targetBlank": True,
                }
            ],
        }
        if kind == "stat":
            result["options"] = {
                "reduceOptions": {
                    "calcs": ["lastNotNull"],
                    "fields": "",
                    "values": False,
                },
                "textMode": "auto",
                "colorMode": "value",
                "graphMode": "none",
                "orientation": "auto",
            }
        panels.append(result)
        return result

    summaries = [
        (
            "Fresh collection",
            "min("
            + metric("observability_collection_success")
            + ")"
            + " * (max(time() - timestamp("
            + metric("observability_collection_success")
            + ")) < bool 90)"
            + ' * min(up{namespace="$namespace",service="intrinsic-observability"})',
            "bool",
            "collection",
        ),
        (
            "Ready components / 10",
            "sum(" + metric("component_ready") + ")",
            "short",
            "workload-readiness",
        ),
        (
            "Ready assignments / 4",
            "sum(" + metric("assignment_ready") + ")",
            "short",
            "assignment-settlement",
        ),
        (
            "Native signals / 9",
            "sum(" + metric("observability_signal_available") + ")",
            "short",
            "native-telemetry",
        ),
        (
            "ICON / hardware enabled",
            metric("simulator_ipc_connected"),
            "bool",
            "simulator-ipc",
        ),
        (
            "Inference GPU memory",
            metric("inference_gpu_memory_used_bytes")
            + " / "
            + metric("inference_gpu_memory_total_bytes"),
            "percentunit",
            "gpu-memory",
        ),
    ]
    for index, (title, expression, unit, anchor) in enumerate(summaries):
        p = panel(
            title,
            [(expression, "")],
            kind="stat",
            unit=unit,
            anchor=anchor,
            width=4,
            height=4,
            x=index * 4,
        )
        steps = (
            [
                {"color": "green", "value": None},
                {"color": "orange", "value": 0.8},
                {"color": "red", "value": 0.9},
            ]
            if unit == "percentunit"
            else [
                {"color": "red", "value": None},
                {"color": "green", "value": [1, 10, 4, 9, 1][index]},
            ]
        )
        p["fieldConfig"]["defaults"]["color"] = {"mode": "thresholds"}
        p["fieldConfig"]["defaults"]["thresholds"] = {
            "mode": "absolute",
            "steps": steps,
        }
    y += 4

    definitions = [
        (
            "Component availability",
            [(metric("component_ready"), "{{component}}")],
            "bool",
            "workload-readiness",
            "Current owned-Pod and rollout readiness; does not establish a successful motion cycle.",
        ),
        (
            "Assignment availability",
            [(metric("assignment_ready"), "{{assignment}}")],
            "bool",
            "assignment-settlement",
            "Current generation and latest ResourceSet must both be ready/settled.",
        ),
        (
            "Controller mean reconciliation duration",
            [(mean("controller_reconcile_duration_seconds"), "Mean reconciliation")],
            "s",
            "controller-and-rpc",
            "Rolling mean over the selected interval. No completed requests produce gaps, not a fabricated zero.",
        ),
        (
            "Controller reconciliations and errors",
            [
                (rate("controller_reconciles_total"), "Reconciliations"),
                (rate("controller_reconcile_errors_total"), "Errors"),
            ],
            "ops",
            "controller-and-rpc",
            "Rates handle source counter resets.",
        ),
        (
            "Gateway mean completed gRPC duration",
            [(mean("gateway_grpc_duration_seconds"), "Mean completed RPC")],
            "s",
            "controller-and-rpc",
            "Streaming request duration includes the stream lifetime.",
        ),
        (
            "Gateway requests and failures",
            [
                (rate("gateway_grpc_requests_total"), "Requests"),
                (rate("gateway_grpc_errors_total"), "All non-OK statuses"),
                (rate("gateway_grpc_unexpected_errors_total"), "Unexpected failures"),
            ],
            "ops",
            "controller-and-rpc",
            "Unexpected failures exclude CANCELLED and SDK UNIMPLEMENTED discovery responses; all failures remain visible.",
        ),
        (
            "Mean model inference duration",
            [
                (
                    mean("inference_request_duration_seconds"),
                    "RF-DETR / FoundationPose mean",
                )
            ],
            "s",
            "inference",
            "Triton execution timing for the two intended models; gaps mean no recent completed inference.",
        ),
        (
            "Inference and authorization failures",
            [
                (rate("inference_request_errors_total"), "Model request failures"),
                (
                    rate("inference_authorization_failures_total"),
                    "Authorization rejections",
                ),
            ],
            "ops",
            "inference",
            "Read-only observations. This panel never submits inference or modifies models.",
        ),
        (
            "Inference GPU device memory",
            [
                (metric("inference_gpu_memory_used_bytes"), "Used"),
                (metric("inference_gpu_memory_total_bytes"), "Capacity"),
            ],
            "bytes",
            "gpu-memory",
            "Device memory visible to Triton, including other processes sharing those devices; not simulator GPU memory.",
        ),
        (
            "Functional simulator IPC health",
            [
                (
                    metric("simulator_ipc_connected"),
                    "ICON operational + cell hardware enabled",
                )
            ],
            "bool",
            "simulator-ipc",
            "Functional status, not a sampled Unix-socket or reconnect counter. No reset or motion is issued.",
        ),
        (
            "Native telemetry coverage",
            [(metric("observability_signal_available"), "{{signal}}")],
            "bool",
            "native-telemetry",
            "Zero means a required source is unavailable; missing measurements must not be interpreted as healthy zeros.",
        ),
        (
            "Collection age",
            [(metric("observability_snapshot_age_seconds"), "Snapshot age")],
            "s",
            "collection",
            "Freshness must be checked before interpreting other measurements.",
        ),
        (
            "Current container restarts",
            [(metric("component_restarts"), "{{component}}")],
            "short",
            "workload-readiness",
            "Current-Pod gauges reset on replacement; not lifetime counters.",
        ),
        (
            "Allocated GPUs",
            [(metric("component_gpu_allocated"), "{{component}}")],
            "short",
            "storage-and-placement",
            "Requested allocations are not device utilization or available cluster capacity.",
        ),
        (
            "Latest ResourceSet settlement",
            [(metric("assignment_settle_seconds"), "{{assignment}}")],
            "s",
            "assignment-settlement",
            "Duration for the latest retained ResourceSet version, not a histogram.",
        ),
        (
            "Unsettled ResourceSet age",
            [(metric("assignment_unsettled_seconds"), "{{assignment}}")],
            "s",
            "assignment-settlement",
            "Elapsed time while the current ResourceSet remains unsettled.",
        ),
        (
            "Recent deployment warnings",
            [(metric("recent_warning_events"), "{{reason}}")],
            "short",
            "image-pull-and-registry",
            "Retained warning event objects observed in ten minutes; not event occurrence counters.",
        ),
        (
            "Assignment failures",
            [(metric("assignment_failed"), "{{assignment}}")],
            "bool",
            "assignment-settlement",
            "Current assignment or latest ResourceSet failure.",
        ),
    ]
    for index, (title, queries, unit, anchor, description) in enumerate(definitions):
        panel(
            title,
            queries,
            unit=unit,
            anchor=anchor,
            description=description,
            x=12 * (index % 2),
        )
        if index % 2:
            y += 8

    return {
        "uid": UID,
        "id": None,
        "title": "Intrinsic E8 Operations",
        "schemaVersion": 38,
        "version": 1,
        "editable": False,
        "tags": ["intrinsic", "e8", "provisioned"],
        "description": "Project-scoped history from the existing OpenShift Prometheus. Read-only; no inference, reset, motion or alert administration.",
        "timezone": "browser",
        "refresh": "30s",
        "time": {"from": "now-1h", "to": "now"},
        "timepicker": {"refresh_intervals": ["30s", "1m", "5m", "15m"]},
        "templating": {
            "list": [
                {
                    "name": "namespace",
                    "type": "constant",
                    "hide": 2,
                    "query": namespace,
                    "current": {"text": namespace, "value": namespace},
                }
            ]
        },
        "panels": panels,
    }
