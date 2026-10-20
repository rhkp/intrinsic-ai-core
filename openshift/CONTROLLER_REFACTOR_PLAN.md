# E4 controller refactor plan

> [!NOTE]
> This project was developed with assistance from AI tools.

## Status and outcome

Implemented and deployed. The main adapter decreased from 3,080 to 157 lines;
domain policy files remain in the existing package. All 120 Go test results, vet
and 115 Python tests pass. Both offline renderer commands match all 223 complete
reference objects across four assignments. All 50 original test declarations and
155 original production declarations are unchanged; resource orchestration alone
changed to call two extracted helpers.

The controller-only rollout passed 42 read-only gates and a reset-based simulated
cycle passed all 52 gates, including fresh RHOAI inference and full application
completion. All original application Pods, 35 other Deployments, 13 StatefulSets,
75 Services, four assignments and ten Bound PVCs retained their identities and
specifications. E8 still scrapes 13 metric families and evaluates nine healthy
rules. Rollback artifacts are prepared; rollback was not exercised during E4.

The objective is to separate the OpenShift adapter into files with clear owners
while preserving the working demo, RHOAI integration and reconciliation behavior.
Use the existing `chartassignment` package. New packages, interfaces, dependency
injection frameworks and configuration redesign are unnecessary for E4.

## Inspected starting point

Repository baseline: `07b6b6a988f26c9c742d8855886b81997cca4f7e`.
The worktree was clean before writing this plan.

- [openshift.go](deployment/chartassignment-controller/controller/openshift.go)
  contained 3,080 lines at the reference commit. Its resource and Pod adaptation functions sequence the
  domain transformations; helper functions implement the actual policies.
- [openshift_test.go](deployment/chartassignment-controller/controller/openshift_test.go)
  contained 2,100 lines and 44 named tests at the reference commit. This count is a source inventory,
  not a coverage measurement or a fresh test result.
- [openshift_ros_zenoh.go](deployment/chartassignment-controller/controller/openshift_ros_zenoh.go)
  already separates 367 lines of ROS, protobuf and Zenoh handling. Keep it intact
  during the initial split.
- [release.go](deployment/chartassignment-controller/controller/release.go)
  runs adaptation after Helm expansion and before Synk apply. Both offline
  renderer commands call the exported `RenderOpenShiftResources` entry point.
- [config.go](deployment/chartassignment-controller/controller/config.go)
  owns package-global `controllerConfig`. Tests replace it and restore it through
  cleanup; [legacy_expectations_test.go](deployment/chartassignment-controller/controller/legacy_expectations_test.go)
  initializes defaults in `TestMain`. No controller tests currently call
  `t.Parallel`. Keep this lifecycle and serial execution unchanged.
- [the Dockerfile](deployment/chartassignment-controller/Dockerfile) copies the
  full module and builds the controller command, rather than naming one Go file.
  The [existing CI workflow](../.github/workflows/canonical-config.yml) formats
  `controller/openshift*.go` and runs module tests/vet, so appropriately named
  extracted files are already included.
- [the configuration audit](audit/audit_config.py) discovers production files
  broadly, but its additional `defaultImageOverrides` / `os.Getenv` prohibition
  is restricted to the filename `openshift.go`. That guard must cover the new
  production adapter files before policy code moves.
- [ADAPTATIONS.json](deployment/ADAPTATIONS.json) records the main adapter and
  test file in multiple entries. Update every affected digest and register every
  new owned file. Do not rewrite historical evidence links pinned to commits.

## Behavior contract

