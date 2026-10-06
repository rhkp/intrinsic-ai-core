# Pinned OMTS build workspace

This is the next deployment gate for dev01. It creates a small tools-only image
and one restricted x86-64 workspace pod. The 100 GiB `gp3-csi` PVC retains the
pinned public source checkout, Bazel cache, and temporary build files across
pod restarts. The image build does **not** compile OMTS or package a runner.

All commands explicitly target `arhkp-intrinsic`; verify the current cluster
is dev01 before running them. The build context is an allowlist of scripts,
configuration, and pinned patches, so local `.env`, credentials, and unrelated
working-tree files are excluded. Source is cloned anonymously from the pinned
upstream tag. The workspace's `omts-deployer` service-account token is mounted
by Kubernetes and used only for project-scoped image publishing; no long-lived
registry credential is created. The short-lived Docker auth file is stored in
the pod's ephemeral `/tmp`, never on the persistent build volume. The workspace
is outside the Service Mesh so Bazel can fetch public dependencies. Its OMTS
client connects only to the
internal project Gateway at port 80; that endpoint's mesh-to-backend policy was
verified by the generic gRPC smoke.

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
passes. The workspace's build scripts and the simulation command order are
documented in [the deployment plan](../../../OPENSHIFT_PLAN.md#ordered-execution-checklist-for-the-first-visible-demo).
