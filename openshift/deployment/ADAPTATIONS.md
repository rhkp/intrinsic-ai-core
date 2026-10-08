# OpenShift adaptations

## Registry image publisher — first implementation slice

Status: applied to the owned copy at `intrinsic-core/`; it is not yet wired into a complete OpenShift deployment.

The pinned Core command unconditionally selected the K3s containerd socket for publishing generated images. The copied source now has an opt-in publisher that pushes through a loopback-only registry port-forward, uses the cluster's registry Service CA for TLS verification, and reads upload credentials through the caller's Docker config. The CLI requires an explicit in-cluster registry reference and a preselected release candidate name on this path. Workload image references keep the internal registry service name for pod pulls.

The reproducible patch is [`../../patches/intrinsic-openshift-registry.patch`](../../patches/intrinsic-openshift-registry.patch). `ADAPTATIONS.json` lists changed files and hashes.

Validation so far: the patch applies cleanly to the pinned copy, changed Go files pass `gofmt`, and the generic dev01 registry push/pull smoke passed. The focused publisher tests were previously run in a temporary test harness; they have not been rerun from this copied tree. The complete Bazel build and an Intrinsic image publish/deploy through the copied command have not been run.

## Image-pull identity and credential removal

Status: applied to the owned Core copy. Resource and skill images reject inline registry usernames/passwords; resource and init-container PodSpecs plus skill templates use the verified `intrinsic-runtime` ServiceAccount with token automount disabled. Generated `.dockercfg` Secrets, chart values containing base64 credentials, and `imagePullSecrets` references were removed. Resource and skill chart namespaces now target the pilot project `arhkp-intrinsic`. The matching ServiceAccount and namespaced `system:image-puller` RoleBinding are recorded in `../manifests/image-pull-access.yaml`.

Validation: `gofmt` and the Helm template check (`python3 openshift/deployment/validate_image_pull_templates.py`) pass. The check confirms both rendered workload templates select the ServiceAccount, disable token automount, and create no registry Secret. The complete Bazel build and live Intrinsic deployment remain unverified; see [`../SECRETS_ADAPTATION.md`](../SECRETS_ADAPTATION.md).

## OpenShift application Secret path

The copied upstream Core workload templates contain no app `Secret` objects or
Secret references, so no real application secret keys are known yet. Added a
small helper that takes future values from ignored `openshift/.env` and streams
an `Opaque` Secret to `oc apply`; the committed `.env.sample` contains only an
obvious smoke placeholder. A restricted dev01 pod consumed a synthetic key
through `secretKeyRef` without printing it. The pod, Secret, and temporary
`.env` were removed. This verifies the OpenShift secret mechanism, not the
application's actual runtime credentials; key mapping remains open until the
rendered Core/OMTS workload inventory identifies consumers. See
[`../SECRETS_ADAPTATION.md`](../SECRETS_ADAPTATION.md).

The adapted resource/skill renderer package also compiled successfully on
2026-10-02 in a clean worktree at pinned Core commit
`523f2e03ddf3cb46a19b15be5e81586ae2f538c2`, using target
`//intrinsic/assets/deploy:render`. Bazel completed 2,217 actions in 1,105.56
seconds. This is a package build only; it does not validate the full deployment
or a live Intrinsic workload.

The namespace-scoped ChartAssignment and ResourceSet controller pilot passed on
dev01 on 2026-10-02. Both CRDs are Namespaced; the controller is limited to
`arhkp-intrinsic`; a ConfigMap-only ChartAssignment reached Ready and cleaned
up through its finalizer. The run details and retained pilot resources are in
[`chartassignment-controller/deploy/README.md`](chartassignment-controller/deploy/README.md).

Still open: resolve the internal gRPC Gateway design; reproduce the chart
render from our patched source build; finish storage and asset-lifecycle review;
validate the runtime-generated resource/skill charts; then apply a minimal Core
slice on dev01. The Core service image is published and digest-locked, and the
updated project-scoped controller is built and running. Those checks and the
offline chart-policy pass do not establish that the full Intrinsic workload is
ready to deploy.

## GPU-node placement for OpenShift resources

