#!/usr/bin/env python3
"""Render copied Intrinsic resource/skill Helm templates and check pull wiring."""

from __future__ import annotations

import shutil
import subprocess
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parent
ASSETS = ROOT / "intrinsic-core/intrinsic/assets/deploy"


def render(template_name: str, values: str) -> str:
    with tempfile.TemporaryDirectory(prefix="intrinsic-template-check-") as temp:
        chart = Path(temp) / "chart"
        (chart / "templates").mkdir(parents=True)
        shutil.copyfile(ASSETS / template_name, chart / "templates" / template_name)
        (chart / "Chart.yaml").write_text("apiVersion: v2\nname: smoke\nversion: 0.1.0\n")
        values_path = chart / "values.yaml"
        values_path.write_text(values)
        result = subprocess.run(
            ["helm", "template", "smoke", str(chart), "--values", str(values_path)],
            check=True,
            capture_output=True,
            text=True,
        )
        return result.stdout


def check_common(output: str, source: str) -> None:
    for forbidden in ("imagePullSecrets:", "kind: Secret", ".dockercfg"):
        if forbidden in output:
            raise AssertionError(f"{source} rendered forbidden credential material: {forbidden}")
    if "serviceAccountName: intrinsic-runtime" not in output:
        raise AssertionError(f"{source} did not render the intrinsic-runtime ServiceAccount")
    if "automountServiceAccountToken: false" not in output:
        raise AssertionError(f"{source} did not disable service-account token automount")


def main() -> None:
    skills = render(
        "skills.yaml",
        """pods:
  - name: skill-group-00
    service_account_name: intrinsic-runtime
    containers:
      - name: smoke-skill
        asset: smoke.skill
        registry: registry.example.invalid
        image_ref: smoke:latest
        port: 8003
        metrics_port: 9101
skills: []
""",
    )
    check_common(skills, "skills.yaml")

    resources = render(
        "resources.yaml",
        """resource_instances:
  - name: smoke-resource
    instance_name: smoke-resource
    context_id: smoke-context
    runtime_context_pb_base64: c21va2U=
    spec: |-
      automountServiceAccountToken: false
      serviceAccountName: intrinsic-runtime
      containers: []
    service: {}
    http: {}
""",
    )
    check_common(resources, "resources.yaml")
    print("Resource and skill templates render with intrinsic-runtime, no token automount, and no generated pull Secrets.")


if __name__ == "__main__":
    main()
