# Enhancement backlog

> [!NOTE]
> This project was developed with assistance from AI tools.

This document records high-impact improvements identified during the repository
review on 2026-10-19. Work these items one at a time, in priority order unless
an operational incident changes the current blocker.

## Current validation baseline

The following commands pass at the time this backlog was created:

```sh
python3 -m unittest discover -s openshift -p 'test_*.py'
python3 openshift/validate_source_manifest.py
cd openshift/deployment/chartassignment-controller && go test ./... && go vet ./...
gofmt -l openshift/deployment/chartassignment-controller
find openshift -type f -name '*.sh' -print0 | xargs -0 -n1 bash -n
```

Also verified: `gofmt` reported no files, 156 upstream copies and 50 adaptation
files passed digest verification, and all Python scripts compiled successfully.

## E1 — Add one-command validation and CI

**Priority:** 1

**Problem:** Validation is manual and scattered across Python, Go, shell, YAML,
and provenance checks. There is no tracked CI workflow, Makefile, Python
project configuration, or pre-commit configuration.

**Improvement:** Add a single `make validate` target and CI workflow that run
the current baseline plus YAML linting and secret scanning.

**Implementation notes**

- Add a root `Makefile` with focused targets such as `test-python`, `test-go`,
  `lint-go`, `lint-shell`, `lint-yaml`, `validate-provenance`, and `validate`.
- Add CI that executes `make validate` on every pull request.
- Prefer dependency-free or repository-local checks first; avoid introducing a
  broad toolchain dependency for the initial version.
- Include a secret scanner and configure it to reject credential-shaped strings
  while allowing the sanitized examples in `.env.sample`.

**Acceptance criteria**

- A contributor can run one command from the repository root to run all offline
  validation.
- CI fails on Python, Go, shell, YAML, provenance, formatting, or secret-scan
  failures.
- The workflow does not require cluster credentials or network access to a live
  OpenShift cluster.

## E2 — Centralize environment and image configuration

**Priority:** 2

**Problem:** Target values are spread across `.env`, YAML placeholders, shell
`sed` substitutions, and Go constants. The controller contains hard-coded
namespace, registry, storage, GPU, and digest placeholders, while image data is
also represented in `openshift/image-lock.json`.

**Improvement:** Define one validated target-configuration model, make the image
lock the sole source of immutable image identity, and migrate every consumer
through a behavior-preserving, offline-verifiable rollout.

### E2 objectives

- Keep one authoritative source for non-secret target values.
- Eliminate independent namespace, registry, storage, GPU, ingress, and image
  digest edits.
- Make invalid or partially rolled-out configuration fail before any workload
  reconciliation.
- Preserve the current workload image set and rendered resources during the
  first rollout.
- Keep real cluster values, credentials, and API URLs out of Git.

### E2 non-goals

- Do not move Secret values into the central configuration.
- Do not make configuration hot-reload in the first implementation.
- Do not change chart semantics, workload placement, image builds, or routing
  behavior while introducing the configuration model.
- Do not replace the image provenance lock with mutable tags.

### E2 baseline inventory (before migration)

Before E2, configuration spanned these surfaces:

| Surface | Examples | Risk |
| --- | --- | --- |
| Local environment | `OPENSHIFT_CONTEXT`, `OPENSHIFT_NAMESPACE`, `OPENSHIFT_API_SERVER_URL`, storage, GPU, registry, PVC, and ingress values in `openshift/.env.sample` | Values can drift between commands |
| Controller constants | Namespace-, registry-, storage-, GPU-, and digest-containing constants in `controller/openshift.go` | Rebuild required; easy to miss one reference |
| Controller environment | routing, storage, RHOAI, and Zenoh values read at reconcile time | Missing values can fail only after a chart update |
| Image lock | Quay images, project images, and an OpenShift reference in `openshift/image-lock.json` | Multiple representations of the same destination reference |
| Manifest templates | `${OPENSHIFT_NAMESPACE}`, `${OPENSHIFT_STORAGE_CLASS}`, and `${PVC_SIZE}` placeholders | `sed` substitutions are not schema-validated |
| Operational scripts | defaults such as `your-project-namespace` | Wrong target can pass silently if a default is accepted |
| Controller deployment | watch namespace and immutable controller image digest | Must change atomically with target configuration |

