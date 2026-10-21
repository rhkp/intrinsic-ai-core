# This project was developed with assistance from AI tools.
"""Render/apply an isolated, port-forward-only Grafana for the E8 metrics."""

import argparse
import hashlib
import importlib.util
import json
import os
import secrets
from pathlib import Path

import yaml

from common import PilotError
from image_model import normalize_lock
from render_config import DEFAULT_CONFIG_PATH, load_image_lock
from runtime_config import load_runtime_target
from validate_demo_gates import Reader

ROOT = Path(__file__).resolve().parent
NAME = "intrinsic-grafana"
LABELS = {"app.kubernetes.io/name": NAME, "app.kubernetes.io/part-of": NAME}
spec = importlib.util.spec_from_file_location(
    "grafana_dashboard", ROOT / "observability/grafana_dashboard.py"
)
model = importlib.util.module_from_spec(spec)
spec.loader.exec_module(model)


def render(target, lock, uid):
    if type(uid) is not int or uid < 1:
        raise ValueError("project_uid_required")
    ns = target.namespace
    images = {
        image["name"]: image
        for image in normalize_lock(lock, target.document["target"])["project_images"]
    }

    def image(name):
        entry = images[name]
        return entry["repository"] + "@" + entry["digest"]

    sources = {
        "grafana_provision.py": (
            ROOT / "observability/grafana_provision.py"
        ).read_text(),
        "dashboard.json": json.dumps(model.dashboard(ns), sort_keys=True),
        "providers.yaml": json.dumps(
            {
                "apiVersion": 1,
                "providers": [
                    {
                        "name": "Intrinsic E8",
                        "orgId": 1,
                        "folder": "Intrinsic",
                        "type": "file",
                        "disableDeletion": True,
                        "allowUiUpdates": False,
                        "updateIntervalSeconds": 30,
                        "options": {"path": "/dashboards"},
                    }
                ],
            }
        ),
    }
    checksum = hashlib.sha256(json.dumps(sources, sort_keys=True).encode()).hexdigest()

    def resource(kind, name=NAME, api="v1", **fields):
        return {
            "apiVersion": api,
            "kind": kind,
            "metadata": {"name": name, "namespace": ns, "labels": LABELS},
            **fields,
        }

    security = {
        "allowPrivilegeEscalation": False,
        "readOnlyRootFilesystem": True,
        "capabilities": {"drop": ["ALL"]},
    }
    python_mounts = [
        {"name": "source", "mountPath": "/source", "readOnly": True},
        {"name": "api", "mountPath": "/var/run/grafana-api", "readOnly": True},
        {"name": "admin", "mountPath": "/var/run/grafana-admin", "readOnly": True},
        {"name": "datasources", "mountPath": "/etc/grafana/provisioning/datasources"},
    ]
    python = {
        "image": image("intrinsic-rviz-novnc"),
        "imagePullPolicy": "IfNotPresent",
        "securityContext": security,
        "volumeMounts": python_mounts,
        "resources": {
            "requests": {"cpu": "10m", "memory": "32Mi"},
            "limits": {"cpu": "100m", "memory": "128Mi"},
        },
    }
    env = {
        "GF_SERVER_HTTP_ADDR": "127.0.0.1",
        "GF_AUTH_ANONYMOUS_ENABLED": "true",
        "GF_AUTH_ANONYMOUS_ORG_ROLE": "Viewer",
        "GF_AUTH_DISABLE_LOGIN_FORM": "true",
        "GF_USERS_ALLOW_SIGN_UP": "false",
        "GF_SECURITY_ADMIN_PASSWORD__FILE": "/var/run/grafana-admin/admin-password",
        "GF_SECURITY_SECRET_KEY__FILE": "/var/run/grafana-admin/secret-key",
        "GF_PATHS_DATA": "/var/lib/grafana",
        "GF_PATHS_LOGS": "/tmp/logs",
        "GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH": "/dashboards/dashboard.json",
        "GF_ANALYTICS_REPORTING_ENABLED": "false",
        "GF_ANALYTICS_CHECK_FOR_UPDATES": "false",
        "GF_ANALYTICS_CHECK_FOR_PLUGIN_UPDATES": "false",
        "GF_NEWS_NEWS_FEED_ENABLED": "false",
        "GF_SECURITY_DISABLE_GRAVATAR": "true",
        "GF_PLUGINS_PREINSTALL_DISABLED": "true",
        "GF_UNIFIED_ALERTING_ENABLED": "false",
    }
    health = {
        "exec": {
            "command": [
                "/bin/sh",
                "-c",
                "wget -q -O /dev/null http://127.0.0.1:3000/api/health",
            ]
        },
        "periodSeconds": 10,
        "timeoutSeconds": 5,
        "failureThreshold": 6,
    }
    return [
        resource("ConfigMap", data=sources),
        {
            **resource("ConfigMap", NAME + "-ca"),
            "metadata": {
                "name": NAME + "-ca",
                "namespace": ns,
                "labels": LABELS,
                "annotations": {"service.beta.openshift.io/inject-cabundle": "true"},
            },
        },
        resource("ServiceAccount", automountServiceAccountToken=False),
        resource(
            "RoleBinding",
            api="rbac.authorization.k8s.io/v1",
            roleRef={
                "apiGroup": "rbac.authorization.k8s.io",
                "kind": "ClusterRole",
                "name": "view",
            },
            subjects=[{"kind": "ServiceAccount", "name": NAME, "namespace": ns}],
        ),
        resource(
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
                            "runAsUser": uid,
                            "fsGroup": uid,
                            "seccompProfile": {"type": "RuntimeDefault"},
                        },
                        "initContainers": [
                            {
                                **python,
                                "name": "datasource-init",
                                "command": [
                                    "python3",
                                    "-I",
                                    "/source/grafana_provision.py",
                                    "--init",
                                ],
                            }
                        ],
                        "containers": [
                            {
                                "name": "grafana",
                                "image": image(NAME),
                                "imagePullPolicy": "IfNotPresent",
                                "env": [
                                    {"name": name, "value": value}
                                    for name, value in env.items()
                                ],
                                "ports": [{"name": "http", "containerPort": 3000}],
                                "securityContext": security,
                                "readinessProbe": health,
                                "livenessProbe": {**health, "initialDelaySeconds": 60},
                                "resources": {
                                    "requests": {"cpu": "100m", "memory": "256Mi"},
                                    "limits": {"cpu": "1", "memory": "768Mi"},
                                },
                                "volumeMounts": [
                                    {
                                        "name": "admin",
                                        "mountPath": "/var/run/grafana-admin",
                                        "readOnly": True,
                                    },
                                    {
                                        "name": "datasources",
                                        "mountPath": "/etc/grafana/provisioning/datasources",
                                        "readOnly": True,
                                    },
                                    {
                                        "name": "providers",
                                        "mountPath": "/etc/grafana/provisioning/dashboards",
                                        "readOnly": True,
                                    },
                                    {
                                        "name": "dashboard",
                                        "mountPath": "/dashboards",
                                        "readOnly": True,
                                    },
                                    {"name": "data", "mountPath": "/var/lib/grafana"},
                                    {"name": "temporary", "mountPath": "/tmp"},
                                ],
                            },
                            {
                                **python,
                                "name": "token-refresh",
                                "command": [
                                    "python3",
                                    "-I",
                                    "/source/grafana_provision.py",
                                ],
                            },
                        ],
                        "volumes": [
                            {
                                "name": "source",
                                "configMap": {
                                    "name": NAME,
                                    "items": [
                                        {
                                            "key": "grafana_provision.py",
                                            "path": "grafana_provision.py",
                                        }
                                    ],
                                },
                            },
                            {
                                "name": "providers",
                                "configMap": {
                                    "name": NAME,
                                    "items": [
                                        {
                                            "key": "providers.yaml",
                                            "path": "providers.yaml",
                                        }
                                    ],
                                },
                            },
                            {
                                "name": "dashboard",
                                "configMap": {
                                    "name": NAME,
                                    "items": [
                                        {
                                            "key": "dashboard.json",
                                            "path": "dashboard.json",
                                        }
                                    ],
                                },
                            },
                            {
                                "name": "admin",
                                "secret": {
                                    "secretName": NAME + "-admin",
                                    "defaultMode": 288,
                                },
                            },
                            {
                                "name": "api",
                                "projected": {
                                    "defaultMode": 288,
                                    "sources": [
                                        {
                                            "serviceAccountToken": {
                                                "path": "token",
                                                "expirationSeconds": 3600,
                                            }
                                        },
                                        {
                                            "configMap": {
                                                "name": NAME + "-ca",
                                                "items": [
                                                    {
                                                        "key": "service-ca.crt",
                                                        "path": "service-ca.crt",
                                                    }
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
                            {
                                "name": "datasources",
                                "emptyDir": {"medium": "Memory", "sizeLimit": "4Mi"},
                            },
                            {"name": "data", "emptyDir": {"sizeLimit": "512Mi"}},
                            {"name": "temporary", "emptyDir": {"sizeLimit": "64Mi"}},
                        ],
                    },
                },
            },
        ),
        resource(
            "Service",
            spec={
                "selector": {"app.kubernetes.io/name": NAME},
                "ports": [{"name": "http", "port": 3000, "targetPort": "http"}],
            },
        ),
        resource(
            "NetworkPolicy",
            api="networking.k8s.io/v1",
            spec={
                "podSelector": {"matchLabels": {"app.kubernetes.io/name": NAME}},
                "policyTypes": ["Ingress", "Egress"],
                "ingress": [],
                "egress": [
                    {
                        "to": [
                            {
                                "namespaceSelector": {
                                    "matchLabels": {
                                        "kubernetes.io/metadata.name": "openshift-monitoring"
                                    }
                                }
                            }
                        ],
                        "ports": [{"protocol": "TCP", "port": 9092}],
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
                            for protocol in ["TCP", "UDP"]
                            for port in [53, 5353]
                        ],
                    },
                ],
            },
        ),
    ]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    parser.add_argument(
        "--output", type=Path, default=ROOT / ".generated/e8/grafana/resources.yaml"
    )
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args(argv)
    try:
        target = load_runtime_target(args.config)
        target.connect()
        reader = Reader(target)
        namespace = reader.get("namespace", target.namespace)
        uid = int(
            namespace["metadata"]["annotations"]["openshift.io/sa.scc.uid-range"].split(
                "/"
            )[0]
        )
        objects = render(target, load_image_lock(target.document, args.config), uid)
        text = (
            "# This project was developed with assistance from AI tools.\n"
            + yaml.safe_dump_all(objects, sort_keys=False)
        )
        args.output.parent.mkdir(parents=True, exist_ok=True)
        fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as file:
            file.write(text)
        if args.apply:
            for obj in objects + [
                {"kind": "Secret", "metadata": {"name": NAME + "-admin"}}
            ]:
                existing = reader.oc(
                    "get",
                    obj["kind"],
                    obj["metadata"]["name"],
                    "-n",
                    target.namespace,
                    "--ignore-not-found",
                    "-o",
                    "jsonpath={.metadata.labels.app\\.kubernetes\\.io/part-of}",
                ).stdout.strip()
                if existing and existing != NAME:
                    raise ValueError("existing_grafana_resource_not_owned")
                if not existing:
                    # Distinguish a missing object from an existing unlabelled object.
                    name = reader.oc(
                        "get",
                        obj["kind"],
                        obj["metadata"]["name"],
                        "-n",
                        target.namespace,
                        "--ignore-not-found",
                        "-o",
                        "name",
                    ).stdout.strip()
                    if name:
                        raise ValueError("existing_grafana_resource_not_owned")
            reader.oc(
                "apply",
                "--dry-run=server",
                "-n",
                target.namespace,
                "-f",
                "-",
                input_data=text,
            )
            exists = reader.oc(
                "get",
                "secret",
                NAME + "-admin",
                "-n",
                target.namespace,
                "--ignore-not-found",
                "-o",
                "name",
            ).stdout.strip()
            if not exists:
                secret = {
                    "apiVersion": "v1",
                    "kind": "Secret",
                    "type": "Opaque",
                    "metadata": {
                        "name": NAME + "-admin",
                        "namespace": target.namespace,
                        "labels": LABELS,
                    },
                    "stringData": {
                        "admin-password": secrets.token_urlsafe(48),
                        "secret-key": secrets.token_urlsafe(48),
                    },
                }
                reader.oc(
                    "create",
                    "-n",
                    target.namespace,
                    "-f",
                    "-",
                    input_data=json.dumps(secret),
                )
            reader.oc("apply", "-n", target.namespace, "-f", "-", input_data=text)
        print(
            json.dumps(
                {
                    "rendered_objects": len(objects),
                    "applied": args.apply,
                    "scope": "project_only",
                    "access": "port_forward_only",
                    "new_prometheus": False,
                }
            )
        )
        return 0
    except (PilotError, OSError, ValueError, TypeError, KeyError):
        print(
            json.dumps(
                {
                    "error": "grafana_render_or_apply_failed",
                    "raw_details_suppressed": True,
                }
            )
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