Status: applied in the OpenShift ChartAssignment adapter after consulting the
pinned Core renderer. The adapter adds the exact
`g5-gpu=true:NoSchedule` toleration to workloads that request or limit
`nvidia.com/gpu`, and to ICON/UR workloads whose required pod affinity pins them
to Gazebo/ICON on the GPU node. Other workloads are unchanged. The earlier Core
renderer patch was not the live rendering boundary; Asset Deployment is served
inside `workcell-cluster-service`, while the controller adapter runs on the
rendered ChartAssignment before apply.

Validation: controller tests passed for GPU requests, ICON/UR co-location,
idempotence, and non-GPU workloads. Controller image
`sha256:8bf9b5a5d8ed20ba1fbfb438e79e2f7a5ad6c73bf26408b79db11ea229894ca9` is
deployed and Ready. On dev01, the ChartAssignments are Ready; Gazebo is 2/2,
ICON 3/3, UR 2/2, and inference 3/3 Running, with inference on an A10G. After
scene reset, ICON reconnected to UR and returned to `kMotionEnabled`. The
one-cycle app reached planning and stopped on the gripper/stock collision also
seen on AWS; the full pick has not completed.


## Quay image mirror and digest lock

Status: the selected release images are mirrored to `quay.io/rhkp/intrinsic` and
recorded by immutable digest in [`image-lock.json`](../../image-lock.json). The
lock covers the 22 amd64 image tarballs in the verified Core release bundle,
plus the separately referenced Zenoh daemon and Jupyter sidecar. The copied
Zenoh and code-execution templates now consume their image references from Helm
values; [`intrinsic-openshift-quay-images.patch`](../../../patches/intrinsic-openshift-quay-images.patch)
records those source edits. `render_quay_image_values.py` generates the values
overlay from the lock.

Validation: the base and app Helm charts render with every image reference
resolved to a digest in the lock. The first dev01 restricted-SCC HTTP gateway
pod could not pull anonymously while the Quay repository was private. After the
repositories were made public, the digest-pinned HTTP gateway, Zenoh daemon,
and Jupyter sidecar images each pulled and started under the restricted SCC
with a project-assigned non-root UID; cleanup was verified. These are basic
startup checks for those images only. The full Intrinsic workload has not been
deployed. Rebuild
only images that fail a later startup/security test for an image-level reason.

## Offline OpenShift chart render — first complete chart-policy pass

Added `cmd/render-openshift-chart`, a no-apply command that accepts an inline
ChartAssignment, uses the controller's production render/adaptation path, and
emits the validated YAML. It refuses remote chart retrieval. Unit tests cover
both a successful namespace-scoped render and rejection of remote charts. Added
[`../validate_rendered_manifests.py`](../validate_rendered_manifests.py) as a
second pre-apply gate for project scope, allowed kinds, stale DNS, host/root
fields, duplicate objects, and digest-lock membership.

On 2026-10-02, the pinned `20260922.0` base and app chart archives were rendered
locally with the checked-in Quay digest overlay. The archive inputs were kept in
`/private/tmp`; they are not committed deployment artifacts. Since those
prebuilt archives predate the checked-in Zenoh/Jupyter template override, the
two corresponding chart image lines were adapted in the temporary archives to
match the source patch before rendering. The owned-source build must still
produce these exact chart values from its patched source before live use.

The policy pass emitted 76 base-chart and 11 app-chart objects, all in
`arhkp-intrinsic`. Across the charts, all 23 rendered container references
matched the Quay digest lock. Three upstream ClusterRoles and bindings became
project Roles and RoleBindings; the render contained no cluster-scoped objects,
Secret objects, stale upstream namespace/DNS references, hostPath, hostPort,
privileged containers, or fixed `runAsUser`/`runAsGroup` fields. K3s VirtualServices and the static local PV were removed. The upstream
`artifacts-deployment` ArtifactServiceApi and headless Service are retained
because OMTS uses them to upload large non-container data bundles. The pinned
released server always creates a containerd client, even when its local registry
listener is disabled, so the OpenShift adapter replaces the upstream node-local
K3s socket with a restricted, pod-local containerd sidecar. It removes the
K3s-only local registry argument, host port, and hostPath; container images use
the separate OpenShift registry publisher. The two application-level gRPC paths
that depended on the removed Istio gateway now use the corresponding in-project
simulation, skill-registry, and workcell Services.
The code-execution NetworkPolicy retains the application peer rules and drops
the obsolete ingress-gateway peer without creating an allow-all rule.

