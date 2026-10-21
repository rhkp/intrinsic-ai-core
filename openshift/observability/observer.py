# This project was developed with assistance from AI tools.
"""Project-scoped, read-only operational metrics and a private live dashboard."""

from __future__ import annotations

import json
import os
import re
import ssl
import threading
import time
import urllib.request
from collections import deque
from datetime import datetime
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import native

COMPONENTS = {
    "controller": ("Deployment", "chartassignment-controller"),
    "gateway": ("Deployment", "intrinsic-grpc-gateway"),
    "workspace": ("Deployment", "omts-build-workspace"),
    "simulator": ("StatefulSet", "rs-gazebo-simulator"),
    "icon": ("StatefulSet", "rs-icon"),
    "robot": ("StatefulSet", "rs-ur-module"),
    "gripper": ("StatefulSet", "rs-hande-gripper"),
    "planner": ("StatefulSet", "rs-motion-planner-service"),
    "inference_wrapper": ("StatefulSet", "rs-inference-service"),
}
ASSIGNMENTS = {
    "base": "intrinsic-base",
    "application": "intrinsic-app-chart",
    "resources": "resources",
    "skills": "skills",
}
WARNING_REASONS = {
    "ErrImagePull": "image_pull",
    "ImagePullBackOff": "image_pull",
    "FailedToRetrieveImagePullSecret": "registry_auth",
    "FailedScheduling": "scheduling",
    "FailedMount": "storage",
    "FailedAttachVolume": "storage",
    "Unhealthy": "probe",
    "BackOff": "restart",
}
UNAVAILABLE = native.SIGNALS
ROOT = Path(__file__).resolve().parent


def warning_category(event):
    """Classify locally; never export the event's message or object identity."""
    reason = event.get("reason", "")
    message = event.get("message", "").lower()
    if reason in {"Failed", "BackOff"} and any(
        text in message for text in ("pull image", "pulling image", "pulling the image")
    ):
        return "image_pull"
    return WARNING_REASONS.get(reason)


def timestamp(value):
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()
    except (AttributeError, TypeError, ValueError):
        return None


class Client:
    def __init__(self):
        mounted = Path("/var/run/observer-api")
        self.namespace = (mounted / "namespace").read_text().strip()
        if not re.fullmatch(r"[a-z0-9]([-a-z0-9]*[a-z0-9])?", self.namespace):
            raise ValueError("invalid_namespace")
        self.token = mounted / "token"
        self.rhoai_service = os.environ["RHOAI_SERVICE_NAME"]
        if not re.fullmatch(r"[a-z0-9]([-a-z0-9]*[a-z0-9])?", self.rhoai_service):
            raise ValueError("invalid_rhoai_service")
        self.context = ssl.create_default_context(cafile=str(mounted / "ca.crt"))
        self.base = "https://kubernetes.default.svc"

    def get(self, path):
        request = urllib.request.Request(
            self.base + path,
            headers={"Authorization": "Bearer " + self.token.read_text().strip()},
        )
        with urllib.request.urlopen(
            request, context=self.context, timeout=5
        ) as response:
            raw = response.read(4_000_001)
        if len(raw) > 4_000_000:
            raise ValueError("response_too_large")
        data = json.loads(raw)
        if data.get("metadata", {}).get("continue"):
            raise ValueError("incomplete_inventory")
        return data

    def inventory(self):
        ns = self.namespace
        paths = {
            "pods": f"/api/v1/namespaces/{ns}/pods?limit=500",
            "deployments": f"/apis/apps/v1/namespaces/{ns}/deployments?limit=100",
            "statefulsets": f"/apis/apps/v1/namespaces/{ns}/statefulsets?limit=100",
            "replicasets": f"/apis/apps/v1/namespaces/{ns}/replicasets?limit=500",
            "assignments": f"/apis/apps.cloudrobotics.com/v1alpha1/namespaces/{ns}/chartassignments?limit=100",
            "resourcesets": f"/apis/apps.cloudrobotics.com/v1alpha1/namespaces/{ns}/resourcesets?limit=500",
            "events": f"/api/v1/namespaces/{ns}/events?fieldSelector=type%3DWarning&limit=500",
        }
        result = {key: self.get(path).get("items", []) for key, path in paths.items()}
        result["rhoai_selector"] = (
            self.get(f"/api/v1/namespaces/{ns}/services/{self.rhoai_service}")
            .get("spec", {})
            .get("selector", {})
        )
        if not result["rhoai_selector"]:
            raise ValueError("rhoai_predictor_selector_unavailable")
        return result


