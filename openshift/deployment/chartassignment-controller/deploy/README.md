# Namespace-scoped ChartAssignment pilot
> [!NOTE]
> This project was developed with assistance from AI tools.

Use the [canonical deployment procedure](../../../README.md#canonical-operational-helpers).
The Deployment and RBAC files here are templates; render them into ignored
`openshift/.generated/` before applying. The controller and offline render commands
require `--target-config` and `--image-lock`; environment-only startup is removed.
The procedures below record earlier deployment experiments and must be adapted
to the generated manifests for a new installation.


These manifests are for an isolated smoke test in `your-project-namespace`. They do
not install the full Intrinsic Core stack. The CRD definitions are registered
at cluster scope, as Kubernetes requires, but both custom-resource kinds are
`Namespaced`; the controller watches one namespace and its RBAC is bound by a
project-scoped RoleBinding.

The smoke ChartAssignment contains one harmless ConfigMap, so the smoke does
not exercise the controller's full Role. The current Role grants the reviewed
namespaced Core workload kinds in `your-project-namespace`, including `resourcesets`
and their required finalizers. It has no Secret, Namespace, CRD, or
cluster-scoped RBAC permissions. Live `can-i` checks confirmed representative
project workload permissions and denied Secrets and cluster-scoped resources.
Synk also rejects chart-supplied CRDs and cluster-scoped resources. Re-review
this Role when the rendered runtime resource/skill chart inventory is added.

The Core `workcell-cluster-service` Role is separate from the controller's
Role. It grants project-scoped lifecycle permissions to the Workcell service
account and is provisioned from
[`../../manifests/workcell-cluster-service-rbac.yaml`](../../manifests/workcell-cluster-service-rbac.yaml)
before the Core base ChartAssignment. The ChartAssignment adapter validates the
upstream RBAC rules but omits that Role and RoleBinding from Synk, because the
controller must not be allowed to grant the Workcell account permissions that
the controller itself does not hold.

## Upstream native PubSub Zenoh DNS compatibility

Some pinned upstream native PubSub clients compile in the
`zenoh-router.app-intrinsic-base.svc.cluster.local:7447` endpoint and ignore
ROS Zenoh environment overrides. Apply the narrowly scoped DNS alias before
running solutions that use those clients:

```bash
oc apply -f openshift/deployment/manifests/upstream-zenoh-router-alias.yaml
```

The alias points only that upstream service name at the project's existing
Zenoh router. The ChartAssignment controller excludes outbound port 7447 from
Istio interception on the ICON, capture-skill, and pose-estimator pod templates
so their raw Zenoh TCP connections remain plaintext as configured upstream.

## Workcell RBAC prerequisite for the Core base ChartAssignment

Apply this project-only Role and RoleBinding before applying or reconciling
`intrinsic-base`:

```bash
oc -n your-project-namespace apply \
  -f openshift/deployment/manifests/workcell-cluster-service-rbac.yaml
oc -n your-project-namespace start-build chartassignment-controller \
  --from-dir=openshift/deployment/chartassignment-controller --follow
oc -n your-project-namespace get istag chartassignment-controller:pilot \
  -o jsonpath='{.image.dockerImageReference}'
# Pin the immutable @sha256: reference from the previous command in
# deploy/controller-deployment.yaml, then apply and wait for the rollout.
oc -n your-project-namespace apply -f \
  openshift/deployment/chartassignment-controller/deploy/controller-deployment.yaml
oc -n your-project-namespace rollout status deployment/chartassignment-controller \
  --timeout=180s
oc -n your-project-namespace get chartassignment intrinsic-base \
  -o jsonpath='{.status.phase}{"\n"}'
oc -n your-project-namespace get resourcesets -l name=intrinsic-base \
  -o custom-columns=NAME:.metadata.name,PHASE:.status.phase
```

The manifest contains only the verbs from the pinned upstream Workcell Role,
scoped to `your-project-namespace`. Do not add these verbs to the
`chartassignment-controller` Role. After rebuilding and rolling out the
adapted controller, its fresh reconciliation of the existing base
ChartAssignment reapplies the project workload, omits only the externally
provisioned Role and RoleBinding, and settles a new ResourceSet. Verify
`intrinsic-base` is Ready and `workcell-cluster-service` remains a namespaced
Role/RoleBinding bound only to the Workcell service account.

## target cluster smoke procedure

Confirm the active context is target cluster and that both CRDs are absent. Do not
change the scope of an existing CRD. Apply the CRDs, then project-scoped RBAC
and the binary BuildConfig:

```bash
oc apply -f chartassignment-crd.yaml
oc wait --for=condition=Established crd/chartassignments.apps.cloudrobotics.com --timeout=60s
oc apply -f resourceset-crd.yaml
oc wait --for=condition=Established crd/resourcesets.apps.cloudrobotics.com --timeout=60s
oc -n your-project-namespace apply -f controller-rbac.yaml
oc -n your-project-namespace apply -f controller-build.yaml
oc -n your-project-namespace start-build chartassignment-controller \
  --from-dir=openshift/deployment/chartassignment-controller --follow
```

After the build succeeds, apply the Deployment and generate the one-ConfigMap
smoke ChartAssignment:

```bash
oc -n your-project-namespace apply -f controller-deployment.yaml
oc -n your-project-namespace rollout status deployment/chartassignment-controller --timeout=120s
python3 generate-configmap-smoke.py | oc -n your-project-namespace apply -f -
oc -n your-project-namespace get chartassignment namespace-scope-smoke -o yaml
oc -n your-project-namespace get configmap chartassignment-smoke-result
```

Success means the namespaced ChartAssignment reaches `Settled` or `Ready` and
the ConfigMap appears in `your-project-namespace`. The smoke validates this ConfigMap
path only; it does not validate the broader project Role against Core workloads.
The Role still denies Secrets and cluster-scoped resources. Clean up by deleting the ChartAssignment and waiting for
its finalizer to finish; verify the ConfigMap is removed before removing the
controller Deployment, project RoleBinding/Role, and the CRDs. Do not delete a
CRD if another user or deployment has started using it.

## Verified target cluster result — 2026-10-02

Both CRDs are established with `Namespaced` scope. The controller image was
built from this directory with a project-local OpenShift Binary BuildConfig and
published to the private `chartassignment-controller:pilot` ImageStream tag.
The single controller replica is Ready. Its project Role was checked as the
controller ServiceAccount: it can create project workloads and PVCs, cannot
create Secrets or Namespaces, and cannot create ClusterRoles or
ClusterRoleBindings.

The generated smoke ChartAssignment reached `Ready`, created its ConfigMap in
`your-project-namespace`, and was then deleted. Deletion completed through the
ChartAssignment finalizer; the ConfigMap and matching ResourceSet were both
confirmed absent afterward. The smoke ChartAssignment itself is gone.

The CRD registrations and project-local controller Deployment, ServiceAccount,
Role, RoleBinding, BuildConfig, and ImageStream remain for the next deployment
step. This validates the namespaced controller path only; it does not validate
or deploy the full Intrinsic Core workload.

## Service Mesh enrollment and updated controller — 2026-10-02

The `your-project-namespace` project is enrolled through the namespaced
[`ServiceMeshMember`](../../manifests/servicemesh-member.yaml) resource, which
references the Ready `your-service-mesh-control-plane` control plane in `istio-system`. The
member reports Ready. The existing project-scoped controller BuildConfig built
and pushed a new image to the `chartassignment-controller:pilot` ImageStream
tag; the Deployment was updated to the resulting immutable digest and rolled
out successfully with one Ready replica.

The dedicated gateway manifest at
[`../../manifests/intrinsic-dedicated-grpc-gateway.yaml`](../../manifests/intrinsic-dedicated-grpc-gateway.yaml)
creates an injected Envoy Deployment and internal ClusterIP Service in the
pilot project, separate from the shared ingress pods. The proxy and Service
endpoint are Ready; the shared `ServiceMeshControlPlane` is unchanged and no
OpenShift Route was created. The project-only routing helper verified the
dedicated Service and exact HTTP/2 listener, then applied
`intrinsic-routing-config`. The controller was restarted and is Ready with the
configuration. No Core workload or live gRPC request has been run yet.

## Durable Core adapter rollout — 2026-10-05

The initial registry image update was reverted when `intrinsic-base` was
reconciled from its stale inline upstream chart. The OpenShift controller
adapter now pins the namespace-scoped Resource Registry and Workcell images
and normalizes the registry watch argument on every render. The adapted
Workcell image and source patches are recorded in
[`../../../image-lock.json`](../../../image-lock.json) and
[`../../ADAPTATIONS.json`](../../ADAPTATIONS.json).

After changing the controller adapter, validate and rebuild it from the repo
root:

```bash
cd openshift/deployment/chartassignment-controller
go test ./controller
cd ../../..
oc -n your-project-namespace start-build chartassignment-controller \
  --from-dir=openshift/deployment/chartassignment-controller
```

Read the `chartassignment-controller:pilot` ImageStream tag, pin its immutable
digest in `controller-deployment.yaml`, apply that manifest, and wait for the
single-replica rollout. The 2026-10-05 rollout uses digest
`sha256:97ac26ba62714f25f96c6ba36d61b424e50493fa6bd5e843796ef5ce7695b0a1`.
The adapter gives the code-execution Jupyter sidecar a writable `HOME` and
runtime directory under its existing `/home/defaultuser` `emptyDir`, verifies
that mount during rendering, and replaces the image entrypoint to bind its
unauthenticated API only to `::1` for the colocated worker. on the target cluster, all three
workers created kernels and completed startup requests; `code-execution` is
2/2 Ready with zero restarts. Both `intrinsic-base` and `intrinsic-app-chart`
ChartAssignments are Ready with Settled ResourceSets; all 22 Deployments are
Ready. A before/after comparison of all 20 Core Deployment image sets found no
changes, including `world-*` workloads. Registry and Workcell images and the
`--configmap_watch_namespace=your-project-namespace` argument remain verified, with
no current cluster-scope RBAC denials in their logs.

## Internal ingress-port correction — 2026-10-06

The adapted Core renderer requires `INTRINSIC_INGRESS_ADDRESS` to use the
internal Gateway Service HTTP/2 port 80. The routing helper, checked-in sample,
controller validation, and live ConfigMap had drifted to port 443, causing
`simulation-service` to exit with code 134 and report that the ingress address
must use port 80. The helper and controller tests now accept only port 80 for
this variable while still checking that the Gateway offers its separate
port-443 `ISTIO_MUTUAL` listener.

Focused validation passed: 8 Python routing tests, both controller Go test
packages, and `git diff --check`. The updated ConfigMap was applied; controller
build 22 was rolled out with digest
`sha256:a80c9901028d90e5bac40b96cf64a557f452bba792c61a65651b107e56010985`.
Both base/app ChartAssignments were re-rendered, then the temporary no-op
reconcile value was removed and reconciled away. Final observed state: all 22
Deployments Ready, `simulation-service` 2/2, all four ChartAssignments
Settled, and no temporary value retained. This restores the static Core
baseline; it does not prove `StartSolution` or generated resource/skill
lifecycle.

## Simulation resource security and IPC permissions — 2026-10-07

The applied `intrinsic-sim-realtime` SCC is restricted and non-privileged. It
uses `requiredDropCapabilities: [ALL]`, the RuntimeDefault seccomp profile, and
the dedicated `intrinsic-sim-realtime` ServiceAccount. The controller overlay
adds only the reviewed capabilities to exact simulator containers and bounds
their resources; other workloads remain fail-closed.

The reset reconnect failure has a measured permission cause. After reset, the
shared CephFS volume contains `ur_module.sock` owned by UID/GID
`1001690000:1001690000` with mode `0755`. ICON runs as UID 0 with supplemental
group `1001690000`, but the socket has no group-write bit and the SCC removes
`DAC_OVERRIDE`. The local adapter change requests `DAC_OVERRIDE` only for the
`rs-icon/rs-icon` container, alongside its existing `IPC_LOCK` and `SYS_NICE`.
The local SCC manifest allows the capability; it is not yet applied in the
cluster. The SCC update, controller rollout, and post-reset ICON reconnect
still need verification. No mesh policy or mTLS setting is involved in this
local filesystem `EACCES`.

The SCC and ServiceAccount are already applied in target cluster. Because a namespace
writer can select this ServiceAccount in a Pod spec, keep write access to
`your-project-namespace` controlled. Confirm the new capability appears only on the
ICON main container after reconciliation; keep privileged mode, host directory,
host network, host PID, and host IPC disabled.