| Area | Required invariant |
|---|---|
| Entry points | Keep exported and internal entry-point names, signatures, CLI flags and callers stable. |
| Configuration | Same schema, image model, namespace checks and canonical-only behavior; no new environment fallback or hot reload. |
| Transformation order | Preserve resource pre-scanning, deep copy, exclusions, RBAC validation, reference rewriting, networking, Pod adaptation, rollout strategy, sidecar handling, final security checks and PVC append order. |
| Objects | Identical object sequence and identities, field values, numeric types, list ordering, omitted fields and explicit nulls. No extra resource or missing object. |
| Error behavior | Same accepted/rejected inputs, error wrapping and retry classification. Do not convert malformed input into a successful empty result. |
| Input ownership | Preserve deep-copy boundaries and caller inputs, including when adaptation fails partway through. |
| RHOAI | Same endpoint, token projection, wrapper command, embedded patch bytes, disabled shared memory and removal of the legacy Triton sidecar. |
| Security | Same identities, token automount exceptions, capability/image allowlists, project-only RBAC, host-access rejection and final validation. |
| Networking | Same mesh enrollment, sidecar exceptions, strict mTLS, gateway routing, DNS/NetworkPolicy peers and ports. |
| Storage and placement | Same PVC names/specifications, host-path replacements, Recreate strategy, GPU tolerations and simulator affinity. |
| Lifecycle | No changes to Synk, watches, finalizers, retries, concurrency or ChartAssignment/ResourceSet ownership semantics. |

Preserve function bodies, embedded strings, constants and initializers during
the file-move stages. Move domain constants with their functions; do not alter
values, evaluation order or shared map semantics. Retain existing parameter
names even where a parameter appears unused. Renaming misleading historical
helpers, eliminating globals and fixing unrelated bugs are separate changes.

### Assumptions explicitly challenged

1. **Passing unit tests alone proves equivalence:** insufficient. Also compare
   reference/candidate rendering of the same complete inputs and embedded scripts.
2. **Only the base chart matters:** insufficient. Include the application chart
   and current resource/skill ChartAssignments plus synthetic rejection fixtures.
3. **A controller-only rollout affects only its Pod:** incorrect. Startup can
   reconcile every watched assignment. A live gate must compare all managed
   workload templates and verify that application Pods were not replaced.
4. **All adapters should be idempotent:** not established. Preserve current
   second-pass outcomes; assert idempotence only for helpers that already promise
   it. Do not add blanket idempotence as a new behavior requirement.
5. **A generic normalization can hide differences:** risky. Preserve all fields,
   types and nested list ordering. E4.0 execution found that `decodeManifests`
   iterates a Helm manifest map, so even two unchanged-reference CLI runs produce
   different top-level resource order. Compare complete CLI objects by unique
   API-version/kind/namespace/name identity, rejecting duplicates. Separately
   assert adapter ordering for a fixed input, including generated PVC order.
   Do not change the existing decoder merely to stabilize this refactor's check.
6. **Tests can run in parallel after splitting files:** incorrect while the
   package-global test configuration is shared. Keep tests serial.
7. **Old deployment README commands are current instructions:** incorrect. Use
   generated canonical configuration and image-lock artifacts. Do not deploy
   static template manifests or stale digests from historical examples.

## Proposed file ownership

All paths below are relative to
`openshift/deployment/chartassignment-controller/controller/`. Keep the public
package unchanged. Names may be refined before their extraction; each declaration
must have exactly one destination in the E4.0 symbol inventory.

