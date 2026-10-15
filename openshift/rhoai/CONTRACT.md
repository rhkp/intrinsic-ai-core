# Pinned inference contract and model inventory

This is phase-0 source evidence for release `20260922.0`, not a claim that the
live images, artifacts, or serving platform match it. Read-only cluster access
has been restored and bounded platform discovery completed. Exact artifact and
model contracts still require the live evidence gates below.

## Caller-facing behavior to preserve

The [Intrinsic servicer](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_inference/assets/inference_service/inference_service.py)
implements these OIP gRPC methods: `ServerLive`, `ServerReady`, `ServerMetadata`,
`ModelReady`, `ModelMetadata`, and `ModelInfer`. It also implements Intrinsic
ServiceState and rejects inference calls while disabled. Its server metadata
clears Triton's advertised extensions. The remote adapter must preserve that
boundary, including resource discovery and Enable/Disable/error behavior. It
must not forward unrestricted repository-administration/shared-memory methods.

The [runner](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_inference/core/inference_runner.py)
already has a non-shared-memory path that forwards the protobuf request. This
makes a network boundary plausible. It does not prove remote lifecycle support:
the same runner starts a model controller and reconciliation loop. Model
catalog/readiness behavior must be replaced or adapted explicitly. Preserve
model names and versions, request IDs, tensor metadata, raw contents, outputs,
and gRPC errors. Propagate bounded deadlines and cancellation across the
network. The retained wrapper will call RHOAI Triton remotely, so shared memory
must be disabled and model readiness/catalog behavior must account for the fact
that its current controller and model files remain local.

The [actual pose-estimator connection code](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_perception/intrinsic/perception/service/ioc_pose_estimator/service/ioc_pose_estimator_service.py)
uses an insecure channel and static metadata from the resolved inference
dependency. It does not implement TLS or rotating bearer credentials there.
Changing an endpoint string cannot supply those features. Existing resource
resolution and gateway routing must be traced before selecting a direct binding,
client change, or transport bridge. The latter can be needed for either candidate.

Preserve the wrapper's Enable/Disable behavior without giving callers
Kubernetes administration. Define how that state maps to remote Triton; it must
not silently unload shared RHOAI models unless the supported repository
lifecycle explicitly assigns that ownership. A backend outage must surface
within a bounded deadline. Do not retry the application or a robot action to
hide an inference failure.

## Actual model packaging found in OMTS source

