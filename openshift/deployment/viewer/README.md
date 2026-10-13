# OpenShift RViz and noVNC viewer
> [!NOTE]
> This project was developed with assistance from AI tools.

> Deployment examples use generic placeholders for cluster context, project namespace, registry, storage class, and GPU settings. Replace them with approved environment values before use; scripts may read `OPENSHIFT_CONTEXT`, `OPENSHIFT_NAMESPACE`, `OPENSHIFT_API_SERVER_URL`, and `OPENSHIFT_GPU_TAINT_KEY` from the environment.


This is a private viewer for the target cluster OMTS simulation. It packages the
upstream `viewer/workcell.rviz` setup in an OpenShift workload and points
`rmw_zenoh_cpp` at the `zenoh-router` Service in `your-project-namespace`. The ROS
desktop image is the official OSRF Lyrical desktop image pinned by digest.

Build and deploy from the repository root. The build script requires
`OPENSHIFT_API_SERVER_URL` and refuses to run unless it exactly matches the API
URL for the active context:

```sh
bash openshift/deployment/viewer/build-viewer-image.sh
oc apply -n your-project-namespace -f openshift/deployment/viewer/deployment.yaml
oc rollout status -n your-project-namespace deployment/intrinsic-rviz-novnc --timeout=10m
```

The deployment image is pinned in `deployment.yaml` and `openshift/image-lock.json`.
When rebuilding it, update both digests from the completed ImageStreamTag before
applying the manifest; a new build does not change a digest-pinned Deployment.

The Service is ClusterIP only; no Route is created. Forward it from the Mac on
loopback and open the noVNC page:

```sh
oc -n your-project-namespace port-forward --address 127.0.0.1 \
  svc/intrinsic-rviz-novnc 6080:6080
```

Open `http://127.0.0.1:6080/vnc.html?autoconnect=true&resize=scale`. The view
uses software OpenGL and reads `/workcell_markers` with transient-local
durability. The viewer runs as the namespace-assigned non-root UID with a
read-only root filesystem, ephemeral `/tmp`, no service-account token, no
service-mesh sidecar, and no public Route. The Service is ClusterIP only; use
the documented loopback port-forward to view the demo.
