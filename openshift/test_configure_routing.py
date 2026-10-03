import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from configure_routing import (
    PilotError,
    read_routing_values,
    safe_selector,
    verify_project_mesh_membership,
    verified_ingress_selector,
)


class RoutingConfigurationTest(unittest.TestCase):
    def test_reads_only_internal_routing_values_from_sample(self):
        sample = Path(__file__).with_name(".env.sample")
        with patch("configure_routing.ENV_FILE", sample):
            values = read_routing_values()
        self.assertEqual(
            set(values), {"INTRINSIC_INGRESS_ADDRESS", "INTRINSIC_INGRESS_GATEWAY"}
        )
        self.assertTrue(values["INTRINSIC_INGRESS_ADDRESS"].endswith(":80"))

    def test_rejects_shell_syntax_and_non_internal_addresses(self):
        with tempfile.TemporaryDirectory() as temp:
            env_file = Path(temp) / ".env"
            for address in (
                "service.example.invalid:80",
                "service.ns.svc.cluster.local:443",
                "service.alias.ns.svc.cluster.local:80",
                "$(print-secret).ns.svc.cluster.local:80",
            ):
                with self.subTest(address=address):
                    env_file.write_text(
                        f"INTRINSIC_INGRESS_ADDRESS={address}\n"
                        "INTRINSIC_INGRESS_GATEWAY=mesh-system/gateway\n"
                    )
                    with patch("configure_routing.ENV_FILE", env_file):
                        with self.assertRaises(PilotError):
                            read_routing_values()

    def test_accepts_simple_service_selector_and_rejects_empty_selector(self):
        self.assertEqual(safe_selector({"app": "mesh-ingress", "mesh.io/rev": "stable"}), {
            "app": "mesh-ingress",
            "mesh.io/rev": "stable",
        })
        with self.assertRaises(PilotError):
            safe_selector({})

    def test_accepts_ready_namespace_scoped_service_mesh_member(self):
        project = {"metadata": {"labels": {}}}
        rolls = {"items": []}
        member = {
            "spec": {"controlPlaneRef": {"namespace": "istio-system", "name": "data-science-smcp"}},
            "status": {"conditions": [{"type": "Ready", "status": "True"}]},
        }
        with patch("configure_routing.get_json", side_effect=[project, member, rolls]):
            verify_project_mesh_membership(object(), "istio-system")

    def test_rejects_member_without_control_plane_reference(self):
        project = {"metadata": {"labels": {}}}
        rolls = {"items": []}
        member = {
            "spec": {"controlPlaneRef": {}},
            "status": {"conditions": [{"type": "Ready", "status": "True"}]},
        }
        with patch("configure_routing.get_json", side_effect=[project, member]):
            with self.assertRaises(PilotError):
                verify_project_mesh_membership(object(), "istio-system")

    def test_rejects_pending_service_mesh_member(self):
        project = {"metadata": {"labels": {}}}
        rolls = {"items": []}
        member = {
            "spec": {"controlPlaneRef": {"namespace": "istio-system", "name": "data-science-smcp"}},
            "status": {"conditions": [{"type": "Ready", "status": "False"}]},
        }
        with patch("configure_routing.get_json", side_effect=[project, member]):
            with self.assertRaises(PilotError):
                verify_project_mesh_membership(object(), "istio-system")

    def test_verifies_exact_internal_http2_gateway(self):
        project = {"metadata": {"labels": {}}}
        rolls = {"items": []}
        member = {
            "spec": {"controlPlaneRef": {"namespace": "istio-system", "name": "data-science-smcp"}},
            "status": {"conditions": [{"type": "Ready", "status": "True"}]},
        }
        service = {
            "spec": {
                "type": "ClusterIP",
                "selector": {
                    "app.kubernetes.io/name": "intrinsic-grpc-gateway",
                    "istio": "intrinsic-grpc-gateway",
                },
                "ports": [{"port": 80, "protocol": "TCP"}],
            }
        }
        endpoints = {
            "subsets": [{"addresses": [{"ip": "pod-ip", "targetRef": {"kind": "Pod", "name": "gateway-pod"}}]}]
        }

        def responses(hosts, protocol="HTTP2"):
            gateway = {
                "spec": {
                    "selector": {
                        "app.kubernetes.io/name": "intrinsic-grpc-gateway",
                        "istio": "intrinsic-grpc-gateway",
                    },
                    "servers": [{
                        "port": {"number": 80, "protocol": protocol},
                        "hosts": hosts,
                    }],
                }
            }
            pods = {
                "items": [{
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
                }]
            }
            return [project, member, rolls, service, endpoints, gateway, pods]

        values = {
            "INTRINSIC_INGRESS_ADDRESS": "intrinsic-grpc-gateway.arhkp-intrinsic.svc.cluster.local:80",
            "INTRINSIC_INGRESS_GATEWAY": "arhkp-intrinsic/intrinsic-grpc-internal",
        }
        with patch("configure_routing.get_json", side_effect=responses([
            "intrinsic-grpc-gateway.arhkp-intrinsic.svc.cluster.local"
        ])):
            selector = verified_ingress_selector(object(), values)
        self.assertEqual(selector, {
            "app.kubernetes.io/name": "intrinsic-grpc-gateway",
            "istio": "intrinsic-grpc-gateway",
        })

        for hosts, protocol in [
            (["*"], "HTTP2"),
            (["intrinsic-grpc-gateway.arhkp-intrinsic.svc.cluster.local"], "HTTP"),
        ]:
            with self.subTest(hosts=hosts, protocol=protocol):
                with patch("configure_routing.get_json", side_effect=responses(hosts, protocol)):
                    with self.assertRaises(PilotError):
                        verified_ingress_selector(object(), values)

    def test_rejects_shared_mesh_ingress_service(self):
        values = {
            "INTRINSIC_INGRESS_ADDRESS": "istio-ingressgateway.istio-system.svc.cluster.local:80",
            "INTRINSIC_INGRESS_GATEWAY": "arhkp-intrinsic/intrinsic-grpc-internal",
        }
        with self.assertRaises(PilotError):
            verified_ingress_selector(object(), values)


if __name__ == "__main__":
    unittest.main()
