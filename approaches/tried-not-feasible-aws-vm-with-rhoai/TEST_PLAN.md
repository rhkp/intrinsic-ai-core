# AWS VM + RHOAI historical test plan

**Status:** historical; hybrid work has stopped and full OpenShift is the
selected direction. This plan records checks considered for dev01 and is not an
active test sequence. The UI-created gRPC model became Ready and internal
inference passed. From the AWS VM, DNS/TLS to its token-authenticated route
worked, but ALPN `h2` was not negotiated and external gRPC inference did not
pass. REST inference from the VM was not validated end to end. dev02 results
remain excluded from dev01 evidence.

## Gate 0 — Confirm the test can be run safely

Before serving-resource writes, record only non-sensitive inventory and get
the cluster owner's agreement on GPU quota, storage, route type, and test
window. The target project is `arhkp-intrinsic`; see the safe workflow in
[`openshift/`](openshift/README.md).

- Re-run preflight on dev01 and record its OpenShift/RHOAI/KServe versions,
  policy, and GPU capacity. Do not reuse dev02's inventory or capacity data.
- Confirm ROSA topology, enabled model-serving
  platform, compatible serving runtimes and deployment mode, GPU type/VRAM,
  available quota, and approved model/object storage. Validate the exact
  OpenShift/RHOAI compatibility entry before selecting versions.
- Confirm the VM-to-endpoint route, DNS, egress policy, TLS trust, authentication
  mechanism, and which team operates each network boundary. A fixed VM address
  is not proof that the VM can route to a private ROSA service.
- Confirm dev01's inference-route exposure and authentication before deploying
  or sending any model input. dev02's external-ingress approval does not apply
  to dev01. Do not infer source-network allowlisting; obtain the applicable
  approval for dev01 before non-synthetic inputs or wider use.
- Confirm the application endpoint is reachable from the VM. A private ROSA
  route does not itself connect the VM's VPC; validate the required private DNS
  and VPC-to-VPC path with the network and cluster owners.
- Confirm the support boundary for the exact target RHOAI/KServe version,
  runtime, deployment mode, and intended pilot scope. The support matrix places
  Triton in a separate Tested and verified category, outside its Supported
  model-serving runtimes table; do not generalize that entry to the Intrinsic
  wrapper or custom integration.
- Trace both request and dependency flows for the moved wrapper: the pose
  request from VM to RHOAI, plus any calls from RHOAI back to Intrinsic
  DataAssets/CAS, resource registration, or model control on the VM. Identify
  exact destinations, ports, sources, and ownership before any network-rule
  change. Prefer private connectivity and narrowly scoped policy.
- Identify an approved sample/model and expected request/response contract. Use
  synthetic or otherwise approved data; classify request and response content
  (including any image/tensor payloads, detections, poses, and metadata) and get
  approval for its transfer and storage. Establish output comparison tolerances
  and a latency target with the application owner before testing.
- Confirm the current VM Only deployment can still be selected for comparison
  and recovery. Record software release and model identifiers, never credentials
  or raw sensitive logs.
- Budget GPU capacity independently on the VM and ROSA while the local path is
  retained. The GPUs are not pooled; record available cluster quota, model VRAM
  needs, and pilot cost before scheduling both paths.

**Pass:** target versions and resources are compatible; the chosen network and
secret path are approved for the bounded test; sample data and measurable
correctness/latency criteria are agreed. **Stop:** any prerequisite is unknown
or requires unreviewed exposure, unprotected credentials, or wider-than-needed
privilege.

## Gate 1 — Prove the RHOAI serving platform

In the `arhkp-intrinsic` project, deploy a minimal Triton model using the
runtime and deployment mode supported by the installed dev01 release. Verify
that release's support matrix and instructions; do not assume the dev02 RHOAI
3.4 workflow or runtime configuration applies. Confirm the applicable support
scope for the exact use. This status does not validate the Intrinsic image or
model bundle. Verify storage, GPU assignment, readiness, model identity, and a
valid request/response. If a toy model is used, record that this proves only
platform plumbing, not RF-DETR/FoundationPose compatibility. Keep the service
scoped to the pilot and verify its route exposure and authentication. Do not
expose the cluster API to the VM.

