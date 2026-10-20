# This project was developed with assistance from AI tools.
"""Render or explicitly apply project-scoped E8 monitoring resources."""

from __future__ import annotations

import argparse
import hashlib
import ipaddress
import json
import os
import re
from pathlib import Path

import yaml

from common import PilotError
from render_config import DEFAULT_CONFIG_PATH, load_image_lock
from runtime_config import load_runtime_target
from validate_demo_gates import Reader

ROOT = Path(__file__).resolve().parent
NAME = "intrinsic-observability"
LABELS = {
    "app.kubernetes.io/name": NAME,
    "app.kubernetes.io/part-of": "intrinsic-observability",
}
HEADER = "# This project was developed with assistance from AI tools.\n"
RUNBOOK = "https://github.com/rhkp/intrinsic-ai-core/blob/main/openshift/observability/README.md"


def rules():
    healthy = "on(namespace) (intrinsic_observability_collection_success == 1)"
    definitions = [
        (
            "IntrinsicObservabilityScrapeMissing",
            f'absent(up{{service="{NAME}"}}) or (up{{service="{NAME}"}} == 0)',
            "5m",
            "warning",
            "Observability scrape is unavailable",
            "collection",
        ),
        (
            "IntrinsicObservabilityCollectionFailed",
            "intrinsic_observability_collection_success == 0",
            "3m",
            "warning",
            "Read-only workload collection is failing or stale",
            "collection",
        ),
        (
            "IntrinsicComponentNotReady",
            f"(intrinsic_component_ready == 0) and {healthy}",
            "5m",
            "warning",
            "A required demo component is not ready",
            "workload-readiness",
        ),
        (
            "IntrinsicAssignmentNotReady",
            f"(intrinsic_assignment_ready == 0) and {healthy}",
            "5m",
            "warning",
            "A demo assignment is not ready and settled",
            "assignment-settlement",
        ),
        (
            "IntrinsicAssignmentFailed",
            f"(intrinsic_assignment_failed == 1) and {healthy}",
            "2m",
            "warning",
            "A current assignment or latest ResourceSet failed",
            "assignment-settlement",
        ),
        (
            "IntrinsicAssignmentSettlementSlow",
            f"(intrinsic_assignment_unsettled_seconds > 600) and {healthy}",
            "2m",
            "warning",
            "The latest ResourceSet has not settled within ten minutes",
            "assignment-settlement",
        ),
        (
            "IntrinsicComponentRestartedRepeatedly",
            f"(intrinsic_component_restarts >= 3) and {healthy}",
            "10m",
            "warning",
            "A current demo component has repeated container restarts",
            "workload-readiness",
        ),
        (
            "IntrinsicImageOrRegistryWarning",
            f'(intrinsic_recent_warning_events{{reason=~"image_pull|registry_auth"}} > 0) and {healthy}',
            "2m",
            "warning",
            "Recent image pull or registry credential warning",
            "image-pull-and-registry",
        ),
        (
            "IntrinsicStorageOrSchedulingWarning",
            f'(intrinsic_recent_warning_events{{reason=~"storage|scheduling"}} > 0) and {healthy}',
            "2m",
            "warning",
            "Recent storage or scheduling warning",
            "storage-and-placement",
        ),
    ]
    return [
        {
            "alert": name,
            "expr": expr,
            "for": duration,
            "labels": {"severity": severity, "team": "intrinsic-demo"},
            "annotations": {
                "summary": summary,
                "description": "Use the linked runbook to inspect this project. This alert does not establish inference or motion-cycle success.",
                "runbook_url": RUNBOOK + "#" + anchor,
            },
        }
        for name, expr, duration, severity, summary, anchor in definitions
    ]


