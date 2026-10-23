# Adversarial review of the RHOAI migration plan

## Outcome and evidence limits

The migration target is **Triton only**. The current inference wrapper remains
in its existing application deployment, and the rest of the demo remains as-is.
The first gate is the RHOAI dashboard workflow: the project is discoverable,
Triton is selected as the serving runtime, the model deployment reads the
dedicated PVC, and RHOAI exposes it through Model access with token
authentication. The existing wrapper will be pointed at that endpoint only
after the generated Route, model availability, and remote Triton contract are
proven. Source review found concrete client-transport and model-repository
constraints that an endpoint-only change would miss.

After login renewal, live bounded discovery completed: the native controller
and owned pods are healthy, required KServe APIs and management permissions are
available, and the archived hybrid Triton runtime configuration was reused for
CLI-created isolated candidates. The namespace has the dashboard marker and
the requester has an Admin RoleBinding; the user confirmed dashboard visibility
and that Triton runtime is available. The CLI candidates, including CIFAR-10,
have been deleted. The user created one dashboard-managed REST deployment from
the dedicated PVC. KServe reports Ready, Model access created a Route, and the
auth annotation/OAuth proxy are present. The InferenceService requests one
GPU and the current predictor is scheduled on an A10G. Initial external Route
requests failed. Follow-up checks confirmed
the mesh denies ingress to predictor pods unless their template sets
`maistra.io/expose-route=true`; the installed KServe schema supports setting
that label through `spec.predictor.labels`. After applying it through the
owning InferenceService, a strict-TLS external REST inference request with a
fresh bearer token returned HTTP 200. The initial gRPC ALPN failure remains a
separate transport issue; this finding does not explain or resolve it. Missing
and invalid-token rejection through the external Route, FoundationPose REST
inference, and wrapper integration remain unproven. A separate 2Gi RWO PVC has
been populated from the REST claim with an 8-file, 576,080,733-byte copy; all
SHA-256 checksums matched. It is reserved for the planned parallel gRPC test.

The platform reports KServe and ModelController component readiness while the
aggregate DataScienceCluster Ready condition is false due to other component
conditions. Investigate only dependencies needed for the serving gate; do not
conclude that all RHOAI is healthy or that unrelated components must be repaired
before any serving test. The current runtime/mode must still be matched to the
installed controller version and generated resources.

The native wrapper has no application probes in the inspected pod. Its pod
Ready condition is therefore insufficient evidence of model readiness. The
former CLI actual-model candidate was cluster-local and had no auth or Route.
The current dashboard-managed deployment has one Ready predictor pod; its
token-authenticated external REST Route has completed one synthetic inference.
These are sanitized observations, not infrastructure inventory snapshots.

The native Triton `/models` mount is an `emptyDir`, not a PVC. A snapshot of its
8 current files (576,080,733 bytes) was copied directly to the new
`triton-model-repository-candidate` PVC, with each file's SHA-256 checked during
transfer. Existing PVCs were not used or modified, and the temporary transfer
pod was deleted. This establishes a verified copy from the live mount only;
KServe consumed the PVC snapshot read-only at `/mnt/models`. Both actual models
loaded, reported metadata matching the pinned tensor contracts, and completed
synthetic-input gRPC inference. GPU logs show CUDA providers and the native
FoundationPose rasterizer on NVIDIA A10G. This does not establish identity
against pinned release bundles, real demo-fixture behavior, or update/rollback.

Scheduling required a `g5-gpu=true:NoSchedule` toleration and
`nvidia.com/gpu.product=NVIDIA-A10G` selector. The first GPU-requested pod was
placed on a node that advertised one allocatable GPU but had no NVIDIA product
labels; Triton reported no CUDA device and exited. Constraining the candidate to
labeled A10G nodes resolved device visibility. The obsolete candidate ReplicaSet
also held the RWO PVC during rollout; scaling that candidate revision to zero
released the attachment. These fixes apply only to the isolated candidate.

## What the previous attempt actually established

| Archived observation | Supported conclusion | Consequence now |
| --- | --- | --- |
| Target-cluster synthetic gRPC inference passed through a local port-forward | A synthetic Triton backend could answer that request | Repeat over the ordinary serving path from an in-cluster client with the intended identity. Port-forwarding does not validate gateway, authorization, mesh, or network policy. |
| VM DNS/TLS succeeded but the external gRPC path did not negotiate HTTP/2 | The tested external transport path failed before application inference was established | Keep the Route in the target design because it is the RHOAI Model access and token-auth path. Re-test the RHOAI-generated Route and protocol on this cluster; if gRPC is unsupported, use a compatible REST client path or an approved bridge. Do not hand-create or hand-patch a Route. |
| A custom Route certificate patch was not retained; the live Route is owned by its `InferenceService` | The same ownership issue applies here. OpenShift 4.20 requires a custom non-wildcard Route certificate for frontend HTTP/2 ALPN, but a certificate alone does not prove gRPC through the OAuth-proxy sidecar | Configure the owning resource through a supported RHOAI interface. First establish token-authenticated gRPC support end to end; otherwise validate a REST-configured runtime and wrapper adapter. Do not hand-patch generated Routes, Services, Deployments, or pods as the migration mechanism. |
| The synthetic sample requested a GPU but its model configuration used CPU execution | GPU scheduling was exercised; GPU model execution was not proven | Use a CPU-only transport smoke first. Actual-model tests must prove GPU execution, including FoundationPose's renderer and ONNX sessions. |
| Successful REST/token results appear in historical comparison-cluster sections | Those results are not target-cluster evidence | The archive mixes historical contexts; use the explicit target-status sections as the conservative evidence boundary. Reproduce target auth tests rather than promote an old success statement. REST success cannot substitute for the actual gRPC caller. |