The E2.1 consistency gap (resolved):
`openshift/image-lock.json` contains an `openshift_image_reference` for the
resource-registry image, but `validate_rendered_manifests.py` currently builds
its accepted reference set only from `images[].quay_repository`,
`images[].quay_digest`, and `project_images`. The validator must include every
authorized destination representation before configuration centralization begins.

### E2 canonical model

Use a checked-in sanitized example and an ignored local file:

- `openshift/config.example.json` — sanitized, schema-valid template.
- `openshift/config.local.json` — ignored file containing approved real values.
- `openshift/config.schema.json` — machine-readable schema.
- `openshift/render_config.py` — validates configuration and emits generated
  artifacts deterministically.

The local file should contain both local-only safety values and deployment
values. The renderer should emit a cluster-facing configuration document that
contains only deployment values, so local context and API URL are not mounted
into workloads.

Checked-in model (real values remain in ignored local configuration):

```json
{
  "schema_version": 1,
  "local": {
    "context": "replace-with-your-context-name",
    "api_server_url": "https://api.example.invalid:6443"
  },
  "target": {
    "namespace": "your-project-namespace",
    "registry": {
      "quay_prefix": "quay.io/your-registry-org/intrinsic",
      "quay_org": "your-registry-org",
      "internal_host": "image-registry.openshift-image-registry.svc:5000"
    },
    "storage": {
      "block_class": "your-block-storage-class",
      "shared_filesystem_class": "your-shared-filesystem-storage-class",
      "pvc_size": "20Gi"
    },
    "scheduling": {
      "gpu_taint_key": "your-gpu-taint-key"
    },
    "networking": {
      "ingress_address": "intrinsic-grpc-gateway.{namespace}.svc.cluster.local:80",
      "ingress_gateway": "{namespace}/intrinsic-grpc-internal",
      "ingress_pod_selector": {
        "app.kubernetes.io/name": "intrinsic-grpc-gateway",
        "istio": "intrinsic-grpc-gateway"
      },
      "zenoh_router_endpoint": "zenoh-router.{namespace}.svc.cluster.local:7447",
      "rhoai_triton_endpoint": "triton-rhoai-grpc.{namespace}.svc.cluster.local:9000",
      "mesh_namespace": "your-mesh-namespace",
      "mesh_revision": "your-service-mesh-control-plane"
    },
    "images": {
      "lock": "image-lock.json"
    }
  }
}
```

Keep `image-lock.json` separate from target configuration. It is provenance and
immutable identity; namespace and registry prefix are target values. The
renderer should compose full image references from:

```text
<registry prefix or internal host>/<namespace>/<repository basename>@<digest>
```

The schema version 2 lock stores repository basenames and digests. The renderer
and controller compose destination references from the canonical target. Old
schema version 1 locks and repeated destination identity fields are rejected.

### E2 incremental delivery plan

Use four independently shippable milestones. Do not start a milestone until the
previous milestone's exit gate passes. Each milestone is safe to stop after, and
each has its own rollback boundary.

#### Milestone E2.1 — Establish a no-change safety net

**Status:** complete on 2026-10-19.

**Goal:** make the current behavior measurable without changing it.

**Resolved baseline finding (2026-10-19):** the offline audit found that
`openshift/deployment/quay-image-values.yaml` pinned the resource-registry image
to a digest that differed from `openshift/image-lock.json`. The image lock is
authoritative, the generated values file was regenerated, and strict audit now
requires the two to match.

1. Add a config audit that reports every:
   - environment variable.
   - hard-coded target placeholder.
   - hard-coded workload image reference.
   - image-lock destination reference.
   - controller deployment image and watch namespace.
2. Extend `validate_rendered_manifests.py` to accept the resource-registry
   `openshift_image_reference` from the lock.
3. Add golden tests for:
   - current image values generated by `render_quay_image_values.py`.
   - current controller image substitutions.
   - current storage, routing, Zenoh, and RHOAI adaptations.
4. Record the current rendered output for every available offline
   ChartAssignment fixture.

**Exit gate:**

- All current Python, Go, formatting, shell, and provenance checks pass.
- The config audit reports every active target and image value.
- Golden tests capture the current generated and rendered outputs.
- No runtime behavior or manifest changes are included.

**Rollback:** revert the audit and tests only; no cluster change is required.

#### Milestone E2.2 — Add canonical configuration beside the current path

**Status:** complete on 2026-10-19.

**Goal:** introduce the authoritative model and renderer while the existing
environment and controller behavior remain active.

