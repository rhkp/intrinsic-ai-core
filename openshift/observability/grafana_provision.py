# This project was developed with assistance from AI tools.
"""Provision only this Grafana's datasource; reload rotating task-owned credentials."""

import argparse
import base64
import hashlib
import json
import os
import re
import time
import urllib.request
from pathlib import Path

MOUNT = Path("/var/run/grafana-api")
ADMIN = Path("/var/run/grafana-admin")
OUTPUT = Path("/etc/grafana/provisioning/datasources/prometheus.yaml")


def datasource(namespace, token, ca):
    if (
        not re.fullmatch(r"[a-z0-9]([-a-z0-9]*[a-z0-9])?", namespace)
        or not token.strip()
        or "-----BEGIN CERTIFICATE-----" not in ca
    ):
        raise ValueError("invalid_datasource_inputs")
    return {
        "apiVersion": 1,
        "datasources": [
            {
                "name": "OpenShift project metrics",
                "uid": "intrinsic-prometheus",
                "type": "prometheus",
                "access": "proxy",
                "url": "https://thanos-querier.openshift-monitoring.svc:9092",
                "isDefault": True,
                "editable": False,
                "jsonData": {
                    # The tenancy proxy authorizes GET with project read access.
                    "httpMethod": "GET",
                    "manageAlerts": False,
                    "customQueryParameters": "namespace=" + namespace,
                    "httpHeaderName1": "Authorization",
                    "tlsAuthWithCACert": True,
                    "tlsSkipVerify": False,
                    "prometheusType": "Thanos",
                    "timeInterval": "30s",
                    "prometheusVersion": "3.5.0",
                },
                "secureJsonData": {
                    "httpHeaderValue1": "Bearer " + token.strip(),
                    "tlsCACert": ca,
                },
            }
        ],
    }


def write_config():
    data = datasource(
        (MOUNT / "namespace").read_text().strip(),
        (MOUNT / "token").read_text(),
        (MOUNT / "service-ca.crt").read_text(),
    )
    text = json.dumps(data, sort_keys=True)
    # Grafana discovers .yaml/.yml files; JSON is valid YAML content.
    # Keep the temporary file outside those extensions.
    temporary = OUTPUT.with_suffix(".pending")
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o640)
    with os.fdopen(fd, "w") as file:
        file.write(text)
    os.replace(temporary, OUTPUT)
    return hashlib.sha256(text.encode()).hexdigest()


def reload_datasource():
    # This random credential is created specifically for this new Grafana instance.
    password = (ADMIN / "admin-password").read_text().strip()
    authorization = "Basic " + base64.b64encode(("admin:" + password).encode()).decode()
    request = urllib.request.Request(
        "http://127.0.0.1:3000/api/admin/provisioning/datasources/reload",
        headers={"Authorization": authorization},
        data=b"",
        method="POST",
    )

    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None

    with urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect).open(
        request, timeout=10
    ) as response:
        if response.status != 200:
            raise ValueError("datasource_reload_failed")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--init", action="store_true")
    parser.add_argument("--once", action="store_true")
    args = parser.parse_args(argv)
    if args.init:
        write_config()
        return 0
    applied = None
    while True:
        try:
            digest = write_config()
            if digest != applied or args.once:
                reload_datasource()
                applied = digest
                print('{"event":"project_datasource_reloaded"}', flush=True)
            if args.once:
                return 0
        except Exception:
            # Never print tokens, passwords, certificate contents or upstream responses.
            print('{"event":"project_datasource_reload_unavailable"}', flush=True)
            if args.once:
                return 1
        time.sleep(15)


if __name__ == "__main__":
    raise SystemExit(main())