Historical sources: the original hybrid status, target/comparison observations,
and synthetic serving record preserved in Git commit `df77a76` (2026-10-13).
The [current hybrid runbook](../../approaches/aws-vm-with-rhoai/README.md) records
the subsequent Route diagnosis and successful VM demo. The earlier observations
above describe the original experiment.

## Challenged assumptions and required fixes

| Assumption | Finding | Required correction / rejection test |
| --- | --- | --- |
| Pointing the pose estimator directly at Triton is sufficient | The pose caller expects the existing Intrinsic wrapper API and resolved dependency metadata | Keep the pose-estimator-to-wrapper contract and resource identity unchanged. Change only the retained wrapper's downstream Triton client. Prove the wrapper-to-RHOAI-Triton service identity/auth path; do not expose Triton directly to the pose caller. |
| Triton's endpoint can replace the wrapper API | The wrapper also provides Intrinsic ServiceState, readiness, model metadata behavior, and caller-facing error semantics | Keep the existing wrapper deployed as the compatibility boundary. Verify its current caller-facing API remains unchanged while its backend call goes to RHOAI Triton. |
| Reusing the native Triton command/image under KServe preserves model behavior | The current Triton shares local model files and `/dev/shm` with the wrapper; KServe will create a separate serving pod | Verify the installed runtime's command, ports, model repository, Python backend and CUDA compatibility, storage mount, UID/groups, readiness behavior, and GPU placement. Keep the wrapper and its CAS adaptation in the native application deployment. |
| A remote endpoint setting alone completes the migration | The wrapper uses a Unix socket and shared memory; its model controller writes repository files locally and sends load/unload RPCs | Change the wrapper client to network gRPC with shared memory disabled, and prove how the RHOAI pod sees the required model repository and handles model version/load lifecycle. |
| RHOAI model readiness proves the existing wrapper can use the service | Existing model names/versions, OIP metadata, message sizes, Route TLS/token authentication, protocol, and model lifecycle still have to match | Test Triton metadata/readiness/inference through the RHOAI-managed token-authenticated Route using the protocol the wrapper will use, then connect the existing wrapper and use the unchanged demo flow. |
| A KServe Triton service automatically supports the full native multi-model repository | Standard model serving may initialize a single model or impose a storage lifecycle; the Intrinsic controller expects local repository writes and dynamic load/unload | Inspect the installed runtime and generated service. Prove the exact repository layout, multi-model behavior, storage mount, model-control mode, and update/rollback path before selecting it. |
| `ServerReady`, pod Ready, or wrapper Enabled means the required models are ready | The runner starts reconciliation asynchronously after server readiness; model-load failures are recorded separately | Require the complete expected model set to pass `ModelReady` and compatible metadata before releasing traffic, then execute meaningful model requests. An empty repository or a failed required model must keep the candidate unavailable for the application. |
| Existing polling timeouts bound network failures | Inspected readiness and inference RPC calls do not set individual deadlines. The caller also ignores the readiness helper's false return before attempting inference | Add bounded per-RPC calls, enforce overall readiness expiry, and stop on false readiness. Propagate cancellation through any bridge. Prove stalled connections and backend responses fail within measured budgets. |
| Container restart is equivalent to clean pod recreation | Controller bookkeeping is in memory; reconciliation compares asset state with its local loaded-model map. A remote Triton restart can lose backend model state while the wrapper retains stale local state | Test wrapper-only, RHOAI Triton-only, and native rollback separately. Detect stale model state and rehydrate it, or use a documented coordinated reset. Do not claim current source repairs this automatically. |
| A transient failed model load will retry when dependencies recover | The base controller explicitly avoids retrying an unchanged FAILED model until reset or configuration change | Test transient CAS/backend/environment failures and recovery without modifying shared model assets to force a retry. Implement bounded retry/reset behavior or document a safe candidate-only restart procedure before cutover. |
| A separate candidate name isolates every mutation | The retained wrapper and candidate Triton may read the same InstalledAssets/DataAssets source | Pin candidate model versions and avoid test installs/updates/removals against the live demo asset set. If isolation needs a separate repository/catalog, design it before lifecycle tests. |

## Source anchors for the new findings

