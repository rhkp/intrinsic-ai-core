#!/usr/bin/env python3
"""Emit a namespace-bound ChartAssignment whose chart creates one ConfigMap."""

import base64
import gzip
import io
import json
import tarfile

NAMESPACE = "arhkp-intrinsic"
CHART = "chartassignment-smoke"

files = {
    f"{CHART}/Chart.yaml": b"apiVersion: v1\nname: chartassignment-smoke\nversion: 0.0.1\n",
    f"{CHART}/values.yaml": b"{}\n",
    f"{CHART}/templates/result-configmap.yaml": (
        b"apiVersion: v1\n"
        b"kind: ConfigMap\n"
        b"metadata:\n"
        b"  name: chartassignment-smoke-result\n"
        b"data:\n"
        b"  result: namespace-scoped-controller-smoke-passed\n"
    ),
}

archive = io.BytesIO()
with gzip.GzipFile(fileobj=archive, mode="wb", mtime=0) as compressed:
    with tarfile.open(fileobj=compressed, mode="w") as tar:
        for path, contents in files.items():
            info = tarfile.TarInfo(path)
            info.size = len(contents)
            info.mode = 0o444
            info.mtime = 0
            tar.addfile(info, io.BytesIO(contents))

document = {
    "apiVersion": "apps.cloudrobotics.com/v1alpha1",
    "kind": "ChartAssignment",
    "metadata": {"name": "namespace-scope-smoke", "namespace": NAMESPACE},
    "spec": {
        "clusterName": "openshift-pilot",
        "namespaceName": NAMESPACE,
        "chart": {
            "name": CHART,
            "version": "0.0.1",
            "inline": base64.b64encode(archive.getvalue()).decode("ascii"),
            "values": {},
        },
    },
}
print(json.dumps(document, separators=(",", ":")))
