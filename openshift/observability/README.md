# E8 operational observability

> [!NOTE]
> This project was developed with assistance from AI tools.

## Install, verify, and open

Use an ignored, valid `openshift/config.local.json` and image lock, an authenticated
`oc` session, and enabled OpenShift user-workload monitoring. The installer binds
to the canonical context and API, checks ownership, and performs a server dry run
before applying its thirteen project-local objects.

```sh
# Render into ignored openshift/.generated/e8/resources.yaml.
python3 openshift/observability.py

# Apply monitoring resources and wait for the observer rollout.
python3 openshift/observability.py --apply
oc rollout status deployment/intrinsic-observability --timeout=180s

# Read-only acceptance: fresh observer, nine native signals, healthy scrape/rules.
python3 openshift/check_observability.py

# Select the canonical context/project before this command; bind only to loopback.
oc port-forward service/intrinsic-observability 9099:9099 --address=127.0.0.1
```

Open <http://127.0.0.1:9099/>. No public Route or separate dashboard login is
created. Kubernetes authorizes the port-forward. Allow one collection/scrape
interval after a rollout before checking acceptance. The checker returns sanitized
JSON and a nonzero exit code for missing/stale data or failed checks. In addition
to project access, its monitoring checks require access to the existing Thanos
query Route and the cluster-published ingress/trust CA ConfigMaps. It uses the
operator's existing login in memory; no credentials are saved.

The observer reuses the digest-locked viewer image's standard-library Python.
There is no new observer image, package installation, PVC, or Prometheus.
The optional [Grafana dashboard](GRAFANA.md) reads the same retained metrics.
The controller image enables its existing controller-runtime metrics listener on
9092; deploy that digest through the canonical image lock before expecting the
controller signals. The E8 installer updates only its monitoring resources.

## Architecture and access

Existing OpenShift user-workload Prometheus scrapes the observer through a
ServiceMonitor. Nineteen warning rules are evaluated by the existing monitoring
stack. OpenShift **Observe → Metrics / Alerting** provides retained history and
alert state. Grafana reads retained Prometheus data; the original private
dashboard reads the observer directly.

The observer polls namespaced Kubernetes objects every thirty seconds, then
collects five native endpoints concurrently with five-second HTTP timeouts and
bounded response sizes. The dashboard refreshes every fifteen seconds and keeps
about an hour of local readiness history; restarting the observer clears it.
Prometheus retention follows platform configuration.

The thirteen owned objects comprise a ConfigMap, ServiceAccount, Role,
RoleBinding, Deployment, two Services, two NetworkPolicies, a ServiceMonitor,
a PrometheusRule, and two workload-selected EnvoyFilters. Ownership labels protect
existing unrelated objects. Reapplying source changes restarts only the observer.

The observer's mesh sidecar authenticates read-only ICON and Triton metrics
requests using existing strict mTLS. Its 9099 scrape port and outbound native
9092/15090 metrics ports are excluded from interception. NetworkPolicy permits
monitoring ingress and narrowly enumerated API, DNS, native telemetry and mesh
control-plane ports. There is no public dashboard or application authorization
exception. The metrics path is already excluded from the platform's RHOAI
external-authorization policy; inference authentication remains enforced.

The two EnvoyFilters enable the mesh's built-in passive statistics filter only
on the configured gateway and the selected predictor's inbound port 9000. They
change live proxy configuration without replacing application Pods. This mesh
has no active Prometheus statistics provider; a Telemetry object alone did not
install the filter. Before using this adapter in a differently configured mesh,
check for an existing statistics filter and avoid installing it twice. The
predictor selector comes from the canonical RHOAI Service and must be nonempty.

The observer can list/get project Pods, events, Deployments, StatefulSets,
ReplicaSets, ChartAssignments and ResourceSets, and get only the configured
RHOAI Service. It cannot read Secrets, logs or cluster nodes, execute commands,
or mutate resources. Its projected API token rotates. API HTTPS verifies the
mounted cluster CA. It does not query platform Prometheus or read other projects.

## Coverage and interpretation

