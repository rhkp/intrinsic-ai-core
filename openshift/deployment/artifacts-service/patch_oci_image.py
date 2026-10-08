#!/usr/bin/env python3
"""Apply the reviewed OpenShift-only OCI image writer adaptation."""

from __future__ import annotations

import hashlib
import sys
from pathlib import Path


UPSTREAM_SHA256 = "d1862e24b744f39c7f3713cad1284d6920bb806badd0edd7ebd80ff0c5ef0d0a"
SOURCE_RELATIVE_PATH = Path("intrinsic/storage/artifacts/writer/oci_image.go")
UPSTREAM_BLOCK = '''\t// and now finally, unpack image, making it ready for containers
\tclientImage := containerd.NewImage(client, img)
\tif err = clientImage.Unpack(ctx, containerd.DefaultSnapshotter, containerd.WithSnapshotterPlatformCheck()); err != nil && !errdefs.IsAlreadyExists(err) {
\t\treturn fmt.Errorf("cannot unpack image %q: %w", imageName, err)
\t}
'''
OPENSHIFT_BLOCK = '''\t// OpenShift adaptation: this pod-local containerd is only an artifact content store.
\t// OpenShift workloads pull the OCI image from the project registry, so this
\t// sidecar does not need an unpacked runtime snapshot. Skipping the unpack
\t// avoids overlay mounts that the restricted pod cannot perform.
\treturn nil
'''
UPSTREAM_IMPORT = '\t"github.com/containerd/containerd"\n'
REGISTRY_SOURCE_RELATIVE_PATH = Path("intrinsic/storage/artifacts/artifacts.go")
REGISTRY_SOURCE_SHA256 = "2d716f9f0ed02544ca5b9fa831783a10281616a05abcde7c255e4c585adafb21"
REGISTRY_USERNAME_FLAG = '\tremoteUsername  = flag.String("registry_username", "", "Username to use for remote registry. If not set, workload identity is used")\n'
REGISTRY_PASSWORD_FLAG = '\tremotePassword  = flag.String("registry_password", "", "Password to use for remote registry.")\n'
REGISTRY_PASSWORD_FILE_FLAG = '\tregistryPasswordFile = flag.String("registry_password_file", "", "File containing the password to use for remote registry.")\n'
REGISTRY_MAIN_ANCHOR = "\tvar opts *artifacts.ServiceOptions\n\n\tif writeToRemoteRegistry {"
REGISTRY_MAIN_BLOCK = (
    '\tvar opts *artifacts.ServiceOptions\n\n'
    '\tif *registryPasswordFile != "" {\n'
    '\t\tpassword, err := os.ReadFile(*registryPasswordFile)\n'
    '\t\tif err != nil {\n'
    '\t\t\tlog.ExitContextf(ctx, "cannot read registry password file: %v", err)\n'
    '\t\t}\n'
    '\t\t*remotePassword = strings.TrimSpace(string(password))\n'
    '\t\tif *remotePassword == "" {\n'
    '\t\t\tlog.ExitContextf(ctx, "registry password file is empty")\n'
    '\t\t}\n'
    '\t}\n\n'
    '\tif writeToRemoteRegistry {'
)
UNREDACTED_LOG = 'log.InfoContextf(ctx, "starting server with following options: %#v", opts)'
REDACTED_LOG = 'log.InfoContext(ctx, "starting artifact service")'
UPSTREAM_REGISTRY_BACKEND_RELATIVE_PATH = Path("intrinsic/storage/artifacts/internal/registry.go")
UPSTREAM_REGISTRY_BACKEND_SHA256 = "de9f3e765a7ee2683ba14907bb2330f3cd50b4e56bdb5bc187c9673d7da6d897"
UPSTREAM_REGISTRY_PREFLIGHT = "\treturn result, result.validatePermissions(ctx)"
OPENSHIFT_REGISTRY_PREFLIGHT = '''\t// OpenShift adaptation: catalog listing is cluster-wide and is not granted by
\t// the project-scoped image-builder role. Actual repository reads and writes
\t// still use the configured registry credentials.
\treturn result, nil'''
PREVIOUS_OPENSHIFT_PARSE_REFERENCE = '''func (o *ociRegistry) parseAsReference(imageName string) (name.Reference, error) {
\t// OpenShift adaptation: OMTS sends upstream's node-local ArtifactService ref.
\t// Store it under the configured project registry and return that portable ref.
\tconst upstreamLocalRegistry = "localhost:17127/"
\tif strings.HasPrefix(imageName, upstreamLocalRegistry) {
\t\timageName = o.baseRegistry.String() + "/" + strings.TrimPrefix(imageName, upstreamLocalRegistry)
\t}
\timgRef, err := name.ParseReference(imageName)
\tif err != nil {
\t\treturn nil, fmt.Errorf("cannot parse %q: %w", imageName, err)
\t}
\tif !strings.HasPrefix(imgRef.String(), o.baseRegistry.String()) {
\t\treturn nil, ErrRepositoryMismatch
\t}
\treturn imgRef, nil
}'''
UPSTREAM_PARSE_REFERENCE = '''func (o *ociRegistry) parseAsReference(imageName string) (name.Reference, error) {
\timgRef, err := name.ParseReference(imageName)
\tif err != nil {
\t\treturn nil, fmt.Errorf("cannot parse %q: %w", imageName, err)
\t}
\tif !strings.HasPrefix(imgRef.String(), o.baseRegistry.String()) {
\t\treturn nil, ErrRepositoryMismatch
\t}
\treturn imgRef, nil
}'''


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: patch_oci_image.py <pinned-intrinsic-core-source-root>", file=sys.stderr)
        return 2

    source_path = Path(sys.argv[1]) / SOURCE_RELATIVE_PATH
    source = source_path.read_text()
    if "OpenShift adaptation: this pod-local containerd is only an artifact content store." not in source:
        digest = hashlib.sha256(source_path.read_bytes()).hexdigest()
        if digest != UPSTREAM_SHA256:
            print("Refusing to patch an unrecognized upstream oci_image.go source.", file=sys.stderr)
            return 1
        if source.count(UPSTREAM_BLOCK) != 1 or source.count(UPSTREAM_IMPORT) != 1:
            print("Refusing to patch: expected upstream image-unpack source was not unique.", file=sys.stderr)
            return 1
        source = source.replace(UPSTREAM_BLOCK, OPENSHIFT_BLOCK, 1)
        source = source.replace(UPSTREAM_IMPORT, "", 1)
        source_path.write_text(source)

    registry_path = Path(sys.argv[1]) / REGISTRY_SOURCE_RELATIVE_PATH
    registry_source = registry_path.read_text()
    if "registry_password_file" not in registry_source:
        registry_digest = hashlib.sha256(registry_path.read_bytes()).hexdigest()
        if registry_digest != REGISTRY_SOURCE_SHA256:
            print("Refusing to patch an unrecognized upstream artifacts.go source.", file=sys.stderr)
            return 1
        if registry_source.count(REGISTRY_USERNAME_FLAG) != 1 or registry_source.count(REGISTRY_PASSWORD_FLAG) != 1:
            print("Refusing to patch: expected remote registry flags were not unique.", file=sys.stderr)
            return 1
        if registry_source.count(REGISTRY_MAIN_ANCHOR) != 1 or registry_source.count(UNREDACTED_LOG) != 1:
            print("Refusing to patch: expected ArtifactService startup blocks were not unique.", file=sys.stderr)
            return 1
        registry_source = registry_source.replace('\t"os"\n', '\t"os"\n\t"strings"\n', 1)
        registry_source = registry_source.replace(REGISTRY_PASSWORD_FLAG, REGISTRY_PASSWORD_FLAG + REGISTRY_PASSWORD_FILE_FLAG, 1)
        registry_source = registry_source.replace(REGISTRY_MAIN_ANCHOR, REGISTRY_MAIN_BLOCK, 1)
        registry_source = registry_source.replace(UNREDACTED_LOG, REDACTED_LOG, 1)
        registry_path.write_text(registry_source)

    registry_backend_path = Path(sys.argv[1]) / UPSTREAM_REGISTRY_BACKEND_RELATIVE_PATH
    registry_backend_source = registry_backend_path.read_text()
    if "OpenShift adaptation: OMTS sends upstream's node-local ArtifactService ref." in registry_backend_source:
        if registry_backend_source.count(PREVIOUS_OPENSHIFT_PARSE_REFERENCE) != 1:
            print("Refusing to restore an unrecognized OpenShift registry reference parser.", file=sys.stderr)
            return 1
        registry_backend_source = registry_backend_source.replace(PREVIOUS_OPENSHIFT_PARSE_REFERENCE, UPSTREAM_PARSE_REFERENCE, 1)
        registry_backend_path.write_text(registry_backend_source)

    if "OpenShift adaptation: catalog listing is cluster-wide" not in registry_backend_source:
        registry_backend_digest = hashlib.sha256(registry_backend_path.read_bytes()).hexdigest()
        if registry_backend_digest != UPSTREAM_REGISTRY_BACKEND_SHA256:
            print("Refusing to patch an unrecognized upstream registry backend source.", file=sys.stderr)
            return 1
        if registry_backend_source.count(UPSTREAM_REGISTRY_PREFLIGHT) != 1:
            print("Refusing to patch: expected upstream registry permission preflight was not unique.", file=sys.stderr)
            return 1
        registry_backend_source = registry_backend_source.replace(UPSTREAM_REGISTRY_PREFLIGHT, OPENSHIFT_REGISTRY_PREFLIGHT, 1)
        registry_backend_path.write_text(registry_backend_source)

    print("Applied OpenShift-only OCI unpack, service-account registry auth, and registry preflight adaptations to pinned Core source.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