Five PVCs request about 22.2 GiB combined on `gp3-csi` with `ReadWriteOnce`;
four preserve upstream storage requests and one is the OpenShift data-store
PVC. A fresh
read-only dev01 query confirmed `gp3-csi` uses the AWS EBS CSI provisioner,
WaitForFirstConsumer binding, and allows expansion. This confirms render and
StorageClass availability only: the data-store capacity, original Zenoh
RWX-to-RWO behavior, retention/backup, and live mount behavior still need
workload-specific review. The rendered inventory has 20 Deployments and 23
containers; only two containers declare CPU/memory requests or limits, and none
requests a GPU. Resource sizing and GPU placement for generated application
charts and any RHOAI inference service remain open. No Intrinsic objects were
applied. The installed
controller has a project-scoped Role covering the Core chart kinds; live
`can-i` checks confirmed expected workload/PVC/ServiceMonitor creation and
denied Secrets, Namespaces, ClusterRoles, and ClusterRoleBindings. The smoke
ChartAssignment itself used only a ConfigMap. Runtime-generated resource/skill
charts and the full application lifecycle remain unvalidated.

## OpenShift gRPC routing adaptation — source and image build pass

The generated skill/resource charts use Istio `VirtualService` rules to route
gRPC by RPC path and `x-resource-instance-name`; replacing those with a plain
Service address would break resource dispatch. The owned controller source now
retains each project-scoped `VirtualService`, attaches it to the dedicated
OpenShift Service Mesh Gateway, limits visibility to the pilot project and
Gateway namespace, and rejects destinations outside the project. It rewrites
the upstream gateway address to the dedicated internal Service and retargets
NetworkPolicy peers to that Service's actual endpoint selector. No external
OpenShift Route carries the gRPC traffic; the design uses plaintext HTTP/2
(`h2c`) to a project `ClusterIP` Service on port 80. The project is now enrolled
with a Ready `ServiceMeshMember` referencing `istio-system/data-science-smcp`;
the reproducible manifest is
[`manifests/servicemesh-member.yaml`](manifests/servicemesh-member.yaml).

The Core resource renderer now requires `INTRINSIC_INGRESS_ADDRESS` when it
generates runtime contexts and points generated simulation references at the
single OpenShift project. The controller injects that value into rendered pod
containers. `openshift/configure_routing.py` reads the two non-secret routing
values from ignored `openshift/.env`, rejects the shared ingress Service,
verifies the dedicated ClusterIP Service, ready endpoints, exact Gateway
selector/listener, and project mesh membership, then applies the routing
ConfigMap consumed by the controller. The controller's ConfigMap import is
optional; routing-dependent ChartAssignments fail closed until all values are
configured. The helper recognizes both member-roll membership and a Ready,
project-scoped `ServiceMeshMember`.
The checked-in routing sample names a new internal `ClusterIP` Service in the
pilot project. Rather than select the shared ingress pods, the dedicated
gateway manifest uses the supported OpenShift Service Mesh 2.6 gateway
injection template to create its own Envoy Deployment, ServiceAccount, and
Istio Gateway in `arhkp-intrinsic`. It does not edit the shared
`ServiceMeshControlPlane`, create an OpenShift Route, or request a LoadBalancer.
The manifest is applied; its proxy and internal Service endpoint are Ready.
The local ignored `.env` contains only routing configuration, and the helper
validated the dedicated Service, selector, exact internal DNS host, and HTTP/2
listener. The project routing ConfigMap is applied and the controller has been
restarted to load it. A temporary Fortio gRPC smoke then passed five h2c calls
through the Gateway and header-matched VirtualService to a sidecar-injected
backend. The sidecar was required by dev01's existing mesh-wide `STRICT`
mTLS policy; no mesh policy was changed. All temporary smoke resources were
removed and verified absent. This proves generic gateway routing, not a live
Intrinsic application RPC. The repeatable smoke is
[`../grpc_gateway_smoke.py`](../grpc_gateway_smoke.py).