def pod_ready(pod):
    status = pod.get("status", {})
    containers = status.get("containerStatuses", [])
    return (
        status.get("phase") == "Running"
        and any(
            c.get("type") == "Ready" and c.get("status") == "True"
            for c in status.get("conditions", [])
        )
        and len(containers) == len(pod.get("spec", {}).get("containers", [])) > 0
        and all(c.get("ready") and "running" in c.get("state", {}) for c in containers)
    )


def owned_pods(workload, inventory):
    uid = workload["metadata"]["uid"]
    owners = {uid}
    for rs in inventory["replicasets"]:
        if any(
            owner.get("uid") == uid
            for owner in rs["metadata"].get("ownerReferences", [])
        ):
            owners.add(rs["metadata"]["uid"])
    return [
        pod
        for pod in inventory["pods"]
        if any(
            owner.get("uid") in owners
            for owner in pod["metadata"].get("ownerReferences", [])
        )
    ]


def summarize(inventory, now):
    components = {}
    for label, (kind, name) in COMPONENTS.items():
        collection = inventory[
            "deployments" if kind == "Deployment" else "statefulsets"
        ]
        workload = next((i for i in collection if i["metadata"]["name"] == name), None)
        if workload is None:
            components[label] = {"ready": False, "restarts": 0, "gpu_allocated": 0}
            continue
        spec, status = workload.get("spec", {}), workload.get("status", {})
        desired = spec.get("replicas", 1)
        pods = owned_pods(workload, inventory)
        current = status.get("observedGeneration", 0) >= workload["metadata"].get(
            "generation", 1
        )
        ready = (
            current
            and desired > 0
            and status.get("readyReplicas", 0) == desired
            and len(pods) == desired
            and all(pod_ready(p) for p in pods)
        )
        if kind == "StatefulSet":
            ready = (
                ready
                and bool(status.get("currentRevision"))
                and status.get("currentRevision") == status.get("updateRevision")
            )
        else:
            ready = ready and status.get("updatedReplicas", 0) == desired
        components[label] = {
            "ready": bool(ready),
            "restarts": sum(
                c.get("restartCount", 0)
                for p in pods
                for c in p.get("status", {}).get("containerStatuses", [])
            ),
            "gpu_allocated": sum(
                float(c.get("resources", {}).get("limits", {}).get("nvidia.com/gpu", 0))
                for p in pods
                for c in p.get("spec", {}).get("containers", [])
            ),
        }
    predictors = [
        p
        for p in inventory["pods"]
        if any(
            c["name"] == "kserve-container"
            for c in p.get("spec", {}).get("containers", [])
        )
        and all(
            p["metadata"].get("labels", {}).get(k) == v
            for k, v in inventory["rhoai_selector"].items()
        )
    ]
    components["rhoai"] = {
        "ready": bool(predictors) and all(pod_ready(p) for p in predictors),
        "restarts": sum(
            c.get("restartCount", 0)
            for p in predictors
            for c in p.get("status", {}).get("containerStatuses", [])
        ),
        "gpu_allocated": sum(
            float(c.get("resources", {}).get("limits", {}).get("nvidia.com/gpu", 0))
            for p in predictors
            for c in p.get("spec", {}).get("containers", [])
        ),
    }
    assignments = {}
    for label, name in ASSIGNMENTS.items():
        item = next(
            (i for i in inventory["assignments"] if i["metadata"]["name"] == name), {}
        )
        status = item.get("status", {})
        conditions = {
            c.get("type"): c.get("status") == "True"
            for c in status.get("conditions", [])
        }
        generation_current = status.get("observedGeneration", 0) == item.get(
            "metadata", {}
        ).get("generation", -1)
        candidates = []
        for rs in inventory["resourcesets"]:
            version = re.fullmatch(
                re.escape(name) + r"\.v([0-9]+)", rs["metadata"]["name"]
            )
            if version and rs["metadata"].get("labels", {}).get("name") == name:
                candidates.append((int(version[1]), rs))
        latest = max(candidates, default=(0, {}), key=lambda x: x[0])[1]
        rs_status = latest.get("status", {})
        settled = rs_status.get("phase") == "Settled" and bool(
            rs_status.get("finishedAt")
        )
        start = timestamp(latest.get("metadata", {}).get("creationTimestamp"))
        end = timestamp(rs_status.get("finishedAt"))
        assignments[label] = {
            "ready": generation_current
            and conditions.get("Ready", False)
            and conditions.get("Settled", False)
            and settled,
            "failed": status.get("phase") == "Failed"
            or rs_status.get("phase") == "Failed",
            "generation_current": generation_current,
            "settled": settled,
            "settle_seconds": max(0, end - start)
            if start is not None and end is not None
            else None,
            "unsettled_seconds": max(0, now - start)
            if start is not None and not settled
            else 0,
        }
    warnings = dict.fromkeys(sorted(set(WARNING_REASONS.values())), 0)
    for event in inventory["events"]:
        category = warning_category(event)
        observed = timestamp(
            event.get("series", {}).get("lastObservedTime")
            or event.get("lastTimestamp")
            or event.get("eventTime")
            or event.get("metadata", {}).get("creationTimestamp")
        )
        if category and observed is not None and 0 <= now - observed <= 600:
            warnings[category] += 1
    return {
        "components": components,
        "assignments": assignments,
        "warnings": warnings,
        "unavailable_signals": list(UNAVAILABLE),
        "native": {"signals": {}, "metrics": []},
    }