1. Add `config.schema.json` and `config.example.json`.
2. Add strict validation for:
   - JSON schema and unknown fields.
   - Kubernetes namespace syntax.
   - HTTPS API URL.
   - registry prefix and internal registry host syntax.
   - storage-class names.
   - Kubernetes storage quantity for PVC size.
   - GPU taint-key syntax.
   - ingress Service address and port 80.
   - Gateway `namespace/name` form.
   - nonempty ingress pod selector.
   - Zenoh and RHOAI endpoint syntax.
3. Add cross-field invariants:
   - ingress Service namespace equals `target.namespace`.
   - Gateway namespace equals `target.namespace`.
   - internal project image repositories compose with `target.namespace`.
   - all workload image references resolve through the image lock.
4. Add a `--allow-placeholders` mode only for the checked-in example and tests;
   local runtime configuration must reject placeholder values.
5. Implement `render_config.py` to emit:
   - `openshift/.env` for backward-compatible local tools.
   - a cluster-facing `target-config.json`.
   - a ConfigMap containing only deployment values.
   - `deployment/quay-image-values.yaml`.
   - controller deployment namespace and checksum patches.
6. Sort keys and output deterministically.
7. Add golden-output tests for every generated artifact.
8. Keep generated output byte-for-byte equivalent to current artifacts for the
   sanitized example wherever possible; enumerate intentional differences.

**Exit gate:**

- One command validates the local config and image lock without a cluster
  connection.
- One command deterministically regenerates all compatibility artifacts.
- The current controller still uses its existing environment path.
- Generated `.env`, image values, and manifests match the golden baseline.

**Rollback:** stop using the renderer and revert its generated artifacts; the
existing environment path remains untouched.

#### Milestone E2.3 — Move the controller to validated configuration

**Status:** complete on 2026-10-19.

**Implementation status (2026-10-19):** the offline controller path is implemented
and tested. The typed loader normalizes image identity, rejects placeholder and
drifted lock data, preserves the legacy override map exactly, and checks all
controller-consumed environment values for conflicts. The renderer delivers the
target config and image lock through one read-only ConfigMap, and its controller
patch rolls on the combined checksum. The offline manifest validator now composes
references with the same normalized model. All available renderer fixtures pass
through canonical configuration with zero golden-output change.

The controlled cluster rollout and post-rollout checks in steps 12–17 passed
on 2026-10-19. This completes E2.3; E2.4 and the broader model-lifecycle and
rollback-rehearsal work remain separate.

**Controlled rollout evidence (2026-10-19)**

- Fixed two remaining image-selection paths: Resource Registry and Workcell
  now take their deployment images from the canonical image model. Missing
  canonical destinations fail rather than falling back to placeholder images.
  Focused regression tests cover both selection and missing-image failures.
- Rendered all four live ChartAssignments through canonical and compatibility
  configuration: all 223 resources were identical. Server-side dry runs also
  reported zero application-template, image-reference, or PVC-specification
  changes.
- Applied the target ConfigMap first, then rolled only the controller image and
  configuration mounts. The rollout used one replica, `maxSurge: 0`, and
  `maxUnavailable: 1` to prevent overlapping controllers without leader election.
- Triggered no-op reconciliation for all four ChartAssignments. All four are
  Ready and all four ResourceSets are Settled. Application pod identities and
  templates were preserved; all active project pods are Ready.
- All 10 original PVCs retain their identities, specifications, and Bound
  status. No application uninstall, PVC deletion, cache cleanup, or OMTS rebuild
  was performed. The existing persistent workspace and cached executables were
  reused.
- The strict-mTLS gateway smoke passed five pings and removed its temporary
  resources. A separate temporary pod verified private-registry pulls under the
  application service account with an assigned non-root UID; it was removed.
- Both RHOAI models reported Ready, and synthetic RF-DETR inference passed
  through the existing wrapper before and after rollout. The RHOAI predictor's
  GPU device query passed.
- Ran the existing cached one-cycle demo. The first attempt reached RHOAI but
  stopped on an unreachable pre-grasp target. After applying the four documented
  cell-specific scene files and resetting the simulator, the retry completed a
  real RHOAI FoundationPose request in 8.184 seconds with one output, then stopped
  on the previously documented `gripper.gripper_finger2` versus
  `raw_stock_2x3x5.base_link` collision. This reproduces the prior checkpoint;
  it does not establish a completed pick cycle. Collision resolution remains E5.
