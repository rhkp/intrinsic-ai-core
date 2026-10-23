# This project was developed with assistance from AI tools.
"""Prepare a guarded ChartAssignment overlay from the captured VM baseline."""

import argparse
import copy
import hashlib
import json
import os
from pathlib import Path

import yaml

from hybrid_config import ConfigError, DEFAULT_CONFIG_PATH, load

ROOT = Path(__file__).resolve().parent
RUNFILES = (
    "/intrinsic_inference/assets/inference_service/inference_service_main.runfiles"
)
PYTHON = (
    RUNFILES + "/rules_python++python+python_3_11_x86_64-unknown-linux-gnu/bin/python3"
)
MAIN = (
    RUNFILES
    + "/intrinsic-core+/intrinsic_inference/assets/inference_service/inference_service_main.py"
)
MOUNT = "/etc/intrinsic-hybrid"


def sha(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()


def prepare(baseline, source_file, endpoint, ca, output, *, endpoint_value=None):
    output.mkdir(parents=True, mode=0o700, exist_ok=False)
    os.chmod(output, 0o700)
    original = next(
        x
        for x in json.loads((baseline / "runtime/chartassignments.json").read_text())[
            "items"
        ]
        if x["metadata"]["name"] == "resources"
    )
    candidate = copy.deepcopy(original["spec"])
    record = next(
        x
        for x in candidate["chart"]["values"]["resource_instances"]
        if x["instance_name"] == "inference_service"
    )
    pod = yaml.safe_load(record["spec"])
    before = copy.deepcopy(pod)
    wrapper = next(c for c in pod["containers"] if c["name"] == "rs-inference-service")
    source = json.loads(source_file.read_text())[MAIN]
    source_digest = hashlib.sha256(source.encode()).hexdigest()
    patcher = (
        "import sys;sys.path.insert(0,'/etc/intrinsic-hybrid'); "
        "from pathlib import Path; "
        f"[sys.path.insert(0,str(p)) for p in Path({RUNFILES!r}).glob('*/site-packages')]; "
        "import hybrid_triton; "
        f"p=Path({MAIN!r}); "
        f"Path('/patch/inference_service_main.py').write_text(hybrid_triton.patch_source(p.read_text(),{source_digest!r}))"
    )
    pod.setdefault("initContainers", []).append(
        {
            "name": "hybrid-rhoai-patch",
            "image": wrapper["image"],
            "command": [PYTHON],
            "args": ["-c", patcher],
            "volumeMounts": [
                {"name": "hybrid-rhoai-config", "mountPath": MOUNT, "readOnly": True},
                {"name": "hybrid-rhoai-patch", "mountPath": "/patch"},
            ],
        }
    )
    pod["volumes"] += [
        {"name": "hybrid-rhoai-config", "configMap": {"name": "hybrid-rhoai-wrapper"}},
        {"name": "hybrid-rhoai-token", "secret": {"secretName": "hybrid-rhoai-token"}},
        {"name": "hybrid-rhoai-patch", "emptyDir": {}},
    ]
    wrapper["volumeMounts"] += [
        {"name": "hybrid-rhoai-config", "mountPath": MOUNT, "readOnly": True},
        {
            "name": "hybrid-rhoai-token",
            "mountPath": "/var/run/secrets/hybrid-rhoai",
            "readOnly": True,
        },
        {
            "name": "hybrid-rhoai-patch",
            "mountPath": MAIN,
            "subPath": "inference_service_main.py",
            "readOnly": True,
        },
    ]
    wrapper.setdefault("env", []).extend(
        [
            {
                "name": "RHOAI_ADAPTER_SHA256",
                "value": hashlib.sha256(
                    (ROOT / "hybrid_triton.py").read_bytes()
                ).hexdigest(),
            },
            {
                "name": "INTRINSIC_RHOAI_TRITON_ENDPOINT",
                "value": endpoint_value
                if endpoint_value is not None
                else endpoint.read_text().strip(),
            },
            {"name": "RHOAI_CA_FILE", "value": MOUNT + "/ca.crt"},
            {
                "name": "RHOAI_TOKEN_FILE",
                "value": "/var/run/secrets/hybrid-rhoai/token",
            },
        ]
    )
    record["spec"] = yaml.safe_dump(pod, sort_keys=False)
    configmap = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {"name": "hybrid-rhoai-wrapper", "namespace": "app-resources"},
        "data": {
            "hybrid_triton.py": (ROOT / "hybrid_triton.py").read_text(),
            "ca.crt": ca.read_text(),
        },
    }
    plan = {
        "baseline_uid": original["metadata"]["uid"],
        "baseline_spec": original["spec"],
        "candidate_spec": candidate,
        "baseline_spec_sha256": sha(original["spec"]),
        "candidate_spec_sha256": sha(candidate),
        "baseline_pod_spec": before,
        "candidate_pod_spec": pod,
        "wrapper_source_sha256": source_digest,
        "configmap": configmap,
    }
    (output / "plan.json").write_text(json.dumps(plan, indent=2) + "\n")
    print("Prepared the inference-only owner overlay and its exact rollback state.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=DEFAULT_CONFIG_PATH)
    for name in ("baseline", "source-file", "output"):
        parser.add_argument("--" + name, type=Path, required=True)
    args = parser.parse_args()
    try:
        config = load(args.config)
    except ConfigError as exc:
        parser.error(str(exc))
    previous = os.umask(0o077)
    try:
        prepare(
            args.baseline,
            args.source_file,
            None,
            config.ca_file,
            args.output,
            endpoint_value=config.endpoint,
        )
    finally:
        os.umask(previous)
