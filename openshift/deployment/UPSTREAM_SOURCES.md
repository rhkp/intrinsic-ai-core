# Pinned upstream source inventory

This is the provenance-tracked copy of deployment-specific upstream assets for the OpenShift implementation. The registry publisher and resource/skill image-pull identity are the first adaptations; most copied files are still not OpenShift-adapted or ready to apply. Our OpenShift renderer and lifecycle will become the source of truth after adaptation and validation.

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

The reference deployment flow is the pinned [Getting Started guide](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/developer_resources/learn/tutorials/getting_started.md). It deploys Core to local K3s and OMTS through the Core gateway. This repository will replace those K3s/controller assumptions with project-scoped OpenShift resources and documented build/deploy/verify/cleanup commands.