| File | Ownership and representative functions |
|---|---|
| `openshift.go` | Resource orchestration: `adaptOpenShiftResources`, `adaptOpenShiftResourcesWithRouting`; preserve dispatch and error boundaries. |
| `openshift_pod.go` | Pod adaptation sequencing: `adaptPodSpec`; preserve container/init-container traversal and write-back. |
| `openshift_images.go` | Locked image lookup: `imageOverrides`, `configuredDeploymentImage`, `imageRepositoryName`. |
| `openshift_storage.go` | `configuredStorageClass`, `sharedStoragePVC`, `adaptPVC`, `adaptKnownHostPath`, `adaptPersistentDeploymentStrategy`; generated PVC construction. |
| `openshift_scheduling.go` | `configuredGPUTaintKey`, `ensureNvidiaGPUToleration`, `requiresGPUCoLocation`, `adaptICONPodAffinity`. |
| `openshift_rbac.go` | Role/binding allowlists, scoping and validation; preprovisioned Workcell RBAC handling. |
| `openshift_references.go` | Namespace-reference rewriting and resource-kind/reference rejection. |
| `openshift_services.go` | `adaptService`, exposed simulator/world ports and their service-address rewrites. |
| `openshift_routing.go` | Routing configuration/validation, gateway backend discovery, sidecar enablement, ingress and VirtualService adaptation. |
| `openshift_networkpolicy.go` | NetworkPolicy adaptation, gateway peers/ports, DNS fallback and mesh control-plane egress. |
| `openshift_inference.go` | RHOAI endpoint getter, inference/pose-estimator adaptation, their constants and embedded patch programs. |
| `openshift_artifacts.go` | ArtifactService registry/containerd adaptation and its constants. |
| `openshift_security.go` | Simulation security policies, identity/capability rules, realtime bounds and final security-context validation. |
| `openshift_deployments.go` | Reviewed special Deployment adaptation, including Jupyter writable home and loopback binding. |
| `openshift_unstructured.go` | Small existing structural helpers: slices, selectors and container argument/environment updates; no policy decisions. |
| Existing `config.go` | Configuration loading and validation; preserve behavior and global ownership. |
| Existing `openshift_ros_zenoh.go` | Existing ROS/Zenoh/protobuf transformations; relocate the existing Zenoh endpoint/argument helpers here when safe. |

This is a responsibility map, not a target file count. Keep tightly coupled
helpers together; avoid tiny files solely to hit a line-count limit. Do not
create a second miscellaneous policy file. The two orchestration files may call
multiple domains, but the policy implementations belong to their domain files.

## Execution stages and exit gates

### E4.0 — Freeze the reference and evidence

- Confirm a clean worktree and record the actual implementation starting commit.
- Verify that the deployed controller digest, canonical lock and recorded source
  provenance agree with the reference. A clean Git tree alone does not establish
  that the cluster runs that source. Resolve any mismatch before using it as the
  known-good rollout baseline.
- Resolve `target.images.lock` through the canonical loader. Local configuration
  can select an ignored copy instead of the checked-in lock. E4 release checks
  caught the local copy retaining the previous controller digest; both copies
  were aligned and the deployed ConfigMap/checksum verified against the canonical
  renderer. Do not assume that editing the checked-in lock updates every target.
- Build both offline renderer commands from that unmodified tree. Store reference
  executables and raw outputs in ignored/private storage, not another source
  worktree. Continue editing the main workdir.
- Record compiler/module versions, input/config/image-lock hashes and the exact
  render command. Use the same inputs and toolchain for reference and candidate.
- Inventory every function, type, constant, variable, initializer and embedded
  script in the affected files; map each to one destination.
- Capture existing test names and test results before changing tests. A baseline
  failure blocks extraction until understood; do not weaken or skip assertions.
- Capture read-only live rollout/assignment state and original workload/PVC/Pod
  identities before any eventual deployment. Keep addresses, IDs and raw evidence
  private. Snapshot current inline ChartAssignments for offline comparison.

**Exit:** a reproducible reference exists independently of the edited source.
Prior evidence of 223 objects is contextual; record the current actual count and
require reference/candidate equality rather than hard-coding the old number.

### E4.1 — Establish guards before moving policies

- Extend the filename-specific audit to all owned production `openshift*.go`
  adapter files, excluding tests. Do not prohibit the deliberately supported
  environment-compatibility validation in `config.go`.
- Add focused coverage that proves a forbidden fallback in an extracted file is
  detected; keep current audit acceptance behavior otherwise unchanged.
- Add missing characterization coverage for input non-mutation, resource/PVC
  ordering and partial-failure handling. Existing fixtures/goldens are the
  baseline; do not regenerate them to accept a candidate difference.
- Add before/after fixtures for the invariant matrix below. Introduce tests and
  guard changes separately from mechanical production code moves.

**Exit:** the safeguards fail on deliberately changed fixtures/policies and pass
against the unmodified adapter. No production behavior change.

### E4.2 — Extract structural and project policies

Move one responsibility at a time: structural helpers/images, storage,
scheduling, RBAC and namespace references. Leave orchestration bodies intact.
Move associated constants and comments with the owned declarations. Retain the
existing test file until production extraction is stable.

