# OMTS OpenShift build/deployer experiment — superseded

**Do not run `build-tool-image.sh` or `deploy-simulation.sh` from this
prototype.** OpenShift Build 13 failed during a CycloneDDS CMake action, and
this packaging has not deployed a solution. Its `Containerfile.toolchain`
builds several OMTS targets, copies the full source tree and Bazel cache into
an image, then invokes Bazel again from a different UBI release. That adds
work and a second toolchain boundary without improving fidelity to upstream.

## Selected path

Keep Intrinsic Core and its 22 ready Deployments as the current baseline. Use
the pinned OMTS release, commit `8253cdfdd173da9d5b7c9bb7b8cee817f66902ae`,
in the tools-only x86-64 Linux workspace defined in
[`omts-workspace/`](../omts-workspace/README.md), with a persistent volume for
source, temporary build files, and Bazel cache. Build and run in that same
toolchain; do not build OMTS into a long-lived runner image. The exact build
failure must be diagnosed before another full target is run.

Once the focused CycloneDDS action passes, use the upstream commands with only
the verified OpenShift boundary adaptations: route gRPC through the dedicated
in-mesh Gateway, replace the upstream K3s/containerd direct image upload with
the project internal registry and its service-account token, and exclude the
real robot endpoint config from the simulation artifact. Pass simulation mode
as the upstream runtime flag; do not add a custom simulation build setting.
Any source-access workaround or dependency checksum correction is recorded
separately from the OpenShift adaptation.

The upstream operation order is:

1. `bazel run //:omts_solution --config=lab_bb_01 -- --address=<gateway>:80 --operation_mode=sim`
2. `bazel run //tools/world:apply_scene_updates -- --address=<gateway>:80 --reset_sim`
3. Register the pinned pose estimator with `//tools/pose_estimation:register_using_train_service`.
4. `bazel run //src:omts_app -- --address=<gateway>:80 --config=configs/lab_bb_01/app_config.yaml --num_cycles=1`

The commands are a proposed runbook, not yet verified on OpenShift. Require a
successful solution start and generated resource/skill lifecycle before
continuing to the one-cycle app run. Keep all execution in simulation mode and
verify the private viewer separately through a loopback-bound `oc
port-forward`. See the ordered gates in [OPENSHIFT_PLAN.md](../../../OPENSHIFT_PLAN.md).