def render(target, lock, api_addresses):
    ns = target.namespace
    endpoint = target.document["target"]["networking"]["rhoai_triton_endpoint"]
    rhoai = re.fullmatch(
        r"([a-z0-9-]+)\.([a-z0-9-]+)\.svc\.cluster\.local:9000", endpoint
    )
    if not rhoai or rhoai[2] != ns:
        raise ValueError("same_project_rhoai_service_required")
    image = next(
        i for i in lock["project_images"] if i["name"] == "intrinsic-rviz-novnc"
    )
    registry = target.document["target"]["registry"]["internal_host"]
    runtime = f"{registry}/{ns}/intrinsic-rviz-novnc@{image['digest']}"
    sources = {
        name: (ROOT / "observability" / name).read_text()
        for name in ("observer.py", "index.html", "dashboard.js", "dashboard.css")
    }
    checksum = hashlib.sha256(json.dumps(sources, sort_keys=True).encode()).hexdigest()

    def resource(kind, name=NAME, api="v1", **fields):
        return {
            "apiVersion": api,
            "kind": kind,
            "metadata": {"name": name, "namespace": ns, "labels": LABELS},
            **fields,
        }

    api_cidrs = [
        str(
            ipaddress.ip_network(
                address + ("/128" if ":" in address else "/32"), strict=False
            )
        )
        for address in sorted(set(api_addresses))
    ]
    if not api_cidrs:
        raise ValueError("approved_api_endpoints_required")
    deployment = resource(
        "Deployment",
        api="apps/v1",
        spec={
            "replicas": 1,
            "strategy": {"type": "Recreate"},
            "selector": {"matchLabels": {"app.kubernetes.io/name": NAME}},
            "template": {
                "metadata": {
                    "labels": {
                        **LABELS,
                        "cloudrobotics.com/opt-out-error-checking": "true",
                    },
                    "annotations": {
                        "sidecar.istio.io/inject": "false",
                        "intrinsic.ai/source-sha256": checksum,
                    },
                },
                "spec": {
                    "serviceAccountName": NAME,
                    "automountServiceAccountToken": False,
                    "securityContext": {
                        "runAsNonRoot": True,
                        "seccompProfile": {"type": "RuntimeDefault"},
                    },
                    "containers": [
                        {
                            "name": "observer",
                            "image": runtime,
                            "imagePullPolicy": "IfNotPresent",
                            "command": ["python3", "-I", "/app/observer.py"],
                            "env": [
                                {"name": "PYTHONDONTWRITEBYTECODE", "value": "1"},
                                {"name": "RHOAI_SERVICE_NAME", "value": rhoai[1]},
                            ],
                            "ports": [{"name": "metrics", "containerPort": 9099}],
                            "readinessProbe": {
                                "httpGet": {"path": "/readyz", "port": "metrics"},
                                "periodSeconds": 10,
                                "failureThreshold": 6,
                            },
                            "livenessProbe": {
                                "httpGet": {"path": "/healthz", "port": "metrics"},
                                "initialDelaySeconds": 10,
                                "periodSeconds": 20,
                            },
                            "resources": {
                                "requests": {"cpu": "25m", "memory": "64Mi"},
                                "limits": {"cpu": "250m", "memory": "192Mi"},
                            },
                            "securityContext": {
                                "allowPrivilegeEscalation": False,
                                "readOnlyRootFilesystem": True,
                                "capabilities": {"drop": ["ALL"]},
                            },
                            "volumeMounts": [
                                {
                                    "name": "source",
                                    "mountPath": "/app",
                                    "readOnly": True,
                                },
                                {
                                    "name": "api",
                                    "mountPath": "/var/run/observer-api",
                                    "readOnly": True,
                                },
                            ],
                        }
                    ],
                    "volumes": [
                        {"name": "source", "configMap": {"name": NAME}},
                        {
                            "name": "api",
                            "projected": {
                                "defaultMode": 420,
                                "sources": [
                                    {
                                        "serviceAccountToken": {
                                            "path": "token",
                                            "expirationSeconds": 3600,
                                        }
                                    },
                                    {
                                        "configMap": {
                                            "name": "kube-root-ca.crt",
                                            "items": [
                                                {"key": "ca.crt", "path": "ca.crt"}
                                            ],
                                        }
                                    },
                                    {
                                        "downwardAPI": {
                                            "items": [
                                                {
                                                    "path": "namespace",
                                                    "fieldRef": {
                                                        "fieldPath": "metadata.namespace"
                                                    },
                                                }
                                            ]
                                        }
                                    },
                                ],
                            },
                        },
                    ],
                },
            },
        },
    )
    return [
        resource("ConfigMap", data=sources),
        resource("ServiceAccount", automountServiceAccountToken=False),
        resource(
            "Role",
            api="rbac.authorization.k8s.io/v1",
            rules=[
                {
                    "apiGroups": [""],
                    "resources": ["pods", "events"],
                    "verbs": ["get", "list"],
                },
                {
                    "apiGroups": [""],
                    "resources": ["services"],
                    "resourceNames": [rhoai[1]],
                    "verbs": ["get"],
                },
                {
                    "apiGroups": ["apps"],
                    "resources": ["deployments", "statefulsets", "replicasets"],
                    "verbs": ["get", "list"],
                },
                {
                    "apiGroups": ["apps.cloudrobotics.com"],
                    "resources": ["chartassignments", "resourcesets"],
                    "verbs": ["get", "list"],
                },
            ],
        ),
        resource(
            "RoleBinding",
            api="rbac.authorization.k8s.io/v1",
            roleRef={
                "apiGroup": "rbac.authorization.k8s.io",
                "kind": "Role",
                "name": NAME,
            },
            subjects=[{"kind": "ServiceAccount", "name": NAME, "namespace": ns}],
        ),
        deployment,
        resource(
            "Service",
            spec={
                "type": "ClusterIP",
                "selector": {"app.kubernetes.io/name": NAME},
                "ports": [{"name": "metrics", "port": 9099, "targetPort": "metrics"}],
            },
        ),
        resource(
            "NetworkPolicy",
            api="networking.k8s.io/v1",
            spec={
                "podSelector": {"matchLabels": {"app.kubernetes.io/name": NAME}},
                "policyTypes": ["Ingress", "Egress"],
                "ingress": [
                    {
                        "from": [
                            {
                                "namespaceSelector": {
                                    "matchLabels": {
                                        "kubernetes.io/metadata.name": "openshift-user-workload-monitoring"
                                    }
                                }
                            }
                        ],
                        "ports": [{"protocol": "TCP", "port": 9099}],
                    }
                ],
                "egress": [
                    {
                        "to": [{"ipBlock": {"cidr": cidr}} for cidr in api_cidrs],
                        "ports": [
                            {"protocol": "TCP", "port": 443},
                            {"protocol": "TCP", "port": 6443},
                        ],
                    },
                    {
                        "to": [
                            {
                                "namespaceSelector": {
                                    "matchLabels": {
                                        "kubernetes.io/metadata.name": "openshift-dns"
                                    }
                                }
                            }
                        ],
                        "ports": [
                            {"protocol": protocol, "port": port}
                            for protocol in ("TCP", "UDP")
                            for port in (53, 5353)
                        ],
                    },
                ],
            },
        ),
        resource(
            "ServiceMonitor",
            api="monitoring.coreos.com/v1",
            spec={
                "selector": {"matchLabels": {"app.kubernetes.io/name": NAME}},
                "endpoints": [
                    {
                        "port": "metrics",
                        "path": "/metrics",
                        "interval": "30s",
                        "scrapeTimeout": "10s",
                        "metricRelabelings": [
                            {
                                "action": "labeldrop",
                                "regex": "instance|pod|endpoint|container",
                            }
                        ],
                    }
                ],
            },
        ),
        resource(
            "PrometheusRule",
            api="monitoring.coreos.com/v1",
            spec={
                "groups": [{"name": "intrinsic-operational-health", "rules": rules()}]
            },
        ),
    ]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    parser.add_argument(
        "--output", type=Path, default=ROOT / ".generated/e8/resources.yaml"
    )
    parser.add_argument(
        "--apply",
        action="store_true",
        help="Create/update only the owned E8 resources after a server dry run",
    )
    args = parser.parse_args(argv)
    try:
        target = load_runtime_target(args.config)
        target.connect()
        reader = Reader(target)
        namespace = reader.get("namespace", target.namespace)
        labels = namespace["metadata"].get("labels", {})
        if (
            labels.get("openshift.io/user-monitoring") == "false"
            or labels.get("openshift.io/cluster-monitoring") == "true"
        ):
            raise ValueError("namespace_excluded_from_user_monitoring")
        service = reader.get("service", "kubernetes", namespace="default")
        endpoints = reader.get("endpoints", "kubernetes", namespace="default")
        addresses = service["spec"].get("clusterIPs", [service["spec"]["clusterIP"]])
        addresses += [
            a["ip"]
            for subset in endpoints.get("subsets", [])
            for a in subset.get("addresses", [])
        ]
        objects = render(
            target, load_image_lock(target.document, args.config), addresses
        )
        args.output.parent.mkdir(parents=True, exist_ok=True)
        text = HEADER + yaml.safe_dump_all(objects, sort_keys=False)
        fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as file:
            file.write(text)
        if args.apply:
            for obj in objects:
                existing = reader.oc(
                    "get",
                    obj["kind"],
                    obj["metadata"]["name"],
                    "-n",
                    target.namespace,
                    "--ignore-not-found",
                    "-o",
                    "json",
                ).stdout.strip()
                if (
                    existing
                    and json.loads(existing)
                    .get("metadata", {})
                    .get("labels", {})
                    .get("app.kubernetes.io/part-of")
                    != NAME
                ):
                    raise ValueError("existing_resource_not_owned_by_observability")
            reader.oc(
                "apply",
                "--dry-run=server",
                "-n",
                target.namespace,
                "-f",
                "-",
                input_data=text,
            )
            reader.oc("apply", "-n", target.namespace, "-f", "-", input_data=text)
        print(
            json.dumps(
                {
                    "rendered_objects": len(objects),
                    "applied": args.apply,
                    "scope": "project_only",
                    "application_workloads_modified": False,
                }
            )
        )
        return 0
    except (PilotError, OSError, ValueError, TypeError, KeyError, StopIteration):
        print(
            json.dumps(
                {
                    "error": "observability_render_or_apply_failed",
                    "raw_details_suppressed": True,
                }
            )
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