- The 44 Python tests, Go tests, `go vet`, deterministic configuration checks,
  formatting, and provenance verification passed. Provenance covers 156
  upstream copies and 61 adaptation-file entries.

Live settings stay in ignored `openshift/config.local.json` and
`openshift/image-lock.local.json`. Generated rollout artifacts and the exact
previous-controller rollback patch are retained privately in ignored
`openshift/.generated/`; raw diagnostic evidence stays outside Git. Rollback
was prepared but was not exercised against the healthy deployment.

The existing Service Mesh control-plane revision remains a build-time
compatibility value in E2.3. The rollout preserved it; migrate that remaining
consumer when completing E2.4.

**Goal:** make the controller consume the canonical config and normalized image
model while preserving the exact current workload output.

1. Add a typed controller configuration loader for:
   - storage classes.
   - GPU taint key.
   - routing.
   - Zenoh endpoint.
   - RHOAI endpoint.
   - image override map.
2. Add an in-memory normalized image model containing:
   - chart image key.
   - repository basename.
   - immutable digest.
   - optional source image.
   - optional OpenShift destination name.
3. Build the controller's override map from that model instead of Go constants.
4. Compose project image references from target namespace and internal registry
   host.
5. Update the rendered-manifest validator to use the same normalized model.
6. Load the cluster-facing config and normalized image lock before the
   controller manager starts.
7. Fail startup on missing, malformed, placeholder, or conflicting values.
8. Keep the existing environment path temporarily as a compatibility mode:
   - if no config file is supplied, use the current environment behavior.
   - if both are supplied, require equivalent values.
   - reject conflicts rather than choosing a winner.
9. Inject the config and image lock through read-only ConfigMap volumes.
10. Add a config digest annotation to the controller Deployment so a ConfigMap
   change causes an explicit rollout.
11. Render every available ChartAssignment fixture before and after the change.
    Require zero resource diffs except:
    - controller config annotation.
    - ConfigMap addition.
    - explicitly documented and reviewed metadata changes.
12. Apply the generated ConfigMap first.
13. Apply the controller Deployment patch and wait for a single-replica Ready
    rollout.
14. Trigger a no-op reconcile for each existing ChartAssignment.
15. Confirm every ChartAssignment remains Ready or Settled and every ResourceSet
    remains Settled.
16. Snapshot workload image references before and after; require no change.
17. Run gateway, image-pull, storage, GPU, and dependency-ladder checks through
    simulation runtime.

**Exit gate:**

- The controller starts only with complete, validated configuration.
- The normalized lock produces exactly the current image references.
- Offline rendering is behavior-preserving apart from documented metadata.
- The controlled cluster rollout changes no workload image or rendered resource
  except config-delivery metadata.
- Existing ChartAssignments and ResourceSets remain Ready or Settled.

**Offline test gate:**

```sh
cd openshift/deployment/chartassignment-controller
GOCACHE=/private/tmp/intrinsic-go-cache go test ./...
GOCACHE=/private/tmp/intrinsic-go-cache go vet ./...
cd ../../..
python3 -m unittest discover -s openshift -p 'test_*.py' -v
python3 openshift/render_config.py \
  --config openshift/config.example.json --allow-placeholders --validate-only
python3 openshift/audit/audit_config.py
/usr/bin/git diff --check
```

The real target check remains `python3 openshift/render_config.py
--validate-only`; it intentionally fails when the ignored
`openshift/config.local.json` is absent or still contains placeholders.

**Rollback:** restore the previous controller image and Deployment, then
re-run no-op reconcile and smoke checks. Do not delete ChartAssignments,
ResourceSets, or workload resources.

#### Milestone E2.4 — Migrate consumers and remove compatibility

**Status:** complete and deployed on 2026-10-19.

**Completion evidence:**

- Operational preflight, routing, image rendering, manifest validation, smoke
  helpers, build/apply scripts, and ChartAssignment preflight consume validated
  canonical settings or generated Pod environment. Workstation commands bind to
  the exact approved context/API. Existing workspace PVCs are skipped on apply.
- Controller and offline render entry points require target configuration and
  the image lock. Storage, GPU taint, registry, mesh namespace/revision, ingress,
  Zenoh, RHOAI, and image destinations have no controller fallback constants.
- Portable image-lock schema version 2 removes repeated registry prefixes and
  destination references. Source tokens expand from the target. The controller
  image and 32 previously omitted dynamic demo images are now recorded by
  digest; all deployed application image references remain unchanged.
