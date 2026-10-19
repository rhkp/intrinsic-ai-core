# This project was developed with assistance from AI tools.
import contextlib
import io
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import Mock, patch

from configure_routing import (
    PilotError,
    apply,
    safe_selector,
    verify_project_mesh_membership,
    verified_ingress_selector,
)

from runtime_config import RuntimeTarget, load_runtime_target
from test_runtime_config import write_target

PROJECT = "your-project-namespace"
GATEWAY_SERVICE = "intrinsic-grpc-gateway"
GATEWAY_RESOURCE = "intrinsic-grpc-internal"
EXPECTED_SELECTOR = {
    "app.kubernetes.io/name": GATEWAY_SERVICE,
    "istio": GATEWAY_SERVICE,
}


class RoutingConfigurationTest(unittest.TestCase):
    def test_apply_uses_canonical_config_without_legacy_environment(self):
        with tempfile.TemporaryDirectory() as directory:
            path = write_target(directory)
            target = load_runtime_target(path)
            access = Mock()
            access.oc.return_value.returncode = 0
            with (
                patch.object(RuntimeTarget, "connect", return_value=access),
                patch(
                    "configure_routing.verified_ingress_selector",
                    return_value=target.ingress_selector,
                ) as verify,
                patch.dict("os.environ", {"OPENSHIFT_NAMESPACE": "stale-project"}),
                contextlib.redirect_stdout(io.StringIO()),
            ):
                apply(path)
        verify.assert_called_once_with(
            access,
            target.routing_values(),
            project=target.namespace,
            expected_selector=target.ingress_selector,
        )
        self.assertEqual(access.oc.call_args.args, ("apply", "-f", "-"))
        actual = json.loads(access.oc.call_args.kwargs["input_data"])
        self.assertEqual(actual, target.routing_configmap(target.ingress_selector))
        self.assertNotIn("api_server_url", json.dumps(actual))

    def test_check_mode_never_applies_resources(self):
        with tempfile.TemporaryDirectory() as directory:
            path = write_target(directory)
            target = load_runtime_target(path)
            access = Mock()
            current = target.routing_configmap(target.ingress_selector)
            with (
                patch.object(RuntimeTarget, "connect", return_value=access),
                patch(
                    "configure_routing.verified_ingress_selector",
                    return_value=target.ingress_selector,
                ),
                patch("configure_routing.get_json", return_value=current),
                contextlib.redirect_stdout(io.StringIO()),
            ):
                apply(path, check_only=True)
        access.oc.assert_not_called()

    def test_check_reports_drift_without_applying(self):
        with tempfile.TemporaryDirectory() as directory:
            path = write_target(directory)
            target = load_runtime_target(path)
            for current in [None, {"data": {"stale": "value"}}]:
                access = Mock()
                with (
                    patch.object(RuntimeTarget, "connect", return_value=access),
                    patch(
                        "configure_routing.verified_ingress_selector",
                        return_value=target.ingress_selector,
                    ),
                    patch("configure_routing.get_json", return_value=current),
                ):
                    with self.assertRaises(PilotError):
                        apply(path, check_only=True)
                access.oc.assert_not_called()

    def test_missing_config_and_unverified_gateway_cannot_apply(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "missing.json"
            with patch.object(RuntimeTarget, "connect") as connect:
                with self.assertRaises(PilotError):
                    apply(path)
            connect.assert_not_called()
            path = write_target(directory)
            access = Mock()
            with (
                patch.object(RuntimeTarget, "connect", return_value=access),
                patch(
                    "configure_routing.verified_ingress_selector",
                    side_effect=PilotError("Gateway not Ready"),
                ),
            ):
                with self.assertRaises(PilotError):
                    apply(path)
            access.oc.assert_not_called()

    def test_accepts_simple_service_selector_and_rejects_empty_selector(self):
        self.assertEqual(
            safe_selector({"app": "mesh-ingress", "mesh.io/rev": "stable"}),
            {
                "app": "mesh-ingress",
                "mesh.io/rev": "stable",
            },
        )
        with self.assertRaises(PilotError):
            safe_selector({})

    def test_accepts_ready_namespace_scoped_service_mesh_member(self):
        project = {"metadata": {"labels": {}}}
        rolls = {"items": []}
        member = {
            "spec": {
                "controlPlaneRef": {
                    "namespace": "istio-system",
                    "name": "your-service-mesh-control-plane",
                }
            },
            "status": {"conditions": [{"type": "Ready", "status": "True"}]},
        }
        with patch("configure_routing.get_json", side_effect=[project, member, rolls]):
            verify_project_mesh_membership(object(), "istio-system", project=PROJECT)

    def test_rejects_member_without_control_plane_reference(self):
        project = {"metadata": {"labels": {}}}
        member = {
            "spec": {"controlPlaneRef": {}},
            "status": {"conditions": [{"type": "Ready", "status": "True"}]},
        }
        with patch("configure_routing.get_json", side_effect=[project, member]):
            with self.assertRaises(PilotError):
                verify_project_mesh_membership(
                    object(), "istio-system", project=PROJECT
                )

    def test_rejects_pending_service_mesh_member(self):
        project = {"metadata": {"labels": {}}}
        member = {
            "spec": {
                "controlPlaneRef": {
                    "namespace": "istio-system",
                    "name": "your-service-mesh-control-plane",
                }
            },
            "status": {"conditions": [{"type": "Ready", "status": "False"}]},
        }
        with patch("configure_routing.get_json", side_effect=[project, member]):
            with self.assertRaises(PilotError):
                verify_project_mesh_membership(
                    object(), "istio-system", project=PROJECT
                )

    def test_verifies_mesh_gateway_listener_and_service_mapping(self):
        project = {"metadata": {"labels": {}}}
        rolls = {"items": []}
        member = {
            "spec": {
                "controlPlaneRef": {
                    "namespace": "istio-system",
                    "name": "your-service-mesh-control-plane",
                }
            },
            "status": {"conditions": [{"type": "Ready", "status": "True"}]},
        }
        service = {
            "spec": {
                "type": "ClusterIP",
                "selector": {
                    "app.kubernetes.io/name": "intrinsic-grpc-gateway",
                    "istio": "intrinsic-grpc-gateway",
                },
                "ports": [
                    {"port": 80, "targetPort": 443, "protocol": "TCP"},
                    {"port": 443, "protocol": "TCP"},
                ],
            }
        }
        endpoints = {
            "subsets": [
                {
                    "addresses": [
                        {
                            "ip": "pod-ip",
                            "targetRef": {"kind": "Pod", "name": "gateway-pod"},
                        }
                    ]
                }
            ]
        }

        service_host = "intrinsic-grpc-gateway.your-project-namespace.svc.cluster.local"

        def responses(
            mesh_hosts=None,
            mesh_protocol="HTTPS",
            mesh_tls="ISTIO_MUTUAL",
            include_tunnel=True,
        ):
            if mesh_hosts is None:
                mesh_hosts = [service_host, "*"]
            servers = []
            if include_tunnel:
                servers.append(
                    {
                        "port": {"number": 80, "protocol": "HTTP2"},
                        "hosts": [service_host],
                    }
                )
            servers.append(
                {
                    "port": {"number": 443, "protocol": mesh_protocol},
                    "hosts": mesh_hosts,
                    "tls": {"mode": mesh_tls} if mesh_tls else {},
                }
            )
            gateway = {
                "spec": {
                    "selector": {
                        "app.kubernetes.io/name": "intrinsic-grpc-gateway",
                        "istio": "intrinsic-grpc-gateway",
                    },
                    "servers": servers,
                }
            }
            pods = {
                "items": [
                    {
                        "metadata": {
                            "name": "gateway-pod",
                            "labels": {
                                "app.kubernetes.io/name": "intrinsic-grpc-gateway",
                                "istio": "intrinsic-grpc-gateway",
                            },
                        },
                        "status": {
                            "phase": "Running",
                            "podIP": "pod-ip",
                            "conditions": [{"type": "Ready", "status": "True"}],
                        },
                    }
                ]
            }
            return [project, member, rolls, service, endpoints, gateway, pods]

        values = {
            "INTRINSIC_INGRESS_ADDRESS": f"{GATEWAY_SERVICE}.{PROJECT}.svc.cluster.local:80",
            "INTRINSIC_INGRESS_GATEWAY": f"{PROJECT}/{GATEWAY_RESOURCE}",
        }
        with patch("configure_routing.get_json", side_effect=responses()):
            selector = verified_ingress_selector(
                object(), values, project=PROJECT, expected_selector=EXPECTED_SELECTOR
            )
        self.assertEqual(
            selector,
            {
                "app.kubernetes.io/name": "intrinsic-grpc-gateway",
                "istio": "intrinsic-grpc-gateway",
            },
        )

        with patch(
            "configure_routing.get_json", side_effect=responses(include_tunnel=False)
        ):
            self.assertEqual(
                verified_ingress_selector(
                    object(),
                    values,
                    project=PROJECT,
                    expected_selector=EXPECTED_SELECTOR,
                ),
                EXPECTED_SELECTOR,
            )

        for invalid_target in [80, "plaintext"]:
            invalid_responses = responses()
            invalid_responses[3]["spec"]["ports"][0]["targetPort"] = invalid_target
            with patch("configure_routing.get_json", side_effect=invalid_responses):
                with self.assertRaises(PilotError):
                    verified_ingress_selector(
                        object(),
                        values,
                        project=PROJECT,
                        expected_selector=EXPECTED_SELECTOR,
                    )
        service["spec"]["ports"][0]["targetPort"] = 443

        for kwargs in [
            {"mesh_hosts": [service_host]},
            {"mesh_hosts": ["*"]},
            {"mesh_protocol": "HTTP2"},
            {"mesh_tls": "SIMPLE"},
        ]:
            with self.subTest(**kwargs):
                with patch(
                    "configure_routing.get_json", side_effect=responses(**kwargs)
                ):
                    with self.assertRaises(PilotError):
                        verified_ingress_selector(
                            object(),
                            values,
                            project=PROJECT,
                            expected_selector=EXPECTED_SELECTOR,
                        )

    def test_rejects_shared_mesh_ingress_service(self):
        values = {
            "INTRINSIC_INGRESS_ADDRESS": "istio-ingressgateway.istio-system.svc.cluster.local:80",
            "INTRINSIC_INGRESS_GATEWAY": f"{PROJECT}/{GATEWAY_RESOURCE}",
        }
        with self.assertRaises(PilotError):
            verified_ingress_selector(
                object(), values, project=PROJECT, expected_selector=EXPECTED_SELECTOR
            )

    def test_uses_configured_gateway_names_namespace_and_selector(self):
        project = "relocated-project"
        host = f"custom-gateway.{project}.svc.cluster.local"
        selector = {"app": "custom-gateway"}
        values = {
            "INTRINSIC_INGRESS_ADDRESS": host + ":80",
            "INTRINSIC_INGRESS_GATEWAY": project + "/custom-listener",
        }
        responses = [
            {"metadata": {"labels": {}}},
            {
                "spec": {
                    "controlPlaneRef": {"namespace": "mesh-system", "name": "mesh"}
                },
                "status": {"conditions": [{"type": "Ready", "status": "True"}]},
            },
            {"items": []},
            {
                "spec": {
                    "type": "ClusterIP",
                    "selector": selector,
                    "ports": [{"port": 80, "targetPort": 443}, {"port": 443}],
                }
            },
            {
                "subsets": [
                    {
                        "addresses": [
                            {
                                "ip": "pod-ip",
                                "targetRef": {"kind": "Pod", "name": "gateway-pod"},
                            }
                        ]
                    }
                ]
            },
            {
                "spec": {
                    "selector": selector,
                    "servers": [
                        {"port": {"number": 80, "protocol": "HTTP2"}, "hosts": [host]},
                        {
                            "port": {"number": 443, "protocol": "HTTPS"},
                            "hosts": [host, "*"],
                            "tls": {"mode": "ISTIO_MUTUAL"},
                        },
                    ],
                }
            },
            {
                "items": [
                    {
                        "metadata": {"name": "gateway-pod"},
                        "status": {
                            "phase": "Running",
                            "podIP": "pod-ip",
                            "conditions": [{"type": "Ready", "status": "True"}],
                        },
                    }
                ]
            },
        ]
        with patch("configure_routing.get_json", side_effect=responses) as queries:
            actual = verified_ingress_selector(
                object(),
                values,
                project=project,
                expected_selector=selector,
            )
        self.assertEqual(actual, selector)
        self.assertEqual(
            queries.call_args_list[3].args[1:],
            ("get", "service", "custom-gateway", "--namespace", project),
        )
        self.assertEqual(
            queries.call_args_list[5].args[1:],
            (
                "get",
                "gateways.networking.istio.io",
                "custom-listener",
                "--namespace",
                project,
            ),
        )

        responses[3] = {"spec": {"selector": {"app": "unreviewed-gateway"}}}
        with patch("configure_routing.get_json", side_effect=responses):
            with self.assertRaises(PilotError):
                verified_ingress_selector(
                    object(),
                    values,
                    project=project,
                    expected_selector=selector,
                )


if __name__ == "__main__":
    unittest.main()