The pinned Bazel graph packages the Workcell service binary, asset deployment
service, and resource/skill templates together in
`//intrinsic/kubernetes/workcell_spec:workcell-spec-service-image.tar`. This
Workcell image is rebuilt for OpenShift gRPC routing and namespace-filtered Pod
and ConfigMap informers. The Resource Registry is a separate adapted image for
its namespace-scoped ConfigMap informer. Other mirrored application images
stay pinned upstream unless their compatibility checks require changes.

The updated controller Go tests and offline manifest-validator tests pass. The
modified Core resource-renderer package compiled on the x86 build VM at the
pinned commit (2,217 Bazel actions, 665 seconds); the dynamic asset-deployment
package and full chart CLI also passed (4,051 actions, 397 seconds). The
workcell service image target now builds successfully on x86
(194 Bazel actions, 21 seconds). That build exposed more upstream consumers of
the generated gRPC address, so the owned copy now resolves the validated
ingress address in asset-instance conversion and propagates configuration
errors through the asset-instance APIs and solution conversion. The resulting
Linux/amd64 image archive is about 48 MB and passed a local secret-marker
scan. It is published to the existing Quay image repository under a new
checksum-based tag; the immutable registry digest and upstream digest are
both recorded in [`../image-lock.json`](../image-lock.json). An anonymous
digest-pinned pull succeeded. The project-scoped controller was rebuilt from
the updated source, rolled out by immutable digest, and verified Ready.

**Runtime update (2026-10-07):** Generated OMTS resource and skill workloads
are applied in dev01. During recovery, the simulator restarted while ICON and
UR retained channels to the old simulator. The pose-estimator Pod was not
crash-looping; its RPC failed because the project Zenoh router did not contain
the requested capture result. UR reconnected to the new simulator endpoint,
but ICON's main control loop had already stopped. Restarting ICON on a
different worker produced `ECONNREFUSED` for
`/tmp/intrinsic_icon/ur_module.sock`: the socket file was on the shared
CephFS-backed volume, while its server process lived on the simulator's node.
After scheduling `rs-icon` with required pod affinity to
`app=rs-gazebo-simulator`, ICON connected, received 48 descriptors, and the
Gazebo HWM reported active and enabled. A later ICON restart could not reconnect
to `ur_module.sock` when `rs-ur-module` landed on another node, despite the
shared CephFS volume. The controller now also schedules `rs-ur-module` with
required pod affinity to `app=rs-icon`, keeping ICON, both hardware modules,
and Gazebo on one node. The headless, undeclared-port simulation Service, mesh
sidecars, and STRICT mTLS policy remain unchanged. A successful end-to-end
OMTS cycle and the in-cluster noVNC viewer are still unverified.

**Latest OMTS attempt:** ICON completed the planned arm trajectory, and the
pose-estimator service successfully loaded the camera result, segmented one
object, ran FoundationPose, and published its visualizations. The run then
failed in `move_to_contact`: ICON entered approach and stabilize, but the
skill's 2-second settle window expired. The skill had no streamed `LogItem`
for ICON action topics `/icon/icon/output_streams/action_1` or
`/icon/icon/output_streams/action_2`, so it could not report the sensed force.
The pinned upstream `move_to_contact.py` constructs `pubsub.PubSub()` with no
configuration. Its native binding defaults to
`zenoh-router.app-intrinsic-base.svc.cluster.local:7447`, while that namespace
does not exist in this OpenShift deployment. The upstream skill chart places
skills outside `app-intrinsic-base` and allows them to reach the base router;
the OpenShift pilot instead co-locates services in `arhkp-intrinsic`. The
controller's `ZENOH_CONFIG_OVERRIDE` was ineffective because this skill uses
Intrinsic's native PubSub binding rather than ROS `rmw_zenoh_cpp`. This explains
the absent action-stream subscription; it is independent of the 2-second
settling threshold and provides no evidence that permissive mTLS is needed.

A standalone diagnostic using the binding's explicit JSON config connected to
the project Zenoh router and registered the exact upstream ICON action topics.
A derived image is now built from the pinned upstream digest; its only source
change passes `INTRINSIC_ZENOH_ROUTER_ENDPOINT` to the native PubSub constructor.
The controller is being rebuilt to pin that image by digest and inject the
endpoint. End-to-end skill readiness and a complete OMTS cycle remain pending.
The pose-estimator Pod was Ready with zero restarts during this contact/stream
diagnostic.

