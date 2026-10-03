# Namespace-scoped ChartAssignment pilot

These manifests are for an isolated smoke test in `arhkp-intrinsic`. They do
not install the full Intrinsic Core stack. The CRD definitions are registered
at cluster scope, as Kubernetes requires, but both custom-resource kinds are
`Namespaced`; the controller watches one namespace and its RBAC is bound by a
project-scoped RoleBinding.

The smoke ChartAssignment contains one harmless ConfigMap, so the smoke does
not exercise the controller's full Role. The current Role grants the reviewed
namespaced Core workload kinds in `arhkp-intrinsic`, including `resourcesets`
and their required finalizers. It has no Secret, Namespace, CRD, or
cluster-scoped RBAC permissions. Live `can-i` checks confirmed representative
project workload permissions and denied Secrets and cluster-scoped resources.
Synk also rejects chart-supplied CRDs and cluster-scoped resources. Re-review
this Role when the rendered runtime resource/skill chart inventory is added.

The Core `workcell-cluster-service` has a separate project-scoped Role in its
Helm template and is not part of the controller smoke test.

## Dev01 smoke procedure

Confirm the active context is dev01 and that both CRDs are absent. Do not
change the scope of an existing CRD. Apply the CRDs, then project-scoped RBAC
and the binary BuildConfig:

```bash
oc apply -f chartassignment-crd.yaml
oc wait --for=condition=Established crd/chartassignments.apps.cloudrobotics.com --timeout=60s
oc apply -f resourceset-crd.yaml
oc wait --for=condition=Established crd/resourcesets.apps.cloudrobotics.com --timeout=60s
oc -n arhkp-intrinsic apply -f controller-rbac.yaml
oc -n arhkp-intrinsic apply -f controller-build.yaml
oc -n arhkp-intrinsic start-build chartassignment-controller \
  --from-dir=openshift/deployment/chartassignment-controller --follow
```

After the build succeeds, apply the Deployment and generate the one-ConfigMap
smoke ChartAssignment:

```bash
oc -n arhkp-intrinsic apply -f controller-deployment.yaml
oc -n arhkp-intrinsic rollout status deployment/chartassignment-controller --timeout=120s
python3 generate-configmap-smoke.py | oc -n arhkp-intrinsic apply -f -
oc -n arhkp-intrinsic get chartassignment namespace-scope-smoke -o yaml
oc -n arhkp-intrinsic get configmap chartassignment-smoke-result
```

Success means the namespaced ChartAssignment reaches `Settled` or `Ready` and
the ConfigMap appears in `arhkp-intrinsic`. The smoke validates this ConfigMap
path only; it does not validate the broader project Role against Core workloads.
The Role still denies Secrets and cluster-scoped resources. Clean up by deleting the ChartAssignment and waiting for
its finalizer to finish; verify the ConfigMap is removed before removing the
controller Deployment, project RoleBinding/Role, and the CRDs. Do not delete a
CRD if another user or deployment has started using it.

## Verified dev01 result — 2026-10-02

Both CRDs are established with `Namespaced` scope. The controller image was
built from this directory with a project-local OpenShift Binary BuildConfig and
published to the private `chartassignment-controller:pilot` ImageStream tag.
The single controller replica is Ready. Its project Role was checked as the
controller ServiceAccount: it can create project workloads and PVCs, cannot
create Secrets or Namespaces, and cannot create ClusterRoles or
ClusterRoleBindings.

The generated smoke ChartAssignment reached `Ready`, created its ConfigMap in
`arhkp-intrinsic`, and was then deleted. Deletion completed through the
ChartAssignment finalizer; the ConfigMap and matching ResourceSet were both
confirmed absent afterward. The smoke ChartAssignment itself is gone.

The CRD registrations and project-local controller Deployment, ServiceAccount,
Role, RoleBinding, BuildConfig, and ImageStream remain for the next deployment
step. This validates the namespaced controller path only; it does not validate
or deploy the full Intrinsic Core workload.

## Service Mesh enrollment and updated controller — 2026-10-02

The `arhkp-intrinsic` project is enrolled through the namespaced
[`ServiceMeshMember`](../../manifests/servicemesh-member.yaml) resource, which
references the Ready `data-science-smcp` control plane in `istio-system`. The
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