- Eight generated artifacts include the full controller Deployment, namespace
  RBAC, and workspace manifest. A namespace-only regression covers endpoint,
  registry, watch namespace, RBAC subject, and checksum coherence.
- CI runs canonical regression tests, strict target-default policy, shell
  syntax, Go checks, and provenance verification. The policy rejects concrete
  environment defaults as well as example placeholders in owned executable
  paths. Vendored source copies retain their separately verified adaptations.
- All 63 Python tests, Go tests, `go vet`, focused Ruff checks, shell syntax,
  `gofmt`, strict configuration audit, and provenance verification pass.
- All four charts rendered identically to the working baseline: 223 objects,
  zero differences. Server dry-run confirmed unchanged application templates
  and PVC specifications. The offline validator accepts all 223 objects and
  59 image references. Its UID check now recognizes only the two existing
  controller-approved realtime simulation exceptions; negative tests reject
  other identities, token automount, and extra capabilities.
- The controller-only rollout is Ready with the expected config checksum.
  All four ChartAssignments are Ready and four ResourceSets Settled. All 10
  original PVCs remain Bound with identical UIDs/specifications, and every
  application pod and workload specification is unchanged. Build caches remain
  intact; no application image rebuild or reinstall was performed.
- Rollback to the exact previous controller and schema was exercised, followed
  by no-op reconciliation. Both rollback and restoration preserved application
  and PVC identities. The final controller uses the completed portable lock.
- Gateway mTLS, private-image pull/startup as a non-root process, both RHOAI
  model readiness checks, and synthetic wrapper inference passed. Temporary
  smoke resources were removed.
- The explicit four-file cell reset succeeded. A one-cycle cached demo request
  completed FoundationPose on RHOAI in **8.060 seconds**, then reproduced the
  known gripper/stock collision. It had no connection failure, unreachable
  target, or timeout. The wrapper has no native Triton container. Full pick
  completion remains an E5 issue, outside configuration migration acceptance.

Live values, snapshots, detailed traces, and exact rollout/rollback patches
remain in ignored local files or private temporary evidence, never Git.

**Goal:** make the canonical config and image lock the only accepted sources
after the controller path is proven stable.

Migrate these consumers to the canonical config or generated artifacts:

- `openshift/configure_routing.py`.
- `openshift/preflight.py`.
- `openshift/validate_rendered_manifests.py`.
- `openshift/render_quay_image_values.py`.
- workspace build and apply scripts.
- OMTS runner scripts.
- resource-registry and adapted-image build scripts.
- ChartAssignment preflight.
- controller deployment and RBAC namespace rendering.

Remove silent defaults such as `your-project-namespace` from executable paths.
Local compatibility through generated `.env` is acceptable during migration, but
the generated file must not become a second hand-edited source.

Then:

1. Remove direct environment-only controller configuration.
2. Remove hard-coded target defaults from operational scripts.
3. Remove old full-reference image fields after the normalized lock schema is
   proven.
4. Add CI checks that reject new hard-coded target values in executable paths.
5. Add a dedicated test that changes only the namespace and proves every
   generated namespace-qualified reference changes coherently.
6. Run Python tests, Go tests, `go vet`, `gofmt`, shell syntax checks, and
   provenance verification.

**Exit gate:**

- Executable paths fail closed without canonical config.
- Changing the canonical target changes all generated outputs consistently.
- The canonical config and image lock are the only accepted sources for target
  and image identity.
- CI rejects reintroduced hard-coded target values in executable paths.

**Rollback:** re-enable the compatibility path from the previous controller
release, regenerate its artifacts, and repeat no-op reconcile plus smoke checks.

