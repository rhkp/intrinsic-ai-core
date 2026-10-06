# OpenShift-owned deployment model

## Decision

Treat the pinned Intrinsic release as the reference for application behavior,
images, APIs, component relationships, deployment assets, and operator
workflow. Prefer its released Core assets and documented OMTS Bazel commands.
Adapt the OpenShift boundary only where a verified K3s assumption prevents that
workflow from running. The current project-scoped controller and chart policy
adapter are a working pilot; they are not proof that every upstream deployment
component must be replaced. Keep each needed source patch traceable to the
pinned commit and remove speculative adaptations after focused tests show they
are unnecessary.

Keep application services, images, and solution configuration upstream unless
a specific OpenShift incompatibility requires a change. Do not run K3s host
installers or mount node containerd sockets on OpenShift. Replace only affected
host-level image-transfer and cluster-scope operations with project-scoped
OpenShift mechanisms.

## What our deployment package owns

- Namespaced Deployments, StatefulSets, Services, ConfigMaps, PVCs,
  NetworkPolicies, and routes, with one reviewed namespace map for
  `arhkp-intrinsic`.
- Adapted copies of the needed upstream charts/templates, with provenance and
  a recorded review of every removed or changed controller assumption.
- OpenShift service accounts, secret references, restricted-SCC compatibility,
  registry access, GPU placement, storage, and resource requests/limits.
- RHOAI-managed serving resources only for components that pass the Intrinsic
  protocol, model, lifecycle, and performance checks.
- Versioned dev01 configuration and reproducible deployment/verification
  commands in this repository.

The registry push and private image-pull mechanisms passed bounded live
smokes. A project-scoped `intrinsic-runtime` ServiceAccount and image-puller
RoleBinding are in place. The owned resource/skill renderer selects that
account, rejects inline image credentials, and targets the pilot project;
Helm template checks pass. A restricted smoke pod also consumed a synthetic
`Opaque` Secret from a Git-ignored `.env`; the test resources were removed.
Project mesh membership, the dedicated internal gRPC Gateway, its routing
ConfigMap, and the namespace-scoped controller are applied and verified. The
Core static slice is deployed: all 22 Deployments are Ready, all four
ChartAssignments are Settled, and five PVCs are Bound. A prior `StartSolution`
attempt failed with gRPC `UNAVAILABLE`; generated resource/skill lifecycle,
active simulation, and viewer are still unproven.

## Dynamic assets and controllers

The upstream stack dynamically installs resources and skills through
cluster-scoped ChartAssignments and broad controller permissions, so that
upstream controller is outside our deployment path. A separate Namespaced
ChartAssignment/ResourceSet controller pilot now passes a ConfigMap-only smoke
with namespace-limited RBAC. The copied Core client changes still need a full
build and test; the first application slice can instead use reviewed static
project-scoped manifests. Use the pilot controller for actual resources/skills
only if runtime add/remove behavior is required and after reviewing its full
object inventory and permissions.

The generated resource/skill charts also use gRPC `VirtualService` routing by
RPC path and resource-instance metadata. The copied controller adapts those
rules to a dedicated injected OpenShift Service Mesh Gateway and confines
destinations to the pilot project. The project is enrolled; the gateway uses a
separate internal `ClusterIP` Service with plaintext HTTP/2 (`h2c`) and has no
OpenShift Route. The routing ConfigMap is applied and the controller restarted.
A temporary five-call h2c smoke passed through the Gateway and header-matched
VirtualService to a sidecar-injected backend under dev01's existing STRICT mTLS
policy; all smoke resources were removed. No Core resource/skill `VirtualService`
workload has been deployed, so an actual Intrinsic RPC remains untested. The
mesh-generated NetworkPolicy permits pod traffic from enrolled mesh namespaces;
project-only workload identity authorization remains open for the demo.

The initial deployment-specific upstream copy is in
[`deployment/`](deployment/UPSTREAM_SOURCES.md); it records each source path,
commit, and source/baseline/current SHA-256. License and trademark notices are
included. The registry publisher and image-pull identity are the first
adaptations; see the [adaptation record](deployment/ADAPTATIONS.md). Keep upstream originals
untouched in temporary inspection checkouts; make OpenShift changes against our
copied files and review their diff whenever the pinned upstream version
changes. The pilot is applied but incomplete; it is not yet evidence of an
end-to-end simulation or a complete replacement for upstream deployment
tooling.

## Validation boundary

Before applying an overlay, render it and verify namespaces, owner references,
ServiceAccounts, secret references, SCC-compatible security contexts, storage,
resource requests, GPU tolerations, and network access. Then deploy a bounded
component slice and compare its APIs and simulation output with the known-good
VM. A successful manifest render or pod start alone is not parity evidence.