## Workcell Role/RoleBinding escalation boundary

The base ChartAssignment's `workcell-cluster-service` Role grants
project-scoped `pods/delete` and ChartAssignment create/delete permissions to
the Workcell service account. Kubernetes correctly rejects the ChartAssignment
controller's attempt to grant permissions it does not itself hold. Keep the
controller Role unchanged. The OpenShift adapter continues validating these
rules against its explicit allowlist, then omits only this Role and its
RoleBinding from the Synk ResourceSet. The exact upstream project permissions
are provisioned from
[`manifests/workcell-cluster-service-rbac.yaml`](manifests/workcell-cluster-service-rbac.yaml)
as a separate project-scoped prerequisite. This leaves the Workcell service
with its required namespaced permissions while preserving the controller's
least-privilege boundary.

Validation: all ChartAssignment controller Go tests pass, including a check
that rejects unapproved Workcell verbs. The static manifest passed dev01's
server-side dry run and was applied. The controller was rebuilt and rolled out
with an immutable image digest; the fresh reconciliation moved
`intrinsic-base` to Ready and its latest ResourceSet to Settled. The Workcell
Role and RoleBinding were verified to match the project-scoped manifest and
have no ResourceSet owner references. The controller Role still lacks
`pods/delete` and ChartAssignment create/delete. All 22 Deployments are now
Ready. The registry, Workcell service, `world-updater`, `scene-object-import`,
and the repaired `code-execution` Jupyter sidecar are healthy.

## Durable ChartAssignment image and informer adaptation

The first namespace-scoped Resource Registry fix was applied directly to its
Deployment. A later `intrinsic-base` reconciliation used the stale inline
upstream chart and restored the old registry image and arguments. The Workcell
service also used cluster-wide Pod and ConfigMap informers despite having only
project-scoped RBAC. The resulting failures cascaded to `world-updater` and
`scene-object-import`.

The controller now applies the OpenShift adapter before Synk writes each
ChartAssignment render. It pins the registry and Workcell service images by
digest and normalizes the registry's
`--configmap_watch_namespace=arhkp-intrinsic` argument to exactly one entry.
The Workcell image uses namespace-filtered Pod and ConfigMap informers and a
namespace-scoped dynamic ChartAssignment client. The source patches and image
provenance are recorded in [`ADAPTATIONS.json`](ADAPTATIONS.json),
[`SOURCE_MANIFEST.json`](SOURCE_MANIFEST.json), and
[`image-lock.json`](../image-lock.json). The controller Role remains unchanged.

Validation: focused controller Go tests pass, including stale-image,
namespace-argument deduplication, and idempotence coverage. The Workcell image
build passed on the pinned Linux/amd64 VM, and Quay allowed an anonymous
digest-pinned pull. The controller BuildConfig completed and the Deployment
rolled out by immutable digest. A fresh base ChartAssignment reconciliation
left `intrinsic-base` Ready with its latest ResourceSet Settled. The registry,
Workcell service, `world-updater`, and `scene-object-import` are Ready 1/1;
their logs show zero current informer `Forbidden` errors and restored gRPC
connections. The Jupyter sidecar fix is recorded below.

## Jupyter writable-home and loopback adaptation

On dev01, the pinned Jupyter image initially failed because its process
resolved its home to `/` while the root filesystem was read-only. The upstream
pod already mounts a bounded 2 GiB `emptyDir` at `/home/defaultuser`. The
ChartAssignment adapter now sets `HOME=/home/defaultuser` and
`JUPYTER_RUNTIME_DIR=/home/defaultuser/.local/share/jupyter/runtime`, and fails
closed unless that path remains a writable mount backed by an `emptyDir`.

The image entrypoint binds the unauthenticated server to `0.0.0.0`. Since
mesh-managed NetworkPolicies can allow mesh traffic on otherwise unlisted
ports, the adapter replaces the image entrypoint with an explicit invocation
that preserves the required port, token-disabled sidecar behavior, XSRF
setting, and notebook directory while binding to `::1`. The code-execution
worker uses `localhost:8888`; live inspection confirmed the only listener on
port 8888 is `::1`, and all three workers created kernels and passed their
startup requests.

