# Intrinsic Core full OpenShift pilot

**Status:** project setup, restricted-pod and GPU smokes, a namespaced
ChartAssignment controller smoke, an `Opaque` Secret injection smoke, and a
dedicated internal gRPC gateway with routing configuration are verified on
dev01. The pinned, OpenShift-adapted deployment CLI builds for Linux x86-64 and
its registry-publisher test passes; no Intrinsic workloads have been deployed
yet. A five-call synthetic gRPC smoke now passes through the internal gateway
to a sidecar-injected backend under the cluster's existing STRICT mTLS policy;
it does not yet test an Intrinsic service. The 24 required Core chart images are mirrored by digest to
Quay; the HTTP gateway, Zenoh daemon, and Jupyter sidecar passed restricted-SCC
startup smokes. This is
the active full-OpenShift track. See the
[migration plan](../OPENSHIFT_PLAN.md) for architecture, blockers, and acceptance
gates. The [secret adaptation assessment](SECRETS_ADAPTATION.md) records the
private-pull and `.env`-to-Secret smokes, plus the remaining app-specific key
mapping. The AWS VM remains the known-good comparison
environment; its K3s service and workload pods are intentionally stopped to
free resources. Restart K3s only when the VM comparison demo is needed again.

## Verified target snapshot — 2026-10-02

The read-only preflight found OpenShift `4.20.26`, RHOAI `2.25.9`, and KServe in
`Managed` state. Seven ServingRuntime resources were visible; no Triton runtime
was detected. The project `arhkp-intrinsic` was recreated and verified with the
`opendatahub.io/dashboard=true` label so it is discoverable to RHOAI.

GPU inventory showed five allocatable GPUs, four requested, and one estimated
free across the cluster: four A10G devices and one GPU with unspecified product.
These figures are estimates, not a reservation for this project. All five GPU
nodes are Ready and carry `g5-gpu=true:NoSchedule`. A restricted one-GPU smoke
pod using the exact taint toleration scheduled successfully and confirmed
`/dev/nvidia0` visibility; it deleted itself after the test. The project has no
GPU quota or reservation. Visible StorageClasses were
`gp2-csi`, `gp3-csi`, `ocs-storagecluster-ceph-rbd`,
`ocs-storagecluster-cephfs`, and `openshift-storage.noobaa.io`; access modes,
quota, topology, and approved object storage are not yet selected.

An aggregate scheduler-capacity snapshot showed 207 allocatable CPU cores and
811.32 GiB memory across the cluster, with active pod requests of 82.37 cores
and 198.84 GiB. GPU nodes account for 37.50 allocatable cores and 149.48 GiB,
with active requests of 17.57 cores and 31.38 GiB. These residuals are cluster
aggregates only; taints, per-node fragmentation, affinities, pod limits, and
platform reservations can prevent scheduling. They are not a placement or
capacity guarantee.

The inventory and bootstrap used a low-output helper that suppressed raw CLI
responses and did not print the cluster URL, identity, or raw resources. The
active read-only preflight is [preflight.py](preflight.py); run it before each
deployment session because capacity and permissions can change:

```bash
python3 openshift/preflight.py
```

Read-only permission checks showed that the current identity can create and
delete namespaces and can create ClusterRoles, ClusterRoleBindings, CRDs, and
SCCs. This is observed authority, not approval to exercise it broadly. The
upstream installer/controller must be reviewed and constrained to the approved
namespace/resource set before using any cluster-scoped permission.

The initial sanitized preflight found no project quota, LimitRange,
NetworkPolicy, or PVC; the default ServiceAccount has one image-pull-secret
reference. Service Mesh enrollment later created its mesh-managed NetworkPolicies.
The upstream cluster-scoped `ChartAssignment` CRD/controller path is not installed.
Namespaced pilot `ChartAssignment` and `ResourceSet` CRDs plus a project-scoped
controller are installed and smoke-tested with a ConfigMap. Its 13-rule Role is
bound only in this project and covers the reviewed namespaced Core chart kinds;
live authorization checks confirmed it cannot create Secrets, namespaces,
ClusterRoles, or ClusterRoleBindings. The interactive identity itself can
create project-level pods, deployments, jobs, services, PVCs, NetworkPolicies,
Routes, and RoleBindings, and has the cluster-scoped create capabilities listed
above. None of those capabilities reserve GPU capacity or authorize a
cluster-wide controller. Temporary smoke pods and Secrets were removed; the
pilot CRDs and controller resources are retained as recorded in
[`controller pilot notes`](deployment/chartassignment-controller/deploy/README.md).

## Project recreation

The project was created with display name **Intrinsic Core on OpenShift** and
description **Project-scoped resources for the Intrinsic Core full OpenShift
deployment pilot**. The RHOAI dashboard marker was then added. This did not
change kubeconfig or create a quota.

