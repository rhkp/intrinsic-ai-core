#!/usr/bin/env python3
"""Tests that internal registry credentials stay in mode-0600 temp files."""

from __future__ import annotations

import base64
import json
import os
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("prepare-internal-registry-auth.py")
CERT = b"-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----\n"


class PrepareInternalRegistryAuthTest(unittest.TestCase):
    def test_writes_scoped_private_docker_config_and_combined_ca(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            token_file = root / "token"
            token_file.write_text("fake-short-lived-token\n", encoding="utf-8")
            system_ca = root / "system-ca.pem"
            system_ca.write_bytes(CERT)
            service_ca = root / "service-ca.pem"
            service_ca.write_bytes(CERT)
            docker_config = root / "docker"
            tmp_dir = root / "tmp"
            env = {
                **os.environ,
                "SERVICE_ACCOUNT_TOKEN_FILE": str(token_file),
                "SYSTEM_CA_BUNDLE": str(system_ca),
                "OPENSHIFT_SERVICE_CA_FILE": str(service_ca),
                "DOCKER_CONFIG": str(docker_config),
                "TMPDIR": str(tmp_dir),
                "INTRINSIC_REGISTRY_HOST": "image-registry.example.svc:5000",
                "OPENSHIFT_NAMESPACE": "arhkp-intrinsic",
            }
            result = subprocess.run(
                [sys.executable, str(SCRIPT)],
                check=True,
                capture_output=True,
                text=True,
                env=env,
            )

            self.assertNotIn("fake-short-lived-token", result.stdout)
            config_path = docker_config / "config.json"
            self.assertEqual(stat.S_IMODE(config_path.stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE(docker_config.stat().st_mode), 0o700)
            config = json.loads(config_path.read_text(encoding="utf-8"))
            self.assertEqual(
                config["auths"]["image-registry.example.svc:5000"]["auth"],
                base64.b64encode(
                    b"openshift-token:fake-short-lived-token"
                ).decode("ascii"),
            )
            ca_bundle = Path(result.stdout.strip())
            self.assertEqual(stat.S_IMODE(ca_bundle.stat().st_mode), 0o644)
            self.assertEqual(ca_bundle.read_bytes(), CERT + CERT)

    def test_rejects_registry_url_instead_of_host_port(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            token_file = root / "token"
            token_file.write_text("fake-token", encoding="utf-8")
            env = {
                **os.environ,
                "SERVICE_ACCOUNT_TOKEN_FILE": str(token_file),
                "INTRINSIC_REGISTRY_HOST": "https://image-registry.example:5000",
                "DOCKER_CONFIG": str(root / "docker"),
                "TMPDIR": str(root / "tmp"),
            }
            result = subprocess.run(
                [sys.executable, str(SCRIPT)], capture_output=True, text=True, env=env
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn("fake-token", result.stderr)


if __name__ == "__main__":
    unittest.main()
