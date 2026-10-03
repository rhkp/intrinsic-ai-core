# OpenShift image-pull identity

**Status:** the copied Core resource/skill renderer now uses the verified
`intrinsic-runtime` ServiceAccount and rejects inline registry credentials.
Helm rendering checks pass. No Intrinsic workload has been deployed from this
copy yet.

## OpenShift path

The resource and skill workloads are placed in the pilot project,
`arhkp-intrinsic`, where the dedicated ServiceAccount and its namespaced
`system:image-puller` RoleBinding live. The reproducible namespaced objects are
in [`deployment/manifests/image-pull-access.yaml`](deployment/manifests/image-pull-access.yaml).
The ServiceAccount disables API-token automount.

In the copied renderer, resource images, init-container images, and skill images
fail closed if either inline registry username or password is present. The
error identifies the required ServiceAccount but never includes credential
values. Resource PodSpecs and skill Deployment templates set
`serviceAccountName: intrinsic-runtime` and disable token automount. The old
base64 `.dockercfg` value generation, `imagePullSecrets` references, and
generated `kubernetes.io/dockercfg` Secret templates have been removed.

This relies on the pilot's in-project OpenShift integrated-registry pull path,
which was verified separately with a restricted smoke pod and synthetic image.
Image publishing remains a separate operation: the adapted publisher reads
push credentials from the operator's private Docker config. The application
workloads do not receive those push credentials.

## Application runtime secrets

The pinned upstream Getting Started deployment and copied Core workload
templates contain no application `Secret` objects or Secret references. The
cluster pull path is handled separately by the `intrinsic-runtime` ServiceAccount.
We therefore have no confirmed Intrinsic runtime credential names to provision
yet; do not invent or pass an empty generic Secret to the actual workload.

The repo provides a repeatable path for a project-local `Opaque` Secret:

1. Copy `openshift/.env.sample` to the ignored `openshift/.env` and replace the
   obvious smoke placeholder locally.
2. Run `bash openshift/deployment/apply-runtime-secret.sh`. The helper checks
   that `.env` is Git-ignored, sets mode `0600`, creates or updates an OpenShift
   Secret in `arhkp-intrinsic`, and streams the generated Secret manifest
   directly to `oc apply` without writing it to disk or printing its values.
3. Reference only required keys from a workload using `secretKeyRef` or a
   read-only Secret volume. Runtime Secret consumption does not require a
   special ServiceAccount; limit who can create pods/deployments in the project,
   because they can configure a pod to consume project Secrets. Do not grant
   workloads API access to list/read Secrets. Environment-based values require
   a pod restart after Secret updates.

`deployment/secrets/runtime-secret-smoke-pod.yaml` exercises the mechanism with
a synthetic key; it is not an Intrinsic workload manifest. The pod references
the `Opaque` Secret through `secretKeyRef`. It uses `intrinsic-runtime` only to
pull the test image; normal runtime Secret consumption does not require a
special ServiceAccount or grant the container Secret API access.

**Verified on dev01:** the helper created the `Opaque` Secret from a throwaway,
Git-ignored `.env`; a restricted pod consumed the value and emitted only a
success marker. The Secret, pod, and local `.env` were deleted and their absence
verified. `.env.sample` contains a fake smoke placeholder only. The actual
app-specific secret mapping remains open until the rendered Core and OMTS
workload inventory identifies a real consumer.

OpenShift Secret objects are the demo mechanism, not a substitute for an
external secrets manager in a production design. Do not commit generated Secret
YAML, base64 data, or local `.env` files. The repository ignores `.env` and
`.env.*`, except `.env.sample`, which contains only a clearly fake smoke value.

## Validation and boundary

Run the local template check with:

```bash
python3 openshift/deployment/validate_image_pull_templates.py
```

It renders both copied Helm templates and checks that the workload manifests
select `intrinsic-runtime`, disable token automount, and contain no generated
pull Secret, `imagePullSecrets`, or `.dockercfg` data. The Go source is formatted
with `gofmt`; the full Bazel build/package tests have not run because Bazel is
not installed in this environment. This is not a live Intrinsic deployment.
Other upstream namespace, DNS, controller, storage, SCC compatibility for
Intrinsic images, GPU capacity reservation, and app-specific runtime-secret
mapping remain open and are tracked in the
[deployment model](DEPLOYMENT_MODEL.md) and [work plan](../OPENSHIFT_PLAN.md).

No password, token, kubeconfig, endpoint, or certificate private key is stored
in this repository.