## First deployment gate

On 2026-10-02, a temporary, namespace-scoped UBI minimal pod successfully pulled
its image and ran as a non-root UID in `arhkp-intrinsic`; the pod removed itself
after the check. Rerun the same bounded check with:

```bash
python3 openshift/smoke_restricted_pod.py
```

This verifies basic project scheduling, image pulls, and the restricted
security path. It is not an Intrinsic deployment or evidence that Intrinsic
images, storage, GPUs, or controllers are ready.

## GPU scheduling and device smoke

On 2026-10-02, a bounded pod using the project-scoped `intrinsic-runtime`
ServiceAccount requested one NVIDIA GPU, tolerated only
`g5-gpu=true:NoSchedule`, and ran under restricted security. It scheduled,
pulled the public UBI image, confirmed `/dev/nvidia0`, and completed; the script
deleted the pod and a follow-up check found zero smoke pods remaining. Run it
again only when checking current capacity and device exposure:

```bash
python3 openshift/gpu_smoke.py
```

This confirms node placement and device injection for a smoke pod. It does not
reserve capacity, prove Intrinsic container compatibility, or establish model
performance. The copied resource renderer adds the exact toleration only when
a resource requests a GPU. The adapted resource/skill renderer Bazel target
now compiles at the pinned Core commit; a rendered Intrinsic workload and live
GPU deployment check remain outstanding.

For this pilot, `arhkp-intrinsic` is the selected application namespace. The
upstream namespace and DNS assumptions must be rewritten and generated
manifests checked so all Intrinsic-owned resources remain in this project;
OpenShift and RHOAI operator namespaces remain platform-managed. This is an
implementation constraint, not proof that the upstream controller currently
supports the layout.

## Registry publishing spike

On 2026-10-02, the OpenShift integrated registry was verified without an
external Route. Its operator state is `Managed`, the default Route is disabled,
and project push/pull permissions are present. The
[registry adaptation](REGISTRY_ADAPTATION.md) records the verified path: service
CA, loopback-only port-forward, and temporary private push credentials. A
restricted pod pulled and ran a private synthetic image under the dedicated
`intrinsic-runtime` ServiceAccount; its project image-puller RoleBinding is
namespaced and remains in place. The smoke pod and ImageStream are gone; OpenShift
may retain cluster-scoped Image metadata records after deleting a stream. No
global prune was run. The project CA ConfigMap remains and contains public CA
material only. The copied renderer now wires resource and skill pods to this
ServiceAccount, rejects inline registry credentials, and omits generated pull
Secrets. A local Helm-template smoke passes; no Intrinsic workload has used
this wiring yet.

## Workload image policy

Do not rebuild every upstream image by default. For each image required by the
selected OpenShift workload, test the pinned upstream digest under the project's
restricted security policy and component startup/behavior checks. If it passes,
use the unchanged image. If the image itself needs a change, build an
OpenShift-derived image from the pinned source and record its source, patch, build
target, and resulting digest; fix pod-template or deployment issues in the
deployment layer. Skip images outside the selected workload.

The pinned Core release bundle's two chart image lists contain 22 image tarballs
(about 0.77 GiB total); its archive digest was verified against the documented
release checksum. The charts also reference two images outside the release tarballs: the Zenoh
daemon and Jupyter sidecar. All 24 pinned upstream images are mirrored to
`quay.io/rhkp/intrinsic` under the `upstream-20260922.0` tag;
[`image-lock.json`](image-lock.json) records Quay digests and source provenance.
The copied OpenShift templates now get both images from this lock instead of
using upstream registry references directly.

The HTTP gateway, Zenoh daemon, and Jupyter sidecar images passed basic
restricted-SCC startup smokes with OpenShift-assigned non-root UIDs. The other
images and full workload compatibility remain unverified. Metadata inspection
found the 22 tarball images are Linux/amd64 and declare image user `0`; this is
a compatibility risk to test, not proof each needs rebuilding,
because OpenShift can assign a project UID at pod admission. The Quay copies are
pinned upstream baselines, not a claim of OpenShift compatibility. An all-image
Bazel build was stopped during analysis before producing images; rebuild only
images with demonstrated image-level issues.

The first dev01 pull smoke was denied while the Quay repositories were private.
After they were made public, digest-pinned HTTP gateway, Zenoh daemon, and
Jupyter sidecar images each pulled, passed restricted-SCC admission, started
with an OpenShift-assigned non-root UID, and were removed; cleanup checks found
zero temporary smoke pods. To repeat the HTTP gateway check, run:

```bash
python3 openshift/smoke_quay_image.py http_gateway_service_jwhjwootqbghfz5q
```