**Pass:** the serving endpoint returns a known-good result to an authorized
client; readiness and model identity are observable; the service can be stopped
and recreated from the recorded versioned configuration. **Stop:** the selected
runtime/model contract or GPU placement is unsupported or not reproducible.

**dev01 observation:** the UI-created Triton gRPC deployment became Ready and
returned CIFAR-10 inference through a temporary localhost-only port-forward,
with output shape `[1, 10]`. This proves internal serving for the synthetic
model only; it does not prove external inference, GPU acceleration, or
Intrinsic-model compatibility. The dev02 REST smoke is historical and excluded
from this gate. Reproduce and record dev01 steps in
[`openshift/serving/README.md`](openshift/serving/README.md).

### Required external-route authentication check

Before any Gate 2 client testing or increase in traffic, configure the model using the
RHOAI-supported external model access and token-authentication flow, or an
approved gateway that provides equivalent controls. Do not treat a manually
created Route, a TLS certificate, or an obscured URL as caller authentication.
The dev02 direct-CR smoke used RHOAI's controller-managed Route and token
authentication, but that configuration/result is out of scope for dev01. On
dev01, use the current RHOAI-supported UI workflow and verify its route and
token behavior there. Do not assume annotations, service-account setup, or
permissions transfer between cluster versions. See
[`openshift/README.md`](openshift/README.md) for the dev01 status and historical
dev02 notes.

From a client outside the cluster on the approved network path, verify all of
the following using synthetic input only:

1. The DNS name resolves through the intended private or public ingress path,
   and its exposure matches the approved network policy. Confirm the AWS VM
   source is allowed while unintended sources are excluded by the chosen
   network boundary.
2. TLS hostname and certificate-chain validation succeed with normal
   verification enabled. The test must not use `curl -k`, disable certificate
   verification, or trust an unapproved CA bundle.
3. A request with no token and a request with an invalid or expired token are
   denied (normally HTTP 401 or 403) and return no inference result.
4. A request with the dedicated, least-privilege service-account token is
   accepted and returns the expected model identity and deterministic smoke
   output.
5. The token is read from protected runtime secret storage, is not printed or
   placed in command history/logs, and can be rotated or replaced without
   changing the application image. Record only sanitized status codes, model
   identity/version, and timing.

**Pass:** token and TLS checks succeed and actual exposure matches the policy
approved for the test. For this bounded synthetic demo, the owner accepted the
external route with required token authentication; source-network allowlisting
was not tested and is not claimed. **Stop:** unauthenticated or invalid-token
requests reach inference, TLS validation needs to be bypassed, or credentials
appear in logs. Revisit source restrictions before non-synthetic data or wider
use.

For the CIFAR10 smoke deployment, the reproducible checks are implemented in
[`openshift/serving/verify_external_route_auth.py`](openshift/serving/verify_external_route_auth.py).
It reads the endpoint and token-file path from the ignored `.env`, submits only
a synthetic zero tensor, and prints sanitized statuses without printing the
route URL or token.

**dev02 result — excluded:** a local client and the AWS VM received HTTP 401
for missing/invalid tokens and HTTP 200 with a valid token for synthetic
CIFAR-10 inference. This is not evidence for dev01. **dev01 status:** the AWS VM
resolved the route and validated TLS, but the route did not negotiate ALPN
`h2`; no authorized external inference result has been obtained. Authentication,
route exposure policy, and source-network restrictions must be tested and
approved specifically for dev01. Gate 2 has not passed.

## Gate 2 — Prove secure VM-to-endpoint connectivity

**Outcome when this plan was stopped:** the AWS VM resolved and reached the
model Route over TLS, but the Route did not negotiate ALPN `h2`, so external
gRPC inference did not pass. Internal Triton gRPC inference worked. External
REST inference from the VM was not validated; dev02 REST results are excluded.
The proposed REST-configured runtime and token-authentication check on dev01
were not carried out. RHOAI documentation for another release is retained below
as historical reference only; it must not be treated as the selected target's
procedure.