**Per-extraction exit:** declaration equivalence, focused tests, full module
tests/vet, unchanged reference renders and matching provenance digests.

### E4.3 — Extract services and networking

Move service adaptation, gateway/VirtualService routing and NetworkPolicy/DNS
handling as separate reviewed steps. Keep gateway backend discovery before
per-object mutation and preserve dedicated gateway and mesh selector checks.

**Exit:** unchanged render output and rejection outcomes for base, application,
resource and skill charts; networking/security negative fixtures still reject.

### E4.4 — Extract deployment, security and inference policies

Move reviewed Deployment adaptations, ArtifactService, simulation/final security
handling, and inference/pose-estimator patches, one concern at a time. Inference
moves last because it is the working RHOAI integration boundary. Keep existing
ROS/Zenoh code intact except for relocating its already-existing helpers.

**Exit:** byte-identical embedded patch strings; unchanged wrapper topology,
token handling, CAS settings, simulator identities/capabilities, IPC mounts,
Jupyter binding and ArtifactService behavior in reference/candidate renders.

### E4.5 — Finish orchestration and organize tests

- Only after file moves pass, extract remaining inline RBAC normalization and
  generated PVC construction into domain helpers if needed. Preserve expressions,
  order, errors and the existing boolean storage-needs contract. These are
  explicit function extractions, reviewed separately from pure file moves.
- Split `openshift_test.go` by the same responsibilities without renaming test
  functions or removing cases. Put shared constructors/configuration helpers in
  one test helper file. Preserve `TestMain`, cleanup and serial execution.
- Keep end-to-end adapter contract tests in `openshift_test.go`; do not turn them
  into mocks of the helpers whose integration they currently exercise.
- Update current source links and all affected adaptation digests. Retain pinned
  historical links. Existing CI filename patterns should include the new files;
  verify that fact rather than adding an unrelated CI redesign.

**Exit:** all original tests remain discoverable with unchanged assertions;
additional validation/error coverage exists where absent; no declaration is lost
or duplicated; only documented helper extraction changes the declaration bodies.

### E4.6 — Build and controlled live confirmation

This stage follows the offline gates; it is not part of planning.

1. Confirm an idle executive and healthy baseline. Privately capture the current
   controller digest/specification and exact canonical ConfigMap/image-lock bytes.
2. Build only the controller image using the current build path. Record its
   immutable digest. Preserve dependency versions and application image digests.
3. Render the narrowly changed controller artifacts. The only intended runtime
   changes are controller image identity and corresponding canonical lock/config
   delivery metadata. Assert all application image destinations and policy values
   are unchanged. Do not apply a full application bundle.
4. Roll out the single controller workload and observe reconciliation of all
   four existing assignments, not just the base chart. Allow new settled
   ResourceSet versions; require unchanged managed workload specifications,
   application Pod identities and original PVC identities/specifications.
5. Run the 42-gate read-only demo acceptance. Then explicitly reset/run a bounded
   simulated cycle using the existing runner, verifying terminal success and
   actual RHOAI inference. Preserve all PVCs and caches.
6. Check the E8 observer still collects successfully and its Prometheus target
   and rules remain healthy. Do not create synthetic notifications to others.

**Exit:** current ChartAssignments Ready, latest ResourceSets Settled, no
application Pod replacement caused by the refactor, all original PVCs Bound and
unchanged, full configured simulated cycle completed through RHOAI, rollback
artifacts available and final source/image identity recorded.

## Required verification matrix

