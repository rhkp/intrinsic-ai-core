# OpenShift demo dependency ladder

> [!NOTE]
> This project was developed with assistance from AI tools.

This is the critical path for the configured simulation demo. Kubernetes
Services use TCP; the API calls listed below are gRPC over TCP unless noted.
Check live Service ports and ChartAssignment conditions before each run. A
running Pod alone does not pass a layer.

## Executable acceptance command

Use the validated, ignored `openshift/config.local.json` and portable image lock:

```sh
# Read-only by default: phases 1–5, no scene reset or application execution.
python3 openshift/validate_demo_gates.py
python3 openshift/validate_demo_gates.py --json

# Select one phase or run the dependency chain through a phase.
python3 openshift/validate_demo_gates.py --phase 4 --json
python3 openshift/validate_demo_gates.py --through-phase 5 --json

# Explicitly allow the documented simulated scene reset and one cached cycle.
python3 openshift/validate_demo_gates.py --through-phase 6 --allow-app-actions --json
```

| CLI phase | Checks |
| --- | --- |
| 1 — workspace | Current workspace rollout, dedicated mesh Gateway, an actual gRPC status response from the workspace, TLS-verified registry authentication and a manifest digest lookup |
| 2 — artifacts | ArtifactService ready endpoints backed by current workloads; visibility of an existing locked solution-image upload |
| 3 — core | Current base ChartAssignment, latest settled ResourceSet, required service ports and ready endpoint pods owned by current workloads |
| 4 — solution | Current app/resource/skill ChartAssignments and ResourceSets, asset service endpoints, immutable image-lock matches and backend READY status |
| 5 — runtime | Current simulator/ICON/UR/gripper/planner/wrapper rollouts, seven Gazebo ports, live ICON hardware state, RHOAI/KServe readiness, GPU device access, authenticated model readiness and wrapper metadata; no native Triton container |
| 6 — application | Fresh pre-action readiness, idle executive, exact simulation images, approved scene/config hashes, four-file reset, post-reset runtime/ICON readiness, robot alignment/attachments/frames, one bounded cached cycle, separately observed RHOAI inference and unchanged PVCs |

Phase numbers are one greater than the historical layer numbers below.
`--phase 6` always includes phases 1–5. Selecting phase 6 without
`--allow-app-actions` exits before accessing the cluster. A failed phase skips
later phases. The command never deploys, rebuilds, deletes PVCs, clears caches,
or cancels another executive operation.

The application uses the existing cached executables. `--cycle-timeout` accepts
30–600 seconds (default 180); a timeout fails the gate and terminates the local
launcher process group. It does not claim the backend operation was canceled;
the executive must be idle before another reset is allowed.

The JSON report includes gate name, phase, pass/fail/skipped status, observation
time, relevant condition, safe failure reason, and boolean/count evidence.
`age_seconds` is the age of the resource's last positive condition transition,
or `null` when no condition timestamp is available. It is not an expiration
policy. Reports exclude API URLs, namespace/context identities, credentials,
raw logs, resource UIDs and cluster capacity. Reports can be redirected to
ignored `openshift/.generated/` for a run record.

`application_cycle_complete` is true only for a successful bounded cycle.
`rhoai_demo_inference_observed` records a FoundationPose completion during that
cycle separately. A known collision remains a **failed cycle with exit code 1**,
even when deployment readiness and RHOAI inference pass. A read-only pass does
not establish pick-cycle success or execute synthetic inference.

### Latest verified automation result — 2026-10-20

The E5 application correction completed two independently reset configured
simulated cycles. The repeat passed all **52 gates**, including RHOAI completion
and unchanged PVCs. An independent SDK trace confirmed all five subtrees and
the executive operation `SUCCEEDED`. Workload/Pod identities, all 10 PVCs,
and caches were preserved. See [the collision runbook](COLLISION_DIAGNOSTICS.md).

### Earlier automation result — 2026-10-19

All 42 read-only gates passed. The explicitly enabled application run passed
reset, ICON recovery, scene alignment and PVC preservation, then reproduced the
known gripper/stock collision. RHOAI's completion-record parser was corrected
against the deployed `with N outputs` format and that same cycle's model
completion was rechecked read-only. The cycle did not complete. Application
workloads and all 10 PVCs/caches were preserved.