This confirms pull, admission, and basic startup for this image only; it does
not verify the gateway API or full Intrinsic behavior. Use another basename from
`image-lock.json` to smoke a different image.

For a future recreation, verify the active context locally without sharing its
name or API address, then use the exact project name:

```bash
ctx=$(oc config current-context)
case "$ctx" in
  *dev01*) ;;
  *) echo "Refusing: current context is not identified as dev01" >&2; exit 1 ;;
esac

oc --context "$ctx" new-project arhkp-intrinsic \
  --skip-config-write=true \
  --display-name="Intrinsic Core on OpenShift" \
  --description="Project-scoped resources for the Intrinsic Core full OpenShift deployment pilot"
oc --context "$ctx" label namespace arhkp-intrinsic \
  opendatahub.io/dashboard=true --overwrite=false
oc --context "$ctx" get project arhkp-intrinsic \
  -o jsonpath='{.metadata.name}{"\n"}'
oc --context "$ctx" get namespace arhkp-intrinsic \
  -o jsonpath='{.metadata.labels.opendatahub\.io/dashboard}{"\n"}'
```

If the project already exists, inspect its metadata and marker instead of
recreating it. Create RHOAI data science projects through the dashboard or use
the version-matched RHOAI metadata workflow; a plain OpenShift namespace is not
automatically selectable in every RHOAI UI.

### Service Mesh gRPC routing

