# E8 operational observability

> [!NOTE]
> This project was developed with assistance from AI tools.

## Architecture

Existing OpenShift user-workload Prometheus scrapes a project-local, read-only
observer through a ServiceMonitor. A PrometheusRule supplies nine warning alerts,
evaluated by the existing monitoring stack. No additional Prometheus or Grafana
instance is installed.

The observer polls Kubernetes every 30 seconds. Its dashboard refreshes every
15 seconds and retains approximately one hour of samples in memory; local history
resets when the observer restarts. Prometheus retention follows the cluster
configuration. The dashboard reads the observer, not the Prometheus query API.
Use OpenShift **Observe → Metrics / Alerting** for retained metrics and alerts.
Notification destinations remain the platform administrator's responsibility;
notification delivery is unverified.

## Install and open

Requires a valid ignored `openshift/config.local.json`, its image lock, a logged-in
`oc` session, enabled user-workload monitoring, monitoring CRDs, and permission to
create the nine namespaced objects. The namespace must not be excluded from
monitoring. Enabling platform monitoring requires a cluster administrator.
Implementation: [installer and alert rules](../observability.py),
[collector/exporter](observer.py), and [dashboard](dashboard.js).

From the repository root:

```sh
# Render only; generated target details remain ignored and private.
python3 openshift/observability.py

# Ownership check and server dry run, then apply only the nine E8 objects.
python3 openshift/observability.py --apply

# Select the same context/project as the canonical configuration first.
oc port-forward service/intrinsic-observability 9099:9099 --address=127.0.0.1
```

Open <http://127.0.0.1:9099/>. No Route or public dashboard is created. Kubernetes
authorizes the port-forward; the local dashboard has no separate login. Keep it
bound to loopback.

The installer creates a ConfigMap, ServiceAccount, Role, RoleBinding, Deployment,
Service, NetworkPolicy, ServiceMonitor and PrometheusRule, all named
`intrinsic-observability`. Existing objects must have its ownership label.
Reapplying source changes restarts only the observer.

The observer reuses the digest-locked viewer image's Python standard library,
replacing its entrypoint. It needs no package installation, image build, PVC or
writable root filesystem. Its opt-out label excludes it from the legacy
controller's namespace-wide application readiness check. Application, controller
and RHOAI workload specifications remain unchanged.

## Coverage and limits