| Asset | Source evidence | Package / execution implications |
| --- | --- | --- |
| RF-DETR segmentation | [OMTS BUILD](https://github.com/intrinsic-ai/intrinsic-omts/blob/20260922.0/BUILD) defines `rfdetr_mlmodel`; [MODULE.bazel](https://github.com/intrinsic-ai/intrinsic-omts/blob/20260922.0/MODULE.bazel) pins `segmentation.tar.gz` with a SHA-256. The [caller's bundled config](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_perception/intrinsic/perception/service/ioc_pose_estimator/model_files/segmentation/config.pbtxt) supplies its expected tensors | The asset packages `1/segmentation.onnx` and a configuration from that release bundle. Caller-side expectations are now known, but must be compared with the actual approved bundle/live metadata and execution provider. A bundled client config alone does not prove the installed backend matches it. |
| FoundationPose | [Model package definition](https://github.com/intrinsic-ai/intrinsic-omts/blob/20260922.0/src/foundationpose/BUILD), [Triton config](https://github.com/intrinsic-ai/intrinsic-omts/blob/20260922.0/src/foundationpose/config.pbtxt), and [Python backend](https://github.com/intrinsic-ai/intrinsic-omts/blob/20260922.0/third_party/foundationpose/model.py) | This is a Triton **Python backend**, not just an ONNX model. The package contains `1/model.py`, `1/foundationpose_refine.onnx`, `1/foundationpose_score.onnx`, and `env.tar.gz` at the model root. The backend creates ONNX Runtime refine/score sessions internally and imports a compiled CUDA renderer. |

FoundationPose's [environment build description](https://github.com/intrinsic-ai/intrinsic-omts/blob/20260922.0/src/foundationpose/README.md)
specifies Python 3.12 with packaged Python dependencies and a compiled native
CUDA extension. Its model configuration points `EXECUTION_ENV_PATH` at the
model-local environment archive and declares a GPU instance. Preserve the
archive layout and prove Python-backend stub/ABI, CUDA, native-library, and
ONNX Runtime provider compatibility with the chosen RHOAI runtime image.

The source backend requests CUDA and CPU execution providers for its ONNX
sessions. Therefore a GPU resource allocation, Python model readiness, or an
ONNX inference response alone does not prove that both sessions executed on
GPU. Validate provider placement and GPU execution during a real candidate
request, including the native renderer. Do not assume TensorRT engines exist:
the inspected FoundationPose package contains ONNX files; inspect the actual
RF-DETR/runtime artifacts before introducing TensorRT-specific requirements.

## FoundationPose tensor contract from its pinned configuration

Dimensions below are configured per request. `-1` denotes a variable dimension.
These are model configuration facts; compare them with live `ModelMetadata`
and the real pose-estimator requests before adapter implementation.

| Direction | Name | Data type | Dimensions | Optional |
| --- | --- | --- | --- | --- |
| Input | `MASK` | UINT8 | `[-1, -1, -1]` | No |
| Input | `RGB` | UINT8 | `[-1, -1, 3]` | No |
| Input | `DEPTH` | FP32 | `[-1, -1]` | No |
| Input | `CAM_K` | FP32 | `[3, 3]` | No |
| Input | `CAD_MODEL_BYTES` | UINT8 | `[-1]` | No |
| Input | `NUM_ITERATIONS` | INT32 | `[1]` | Yes |
| Input | `BATCH_SIZE` | INT32 | `[1]` | Yes |
| Output | `ROTATION` | FP32 | `[-1, 3, 3]` | — |
| Output | `TRANSLATION` | FP32 | `[-1, 3]` | — |
| Output | `CONFIDENCE` | FP32 | `[-1, 1]` | — |

`CAD_MODEL_BYTES` is an array of UINT8 in this contract, despite its name; it
must not be converted to Triton's BYTES/string datatype. The pose-estimator
configuration's `batch_size` is not proof that Triton dynamic batching is
enabled. Preserve optional control tensors and verify actual shapes and
batch behavior before enabling serving optimizations.

## RF-DETR caller expectations and request encoding

The pinned caller configuration declares an ONNX Runtime backend and GPU model
instance with these tensors. This is the **caller-side expected contract**;
validate it against the released asset configuration and live model metadata.

| Direction | Name | Data type | Dimensions |
| --- | --- | --- | --- |
| Input | `input` | UINT8 | `[1, 3, -1, -1]` |
| Input | `thresholds` | FP32 | `[1, 2]` |
| Output | `boxes` | FP32 | `[-1, 4]` |
| Output | `scores` | FP32 | `[-1]` |
| Output | `masks` | BOOL | `[-1, -1, -1]` |
| Output | `visibility` | FP32 | `[-1]` |

The pose-estimator service explicitly converts the image from HWC to NCHW and
adds a leading dimension before invoking the segmentation helper. Preserve that
layout; do not infer HWC wire tensors from the image's original shape. The
thresholds are confidence and visibility in that order.

Both [model helpers](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_perception/intrinsic/perception/service/ioc_pose_estimator/service/segmentation_model.py)
construct tensor metadata with actual NumPy shapes/dtypes and contiguous
`raw_input_contents`; requested output names come from their loaded config.
The constructors omit an explicit model version. Preserve the intended default
model selection and distinguish **Intrinsic asset version, Triton numeric model
version, and KServe deployment identity**. They are not interchangeable labels.

Validate both request and response sizes through every hop: the mask response
can be large. Channel settings in the inspected caller/wrapper remove message
size limits; a new proxy's default limits may behave differently. Choose bounded
budgets from measured fixtures rather than preserving unlimited allocations.

The readiness helpers contain an outer polling limit but issue RPCs without
individual deadlines, and inference proceeds after calling the helper without
checking its false return. A remote-safe path must enforce both bounds and
readiness failure, with tests for stalled RPCs and cancellation. See the
[review experiment matrix](REVIEW.md) for the required fixes and evidence.

## Model materialization and lifecycle boundary

The [asset manager](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_inference/assets/inference_service/model_assets_manager.py)
lists installed MLModel data assets, derives logical model name/version from
asset identity, and downloads referenced model files through DataAssets/CAS.
The [Triton controller](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_inference/core/model_controller_triton.py)
writes the backend configuration and sends load/unload requests. Changing a
gRPC URL cannot make those local files appear in a remote serving pod.

The current wrapper downloads models and writes configurations locally before
asking its local Triton to load/unload them. RHOAI Triton cannot read those local
files. The migration therefore needs an explicit model handoff: an approved
shared repository with compatible dynamic load/unload, or immutable RHOAI-owned
model deployments plus a small wrapper lifecycle adaptation. Verify exact
model versions and bundle hashes, restart/reload behavior, and rollback before
switching the wrapper endpoint. Keep the wrapper in its current application
pod; only Triton moves. The native controller also patches the wrapper's CAS
address at startup, so retain that adaptation as currently deployed.

Reconciliation is asynchronous after server readiness. Its base implementation
avoids retrying unchanged FAILED models until reset or configuration change.
Test transient-load recovery and a Triton-only restart while the wrapper still
holds its in-memory model/shared-memory state. Do not equate server readiness,
wrapper Enabled, or an open socket with required-model readiness or recovery.

If a repository export is selected, it must produce a complete immutable Triton
repository with file hashes, logical asset/model versions, backend requirements,
and all dependencies. Start from approved immutable assets; use the live
`/models` repository for comparison. If a live snapshot is used, detect
concurrent reconciliation/file changes and verify the entire snapshot before
publication. Do not export partial files or request data. The existing wrapper
must fail clearly if its requested asset/model version does not match the
RHOAI-served repository. Do not claim current dynamic install/remove behavior
is preserved until that lifecycle has explicit acceptance evidence.

## Open evidence after the actual-model smoke

The synthetic CIFAR-10 RHOAI Triton service passes in-cluster gRPC readiness,
metadata, and inference. A second KServe candidate mounts the native `/models`
`emptyDir` snapshot from the new `triton-model-repository-candidate` PVC. All 8
files (576,080,733 bytes) were transferred pod-to-pod and SHA-256 checked per
file. KServe consumption, live RF-DETR/FoundationPose metadata, model readiness,
and synthetic-input inference over gRPC passed. Logs confirm CUDA providers
and FoundationPose's native CUDA rasterizer on NVIDIA A10G. This does not yet
prove identity against pinned release bundles, real demo-fixture behavior,
wrapper compatibility, or update/rollback behavior.

1. Compare the private native repository snapshot and live image identities
   with the pinned release bundles and OIP metadata. Preserve the private
   inventory outside Git.
2. Validate the staged snapshot against pinned immutable RF-DETR and
   FoundationPose release bundles. Do not mutate or share the native live
   repository.
3. Define immutable snapshot updates/rollback and actual-model version
   ownership. The candidate currently uses Triton `MODE_NONE`; do not claim the
   wrapper's dynamic load/unload semantics are preserved.
4. Resolve the native socket discrepancy and prove the intended wrapper-to-RHOAI
   gRPC identity/auth path, deadlines, and request-size limits.
5. Change only the existing inference wrapper's Triton client configuration
   after those gates pass, then validate the unchanged demo and native rollback.

A pose fixture is not needed to prove synthetic server startup/metadata. Use the
existing demo for end-to-end caller validation after actual model serving is
proven. Do not infer a complete root cause from partial logs or the directory
name.