Controller BuildConfig build #11 is pinned at
`image-registry.openshift-image-registry.svc:5000/arhkp-intrinsic/chartassignment-controller@sha256:97ac26ba62714f25f96c6ba36d61b424e50493fa6bd5e843796ef5ce7695b0a1`.
Both Core ChartAssignments are Ready with Settled ResourceSets. The deployment
is 22/22 Ready, and a before/after comparison found no changes in the 20 Core
Deployment image references across the two live ChartAssignments.

## Inference model downloads use the project CAS Service

The inference image's compiled `_CAS_ADDRESS` names the upstream
`app-intrinsic-base` Service. On dev01, the model objects are available from
the project `content-addressable-storage` Service, while routing the upstream
hostname to that Service IP resets at the mesh proxy. The controller now adds
a narrowly scoped init container to `rs-inference-service` that copies the
entrypoint module to an `EmptyDir`, changes only the CAS authority to the target
project Service DNS name, and mounts the patched module over the original. The
app image, model bytes, and mesh mTLS policy are unchanged. On dev01, the
patched pod reached Ready, both Triton models loaded, and a pose-estimator RPC
completed segmentation and FoundationPose inference.

## Hand-E simulated gripper joins the project Zenoh mesh

The live `rs-hande-gripper` pod used `rmw_zenoh_cpp` but had no Zenoh endpoint
override and no mesh sidecar. A ROS graph query from Gemini showed only the
Orbbec and Flowstate nodes, with no gripper action server; an in-pod graph query
from Hand-E also warned that it could not connect to a Zenoh router. The
controller now configures Hand-E to connect to the project Zenoh Service and
injects its mesh sidecar, which is required by the project's strict mTLS policy.
Gemini now sees the Hand-E action server. The remaining request failed because
the separate `gripper_cmd_skill` container also had no project Zenoh endpoint;
the controller now identifies that container by its pinned image repository
and injects the same client override. Its live rollout and a successful gripper
command are still pending.

## Zenoh and Conductor paths through Service Mesh

The pinned upstream World manifest waits for the Zenoh router before starting,
and the pinned Workcell chart passes `world.<namespace>.svc.cluster.local:8082`
to its Conductor client. On dev01, the Zenoh router is not a mesh member, so
Envoy's automatic mTLS on port 7447 breaks the raw Zenoh connection. The
OpenShift renderer now excludes only outbound port 7447 from Istio interception
on recognized Zenoh clients. The OpenShift World chart also exposes a stable
`conductor` ClusterIP Service. Workcell now targets that Service instead of the
headless `world` Service: after a World pod replacement, the sidecar's cached
headless endpoint still pointed at the terminated pod, causing `StartSolution`
to time out. These are OpenShift-only render changes; the pinned upstream and
K3S source remain unchanged.

The ICON action-output publisher, capture-images skill, and pose-estimator use
Intrinsic's native PubSub binding, which uses the upstream router hostname and
opens raw Zenoh TCP. The OpenShift pilot has no `app-intrinsic-base` namespace,
and mesh interception breaks direct access to the non-mesh router. The
controller marks only the ICON, capture-images skill group, and pose-estimator
pod templates to exclude outbound port 7447 from Istio. The checked-in
[`manifests/upstream-zenoh-router-alias.yaml`](manifests/upstream-zenoh-router-alias.yaml)
provides the upstream DNS name as an `ExternalName` alias to the existing
project router; it does not create another router or alter upstream/K3S.
Reconcile the existing ChartAssignments to replace those pods after applying
the alias. PVCs and the workspace build cache remain untouched.

## ArtifactService publishing to the OpenShift registry

Pinned upstream ArtifactService already provides a remote OCI registry backend
through `--registry`. OpenShift configures this backend for the project
integrated registry instead of using K3S's node-local containerd registry. Its
`omts-deployer` ServiceAccount can push through `system:image-builder`; the
pod reads the projected token through an OpenShift-only password-file flag,
trusts the injected service CA, and excludes outbound port 5000 from mesh
interception. The service no longer logs its options struct because it contains
the registry password. The client continues uploading over ArtifactService's
gRPC API, and `CheckImage` returns project-registry image references for node
pulls. The separate OpenShift OCI writer adaptation still skips restricted
pod-local overlay unpack. Pinned upstream Core/K3S behavior remains unchanged.