def metrics(data, success, age):
    lines = [
        "# HELP intrinsic_observability_collection_success Last full read-only API collection succeeded.",
        "# TYPE intrinsic_observability_collection_success gauge",
        f"intrinsic_observability_collection_success {int(success)}",
        "# TYPE intrinsic_observability_snapshot_age_seconds gauge",
        f"intrinsic_observability_snapshot_age_seconds {age:.3f}",
    ]
    for name in (
        "intrinsic_component_ready",
        "intrinsic_component_restarts",
        "intrinsic_component_gpu_allocated",
        "intrinsic_assignment_ready",
        "intrinsic_assignment_failed",
        "intrinsic_assignment_generation_current",
        "intrinsic_assignment_settled",
        "intrinsic_assignment_settle_seconds",
        "intrinsic_assignment_unsettled_seconds",
        "intrinsic_recent_warning_events",
        "intrinsic_observability_signal_available",
    ):
        lines.append(f"# TYPE {name} gauge")
    if data:
        for label, item in data["components"].items():
            for key, suffix in (
                ("ready", "ready"),
                ("restarts", "restarts"),
                ("gpu_allocated", "gpu_allocated"),
            ):
                lines.append(
                    f'intrinsic_component_{suffix}{{component="{label}"}} {float(item[key])}'
                )
        for label, item in data["assignments"].items():
            for key, value in item.items():
                if value is not None:
                    lines.append(
                        f'intrinsic_assignment_{key}{{assignment="{label}"}} {float(value)}'
                    )
        for category, value in data["warnings"].items():
            lines.append(
                f'intrinsic_recent_warning_events{{reason="{category}"}} {value}'
            )
    signals = data.get("native", {}).get("signals", {}) if success and data else {}
    for signal in UNAVAILABLE:
        lines.append(
            f'intrinsic_observability_signal_available{{signal="{signal}"}} {int(signal in signals)}'
        )
    if success and data:
        for name, value in data.get("native", {}).get("metrics", []):
            kind = "counter" if name.endswith(("_total", "_sum", "_count")) else "gauge"
            lines.extend(
                [f"# TYPE intrinsic_{name} {kind}", f"intrinsic_{name} {value}"]
            )
    return "\n".join(lines) + "\n"


