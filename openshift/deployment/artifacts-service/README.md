# OpenShift ArtifactService image

The pinned upstream `oci_image.go` writer imports OCI content into containerd
and then unpacks it with `containerd.DefaultSnapshotter` (`overlayfs`). Upstream
K3S points this writer at the node containerd runtime. The OpenShift deployment
uses a restricted pod-local containerd sidecar only as an artifact content
store; workloads pull images from the project registry. Overlay mounts are not
available to that sidecar, and unpacking is not needed to store or serve the OCI
content and image record.

The patch script checks the pinned upstream file digest and removes only the
final unpack call in the OpenShift derived ArtifactService image. It does not
change the upstream K3S source or deployment behavior.

From the pinned OMTS workspace, apply the patch to the Core source fetched by
Bazel and build the artifact service binary. The patch also skips upstream's
startup `CatalogPage` permission probe for the OpenShift registry backend:
OpenShift's project-scoped `system:image-builder` role permits project image
uploads but does not grant a cluster-wide registry catalog listing. The actual
image reads and writes still use the configured registry credentials and are
authorized per project repository. OMTS's direct uploader normally returns
image references under its node-local `localhost:17127` registry prefix. The
OpenShift-only Core patch configures that upstream uploader with
`INTRINSIC_REGISTRY_HOST/OPENSHIFT_NAMESPACE` so the solution stores portable
project-registry references. ArtifactService continues to enforce its
configured registry prefix; it does not rewrite loopback image references.

```sh
cd "${INTRINSIC_OMTS_SRC:-${HOME}/intrinsic-omts}"
core_source="$(bazel info output_base)/external/intrinsic-core+"
python3 /opt/intrinsic/repo/openshift/deployment/artifacts-service/patch_oci_image.py "$core_source"
(cd "$core_source" && bazel build //intrinsic/storage/artifacts:artifact_service)
test -x "$core_source/bazel-bin/intrinsic/storage/artifacts/artifact_service"
```

Copy the built `artifact_service` binary into a temporary local build context
with this directory's `Dockerfile`, apply `build.yaml`, and start the binary
OpenShift build. The OpenShift controller configures `--registry` to the project registry,
mounts the `intrinsic-registry-service-ca` ConfigMap, and selects the
`omts-deployer` ServiceAccount, which has the project `system:image-builder`
role. The service template excludes outbound port 5000 from mesh interception.
Pin the resulting immutable project image digest in `openshift/image-lock.json`
and the controller's image override before reconciling Core.
