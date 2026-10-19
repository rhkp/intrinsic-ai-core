# RHOAI migration handoff

## Read first

1. [README.md](README.md): sequential plan and acceptance gates.
2. [REVIEW.md](REVIEW.md): adversarial findings, prior-failure evidence, and E0–E7 experiment matrix.
3. [CONTRACT.md](CONTRACT.md): pinned source behavior and both model tensor contracts.

## Current state

### Latest result: E2 canonical consumer migration complete (2026-10-19)

- E2.4 is deployed. Canonical configuration and the portable schema version 2
  image lock are required by controller/offline entry points. Operational tools
  use the same validated target; full controller/RBAC/workspace manifests are
  generated under ignored `openshift/.generated/`.
- The 223 rendered objects are unchanged. All four ChartAssignments are Ready,
  four ResourceSets Settled, and all 10 original PVCs Bound with identical
  UIDs/specifications. Application pod identities and workload specifications
  are unchanged. PVCs and build caches were preserved.
- Previous controller/schema rollback and canonical restoration both passed
  no-op reconciliation without application or PVC changes. The final deployed
  checksum matches the complete image lock, including existing demo images.
- Gateway mTLS, private-image pull/startup, RHOAI model readiness and synthetic
  wrapper inference pass. Temporary smoke resources are gone.
- The documented four-file scene reset succeeded. The cached one-cycle demo
  completed FoundationPose on RHOAI in **8.060 seconds**, then stopped on the
  known `gripper.gripper_finger2` / `raw_stock_2x3x5.base_link` collision. No
  timeout, connection failure or unreachable target was reported. The wrapper
  has no native Triton container. Full pick completion remains unproven (E5).
- All 63 Python tests, Go tests/vet, formatting/lint, strict target audit, shell
  syntax and provenance checks pass. CI now enforces canonical target policy.
