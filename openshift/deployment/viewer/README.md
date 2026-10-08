# OpenShift RViz and noVNC viewer

This is a private viewer for the dev01 OMTS simulation. It packages the
upstream `viewer/workcell.rviz` setup in an OpenShift workload and points
`rmw_zenoh_cpp` at the `zenoh-router` Service in `arhkp-intrinsic`. The ROS
desktop image is the official OSRF Lyrical desktop image pinned by digest.

Build and deploy from the repository root. The build script refuses to run
unless the active cluster API identifies as dev01:

```sh
bash openshift/deployment/viewer/build-viewer-image.sh
oc apply -n arhkp-intrinsic -f openshift/deployment/viewer/deployment.yaml
oc rollout status -n arhkp-intrinsic deployment/intrinsic-rviz-novnc --timeout=10m
```

The deployment image is pinned in `deployment.yaml` and `openshift/image-lock.json`.
When rebuilding it, update both digests from the completed ImageStreamTag before
applying the manifest; a new build does not change a digest-pinned Deployment.

The Service is ClusterIP only; no Route is created. Forward it from the Mac on
loopback and open the noVNC page:

```sh
oc -n arhkp-intrinsic port-forward --address 127.0.0.1 \
  svc/intrinsic-rviz-novnc 6080:6080
```

Open `http://127.0.0.1:6080/vnc.html?autoconnect=true&resize=scale`. The view
uses software OpenGL and reads `/workcell_markers` with transient-local
durability. The viewer runs as the namespace-assigned non-root UID with a
read-only root filesystem, ephemeral `/tmp`, no service-account token, no
service-mesh sidecar, and no public Route. The Service is ClusterIP only; use
the documented loopback port-forward to view the demo.