class State:
    def __init__(self):
        self.lock = threading.Lock()
        self.data = None
        self.updated = None
        self.success = False
        self.history = deque(maxlen=120)

    def poll(self, client):
        while True:
            started = time.time()
            try:
                inventory = client.inventory()
                data = summarize(inventory, time.time())
                targets = {}
                for label in ("controller", "gateway"):
                    kind, name = COMPONENTS[label]
                    workload = next(
                        (
                            w
                            for w in inventory["deployments"]
                            if w["metadata"]["name"] == name
                        ),
                        None,
                    )
                    targets[label] = (
                        [
                            p
                            for p in owned_pods(workload, inventory)
                            if pod_ready(p)
                            and not p["metadata"].get("deletionTimestamp")
                        ]
                        if workload
                        else []
                    )
                targets["rhoai"] = [
                    p
                    for p in inventory["pods"]
                    if pod_ready(p)
                    and not p["metadata"].get("deletionTimestamp")
                    and all(
                        p["metadata"].get("labels", {}).get(k) == v
                        for k, v in inventory["rhoai_selector"].items()
                    )
                ]
                data["native"] = native.collect(targets, client.namespace)
                data["unavailable_signals"] = [
                    name
                    for name in UNAVAILABLE
                    if name not in data["native"]["signals"]
                ]
                with self.lock:
                    self.data, self.updated, self.success = data, time.time(), True
                    self.history.append(
                        {
                            "time": int(self.updated),
                            "ready": sum(
                                c["ready"] for c in data["components"].values()
                            ),
                            "total": len(data["components"]),
                            "assignments_ready": sum(
                                a["ready"] for a in data["assignments"].values()
                            ),
                        }
                    )
            except Exception:
                # Never log Kubernetes responses, tokens, resource names or exceptions.
                with self.lock:
                    self.success = False
            time.sleep(max(1, 30 - (time.time() - started)))

    def snapshot(self):
        with self.lock:
            age = (
                max(0, time.time() - self.updated) if self.updated is not None else 1e9
            )
            return {
                "collection_success": self.success and age < 90,
                "age_seconds": age,
                "data": self.data,
                "history": list(self.history),
            }


def main():
    state = State()
    client = Client()
    threading.Thread(target=state.poll, args=(client,), daemon=True).start()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_GET(self):
            snapshot = state.snapshot()
            content_type = "text/plain; charset=utf-8"
            code = 200
            if self.path == "/healthz":
                body = "ok\n"
            elif self.path == "/readyz":
                code = 200 if snapshot["collection_success"] else 503
                body = "ready\n" if code == 200 else "collection_unavailable\n"
            elif self.path == "/metrics":
                content_type = "text/plain; version=0.0.4; charset=utf-8"
                body = metrics(
                    snapshot["data"],
                    snapshot["collection_success"],
                    snapshot["age_seconds"],
                )
            elif self.path == "/api/status":
                content_type = "application/json"
                body = json.dumps(snapshot, allow_nan=False)
            elif self.path in ("/", "/dashboard.js", "/dashboard.css"):
                file = {
                    "/": "index.html",
                    "/dashboard.js": "dashboard.js",
                    "/dashboard.css": "dashboard.css",
                }[self.path]
                body = (ROOT / file).read_text()
                content_type = {
                    "index.html": "text/html",
                    "dashboard.js": "application/javascript",
                    "dashboard.css": "text/css",
                }[file]
            else:
                code, body = 404, "not_found\n"
            raw = body.encode()
            self.send_response(code)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(raw)))
            self.send_header("Cache-Control", "no-store")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header(
                "Content-Security-Policy",
                "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; frame-ancestors 'none'",
            )
            self.end_headers()
            self.wfile.write(raw)

    ThreadingHTTPServer(("", 9099), Handler).serve_forever()


if __name__ == "__main__":
    main()