The copied upstream skill/resource charts route gRPC by method path and
resource-instance header. The OpenShift adapter preserves those rules through
an internal Service Mesh Gateway; it does not send gRPC through an OpenShift
Route. The `arhkp-intrinsic` project is enrolled through the namespaced
`ServiceMeshMember` in `deployment/manifests/servicemesh-member.yaml`, and the
updated namespace-scoped controller is built and running. To keep this route
separate from shared ingress, the dedicated gateway uses OpenShift Service Mesh
gateway injection in the pilot project; it does not change the shared
`ServiceMeshControlPlane` or shared gateway. The project-scoped Service is
`ClusterIP` only, with an exact-host HTTP/2 listener and no OpenShift Route or
LoadBalancer. The manifest has been applied; the injected proxy is Ready and
the routing helper verified its Service and listener. The manifest is
[`deployment/manifests/intrinsic-dedicated-grpc-gateway.yaml`](deployment/manifests/intrinsic-dedicated-grpc-gateway.yaml).
The local ignored `openshift/.env` holds the non-secret routing values, and
`python3 openshift/configure_routing.py` applied the project-only routing
ConfigMap. The controller was restarted and is Ready with that configuration.
The helper rejects the shared ingress Service and checks the dedicated Service
endpoints, matching Gateway selector, exact internal host, HTTP/2 protocol,
and port. No Route exists in the project. Service Mesh's generated network
policy currently permits pod traffic from member namespaces; this demo has not
added a workload-identity AuthorizationPolicy, so assess that boundary before
production use. Red Hat documents gateway injection for
separately managed Service Mesh gateway Deployments in the [OpenShift Service
Mesh 2.x documentation](https://docs.redhat.com/en/documentation/openshift_container_platform/4.20/html-single/service_mesh/service_mesh).

## Deployment gate

The upstream [Getting Started guide](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/developer_resources/learn/tutorials/getting_started.md)
installs K3s and deploys the solution to that local Kubernetes runtime. Do not
run its K3s or NVIDIA host setup scripts on OpenShift workers. This workspace
now has a provenance-tracked copy of the deployment-specific Core sources,
their referenced chart templates, and OMTS demo configuration under
[`deployment/`](deployment/UPSTREAM_SOURCES.md). These are pinned upstream
baselines with the registry-publisher and image-pull adaptations applied. No
Intrinsic workload has been deployed here. The copied source paths and SHA-256
digests are recorded in `deployment/SOURCE_MANIFEST.json`. Two OMTS hardware
configs with fixed robot network addresses were excluded. The no-apply
renderer/policy adapter now passes local base/app chart snapshots; we still own
the source-built chart generation, configuration, project-scoped apply/health
checks, and lifecycle. The upstream temporary checkouts remain separate from
this repository.

That inspection found concrete installer blockers. The [deployment model](DEPLOYMENT_MODEL.md)
defines our OpenShift deployment code boundary. The first registry-publisher
adaptation is applied to the owned copy and documented in
[REGISTRY_ADAPTATION.md](REGISTRY_ADAPTATION.md); the patch is at
[`patches/intrinsic-openshift-registry.patch`](../patches/intrinsic-openshift-registry.patch).
`gofmt`, patch checks, and the local Helm image-pull template check pass; the
generic dev01 registry smoke passed.
On 2026-10-02, the pinned upstream commit
`523f2e03ddf3cb46a19b15be5e81586ae2f538c2` plus the checked-in OpenShift patch
successfully built `//intrinsic/kubernetes:chart_executable` on a native x86-64
AWS build host. The focused
`//intrinsic/production:openshift_registry_publisher_test` passed (1/1); the
artifact was verified as ELF x86-64 and its `start --help` path ran in the local
x86-64 container. This validates the patched CLI build and publisher tests,
not an Intrinsic workload deployment. The earlier adapted
`//intrinsic/assets/deploy:render` target also compiled at the pinned commit.
The x86-64 build host received source code only; cluster kubeconfig and
application secrets remain on the authorized local host. Authenticated dev01
access was verified for project-scoped smoke checks earlier. The new offline
renderer and policy adapter now render the pinned base and app chart snapshots
into the selected project, rewrite their known internal service paths, and
reject unreviewed cluster-scoped or unsafe host resources. That render is
documented in [`deployment/ADAPTATIONS.md`](deployment/ADAPTATIONS.md); it has
not been applied. Full deployment remains gated on reproducing the render from
the owned patched-source build, reviewing storage and runtime-generated
resource/skill charts, resolving gRPC Gateway routing, then validating health
and cleanup. The adapted workcell service image now builds
for Linux/amd64 from the pinned source, is published under a new digest-locked
Quay tag, and passed an anonymous pull. It has not been deployed. The
controller previously ran a fail-closed
guard for the removed upstream gateway; a live smoke verified that guard only.
The owned source now adapts gRPC `VirtualService` routing to a project-dedicated
Service Mesh Gateway and internal ClusterIP Service. The project is enrolled
and the updated controller has one Ready replica on the new immutable image
digest. A dedicated injected gateway is deployed from a project-only manifest;
its Deployment and Service are separate from the shared ingress pods and do not
modify the shared control plane. The configuration helper rejects shared
ingress Services and verified the dedicated Service endpoint, Gateway selector,
exact service DNS host, HTTP/2 protocol, port 80, and mesh membership. The
project-only routing ConfigMap is applied and the controller restarted. The
project has no OpenShift Route for this Gateway. On 2026-10-02,
[`grpc_gateway_smoke.py`](grpc_gateway_smoke.py) sent five gRPC pings over the
internal h2c listener through a temporary header-matched VirtualService to a
sidecar-injected backend; all five passed under the cluster's existing STRICT
mTLS policy, which was left unchanged. The script removed and verified absence
of its temporary pods, Service, and VirtualService. This verifies generic
gRPC transport and gateway dispatch, not an Intrinsic application method. No
Core workload or application VirtualService has been applied yet. The upstream
[`chart_executable.go`](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic/kubernetes/chart_executable.go)
defaults to a containerd publisher at `/run/k3s/containerd/containerd.sock`.
The checked-in patch adds an OpenShift registry publisher selected with
`--registry_forward`; it requires an in-cluster `--registry` reference, a
service CA file, and a loopback-only port-forward. The pinned
[`chartapply.go`](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic/kubernetes/chartapply.go)
creates missing chart namespaces and may delete an emptied `app-<chart>`
namespace on chart removal. The pinned
[`renderutil.go`](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic/assets/deploy/renderutil.go)
fixes resource and skill namespaces to `app-resources` and `skills`; our owned
copy points both to `arhkp-intrinsic` for this pilot. The base
chart also uses `app-intrinsic-base` DNS names. Its generated
[`workcell-cluster-service` RBAC](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic/kubernetes/workcell_spec/templates/workcell-cluster-service.yaml)
includes a ClusterRole/ClusterRoleBinding. The upstream controller's ClusterRole
can create, update, patch, and delete cluster-scoped ChartAssignments, and can
inspect pods and services across namespaces; namespace-specific `Role` rules
alone cannot constrain those cluster-scoped APIs. The upstream CRD/controller
path remains absent on dev01. We installed a separate Namespaced ChartAssignment
and ResourceSet pilot with project-only RBAC; its ConfigMap smoke passed, but it
does not yet deploy Core resources or skills. The checked-in patch changes the
Core client to use the namespaced API and this patch passed the full CLI build;
the separate copied source tree still needs to be kept aligned with that patch.
Use the adapted namespace-scoped controller for dynamic resource/skill charts;
do not install the broad upstream controller on dev01.

No GPU quota or GPU workload should be created until project allocation,
taint/toleration policy, GPU capacity, and required model/runtime resources are
confirmed with the cluster owners. Current inventory shows one estimated free
A10G cluster-wide, with no quota or reservation for this project.

Verify the copied upstream source and adaptation digests before building with:

```bash
python3 openshift/validate_source_manifest.py
```

Keep credentials, tokens, kubeconfigs, API addresses, private registry/object
storage endpoints, and certificates outside Git. `.env` files are ignored by
the repository; commit only reviewed placeholders in `.env.sample` files.