| Signal | Measurement | Runbook |
|---|---|---|
| Ten required components | Current rollout, owned Pod readiness, current container restarts and allocated GPUs | [Workloads](#workload-readiness) |
| Four ChartAssignments | Observed generation, Ready/Settled, latest numbered ResourceSet failure and settlement duration | [Assignments](#assignment-settlement) |
| Image pull, registry, scheduling, storage and probes | Fixed categories from retained Warning events seen within ten minutes | [Warnings](#image-pull-and-registry) |
| Controller latency and errors | Native reconciliation duration sum/count and reconciliation outcome counters | [Controller/RPC](#controller-and-rpc) |
| Gateway gRPC latency and errors | Native completed-request duration and HTTP/gRPC result counters | [Controller/RPC](#controller-and-rpc) |
| Inference authorization failures | Predictor HTTP 401/403 or gRPC 7/16 responses | [Inference](#inference) |
| Inference latency and errors | Triton request duration, successes and failures for the two intended models | [Inference](#inference) |
| GPU memory | Used/total device memory reported by Triton for its visible inference GPU(s) | [GPU memory](#gpu-memory) |
| Simulator IPC health | Read-only ICON operational and cell hardware states must both be ENABLED | [Simulator IPC](#simulator-ipc) |
| Collection and source availability | Fresh API snapshot, scrape health and nine independent native-source markers | [Collection](#collection) |

The dashboard shows cumulative counts and mean completed-request durations since
each source restarted. Latency alert expressions use five-minute rates of the
native sums/counts and minimum traffic thresholds. No completed requests means
latency is unavailable, rather than zero. GPU memory describes the device(s)
visible to Triton, not simulator GPU memory, per-process usage, or cluster capacity.
IPC health is a functional ICON/hardware check, not a sampled Unix-socket or
reconnection counter. Simulator restarts remain covered separately.

All non-OK gRPC statuses remain in `intrinsic_gateway_grpc_errors_total`.
`intrinsic_gateway_grpc_unexpected_errors_total` excludes CANCELLED (1) and
UNIMPLEMENTED (12): SDK feature discovery generates UNIMPLEMENTED responses on
this gateway. The operational failure-rate alert uses the latter counter;
HTTP errors still count as unexpected failures.

Current restart counts reset on Pod replacement. Settlement duration covers only
the latest retained ResourceSet, not a latency histogram. Warning gauges count
retained event objects, not occurrences, and expire after ten minutes. Neither
readiness nor successful native collection establishes successful inference or
robot motion. E7 recovery testing and E6 inference lifecycle acceptance remain
separate work.

## Metrics and verification

The exporter exposes thirty metric families. Names and component/assignment/
warning/signal labels are fixed. Native endpoint labels are parsed locally and
discarded: no model identifier, principal, hostname, GPU UUID, Pod IP, credential,
raw log or inference payload is exported. Monitoring adds its normal project,
job and service scope; the ServiceMonitor removes instance/Pod/container labels
from application samples. Native Prometheus target discovery retains its own
platform-controlled metadata.

Native endpoint failures are isolated. A failed source loses its measurements
and its availability marker becomes zero; other sources and Kubernetes collection
continue. Failed/stale API collection suppresses all native measurements and
marks them unavailable, rather than publishing stale values as current.
Inference duration and error coverage each require measurements for both intended
models; a partial model scrape cannot establish full coverage.

In project-scoped OpenShift Observe:

```promql
up{service="intrinsic-observability"}
intrinsic_observability_collection_success
intrinsic_observability_signal_available
rate(intrinsic_controller_reconcile_errors_total[5m])
rate(intrinsic_gateway_grpc_unexpected_errors_total[5m])
rate(intrinsic_inference_request_duration_seconds_sum[5m])
  / rate(intrinsic_inference_request_duration_seconds_count[5m])
intrinsic_inference_gpu_memory_used_bytes / intrinsic_inference_gpu_memory_total_bytes
intrinsic_simulator_ipc_connected
```

Use `python3 -m unittest discover -s openshift -p 'test_observability.py'` for
offline coverage of parsing, sanitization, source failure isolation, stale data,
request classification, GPU capacity, resource scope and acceptance failures.
Live acceptance additionally checks that all nine availability markers reached
Prometheus and all nineteen rules loaded without evaluation errors. Loaded rule
expressions, thresholds, pending durations, labels and runbook annotations must
match source; only formatting and injected project selectors are normalized.

With `promtool` on PATH, unittest discovery also executes the eight checked-in
alert scenarios in `testdata/observability-rules.test.yaml`. Without it, that
optional test is skipped. The completion run executed the same fixture with the
platform's existing immutable Prometheus image in a disposable project Pod with
an emptyDir; the Pod was removed. Scenarios cover missing sources, IPC faults,
GPU pressure, expected SDK discovery errors, unexpected failures, no/low traffic,
and authorization-alert firing/resolution. No live fault or notification was
required for these synthetic rule tests.

The subsequent Grafana audit also executes four freshness scenarios using the
actual dashboard expression: healthy collection, failed collection, failed scrape
and an old retained sample. All twelve synthetic scenarios pass. Grafana's
summary colors reflect readiness and memory pressure; a cached successful sample
cannot show a failed scrape as fresh. The full Python suite passes 137 tests,
with one optional local-promtool skip; the twelve scenarios were separately
executed with the platform binary. Browser visual verification was unavailable;
Grafana health, all twenty-nine panel queries, retained samples and project
isolation were verified through its API.

Completion evidence (2026-10-21): all nine E8 acceptance checks and all forty-two
read-only demo gates pass. Thirty metric families are exposed and every native
family is present in Prometheus; all nineteen current rules evaluate cleanly.
The original thirty-four application Deployments, thirteen StatefulSets,
seventy-four Services, forty-seven application Pods and ten Bound PVCs retain
their specifications/identities. Only the controller and observer rolled.

## Runbooks

### Collection

Missing scrapes wait five minutes; failed API collection waits three. Check the
observer rollout, `/readyz`, Service endpoint, ServiceMonitor and monitoring
NetworkPolicy ingress. Inspect project API permissions and monitoring exclusions.
The observer's readiness reflects API collection; native sources have separate
availability alerts. Do not expose the dashboard publicly as a workaround.

### Native telemetry

A missing source waits five minutes. Check current source ownership/readiness,
9092 controller listener, 15090 proxy metrics, predictor selection, 8002 Triton
metrics Service, ICON 9091 HTTP endpoint, observer sidecar/XDS and NetworkPolicy.
Check both source freshness and API collection. If there has been no gRPC traffic
since filter installation, run the existing read-only E3 acceptance command to
populate gateway and authenticated model-readiness observations.

### Workload readiness

Unavailability waits five minutes. Three or more current container restarts wait
ten minutes. Inspect rollout, container reasons and recent events. Start with:

```sh
python3 openshift/validate_demo_gates.py --json
```

Use the [collision runbook](../COLLISION_DIAGNOSTICS.md) for an explicitly
requested reset or motion cycle. Preserve application PVCs and build caches.

### Assignment settlement

Not-ready assignments wait five minutes; failures wait two. Settlement older
than ten minutes waits a further two. Inspect observed generation, conditions,
latest ResourceSet and the blocked workload before reconciliation.

### Controller and RPC

Controller errors wait two minutes; mean reconciliation over ten seconds waits
five and requires at least five recent reconciles. Gateway unexpected failures
over five percent or mean completed-request duration over five seconds wait five
minutes and require at least twenty recent calls. Inspect readiness, current
assignments and transport state. Streaming RPC duration includes the stream's
lifetime. Do not retry robot actions to clear an alert.

### Inference

New authorization or model-inference failures wait two minutes. Mean inference
over thirty seconds waits five minutes and requires at least three recent
requests. Check predictor/model readiness, the wrapper's projected-token audience
and rotation, its existing named-resource authorization and mesh policy. Preserve
model repositories and PVCs. Use the existing read-only authenticated readiness
probe; keep tokens, tensor data and raw inference responses out of reports.

### GPU memory

Used memory above ninety percent of visible inference-device capacity waits five
minutes. Inspect current model load and concurrent demand before changing model
placement. Device memory includes other processes sharing that device. GPU
allocation alone does not establish memory pressure. Missing telemetry is a
source failure, not evidence of free memory.

### Simulator IPC

An operational or cell hardware state other than ENABLED waits two minutes.
Inspect simulator, ICON and UR readiness, required co-location, shared IPC mounts
and permissions using the dependency ladder. A successful HTTP request alone
does not pass this check. Recovery/reset and fault injection remain explicit,
bounded operational actions; the observer never performs them.

### Image pull and registry

Recent image/credential warnings wait two minutes. Inspect waiting reasons,
digest identity and ServiceAccount pull access without printing credentials.
Recent events may outlive recovery.

### Storage and placement

Recent scheduling/storage warnings wait two minutes. Inspect pending reasons,
Bound PVCs, resources, GPU tolerations and required affinity. Preserve PVCs and
caches; allocation panels do not show cluster capacity.

## Rollback and notification boundary

Before changing controller identity or monitoring, preserve the controller
Deployment, target ConfigMap, canonical locks and owned monitoring objects in
ignored private evidence. Restore the previous controller image/configuration
as one controlled rollout, restore the nine baseline monitoring objects, and
remove only the four newly owned native-integration objects. Never delete
ChartAssignments, ResourceSets, application Pods, model repositories or PVCs.
Then re-run read-only acceptance. The E8 completion run retains a target- and
controller-generation-checked rollback at
`openshift/.generated/e8/finish/rollback.py`; it was prepared, not exercised.
That snapshot predates the Grafana image-lock addition and audit fixes. Its
generation/configuration guard should reject newer state; do not bypass it.
Prepare a new matching snapshot before a later rollback. For Grafana-only
removal, use the [trial's removal instructions](GRAFANA.md#verify-and-remove).

Prometheus evaluation and in-console alert state are verified. External email,
Slack, paging receivers and notification delivery remain the platform
administrator's responsibility; E8 does not configure or send external messages.