- [Actual pose caller](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_perception/intrinsic/perception/service/ioc_pose_estimator/service/ioc_pose_estimator_service.py): resolved inference dependency, insecure channel, static header interceptor, unlimited channel message settings, and RF-DETR input layout transformation.
- [Segmentation caller](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_perception/intrinsic/perception/service/ioc_pose_estimator/service/segmentation_model.py) and [pose caller](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_perception/intrinsic/perception/service/ioc_pose_estimator/service/pose_estimator_model.py): contiguous raw tensor requests, metadata-selected outputs, polling and inference without per-RPC deadlines.
- [Inference adaptation](../deployment/chartassignment-controller/controller/openshift_inference.go): `adaptInferenceCASAddress`; [Pod adaptation](../deployment/chartassignment-controller/controller/openshift_pod.go): ingress/environment injection. [Resource renderer](../deployment/intrinsic-core/intrinsic/assets/deploy/renderutil.go) and [template](../deployment/intrinsic-core/intrinsic/assets/deploy/resources.yaml): resolved RuntimeContext and resource Services/metadata/VirtualServices.
- [Runner](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_inference/core/inference_runner.py) and [base controller](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_inference/core/model_controller_base.py): asynchronous reconciliation and unchanged FAILED-model retry suppression.
- Representative upstream **KServe v0.15.2**, [runtime schema](https://github.com/kserve/kserve/blob/v0.15.2/pkg/apis/serving/v1alpha1/servingruntime_types.go) and [predictor builder](https://github.com/kserve/kserve/blob/v0.15.2/pkg/controller/v1beta1/inferenceservice/components/predictor.go), support additional containers and distinguish the primary serving container. This is **not identification of the installed version**. The schema groups `grpcDataEndpoint` and related management fields under ModelMesh; do not use them as a recipe for Standard serving ports. Do not set `multiModel: true` merely because the pair hosts two model assets.

## Required experiment matrix

Run experiments in order. Each records the owning desired resource, exact image
and artifact identities in private evidence, source/destination service accounts,
path/protocol, expected result, actual result, and cleanup/rollback action. Only
sanitized outcomes belong in Git. Unknown or unrun checks remain unproven.

| ID | Experiment | Acceptance condition |
| --- | --- | --- |
| E0 | RHOAI dashboard, runtime, PVC, and Route discovery | Project visibility, installed runtime, PVC mount, dashboard deployment, auth annotation, and generated Route are confirmed. Strict TLS and valid-token REST inference are confirmed. Still verify missing/invalid-token rejection and safe GPU capacity. |
| E1 | Isolated RHOAI Triton backend smoke | **Backend and external REST smoke passed:** in-cluster gRPC readiness, metadata, and synthetic inference passed for actual models; external Route REST inference passed for RF-DETR. The CIFAR service was deleted. Route gRPC remains unproven due to ALPN negotiation. |
| E2 | Required model repository on RHOAI | Current dashboard deployment loads RF-DETR and FoundationPose from the dedicated PVC on the current A10G pod. Still verify artifact/config identity, FoundationPose REST inference, Python backend and CUDA compatibility, and repository lifecycle. Do not mutate or retire native assets. |
| E3 | Existing wrapper remote-client integration | Configure the wrapper for the proven RHOAI Route transport with TLS and token delivery, disable shared memory, and prove request/response parity, message-size limits, bounded deadlines, cancellation, and a supported answer for local asset materialization plus remote model load/unload. Keep every other demo component unchanged. |
| E4 | Existing demo flow and native rollback | Exercise the current demo through the existing wrapper with RHOAI Triton. Verify model outputs, latency, failure surfacing, and correlation to the candidate; then revert only the wrapper's Triton endpoint and verify native recovery. |

### Budgets and ownership must be written down before E1/E2

Set measured budgets for cold initialization, one readiness RPC, total readiness
wait, one inference RPC, end-to-end demo inference, retries, payload sizes, and memory.
Apply them consistently to clients, any transport bridge, mesh/proxy layers,
probes, and serving. Do not inherit unlimited client message sizes blindly or
hide a shorter proxy timeout behind a longer outer polling loop.

Record one owner for each layer: application resource metadata/binding and
RuntimeContext, custom runtime/configuration, KServe-generated children,
credential delivery, model assets/catalog, and rollback selection. Ensure
application cleanup cannot garbage-collect candidate configuration still needed
by serving, and KServe cleanup cannot remove native model assets. Two controllers
must not compete to own the same Service, Deployment, or ConfigMap.

## Discovery tool corrections implemented in this review

The preflight now requires current StatefulSet ownership/rollout rather than a
matching pod-name prefix; verifies CRD establishment and served versions; accepts
only successful, unambiguous operator versions; excludes disabled, REST-only,
ModelMesh and unrecognized runtime shapes; checks inspect/create/patch/delete
permissions separately; distinguishes denial from an unverified authorization
query; and aborts if authentication/connectivity is lost during discovery.

Its `discovery_complete` flag only describes the bounded discovery checks.
`phase_0_complete` and `candidate_deployment_ready` remain false, with explicit
missing evidence. Neither `managementState: Managed` nor a namespace dashboard
label is presented as live serving readiness. These are technical evidence
gates, not a new permission or approval process.