### E2 risk register

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Partial config rollout | Controller renders with stale values | Validate before manager startup; ConfigMap digest annotation; single-replica rollout |
| Invalid config reaches reconcile | ChartAssignment marked Failed or workloads replaced | Fail controller startup before reconciliation; no placeholder fallback |
| Image reference mismatch | Pods cannot pull or run the wrong image | Normalize lock, validator, and controller from one model; digest-only references |
| Namespace drift | RBAC, Services, image pulls, or routing target the wrong project | Cross-field validation; generated watch namespace and RBAC; no silent defaults |
| Storage-class drift | PVCs remain Pending | Preflight storage-class existence and mode before apply |
| GPU taint drift | Simulator or inference Pods remain unschedulable | Preflight taint and GPU resource availability before apply |
| Ingress drift | gRPC clients route to the wrong Service or Gateway | Keep existing strict address, namespace, Gateway, and selector validation |
| Zenoh or RHOAI endpoint drift | Skills or inference fail at runtime | Validate endpoint shape and verify live Service before rollout |
| Inline chart reconciliation | Adapter changes are overwritten or partially applied | Use the same render path offline and in-cluster; no-op reconcile before claiming success |
| Hot reload | Reconciliation sees mixed configuration generations | No hot reload in phase 1; restart controller atomically |
| Parser drift between Go and Python | Same config means different things | Shared schema and cross-language fixture tests |
| Test placeholders hide failures | Tests pass while local config is invalid | Separate example mode from runtime mode; reject placeholders locally |
| Secret leakage | Credentials enter config or Git | Config stores references only; secret scanner; Secret values remain out of band |
| Accidental mutable image use | Image behavior changes without provenance | Digest-only validator and lock normalization |

### E2 rollback plan

1. Preserve the previous controller image digest and Deployment manifest.
2. Preserve the previous generated ConfigMap and target configuration.
3. Roll back the controller Deployment first; do not delete ResourceSets or
   ChartAssignments.
4. Verify the previous controller is Ready.
5. Trigger a no-op reconcile and confirm workload images are unchanged.
6. Run gateway and image-pull smoke checks.
7. Only after stability, remove the new ConfigMap if it is no longer needed.

### E2 acceptance criteria

- One canonical local config and one image lock define all target and image
  identity used by executable paths.
- Configuration is validated in Python and Go before deployment or
  reconciliation.
- All workload images are digest-pinned and derived from the normalized image
  model.
- The first cluster rollout changes no workload image and no rendered resource
  except documented config-delivery metadata.
- Existing ChartAssignments and ResourceSets remain Ready or Settled after a
  no-op reconcile.
- Gateway, image-pull, storage, GPU, and simulation-runtime checks pass.
- Rollback to the previous controller and config is documented and tested.
- No real credentials, API URLs, private inventory, or raw logs are committed.

## E3 — Convert the demo dependency ladder into executable gates

**Priority:** 3

**Status:** implemented and verified on 2026-10-19.

- [validate_demo_gates.py](openshift/validate_demo_gates.py) implements phases
  1–6, exact canonical targeting, bounded/captured queries, fail-fast dependency
  handling, sanitized JSON and explicit application-action authorization.
- All 42 default read-only gates pass. Running Pods alone cannot pass: checks
  require current workload ownership/generations/revisions, Ready containers,
  valid endpoint identities/ports and current settled ResourceSets.
- The explicit application phase verified scene/config hashes, reset all four
  documented files, checked ICON recovery and robot placement, then reproduced
  the known collision with a nonzero result. RHOAI completion is independently
  reported and was rechecked against that cycle's live logs after correcting
  the deployed completion-record format. Full pick completion remains E5.
- All original application workloads, 10 PVCs and build caches were preserved.
  The command installs nothing and does not rebuild application artifacts.
- All 94 Python tests, Go tests/vet, Ruff lint/format, strict configuration
  audit and provenance checks pass.
- The offline suite covers action isolation, stale readiness, resource identity,
  image-lock drift, GPU absence, safe reporting, changed scene files, timeouts,
  process cleanup and collision classification. Existing CI discovers these tests.