| Signal | Implementation | Recovery pointer |
|---|---|---|
| Ten required components | Current rollout/Pod readiness and current Pod container restarts | [Workload readiness](#workload-readiness) |
| Four ChartAssignments | Observed generation, Ready/Settled, latest numbered ResourceSet failure and settlement duration | [Assignment settlement](#assignment-settlement) |
| GPU allocation | Sum of current component container `nvidia.com/gpu` limits | [Storage and placement](#storage-and-placement) |
| Image pull and registry warnings | Retained Warning event objects observed within ten minutes | [Image pull and registry](#image-pull-and-registry) |
| Storage, placement, probe and restart warnings | Fixed categories from recent retained Warning events | [Storage and placement](#storage-and-placement) |
| Collector health | Last complete API collection, snapshot age and scrape health | [Collection](#collection) |
| Native controller reconcile errors/latency, gateway gRPC errors/latency, inference authorization errors/latency, simulator IPC disconnects, GPU memory | Not connected in this slice; unavailable markers are zero, without fabricated measurements | Requires native instrumentation/exporter integration |

Readiness does not establish successful inference or robot motion. Current Pod
restart counts are gauges, not lifetime counters; replacement resets them.
Settlement duration describes only the latest retained ResourceSet version,
not a latency histogram. Warning values count retained event objects, not
occurrences, and disappear after ten minutes. Image-pull message classification
is best effort; kubelet wording and event retention vary. GPU allocation does not
describe device utilization, free capacity or memory.

The active RHOAI predictor is selected through the configured internal Service,
so an unused second deployment is not mistaken for the demo's predictor.
Collection failures retain the last snapshot, mark it unverified, and suppress
secondary workload alerts until collection succeeds again.

## Metrics and verification

The exporter uses fixed metric names and bounded component, assignment, warning
and unavailable-signal labels. It emits no object names, messages, API addresses,
credentials or inference payloads. The ServiceMonitor drops target instance,
Pod and container labels from application samples. OpenShift adds namespace,
job and service scope. Native Prometheus `up` and target discovery retain
platform-managed target metadata; access follows platform permissions.

In OpenShift Observe, scoped to the configured project:

```promql
up{service="intrinsic-observability"}
intrinsic_observability_collection_success
intrinsic_component_ready
intrinsic_assignment_ready
intrinsic_observability_signal_available
```

Expected initial results: healthy scrape, collection success `1`, ten component
readiness samples, four assignment readiness samples and six unavailable-signal
markers. Check freshness before interpreting stored values. The operational
baseline was verified with 13 metric families and nine healthy, inactive rules.
This establishes collection/evaluation, not notification delivery. No synthetic
alert or motion was triggered for verification.

The existing read-only demo acceptance passed all 42 gates after deployment.
Baseline comparison preserved 35 Deployments, 13 StatefulSets, all original Pod
identities and all ten PVC identities/specifications/Bound states. No motion
cycle was replayed for E8, and no new unit tests were run in this implementation.

## Runbooks

### Collection

Missing scrapes wait five minutes; API collection failures wait three. Check
the observer rollout and `/readyz`, Service endpoint, ServiceMonitor target and
monitoring ingress. Inspect API access/RBAC, project monitoring exclusions and
the observer NetworkPolicy. Re-render after API endpoint changes. Do not loosen
policy or expose the dashboard publicly as a workaround. A missing scrape makes
the other measurements unverified.

### Workload readiness

Unavailability waits five minutes. Repeated restarts require a current Pod count
of at least three for ten minutes. Inspect rollout, container waiting/termination
reasons and recent events. Run the existing read-only acceptance command before
an explicit recovery action:

```sh
python3 openshift/validate_demo_gates.py --json
```

Use [the collision runbook](../COLLISION_DIAGNOSTICS.md) for an explicitly
authorized reset/motion cycle. Preserve application PVCs and build caches.

### Assignment settlement

Not-ready assignments wait five minutes; failures wait two. Slow settlement means
the latest ResourceSet has remained unsettled over ten minutes, for another two
minutes. Inspect current generation, conditions and latest ResourceSet phase.
Find the blocked workload before reconciliation. An old settled version does
not establish readiness of the current generation.

### Image pull and registry

Recent image/credential warnings wait two minutes. Inspect the affected Pod's
waiting reason, image reference and ServiceAccount pull permissions. Confirm the
digest exists in the canonical image lock and registry. Do not print credentials
or create inline credential values. Recent events can outlive a recovered Pod.

### Storage and placement

Recent scheduling/storage warnings wait two minutes. Inspect pending Pod events,
PVC Bound state, requested resources, GPU tolerations and required affinity.
Preserve PVCs and caches. Allocation panels cannot establish available cluster
capacity or GPU memory pressure.

## Access boundaries

The observer can list/get project Pods, events, Deployments, StatefulSets,
ReplicaSets, ChartAssignments and ResourceSets, and get only the configured
RHOAI Service. Event requests filter Warning events. It cannot read Secrets,
logs, cluster nodes or other projects, execute commands or mutate resources.
Its projected token rotates; HTTPS uses the mounted cluster CA. Tokens and raw
API responses are never written to logs or dashboard output.

NetworkPolicy permits monitoring ingress on TCP 9099 and outbound API/DNS traffic.
The observer has no mesh sidecar or application RPC calls. Existing application
mesh, RHOAI authentication, storage and motion behavior remain unchanged.