Only after that works should we test Intrinsic's client contract or consider
an adapter. Also test an expired credential, an unreachable endpoint, a slow
response, and a service restart. Record sanitized timings and statuses. Verify
errors stop the request cleanly and do not advance the behavior tree into
motion.

**Verified on dev01 so far:** internal Triton gRPC inference succeeds, and the
AWS VM can resolve the model Route and validate its TLS certificate. External
authorized inference has not passed: gRPC lacks negotiated `h2`, and REST has
not been deployed/tested. The dev02 REST/auth results do not count. The
resilience cases above remain untested. This isolated check needs no new VM
inbound rule.
This result does not settle the bidirectional Core dependencies for Gate 3.
**Stop:** the service accepts unauthenticated calls, TLS checks must be bypassed,
or a failed call can trigger movement.

## Gate 3 — Compare Intrinsic integration patterns

Keep the executive, skills, pose-estimator orchestration, simulation, and
robot-control components on the VM. Keep its local inference pair available for
comparison and manual recovery. Do not select a serving design until the source
API and target RHOAI/KServe contract have been compared. Evaluate these two
patterns as isolated spikes:

**Pattern B — custom paired runtime.** Place the Intrinsic wrapper and Triton
together in a user-maintained RHOAI `ServingRuntime`/`InferenceService`, if the
target KServe version supports the pod shape. Preserve their local socket and
shared-memory path. Verify the wrapper exposes the request protocol that the
KServe endpoint actually routes; verify sidecar/container ordering, port
selection, health/readiness, restricted UID/permissions, GPU assignment, model
staging, and registration with Intrinsic services. Treat this as custom and
user-maintained unless Red Hat confirms the applicable support scope.

**Pattern C — RHOAI Triton endpoint with an Intrinsic client change.** Use the
RHOAI Triton serving endpoint and adapt the Intrinsic inference client to remote
gRPC/HTTP. This needs code/configuration changes for the currently hard-coded
Unix socket and shared-memory calls. Define how model files reach the service,
how RHOAI/KServe model rollout replaces Intrinsic's dynamic load/unload loop,
and how endpoint/model versions map to the two required models. Do not expose
Triton's administrative model-management API to the general client.

For either pattern, answer and test the following before sending a task through
remote inference:

- Does the integration use the KServe protocol and model-serving API surfaced by
  the target RHOAI deployment, or does it require a custom endpoint outside the
  documented `InferenceService` contract?
- How does RHOAI's per-model server lifecycle map to the current model set
  (RF-DETR and FoundationPose) and Intrinsic's model/resource lifecycle? Are
  separate `InferenceService` resources or a versioned combined model package
  required?
- For Pattern B, can the target ServingRuntime template keep both containers,
  shared model files and `/dev/shm`, required ports/probes, and GPU allocation
  under cluster policy?
- For Pattern B, can the wrapper register the same Intrinsic resource identity
  and reach DataAssets/CAS and control dependencies from ROSA? Which narrowly
  scoped ROSA-to-VM flows and DNS/VPC routes are needed?
- For Pattern C, can the VM-side client call inference over TLS/authenticated
  gRPC/HTTP, while KServe owns model readiness and rollout without exposing
  Triton's admin APIs?
- Which approved storage location stages the exact same pinned model artifacts
  as the VM Only baseline, and who owns version promotion and rollback?
- Are request/response tensors, coordinate conventions, labels, and pose output
  semantics identical to the local baseline?
- Can each required VM↔ROSA flow use an authenticated, encrypted, narrowly
  scoped route without exposing unrelated Core APIs or the Kubernetes API?
- If the wrapper moves to ROSA, can its required calls to VM Core be routed
  privately and narrowly; if the wrapper stays on the VM, can inference requests
  reach the RHOAI endpoint outbound from the VM?
- What are model startup/readiness time, warm-request latency, errors, and GPU
  memory usage for the representative model?
- Can the VM pin a model/version and fail closed if the service is missing,
  incompatible, or returns an invalid result?