## Historical topology observations

The table records the earlier 2026-10-08 baseline. Current inference runs in
RHOAI; phase 5 checks the remote predictor and the wrapper's separated topology.

| Layer | Services and transport | Gate before moving up | target cluster observation (2026-10-08) |
| --- | --- | --- | --- |
| 0. Workspace ingress | `intrinsic-grpc-gateway`: Service TCP 80 (workspace client), target 443 (Gateway TLS / HTTP/2 gRPC); `image-registry.openshift-image-registry.svc:5000`: OCI registry HTTPS | Workspace can call Core through the Gateway and authenticate to registry | OMTS workspace is running; solution images uploaded successfully and use project-registry references |
| 1. Artifact storage | `artifacts-deployment`: gRPC TCP 8080, metrics TCP 9101; remote writes to project registry | ArtifactService is Ready; one image upload is visible by digest in the project registry | ArtifactService Pod is 3/3 Running and listens on 8080; OMTS upload completed |
| 2. Core control plane | `content-addressable-storage` gRPC 9747; `hot-shared-state` gRPC 9777; `operations` gRPC 8080; `resource-registry` gRPC 8080; `skill-registry` gRPC 8080; `runtime-db` TCP 8080; `proto-registry` gRPC 8080; `solution-service` gRPC 8082; `solution-deployment-v1` gRPC Service 8080 -> target 9769; `workcell-cluster-service` gRPC 9957 | Core service Pods Ready and `intrinsic-base` ChartAssignment Ready / ResourceSet Settled | `intrinsic-base` generation 4 is Settled; base services needed by the demo are available |
| 3. Solution deployment | `asset-artifacts-v1`, `asset-deployment`, `asset-instances-v1` gRPC 8080; Workcell deploy path above; namespaced ChartAssignment controller | New solution-generated ChartAssignments use project-registry image refs and their ResourceSets settle | `resources` generation 10, `skills` generation 3, and `intrinsic-app-chart` generation 10 are Ready; generated image refs use the project registry |
| 4. Simulation runtime | `simulation-service` gRPC/TCP 8088; `rs-icon` gRPC/TCP 9090 and TCP 9091; `rs-ur-module` gRPC/TCP 9090; OpenShift adapter declares the seven upstream Gazebo gRPC ports on headless `simulation-server` for Istio mTLS | Runtime Pods Ready; ICON reports the HWM active and remains connected to `ur_module` after reset; Gazebo stays healthy; inference has a GPU | Gazebo 2/2, ICON 3/3, UR 2/2, gripper 2/2, and motion planner 3/3 are Running. Inference is 3/3 Ready with an NVIDIA GPUs; the RViz/noVNC viewer is 1/1 Running |
| 5. Scene and app | `world` Service TCP 8080/8082; `world-updater` TCP 8080; scene update and solution clients use Gateway gRPC | Apply all four `configs/your-workcell/*.updates.pbtxt` files in the documented order, verify expected scene, then complete one bounded simulation-only app cycle including `move_to_contact` | The documented scene updates and simulator reset succeeded; ICON returned to `kMotionEnabled`. A one-cycle app reached motion planning, then stopped on collision between `gripper.gripper_finger2` and `raw_stock_2x3x5.base_link`. The full cycle did not complete |

## Resolved controlling blocker

E5 identified missing post-release vertical clearance at the loading and infeed
exits. Picking had succeeded in the earlier failed runs. The application patch
now completes the configured cycle with RHOAI inference; two reset-based runs
passed. A fresh AWS comparison and detailed contact telemetry remain separate
follow-ups. Historical observations above predate this correction.

## Recovery order

1. Keep the private viewer available with the loopback-only port-forward in
   [`viewer/README.md`](deployment/viewer/README.md).
2. Before another app run, apply the documented `your-workcell` scene updates and
   verify ICON reconnects and reports `kMotionEnabled` after reset.
3. Verify the pinned application patch, then run and record one configured
   simulation cycle using the explicit phase 6 command. Export a collision trace
   when any task fails; readiness alone does not prove cycle completion.
