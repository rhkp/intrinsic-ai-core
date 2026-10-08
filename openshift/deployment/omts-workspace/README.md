# Pinned OMTS build workspace

This is the next deployment gate for dev01. It creates a small tools-only image
and one restricted x86-64 workspace pod. The 200 GiB `gp3-csi` PVC retains the
pinned public source checkout, Bazel cache, and temporary build files across
pod restarts. The image build does **not** compile OMTS or package a runner.

All commands explicitly target `arhkp-intrinsic`; verify the current cluster
is dev01 before running them. The build context is an allowlist of scripts,
configuration, and pinned patches, so local `.env`, credentials, and unrelated
working-tree files are excluded. Source is cloned anonymously from the pinned
upstream tag. The workspace's `omts-deployer` service-account token is mounted
by Kubernetes and used only for project-scoped image publishing; no long-lived
registry credential is created. The short-lived Docker auth file is stored in
the pod's ephemeral `/tmp`, never on the persistent build volume. The workspace joins the Service Mesh so its OMTS gRPC client can present
workload identity to the internal project Gateway, whose listener requires
`ISTIO_MUTUAL`. The service port remains 80 and targets the Gateway's TLS port
443. Public Bazel dependency access goes through the mesh egress path and is
verified during workspace bring-up.

If the ArtifactService rejects OCI uploads with `failed to unpack image` and
`operation not permitted`, follow the pinned, OpenShift-only
[ArtifactService adaptation](../artifacts-service/README.md). The root cause is
the upstream overlayfs unpack running against the restricted pod-local
containerd sidecar, not a failed workspace PVC or Bazel cache.

Build the small tool image and wait for its OpenShift Build to complete:

```sh
bash openshift/deployment/omts-workspace/build-workspace-image.sh
```

If the workspace already exists, the build script restarts it after a
successful image push so it picks up the new tag digest.

Create the PVC and workspace Deployment after the image tag exists:

```sh
bash openshift/deployment/omts-workspace/apply-workspace.sh
```

Enter the persistent workspace:

```sh
oc rsh -n arhkp-intrinsic deploy/omts-build-workspace
```

Inside the pod, first prepare the anonymous pinned checkout and inspect the
resolved CycloneDDS target. Run only that target with verbose Bazel failures
before building the OMTS solution. Stop and diagnose the actual CMake failure
if it recurs; do not start the full action graph until the focused target
passes. The ordered simulation gates are documented in
[the deployment plan](../../../OPENSHIFT_PLAN.md#current-phased-execution-plan).

## Select the matching cell for world updates

The OpenShift solution is built with `--config=lab_bb_01`. The upstream
`apply_scene_updates` executable has its own default list, which targets the
`omts` CNC cell; the Bazel build setting does not change that runtime default.
Run the updater from the OMTS source root and pass the `lab_bb_01` files
explicitly:

```sh
cd "${INTRINSIC_OMTS_SRC:-${HOME}/intrinsic-omts}"
./bazel-bin/tools/world/apply_scene_updates \
  --address="${INTRINSIC_GATEWAY_ADDRESS}" \
  --files \
  configs/lab_bb_01/ur_module.attachments.updates.pbtxt \
  configs/lab_bb_01/scene.updates.pbtxt \
  configs/lab_bb_01/align_robot.updates.pbtxt \
  configs/lab_bb_01/orbbec_gemini.updates.pbtxt \
  --reset_sim
```

## Publish and deploy the simulation

The OpenShift workspace must create its ephemeral registry auth and CA files
before running OMTS. This uses the mounted `omts-deployer` token and does not
store credentials on the persistent PVC. Run the simulation from the pinned
source checkout:

```sh
cd "${INTRINSIC_OMTS_SRC:-${HOME}/intrinsic-omts}"
registry_ca_bundle="$(python3 /opt/intrinsic/repo/openshift/deployment/omts-runner/prepare-internal-registry-auth.py)"
export SSL_CERT_FILE="${registry_ca_bundle}"
bazel run //:omts_solution --config=lab_bb_01 -- \
  --address="${INTRINSIC_GATEWAY_ADDRESS}" --operation_mode=sim
```

The OpenShift ArtifactService remote-registry backend publishes generated
images to `image-registry.openshift-image-registry.svc:5000/${OPENSHIFT_NAMESPACE}`
using its own projected `omts-deployer` token. The workspace auth helper also
prepares the fallback Core image transferer. Confirm new ChartAssignments
reference the project registry and their runtime pods settle before proceeding
to scene updates.

After a simulator reset, verify ICON has reconnected to `ur_module` and reports
the HWM active before starting the one-cycle app. On dev01, reset recreated
`ur_module.sock` as UID/GID `1001690000` with mode `0755`; ICON runs as UID 0
but the OpenShift SCC drops `DAC_OVERRIDE`, so the shared group cannot reconnect.
The OpenShift adapter requests that capability for the ICON container only.
The controller adaptation is deployed on dev01. After the documented scene
reset, ICON reconnected to `ur_module` and returned to `kMotionEnabled`; repeat
that check after each future reset before running the app.

The one-cycle app is confirmed to connect through inference and reach motion
planning. The planner reports a collision between
`gripper.gripper_finger2` and `raw_stock_2x3x5.base_link`, matching the known
AWS behavior; the pick does not complete. The dev01 RViz/noVNC viewer and its
loopback-only port-forward are documented in
[`../viewer/README.md`](../viewer/README.md).