Compare identical approved input through local and each feasible RHOAI-managed
pattern. Verify outputs against the agreed criteria and exercise timeout,
endpoint restart, resource-registration failure, and model-version mismatch.
Run on the simulator only; never attach a physical robot for this gate. Record
which pattern was actually tested and its support/maintenance owner. If neither
pattern preserves the contract, stop RHOAI inference integration and retain
local inference; do not silently replace the serving path with an ordinary
Deployment and call it RHOAI-managed serving.

**Pass:** one explicitly selected pattern returns the required model results
through the validated request/response contract, meets agreed accuracy and
latency criteria, fails closed before motion, has all required cross-environment
flows approved, and documents model lifecycle and support ownership. **Stop:**
the KServe/API contract, model set, shared-memory or artifact assumptions cannot
be resolved, results drift outside criteria, or errors are not safe. In that
case, evaluate RHOAI workbench/pipeline/registry value separately and retain
local inference.

## Gate 4 — Bounded simulation demonstration

Only after Gates 0–3 pass, switch one controlled simulation run to the remote
model. Keep the existing local path available for manual rollback, pin both
application and model versions, and capture sanitized outcomes. Do not run an
unbounded loop or automatically fail over between models; a remote failure must
stop the current task. Treat the known unloading collision as a separate
simulation issue and do not claim full cycle success unless unloading also
passes.

**Pass:** perception and expected simulated arm actions complete with the remote
model, no unsafe motion follows an endpoint error, measured resource/latency data
are recorded, and rollback to the known VM Only path is demonstrated. A full
machine-tending pass additionally requires resolution of the known unloading
collision. **Stop:** unexpected motion, output mismatch, repeated timeout, or
baseline recovery failure; restore the VM Only endpoint and investigate.

## Comparison record

For each test, record a timestamp, software and model versions, gate, synthetic
case identifier, pass/fail, request latency, output comparison, and sanitized
failure category. Record CPU/GPU/memory observations when relevant. Do not save
tokens, endpoint addresses, certificates, user/customer data, full images, or
unredacted logs. Store raw evidence only in an approved location with its own
access controls.

| Measure | VM Only baseline | AWS VM + RHOAI | Acceptance threshold |
| --- | --- | --- | --- |
| Model/runtime version | Record at test time | Record at test time | Pinned and traceable |
| Request/response compatibility | Record representative output | Record same input/output | Agreed before Gate 3 |
| Warm and cold latency | Measure | Measure, including network | Agreed before Gate 3 |
| Failure behavior | Verify local path | Verify remote fail-closed path | No motion after invalid/missing inference |
| Recovery | Existing deployment is reference | Manual switch back to VM Only | Demonstrated before Gate 4 |
| Full demo | Known unloading collision | Same independent issue tracked | Full pass only after collision is resolved |

## Rollback

Keep the original local inference deployment and configuration unchanged during
Gates 0–3. Select the remote endpoint only through the isolated experiment
configuration. On any gate failure, stop the current simulation task, restore
the VM Only inference setting, verify the local path with the approved sample,
and record the failure category. Do not delete or modify the baseline as part of
this experiment.

## References checked 2026-10-01

The versioned RHOAI 3.4 documentation below was consulted during the dev02
investigation. Confirm dev01's installed release and use its matching docs
before treating any version-specific UI, API, or runtime behavior as applicable.

- [RHOAI 3.x supported configurations](https://access.redhat.com/articles/rhoai-supported-configs-3.x), including the RHOAI/OpenShift version matrix and serving-runtime categories.
- [RHOAI 3.4 model-serving platform](https://docs.redhat.com/en/documentation/red_hat_openshift_ai_self-managed/3.4/html/configuring_your_model-serving_platform/configuring_model_servers), including runtime configuration and the Triton runtime procedure.
- [RHOAI 3.4 model deployment](https://docs.redhat.com/en/documentation/red_hat_openshift_ai_self-managed/3.4/html/deploying_models/deploying_models), including KServe deployment modes and per-model server behavior.
- [ROSA infrastructure security](https://docs.aws.amazon.com/rosa/latest/userguide/infrastructure-security.html), including private API/application routes and cluster/pod network isolation.
- [ROSA responsibilities](https://docs.aws.amazon.com/rosa/latest/userguide/rosa-responsibilities.html), including customer-managed cluster networking and firewall responsibilities.
