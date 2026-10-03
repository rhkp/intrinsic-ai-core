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

Status: applied to the owned Core copy. Resource PodSpecs add the exact
`g5-gpu=true:NoSchedule` toleration only when at least one rendered container
requests or limits an allowed GPU resource; non-GPU resources do not receive
it. This reflects the taint observed on all five dev01 GPU nodes and avoids a
broad `Exists` toleration.

Validation: a one-GPU pod with the same exact toleration passed the restricted
admission, scheduling, public image pull, and `/dev/nvidia0` visibility check in
`arhkp-intrinsic`; cleanup was verified. This validates cluster placement and
device injection only. The adapted renderer package build passed as recorded
above; no Intrinsic GPU workload has been applied.


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
privileged containers, or fixed `runAsUser`/`runAsGroup` fields. K3s VirtualServices,
the K3s artifact importer, and its static local PV were removed. The two
application-level gRPC paths that depended on the removed Istio gateway now use
the corresponding in-project simulation, skill-registry, and workcell Services.
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
Intrinsic application RPC; no Core workload has been applied yet. The repeatable
smoke is [`../grpc_gateway_smoke.py`](../grpc_gateway_smoke.py).

The pinned Bazel graph packages the workcell-cluster-service binary, asset
deployment service, and resource/skill templates together in
`//intrinsic/kubernetes/workcell_spec:workcell-spec-service-image.tar`. That is
the only upstream application image identified so far whose source must be
rebuilt for this routing change; the remaining mirrored application images
stay pinned upstream unless their own compatibility tests require changes.

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
the updated source, rolled out by immutable digest, and verified Ready. No
Intrinsic workload has been applied. The dedicated gateway and project routing
ConfigMap are live, but no application `VirtualService` has been deployed, so
the generic gRPC gateway path now passes a five-call smoke, but no Intrinsic
application RPC has been tested. Remaining gates are to render and review
dynamic charts, deploy a minimal Core slice, smoke a real Core gRPC method
through the dedicated Service, then test resource and skill lifecycle plus
cleanup.
