# OpenShift registry adaptation — first spike

The pinned Core source (`20260922.0`, commit `523f2e03ddf3`) always creates a
publisher for the K3s node's `/run/k3s/containerd/containerd.sock`. That path is
not available to OpenShift clients. The first registry-publisher adaptation is
now applied to our owned copy under `deployment/intrinsic-core/`; its source
patch is at [`patches/intrinsic-openshift-registry.patch`](../patches/intrinsic-openshift-registry.patch),
and the per-file hashes are recorded in
[`deployment/ADAPTATIONS.json`](deployment/ADAPTATIONS.json).

The patch adds an opt-in publisher for the OpenShift integrated registry. It
keeps image references on the in-cluster registry service name, uses a local
loopback `oc port-forward` only for the registry connection, trusts the supplied
service CA plus system roots, and obtains push credentials from the caller's
private Docker config via `authn.DefaultKeychain`. It has no insecure-TLS mode
and does not require a public registry Route. Workload images keep the
in-cluster registry name so pods can use their project ServiceAccount's image
pull credentials. This section covers registry push only. The integrated-
registry private-pull path has since been checked using a dedicated project
ServiceAccount; see the [secret adaptation assessment](SECRETS_ADAPTATION.md).
Other upstream runtime credentials need a separate inventory and migration to
OpenShift service accounts, Secrets, or an approved external secret provider.

The design follows the pinned upstream [`go-containerregistry` remote options](https://github.com/google/go-containerregistry/blob/v0.20.3/pkg/v1/remote/options.go): the release uses `remote.WithTransport` and its default transport attempts HTTP/2. The patch maps only the internal registry host to the loopback forward; separate storage hosts returned during upload use normal host-based TLS validation. The cluster CA workflow follows the [OpenShift 4.20 service CA documentation](https://docs.redhat.com/en/documentation/openshift_container_platform/4.20/html/security_and_compliance/certificate-types-and-descriptions).

## Verification status — 2026-10-02

The patch applies cleanly to the pinned source copy and `gofmt` passes on the
adapted Go files. Focused unit tests passed earlier in a temporary module
harness using pinned external dependencies and small stubs for Bazel-mapped
internal aliases; they have not been rerun from our owned copy. The full
upstream Go package and Bazel build were not run, and the copied command has
not yet published an Intrinsic workload image or deployed a workload.

The live dev01 registry check passed. Its operator state is `Managed`, the
external default Route is disabled, and the current project identity can push
and pull image-stream layers. An annotated project ConfigMap named
`intrinsic-registry-ca` now holds the injected public service CA bundle. The
registry certificate verified with the internal service DNS name over a
loopback-only `oc port-forward`; `/v2/` returned `401`, the expected
unauthenticated registry challenge. With `oc registry login` writing credentials
to a mode-0600 temporary Docker config, the patched publisher pushed a small
synthetic Linux image. A restricted pod using the default ServiceAccount then
pulled and ran it successfully with `imagePullPolicy: Always`. The test set
`RELEASE_CANDIDATE_NAME` to a synthetic smoke tag. The OpenShift command path
now requires this variable so it cannot
fall back to upstream's local-username-derived tag.

The test pod and ImageStream were deleted. OpenShift retains two matching
cluster-scoped Image metadata records after the stream deletion; no cluster-wide
image prune was run. The CA ConfigMap remains because it contains only public
CA material and is needed for repeatable registry access. The temporary auth
config and CA file were removed. No public Route was created.

A follow-up test used the project-scoped `intrinsic-runtime` ServiceAccount,
with API-token automount disabled and a project `system:image-puller`
RoleBinding. The account's OpenShift-generated pull-secret reference fetched a
synthetic image from the integrated registry with `imagePullPolicy: Always`,
and the restricted pod completed successfully. The temporary test pod and
ImageStream, ignored `.env`, local auth config, and CA file were removed; the
ServiceAccount and RoleBinding remain for the pilot. No global image prune was
run.

Before using this publisher for Intrinsic images, set a nonpersonal unique
`RELEASE_CANDIDATE_NAME`, add the CA to a temporary local file, and use `oc
registry login` to write the token to a private Docker config outside the
repository. Bind `oc port-forward` explicitly to
`127.0.0.1`. Do not create a public Route for this workflow.


This adaptation solves only the image-publisher assumption. It does not make
the upstream ChartAssignment CRD/controller namespace-scoped, constrain its
broad ClusterRole, or fix the upstream namespace creation/cleanup and fixed DNS
assumptions. Those remain deployment blockers; do not install the upstream
controller or apply its cluster-scoped resources as part of this spike.