| Area | Evidence required before deployment |
|---|---|
| Mechanical moves | Compare parsed Go declarations before/after, ignoring source positions and imports. Match receivers, signatures, bodies, constant/variable specs and embedded strings. Preserve initializer dependencies. |
| Complete render | Reference and candidate process the same four current inline assignments/config/lock. Compare complete objects by unique identity because Helm decoding order is nondeterministic; preserve all fields/types/nested list ordering and reject duplicates. Print only safe counts/hashes. |
| Portable fixtures | Existing inline/resource/skill golden output unchanged; an additional coherent synthetic namespace/config proves target references still follow canonical settings. |
| Mutation/order | Inputs unchanged after success/failure; output resource/list/PVC order unchanged; repeated-pass behavior matches the reference. |
| Fail closed | Missing/wrong namespace/config, nil resources, Secrets/Namespace/cluster-scoped kinds, unexpected RBAC, host access, unsupported simulation privileges/images, malformed/conflicting routing and network peers. Use one-fault fixtures for stable error comparisons. |
| Inference | Exact embedded patch hashes, token projection, endpoint, commands/volumes; legacy Triton container absent; shared memory remains disabled. |
| Stateful application | PVC specs, host-path mapping, deployment strategy, GPU placement, simulator identity and IPC behavior unchanged. |
| Build/tooling | Same Go dependencies; all commands compile; existing CI includes every new file; audit/provenance checks fail rather than bypass moved policy. |

During implementation, run from the controller module:

```sh
go test ./...
go vet ./...
gofmt -l controller/openshift*.go
```

Run the existing Python suite, focused audit tests, strict configuration audit,
canonical render check, source-manifest verification and staged AI-policy/leak
checks from the repository root. Capture test output privately if it includes
rendered fixtures. Do not treat cached test results as fresh verification; use
`go test -count=1 ./...` at the final offline gate. The declaration comparison is
a supplement to compilation and rendering, not a proof of semantic equivalence
by itself. Full raw manifests, tokens and infrastructure values stay out of Git.

## Stop and rollback rules

- Any unexplained render/error/embedded-script difference stops that extraction.
  Restore only that step from its saved checkpoint; keep unrelated work intact.
  Never refresh golden files merely to make a refactor pass.
- Missing baseline evidence, a failed prerequisite or an unhealthy demo is a
  blocker to rollout. Do not combine recovery of an existing incident with the
  refactor and then attribute its effects to file movement.
- No build dependency, runtime permission, namespace, model, mesh or storage
  change may be hidden inside E4. Record unrelated findings separately.
- If live reconciliation would change an application template or PVC, do not
  continue rollout. Restore the exact previous controller image and canonical
  lock/config together. An image-only rollback can leave mismatched config.
- If unexpected reconciliation has already changed managed objects, pause the
  candidate controller, capture private evidence and restore the known-good
  controller/config before narrowly recovering affected resources. Do not delete
  ChartAssignments, ResourceSets, PVCs, caches or the full deployment.
- A controller rollback cannot undo completed workload mutations. Re-run the
  readiness/preservation checks; require application evidence before declaring
  recovery successful.

## Completion record

Keep this checklist current as implementation proceeds:

- [x] E4.0 reference binaries, symbol map and evidence captured.
- [x] E4.1 guards and characterization coverage established.
- [x] E4.2 structural/project policies extracted and verified.
- [x] E4.3 services/networking extracted and verified.
- [x] E4.4 deployments/security/inference extracted and verified.
- [x] E4.5 orchestration/test organization and provenance complete.
- [x] E4.6 controller-only rollout and complete RHOAI demo verified.
- [ ] Final leak/AI-policy checks and human review complete before merging.

Use small reviewable checkpoints. A source-only refactor can be reported complete
offline while live confirmation is pending, but E4 deployment acceptance remains
open until its live gates pass. No guarantee is inferred from file organization
or a Running controller alone.

## Recorded release and evidence

Controller image digest:
`sha256:ca0f1689ccc96a420247fde86673b403972aefdbb15176bd0eec8fbe0104f263`.
The checked-in image lock and the ignored lock selected by canonical configuration
agree with the deployed controller and its ConfigMap. Only the controller digest,
delivered lock and checksum changed. Three configuration goldens changed solely
for that release identity; the adapter/rendering golden manifests did not change.

Private reference binaries, input snapshots, results and the matching previous
controller/configuration rollback are retained under ignored
`openshift/.generated/e4/`. Keep those files private. They include target details
and must never be force-added to Git. The rollback script is prepared for this
specific controller/target and rejects a different target or subsequent release.
It was not run during successful E4 acceptance.