- Use [the current canonical runbook](../README.md#canonical-operational-helpers)
  and [E2.4 completion evidence](../../ENHANCEMENTS.md#milestone-e24--migrate-consumers-and-remove-compatibility).
  Older entries below describe historical milestones and rollback boundaries.

### Latest result: canonical controller configuration rollout (2026-10-19)

- E2.2/E2.3 configuration delivery is deployed. The controller reads the target
  configuration and image lock before starting reconciliation; application
  image references and templates are unchanged. All four ChartAssignments are
  Ready and all four ResourceSets are Settled.
- All 10 original PVCs retain their identities, specifications, and Bound
  status. The workspace and its caches were preserved, and the demo used its
  existing cached executable. No application pods were replaced by the rollout.
- Internal RHOAI model readiness and synthetic wrapper inference passed before
  and after rollout. The gateway mTLS and private-registry pull smokes passed.
- A real demo attempt reached RHOAI but stopped on an unreachable pre-grasp
  target. The documented four-file cell reset succeeded. The subsequent demo
  retry completed FoundationPose on RHOAI in 8.184 seconds with one output and
  reached the previously observed gripper/stock collision. Full pick completion
  remains unproven; do not report it as a configuration-rollout acceptance pass.
- Ignored `openshift/config.local.json` and `openshift/image-lock.local.json`
  hold the approved local settings. Ignored `openshift/.generated/` holds the
  deployed config artifacts and exact controller rollout/rollback patches.
  Rollback is prepared but has not been exercised. See
  [E2.3 evidence](../../ENHANCEMENTS.md#milestone-e23--move-the-controller-to-validated-configuration)
  for the completed gates and the remaining E2.4 compatibility consumer.

### Latest result: controller-delivered internal gRPC adapter (2026-10-15)

- Rolled out the existing ChartAssignment controller with only the RHOAI
  inference adapter added to its last working source baseline. The controller
  is 1/1 Ready. All four ChartAssignments are Ready, all four ResourceSets are
  Settled, and all 10 project PVCs remain Bound. No PVC or build cache was
  deleted.
- The controller reconciliation applied the adapter to the existing
  `rs-inference-service` StatefulSet. Its native Triton sidecar is absent; the
  wrapper is 2/2 Ready with the internal RHOAI gRPC endpoint and projected
  service-account token mounted.
- From inside the wrapper pod, token-authenticated calls to the RHOAI Triton
  service returned `ServerLive=true` and `ServerReady=true`. Both
  FoundationPose and RF-DETR reported READY, and their metadata matched the
  expected tensors.
- A synthetic zero-image RF-DETR `ModelInfer` through the wrapper's ordinary
  gRPC API succeeded in 0.038 seconds. It returned empty boxes `[0,4]`, scores
  `[0]`, masks `[0,224,224]`, and visibility `[0]`. This proves the wrapper
  request reached RHOAI; it is not a real demo or pose-estimation run.
- On pod startup, the wrapper also logged CAS model-file download failures
  (`RST_STREAM` / canceled response) while the CAS backing pod was Ready. The
  wrapper and remote inference smoke passed, but investigate these asset-load
  errors before claiming the complete pose-estimation demo works.
- No uninstall/reinstall was performed. No RHOAI model PVC or build cache was
  changed. The controller rollout did reconcile the four existing
  ChartAssignments; their resulting ResourceSets are Settled.

### Previous result: direct StatefulSet adapter (2026-10-15)

- Enabled documented Istio sidecar injection on the gRPC `InferenceService`
  only. Its predictor is Ready with Triton, OAuth proxy, and Istio proxy. The
  REST deployment was untouched.
- Added a namespace-local ClusterIP Service on port 9000 targeting predictor
  `h2c`, plus a RoleBinding granting the wrapper service account `get` access
  only to the named `InferenceService`.
- The first in-wrapper gRPC probe returned `UNIMPLEMENTED` with
  `x-ext-auth-reason: Service not found`. A temporary namespace-scoped AuthConfig
  mapped the Service authority to Kubernetes TokenReview and a
  SubjectAccessReview. Its first version omitted the SAR user and returned a
  validation error. Adding the documented selector
  `auth.identity.user.username` fixed it. No cluster-wide CRD or policy changes
  were made. The installed CRD serves `v1beta2` and `v1beta3`; Authorino Operator
  CSV is 1.3.1, so the Red Hat workaround for disabled `v1beta2` reconciliation
  does not match this cluster.
- The AuthConfig accepted both the RHOAI-generated serving token and a
  short-lived token for the existing wrapper service account. The latter is
  authorized only for `get` on the named InferenceService. Tokens were passed
  through stdin and never printed, passed as arguments, or logged. The running
  wrapper disables default token automount, so its implementation projects a
  rotating token with audience `https://kubernetes.default.svc`.
- Before the wrapper switch, its pod used a short-lived service-account token to
  call the internal RHOAI Triton gRPC service; `ServerReady`, `ModelReady`, and
  `ModelMetadata` succeeded for RF-DETR. A synthetic zero-image inference also
  succeeded directly against that service.
- Applied the wrapper adapter to only the existing inference StatefulSet. The
  wrapper now uses the internal RHOAI gRPC endpoint and a projected rotating
  service-account token; the native Triton sidecar was removed from that
  StatefulSet. Its existing ingress settings and GPU toleration were preserved.
  The serving Route and REST deployment were not changed, and no unrelated
  workload templates were modified. Four already-Pending application pods were
  recreated from their corrected StatefulSet templates; all are Ready. The
  controller Deployment remains on its previous image.
- After rollout, the wrapper was 2/2 Ready (application plus mesh proxy), with
  no native Triton sidecar. Through the wrapper's ordinary Triton gRPC API,
  `ModelReady` and `ModelMetadata` succeeded for the RF-DETR model, and a
  synthetic `ModelInfer` returned valid empty outputs: boxes FP32 `[0,4]`, masks
  BOOL `[0,560,560]`, scores FP32 `[0]`, and visibility FP32 `[0]`. The call
  completed in 0.182 seconds. Since the native sidecar was absent, the response
  came through the wrapper's RHOAI client path.
- At this point the source adapter had not yet been delivered through the
  controller; the live wrapper change was applied directly to its StatefulSet.
  The later controller-delivered result is recorded above.
- The Service, RoleBinding, and AuthConfig manifest uses
  `RHOAI_NAMESPACE` and `RHOAI_INFERENCESERVICE_NAME` placeholders. Render it
  with `envsubst` before applying. Do not commit live cluster identifiers.
- The generated predictor Service on port 8443 still routes through the OAuth
  proxy. App traffic uses the separate internal ClusterIP targeting Triton's
  h2c port 9000; no external Route is used.
- The successful Python probe uses the wrapper's runfiles Python plus its
  bundled pip site-packages. The initial bare Python command lacked those
  imports and is not the valid reproduction.
- A temporary port-forward also returned `ServerLive=true`; Triton's model
  repository contained READY FoundationPose and RF-DETR models. The earlier
  short-name metadata probe used an incorrect model name.

### Latest verified result: internal gRPC smoke (2026-10-14)

- The generated Route is not needed for calls between workloads in the same
  cluster. Its gRPC ALPN failure is separate from the internal Triton path.
- The RHOAI predictor Service on port 8443 goes through the OAuth proxy and
  does not negotiate HTTP/2, even when reached inside the cluster. Triton
  itself listens for gRPC on port 9000 using h2c.
- The existing wrapper pod reached port 9000 through a temporary ClusterIP
  Service and completed an HTTP/2 handshake. The temporary Service was
  deleted afterward.
- A synthetic RF-DETR inference over gRPC passed through a temporary
  port-forward to port 9000. Metadata matched the expected input/output
  tensors. Zero-image outputs were `boxes` FP32 `[0,4]`, `scores` FP32 `[0]`,
  `masks` BOOL `[0,224,224]`, and `visibility` FP32 `[0]`.
- The inference call came from a temporary local Triton client through
  `oc port-forward`; it was not issued by the wrapper. From the wrapper pod,
  only the internal TCP/HTTP2 transport was verified. No stable gRPC Service
  or wrapper change is in place.
- Port 9000 is unencrypted h2c and bypasses the OAuth proxy and its token
  authentication. Do not point the application at it until a stable internal
  endpoint and the required access-control and encryption design are settled.
- The temporary client, port-forward, and Service were removed. No serving or
  application deployment was changed by this smoke.

- The migration scope is Triton only. The existing wrapper, pose estimator,
  simulation, and demo deployment remain unchanged.
- RHOAI/KServe owns the dashboard-managed REST `InferenceService`, serving pod,
  internal Service, and Model access Route. A separate gRPC candidate will use
  its own model PVC and ServingRuntime for protocol testing.
- The project namespace is visible in RHOAI with
  `opendatahub.io/dashboard=true`, and the requester has a namespace Admin
  RoleBinding. The user confirmed a Triton
  runtime is available. The active dashboard-managed model deployment uses a
  REST-configured runtime. The user plans to create a separate gRPC runtime for
  protocol testing; do not remove the working REST deployment before that test
  succeeds.
- The user created one dashboard-managed deployment using
  `pvc://triton-model-repository-candidate/snapshot`. KServe reports the
  `InferenceService` Ready, Model access created a Route, and
  `security.opendatahub.io/enable-auth=true`; an OAuth proxy sidecar is present.
  Triton logs show RF-DETR and FoundationPose loaded and READY on an A10G. The
  InferenceService requests one GPU, and the current pod has an A10G node
  selector and is scheduled on an A10G.
- The previous CLI-created actual-model candidate showed `Unknown Serving
  Runtime` and has been deleted, along with the synthetic CIFAR-10 smoke
  deployment and both candidate-only runtimes. The model PVC remains Bound.
- A separate 2Gi RWO PVC, `triton-model-repository-grpc-candidate`, was created
  for the parallel gRPC candidate. The 8 files (576,080,733 bytes) at
  `/snapshot` were copied from the existing PVC using a temporary helper on the
  source pod's node; all SHA-256 checksums matched. The original REST PVC and
  model contents were not changed. Use
  `pvc://triton-model-repository-grpc-candidate/snapshot` for the new gRPC
  deployment. Both PVCs are Bound and the helper pod was deleted.
- A disposable in-cluster gRPC client tested the generated Model access Route
  with no token, an invalid token, and the generated service-account token,
  with TLS verification enabled. All three requests failed during ALPN/HTTP2
  negotiation (`UNAVAILABLE`) before an auth or Triton response. This gRPC
  attempt did not test token rejection/acceptance or send inference. The test
  pod was deleted; do not connect the application wrapper yet. See the newer
  REST Route result below.
- Follow-up read-only checks confirmed the Route is owned by the
  `InferenceService`, uses re-encrypt termination, and has no per-Route
  certificate. With the active default ingress certificate trusted, the TLS
  handshake verified but negotiated no ALPN protocol for gRPC. OpenShift 4.20
  Route documentation says Routes using the default certificate do not
  negotiate HTTP/2; HTTP/2
  requires a custom, non-wildcard Route certificate. The attempted certificate
  patch in the archived hybrid work was not retained, so do not patch this
  controller-owned Route. A custom certificate could address the router
  frontend only; it does not establish gRPC through the auth proxy.
- **External REST inference is now proven.** Namespace mesh policies deny
  ingress to unmarked predictor pods: the general mesh policy admits only
  mesh-member namespaces, while the ingress-router policy selects pods labeled
  `maistra.io/expose-route=true`. The installed KServe schema supports a
  `labels` map on `InferenceService.spec.predictor`, so that label was set on
  the owning predictor template. This is the durable fix; the generated Route
  was not patched. The first template update caused a rolling update that
  stalled because the old and new predictors competed for the model's RWO PVC.
  Setting `spec.predictor.deploymentStrategy.type: Recreate` let the old pod
  release the PVC; the replacement became Ready with both containers. A
  synthetic RF-DETR REST inference through the external Model access Route then
  returned HTTP 200 with strict TLS verification and a fresh generated bearer
  token. The zero-input response had `boxes` FP32 `[0,4]`, `masks` BOOL
  `[0,224,224]`, `scores` FP32 `[0]`, and `visibility` FP32 `[0]`. The token
  was kept out of output and files. This mesh fix establishes Route ingress
  for REST; it does not explain or resolve the separate gRPC ALPN failures.
  Missing/invalid-token rejection through the external Route and
  application-wrapper integration remain untested.
- A temporary probe using the namespace service CA and the Service's valid
  `.svc` hostname also failed gRPC at ALPN on the OAuth-proxy Service. The
  active proxy upstream is plain HTTP to the runtime's gRPC port. An HTTP/1.1
  REST request through that same internal Service returned `502`, consistent
  with the current runtime selecting its gRPC port. The probe pod and its
  mounted token were removed. These checks do not establish working token
  authentication or inference through either path.
- The target cluster is on the RHOAI 2.25 and OpenShift 4.20 release lines.
  RHOAI 2.25 deployment documentation documents both Triton REST and gRPC
  endpoints with bearer-token examples, but
  the live Route behavior above does not match the documented gRPC path. The
  RHOAI 2.25 IBM Z workaround creates a direct Route to Triton's gRPC port and
  does not demonstrate
  token-authenticated routing through this deployment's OAuth proxy.
- An in-cluster Triton SDK client reached the generated headless Service on
  Triton's target gRPC port. Server readiness, CIFAR-10 model readiness,
  metadata, and inference passed; `OUTPUT__0` was `[1,10]` FP32. The temporary
  client Job was deleted. This proves synthetic CPU serving only. The separate
  CIFAR-10 smoke `InferenceService` and `ServingRuntime` have since been
  deleted.
- The dashboard-managed deployment has one predictor replica and one Triton
  pod. KServe reports Ready, and Triton logs report both actual models READY.
  The current pod inherits `maistra.io/expose-route=true` from the predictor
  template. The model PVC remains intact.
- The cluster reports KServe and ModelController component readiness, but
  aggregate RHOAI readiness is false because of other component conditions. Do
  not silently repair unrelated platform components.
- The native controller and owned pods remain healthy and unchanged. Existing
  native Triton config and model tensor contracts were recorded earlier. Its
  configured local socket discrepancy, artifact identity comparison, and live
  model readiness over native transport remain unresolved.
- The native Triton `/models` mount is an `emptyDir`, not a PVC. A consistent
  snapshot of its current contents (8 files, 576,080,733 bytes) was copied
  directly pod-to-pod onto the new `triton-model-repository-candidate` PVC at
  `/snapshot`; each file's SHA-256 was checked during transfer. No existing
  PVC was read or changed. The temporary helper pod is gone and the new 2Gi PVC
  remains Bound.
- A second KServe candidate consumes `pvc://triton-model-repository-candidate/snapshot`.
  KServe mounted it read-only at `/mnt/models`; RF-DETR and FoundationPose both
  loaded and the `InferenceService` is Ready. Live metadata matches the pinned
  tensor contracts. CUDA providers and FoundationPose's native CUDA rasterizer
  were confirmed on NVIDIA A10G. Synthetic-input inference for both models
  passed over gRPC. FoundationPose used a tiny synthetic OBJ, not a demo fixture.
- GPU placement needs a `g5-gpu=true:NoSchedule` toleration and
  `nvidia.com/gpu.product=NVIDIA-A10G` node selector. The first GPU-requested
  pod landed on a node that advertised allocatable GPU but lacked NVIDIA product
  labels and CUDA devices; that replica was removed. The final pod runs on a
  labeled A10G node. Keep the placement constraints in the GPU runtime config.
- The candidate runs on an NVIDIA A10G and both models executed synthetic
  inference. Memory under concurrent load and real demo-fixture behavior remain
  unproven.
- The native CAS patch is present. The actual wrapper lacks application probes;
  pod Ready does not establish model readiness.
- **E0 remains incomplete; synthetic E1 and the actual-model serving smoke
  passed.** E0 still needs artifact identity against pinned release bundles,
  native socket resolution, the intended wrapper identity/auth path, and model
  update/rollback evidence. Both actual models load and answer synthetic gRPC
  requests on RHOAI. The existing wrapper has not changed; its remote lifecycle
  and the unchanged demo flow remain to be proven.
- Work is on disk and uncommitted. This task has not committed/pushed migration
  changes. `openshift/rhoai/.env` is local, ignored, and mode 600.

## Architectural corrections to retain

The target is RHOAI-managed Triton with the existing inference wrapper left in
its current application deployment. For same-cluster calls, use an internal
Kubernetes Service targeting Triton's gRPC port; the external Model access
Route is not required. The wrapper currently uses a local socket and shared
memory, so it needs a remote gRPC client with shared memory disabled. Direct
port 9000 access bypasses the OAuth proxy and is unencrypted, so establish
service identity/access control and encryption before connecting the live
wrapper. The gRPC predictor now has an Istio sidecar, but direct in-mesh
requests still return gRPC `UNIMPLEMENTED`; verify the RHOAI external-auth and
HTTP/2 routing behavior before treating mesh mTLS as proven. The wrapper also
writes model files locally and sends Triton repository load/unload RPCs; resolve
that lifecycle against the PVC-backed deployment before switching it. Keep pose
estimation, simulation, and all other demo components fixed.

The pose-estimator-to-wrapper channel and metadata stay unchanged. The in-cluster
wrapper-to-RHOAI-Triton identity/auth path and synthetic inference now pass.
Carry over the existing CAS adaptation. The remaining work is building and
rolling out the controller change, validating the unchanged demo, and rehearsing
native rollback. Shared asset sources mean a candidate endpoint alone does not
isolate model-version changes.

## Next actions

1. Exercise the unchanged pose-estimator/demo flow with a real capture and
   confirm both RF-DETR and FoundationPose calls reach RHOAI.
2. Make the live wrapper change durable through a narrowly scoped delivery path
   that does not re-render unrelated ChartAssignments; verify how the current
   controller handles this StatefulSet drift before changing its image.
3. Rehearse rollback to native Triton and verify demo recovery. Keep the REST
   deployment and existing PVCs intact through acceptance.
4. Complete E0: resolve the native local socket discrepancy and privately
   compare the PVC snapshot/image with pinned release artifact identities.
   Separately retain the REST Route's missing/invalid-token checks and
   FoundationPose REST mapping as fallback-path evidence.

## Local evidence and checks

Public pinned source extracts are under `/private/tmp/intrinsic-rhoai-source/`
and `/private/tmp/intrinsic-rhoai-review/`. The latter contains a mode-600 private
live inventory. Keep it outside Git and do not print identities/endpoints or raw
configuration. Temporary evidence may need to be recreated in another workspace.

```sh
set -a
source openshift/rhoai/.env
set +a
python3 openshift/rhoai/preflight.py
python3 -m unittest discover -s openshift/rhoai -p 'test_*.py' -v
ruff check openshift/rhoai
ruff format --check openshift/rhoai
git diff --check
python3 openshift/validate_source_manifest.py
```

Preflight reports only bounded discovery. `phase_0_complete` and
`candidate_deployment_ready` stay false with a required-evidence list. All code
and tracked documentation must continue following the Red Hat AI skill and
generic-placeholder policy. Never stage the local `.env` or private inventory.
