# Pod-local containerd endpoint for ArtifactService

The pinned upstream `artifacts-deployment` binary always creates a containerd
client during startup. Its chart supplies the node's K3s containerd socket, even
when the local registry listener is disabled. OpenShift has no compatible
containerd socket to mount, and its image publisher writes directly to the
project registry.

This small image runs an unprivileged containerd API endpoint in the same pod as
the upstream ArtifactService. It uses only pod-local storage, disables the CRI
plugin, and does not start workloads. The upstream `ArtifactServiceApi` and its
file store remain intact; the local registry port and node socket stay disabled.

Build it in the target project before applying the ChartAssignments:

```sh
oc apply -f openshift/deployment/manifests/artifacts-containerd-image-build.yaml
oc -n arhkp-intrinsic start-build artifacts-containerd \
  --from-dir=openshift/deployment/artifacts-containerd --follow
```

Record the resulting immutable project image reference in
`openshift/image-lock.json` and the controller's reviewed image constant before
rendering or applying the charts.
