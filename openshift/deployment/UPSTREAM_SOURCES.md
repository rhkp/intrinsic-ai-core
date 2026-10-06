# Pinned upstream source inventory

This is the provenance-tracked copy of selected upstream assets used to test an
OpenShift port. The pinned upstream release remains the behavioral source of
truth. Prefer its released Core artifacts and documented OMTS Bazel workflow;
keep copied code and patches only where a verified OpenShift incompatibility
requires an adapter.

## Source pins

- `intrinsic-ai/intrinsic-core`, release `20260922.0`, commit `523f2e03ddf3cb46a19b15be5e81586ae2f538c2`
- `intrinsic-ai/intrinsic-omts`, release `20260922.0`, commit `8253cdfdd173da9d5b7c9bb7b8cee817f66902ae`

`SOURCE_MANIFEST.json` records every copied source path and source, baseline-copy, and current SHA-256. Source bytes are read from each checkout's pinned `HEAD`, not from modified checkout files.

## Copied scope

- Core Kubernetes deployment, chart application, value/rendering, workcell deployment, and asset resource/skill deployment code: `intrinsic/kubernetes/`, `intrinsic/assets/deploy/`, and `intrinsic_runtime/kubernetes/`.
- Core templates and their Bazel package definitions referenced by the pinned `intrinsic_base` chart.
- OMTS cell configuration assets under `configs/` (shared, OMTS, and lab BB-01 configs).
- Upstream `LICENSE` and `TRADEMARK.md` files where present; source headers are preserved.

The pinned copy now includes **129 Core files and 27 OMTS files** (156 upstream source entries). Two Core asset-instance files were added from the pinned commit to adapt the resource gRPC address through the OpenShift ingress configuration. Three Core image-publisher package files were added from the pinned commit for the first adaptation. 6 example email literal(s) in copied source were replaced with `example@example.invalid`; this changes no credentials or runtime identity.

## Exclusions

- `configs/lab_bb_01/ur_module_config.textproto` and `configs/omts/ur_module_config.textproto` are excluded because they contain fixed robot network addresses. Supply a reviewed, lab-local hardware configuration only if a real robot is needed; do not put that address in Git.
- Host setup scripts for K3s, NVIDIA Container Toolkit, and real-time kernel configuration are intentionally excluded; they configure Ubuntu hosts and are not an OpenShift deployment mechanism.
- Generated image tarballs, build outputs, vendored dependencies, and unrelated Core runtime/application source are not copied. Reuse upstream images/APIs where compatible; adapt the deployment layer here.

The reference deployment flow is the pinned [Getting Started guide](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/developer_resources/learn/tutorials/getting_started.md): deploy Core from its release artifact, then build and run OMTS through the Core gateway. The OpenShift port should preserve that application flow while replacing only verified K3s-specific operations such as direct node-containerd image loading or cluster-scoped permissions. It is not a goal to replace the upstream deployment model wholesale.
