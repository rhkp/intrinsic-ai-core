# E8 Grafana dashboard

This project was developed with assistance from AI tools.

Grafana provides retained history and interactive time ranges for the existing
E8 metrics. The original dashboard was the first implementation; Grafana had not
previously been trialled. Both use the same observer. Grafana does not replace
collection, Prometheus retention, or the nineteen OpenShift alert rules.

## Install and open

Use the canonical ignored configuration, authenticated `oc` session and enabled
user-workload monitoring described in the [E8 runbook](README.md). The portable
image lock includes the official Grafana 13.2.2 image by immutable digest. Import
that image into the configured project registry using the normal image-mirroring
workflow before installation.

```sh
# Render only into ignored openshift/.generated/e8/grafana/resources.yaml.
python3 openshift/grafana.py

# Ownership checks and server dry-run precede changes.
python3 openshift/grafana.py --apply

# Select the canonical context/project, then wait and open a private tunnel.
oc rollout status deployment/intrinsic-grafana --timeout=180s
oc port-forward service/intrinsic-grafana 3000:3000 --address=127.0.0.1
```

Open <http://127.0.0.1:3000/d/intrinsic-e8/intrinsic-e8-operations> while the
port-forward is running. No separate login is needed: Kubernetes authorizes the
tunnel and Grafana grants anonymous Viewer access. Grafana binds to loopback
inside its Pod as well; there is no public Route. The browser must run on the
machine hosting the tunnel.

The provisioned dashboard has twenty-four panels and refreshes every thirty
seconds. Change the time picker to explore Prometheus history. Retention is
platform controlled. The fresh-collection summary also requires a successful
scrape and a sample less than ninety seconds old. Summary colors show incomplete
readiness and memory pressure. Idle latency panels may have gaps: no completed requests
does not mean zero latency. GPU memory covers Triton-visible inference devices;
IPC reflects functional ICON/hardware state. Panel descriptions and runbook
links explain the remaining measurement limits.

## Access and persistence

The installer creates seven project-owned resources and a dedicated random
admin/encryption Secret. It never reads another Grafana instance's credentials.
Reapplication preserves the Secret. Browser access stays Viewer-only; dashboard
and datasource definitions are provisioned from source and cannot be edited in
the UI.

A dedicated ServiceAccount receives the built-in `view` role through a binding
in this project only. The datasource queries the OpenShift Thanos tenancy proxy
on 9092 using read-only GET requests, a required namespace query parameter and
namespace selectors in every dashboard expression. HTTPS validates the injected
service CA. No cluster-wide monitoring grant is added.

A small Python sidecar checks the projected rotating token and service CA every
fifteen seconds, atomically writes JSON-compatible YAML into a memory volume,
and reloads this instance's datasource using its own task-created admin Secret.
Neither credentials nor certificates are printed. The main process and sidecar
run as the project's non-root UID with read-only root filesystems. Egress rules
enumerate monitoring and DNS destinations; loopback binding also protects against
additive mesh ingress policies.

SQLite/cache state uses bounded `emptyDir` storage. A Pod replacement loses
local preferences and sessions; the dashboard and datasource are reprovisioned.
Prometheus history remains in the existing monitoring stack. No PVC is created.
This is a private development trial; shared access would require an explicitly
designed authenticated endpoint and ownership/persistence decisions.

## Verify and remove

```sh
python3 -m unittest discover -s openshift -p 'test_grafana.py'
python3 openshift/check_observability.py
```

Check `/api/health`, datasource health and panel data through the loopback
tunnel. An HTTP 200 dashboard page alone does not establish working queries.
Check the token-refresh container's fixed event messages if queries fail after
rotation; do not print its mounted token, admin files or provisioned datasource.

To remove this trial, first stop its tunnel. In the canonical project, delete
only resources with `app.kubernetes.io/part-of=intrinsic-grafana`: its Deployment,
Service, two ConfigMaps, ServiceAccount, RoleBinding, NetworkPolicy, admin Secret
and ImageStream. Review the selected names before deletion. Removing the
Grafana entry from the canonical image lock also requires regenerating the target
ConfigMap/controller checksum and the checked-in config-render goldens. Preserve
the observer, ServiceMonitor, PrometheusRule and all application resources.

References: [Grafana file provisioning](https://grafana.com/docs/grafana/latest/administration/provisioning/)
and [OpenShift monitoring metrics access](https://docs.redhat.com/en/documentation/monitoring_stack_for_red_hat_openshift/4.20/html-single/accessing_metrics/index).