See [the executable ladder](openshift/DEMO_DEPENDENCY_LADDER.md#executable-acceptance-command)
for the commands, evidence contract and runtime limits.

**Problem:** The ladder in `openshift/DEMO_DEPENDENCY_LADDER.md` is clear but
manual. A running Pod is not currently sufficient to prove each gate.

**Improvement:** Add a phase-based, read-mostly acceptance CLI that executes
each documented gate and emits structured pass/fail evidence.

**Implementation notes**

- Add a command such as `python3 openshift/validate_demo_gates.py`.
- Implement phases for:
  1. workspace ingress and registry authentication.
  2. ArtifactService readiness and upload visibility.
  3. core control-plane readiness.
  4. solution deployment and ResourceSet settlement.
  5. simulator, ICON, UR, gripper, and GPU readiness.
  6. scene reset and bounded one-cycle application execution.
- Return a nonzero exit code when any selected phase fails.
- Emit JSON with sanitized evidence: gate name, status, age, relevant
  condition, and a safe failure reason.
- Support `--through-phase`, `--phase`, and `--json` options.
- Never print API URLs, identity, tokens, raw logs, or cluster-specific
  capacity.

**Acceptance criteria**

- The command can reproduce every non-destructive gate in the dependency ladder.
- It fails when a documented gate is not met, even if Pods are Running.
- Its JSON output is suitable for attachment to a run record.
- The destructive or application-acting final phase requires an explicit flag.

## E4 — Refactor the OpenShift controller adapter

**Priority:** 4

**Problem:** `controller/openshift.go` is approximately 3,000 lines and covers
image replacement, storage, scheduling, routing, NetworkPolicy, inference,
pose estimation, simulator IPC, and other concerns.

**Improvement:** Split the adapter into focused, independently tested packages
or files while preserving external behavior.

**Suggested split**

- image overrides and registry identity.
- storage and PVC adaptations.
- GPU scheduling and tolerations.
- gateway and VirtualService routing.
- NetworkPolicy and DNS rewriting.
- inference and pose-estimator patches.
- simulator IPC, affinity, and security context.
- RBAC and namespace-scope validation.

**Implementation notes**

- Do this only as a behavior-preserving refactor.
- Move existing tests alongside each extracted adapter.
- Add table-driven tests for any currently implicit configuration failures.
- Keep the public `Adapt...` entry points stable until all consumers are
  verified.

**Acceptance criteria**

- No adapter file is responsible for more than one major concern.
- Existing controller tests pass without weakening assertions.
- New tests cover each extracted adapter’s validation and error paths.
- `go test`, `go vet`, and `gofmt -l` remain clean.

## E5 — Instrument and resolve the demo collision

**Priority:** 5

**Status:** application collision resolved and repeat simulation verified on
2026-10-20; read-only trace export and offline comparison implemented.

- Executive evidence corrected the earlier stage assumption: picking succeeded;
  the first failure was the loading exit after releasing the workpiece.
- Added explicit post-release vertical clearance before lateral transit at the
  machine and infeed exits, using the configured approach offset. Contact-escape
  exclusions are scoped to the gripper/workpiece pair. Scene geometry, force
  thresholds, controller and deployment specifications are unchanged.
- Two independently reset configured simulated cycles completed with RHOAI
  inference. The repeat passed all 52 gates; an independent SDK trace confirmed
  all five subtrees and the operation succeeded.
- All 10 PVCs, workload specifications, Pod identities and caches were preserved.
  The patch and both build/source-preparation paths are tracked.
- Added a pinned/idempotent workspace patch helper, safe trace exporter and
  offline comparison with planner/model identities and numeric state deltas.
- No fresh AWS run, sampled contact-command stream, measured contact points,
  or actual simulator jaw feedback is claimed. These remain diagnostic follow-ups;
  the trace explicitly marks uncollected feedback.

See [collision diagnosis, commands and evidence](openshift/COLLISION_DIAGNOSTICS.md).

**Problem:** The controlling application gate is the gripper/stock collision
observed during the one-cycle simulation. Reproducing the same failure does not
establish demo parity.

**Improvement:** Build a bounded collision-reproduction and diagnostic workflow
that captures comparable evidence on AWS and OpenShift.

**Implementation notes**

- Capture a sanitized trace for each failed attempt:
  - planner request and result.
  - gripper joint states.
  - collision object and contact geometry.
  - `move_to_contact` command stream.
  - simulator state immediately before and after failure.
  - relevant component readiness and connection state.
- Add a fixture-based offline parser if logs are exported rather than streamed.
- Compare AWS and OpenShift traces for planner version, scene state, collision
  model, timing, and gripper state.
- Keep any motion, geometry, or collision-rule change separate from deployment
  adaptation and record its rationale.

**Acceptance criteria**

- One command exports a collision trace without credentials or raw system logs.
- The trace identifies the exact collision pair, planner stage, and state delta.
- AWS and OpenShift can be compared with the same report format.
- A completed one-cycle simulation is distinguished clearly from a reproduced
  known failure.

## E6 — Complete the RHOAI inference migration safely

**Priority:** 6

**Problem:** The wrapper adapter is deployed and real demo inference reaches
RHOAI/KServe. Model artifact identity, lifecycle, security and rollback still
need the additional evidence described below.

**Improvement:** Finish the remaining model identity, lifecycle, security, and
application gates for the deployed wrapper integration.

**Implementation notes**

- Verify the RHOAI model repository against the pinned RF-DETR and
  FoundationPose release bundles, including file hashes and model versions.
- Define immutable snapshot publication, update, and rollback ownership.
- Prove the intended wrapper-to-RHOAI identity and token-rotation path.
- Enforce per-RPC deadlines and bounded request/response sizes.
- Run a real demo fixture and not only synthetic inputs.
- Preserve a tested one-command rollback to the native Triton path.
- Do not claim dynamic model load/unload parity until that lifecycle is
  explicitly tested.

**Acceptance criteria**

- Model artifact identity, runtime compatibility, authorization, deadlines, and
  request limits are all evidenced.
- A real demo fixture passes through RHOAI.
- Rollback to the native endpoint is tested and does not mutate the RHOAI
  model repository.
- The existing known-good RHOAI deployment remains intact until all gates pass.

## E7 — Add simulator recovery and placement tests

**Priority:** 7

**Problem:** Simulator, ICON, UR module, and Unix-socket behavior depends on
co-location and shared filesystem permissions. Reset and node rescheduling are
therefore high-risk operational paths.

**Improvement:** Add repeatable recovery tests and alerts for simulator IPC
dependencies.

**Implementation notes**

- Test simulator restart, ICON reconnect, UR-module affinity, socket recreation,
  and stale-socket handling.
- Record socket owner, group, and permission expectations in a sanitized check.
- Add a readiness or diagnostic condition for stale IPC paths.
- Verify that required affinity does not accidentally create an unschedulable
  workload.
- Include node-failure or Pod-deletion recovery in a bounded, opt-in test mode.

**Acceptance criteria**

- A reset/recovery check can be executed with one command and a timeout.
- It detects stale sockets, disconnected components, and affinity failures.
- Recovery behavior is recorded without exposing raw logs or cluster details.

## E8 — Add operational observability

**Priority:** 8

**Problem:** Current validation relies heavily on manual inspection with
`oc logs`, `oc get`, and documentation updates.

**Improvement:** Add dashboards and alerts for migration-critical conditions.

**Suggested signals**

- controller reconcile errors and reconcile latency.
- ChartAssignment and ResourceSet failure states and settle latency.
- gateway gRPC failure rate and latency.
- simulator restarts and IPC disconnects.
- inference authorization failures and request latency.
- GPU allocation and memory utilization.
- image pull and registry authentication failures.

**Acceptance criteria**

- Each dashboard panel maps to an actionable failure mode.
- Alerts include a short runbook pointer.
- Metrics and labels do not expose credentials, API URLs, or private topology.
- Dashboards can be installed through the existing project-scoped deployment
  path.

## E9 — Track sanitized architecture decisions in Git

**Priority:** 9

**Problem:** The architecture directory is intentionally ignored. Important
architectural rationale and decision history are therefore outside version
control.

**Improvement:** Commit a sanitized, text-first architecture decision record
while leaving large binary decks and local working artifacts untracked.

**Implementation notes**

- Convert the current architecture review into short decision records.
- Record context, decision, alternatives, consequences, and evidence links.
- Keep exact hardware inventory, cluster addresses, and environment-specific
  values local.

**Acceptance criteria**

- The authoritative architectural decisions are tracked in Git.
- No private infrastructure or capacity details are committed.
- Binary decks remain optional presentation artifacts, not the only source of
  truth.

## E10 — Expand smoke and preflight test coverage

**Priority:** 10

**Problem:** The controller has substantial tests, but several operational
Python helpers are not directly tested and the general preflight primarily
prints observations rather than enforcing thresholds.

**Improvement:** Add deterministic tests using fake `oc` command results and
make preflight support explicit required gates.

**Implementation notes**

- Add fake command fixtures for context resolution, permission checks, JSON
  responses, and CLI failures.
- Cover `openshift/preflight.py`, gateway smoke, image-pull smoke, and routing
  helpers.
- Add a required-gate mode that exits nonzero when mandatory conditions fail.
- Keep output sanitized and machine-readable.

**Acceptance criteria**

- Operational Python helpers have unit tests that do not require a live
  cluster.
- Preflight can distinguish informational output from required gate failures.
- Simulated CLI errors and malformed responses produce safe, deterministic
  errors.

## Working rules

- Implement one enhancement at a time and keep unrelated changes out of the
  same patch.
- Add or update tests before changing behavior where practical.
- Re-run the relevant acceptance checks from this document before handoff.
- Update the status of an item only when its acceptance criteria are met.
- Do not commit credentials, API URLs, private inventory, raw system logs, or
  other environment-specific values.
