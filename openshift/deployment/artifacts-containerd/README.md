# Pod-local containerd endpoint for ArtifactService
> [!NOTE]
> This project was developed with assistance from AI tools.

> Deployment examples use generic placeholders for cluster context, project namespace, registry, storage class, and GPU settings. Replace them with approved environment values before use; scripts may read `OPENSHIFT_CONTEXT`, `OPENSHIFT_NAMESPACE`, `OPENSHIFT_API_SERVER_URL`, and `OPENSHIFT_GPU_TAINT_KEY` from the environment.


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
oc -n your-project-namespace start-build artifacts-containerd \
  --from-dir=openshift/deployment/artifacts-containerd --follow
```

Record the resulting immutable project image reference in
`openshift/image-lock.json` and the controller's reviewed image constant before
rendering or applying the charts.
