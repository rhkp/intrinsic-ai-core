# OpenShift demo dependency ladder

This is the critical path for the `lab_bb_01` simulation demo. Kubernetes
Services use TCP; the API calls listed below are gRPC over TCP unless noted.
Check live Service ports and ChartAssignment conditions before each run. A
running Pod alone does not pass a layer.

| Layer | Services and transport | Gate before moving up | dev01 observation (2026-10-07) |
| --- | --- | --- | --- |
| 0. Workspace ingress | `intrinsic-grpc-gateway`: Service TCP 80 (workspace client), target 443 (Gateway TLS / HTTP/2 gRPC); `image-registry.openshift-image-registry.svc:5000`: OCI registry HTTPS | Workspace can call Core through the Gateway and authenticate to registry | OMTS workspace is running; solution images uploaded successfully and use project-registry references |
| 1. Artifact storage | `artifacts-deployment`: gRPC TCP 8080, metrics TCP 9101; remote writes to project registry | ArtifactService is Ready; one image upload is visible by digest in the project registry | ArtifactService Pod is 3/3 Running and listens on 8080; OMTS upload completed |
| 2. Core control plane | `content-addressable-storage` gRPC 9747; `hot-shared-state` gRPC 9777; `operations` gRPC 8080; `resource-registry` gRPC 8080; `skill-registry` gRPC 8080; `runtime-db` TCP 8080; `proto-registry` gRPC 8080; `solution-service` gRPC 8082; `solution-deployment-v1` gRPC Service 8080 -> target 9769; `workcell-cluster-service` gRPC 9957 | Core service Pods Ready and `intrinsic-base` ChartAssignment Ready / ResourceSet Settled | `intrinsic-base` generation 4 is Settled; base services needed by the demo are available |
| 3. Solution deployment | `asset-artifacts-v1`, `asset-deployment`, `asset-instances-v1` gRPC 8080; Workcell deploy path above; namespaced ChartAssignment controller | New solution-generated ChartAssignments use project-registry image refs and their ResourceSets settle | `resources` generation 7, `skills` generation 3, and `intrinsic-app-chart` generation 7 are Settled; generated image refs use the project registry |
| 4. Simulation runtime | `simulation-service` gRPC/TCP 8088; `rs-icon` gRPC/TCP 9090 and TCP 9091; `rs-ur-module` gRPC/TCP 9090; simulator service `simulation-server` intentionally has no declared Service ports | Runtime Pods Ready; ICON reports the HWM active and remains connected to `ur_module` after reset; Gazebo stays healthy; inference has a GPU | Gazebo 2/2, ICON 3/3, and UR 2/2 are Running together on a GPU node. Inference has the correct template but is Pending: all five cluster GPU slots are occupied |
| 5. Scene and app | `world` Service TCP 8080/8082; `world-updater` TCP 8080; scene update and solution clients use Gateway gRPC | Apply all four `configs/lab_bb_01/*.updates.pbtxt` files in the documented order, verify expected scene, then complete one bounded simulation-only app cycle including `move_to_contact` | Not run. The simulation retry was stopped before the scene update and app-cycle flow; end-to-end behavior is unconfirmed |

## Current controlling blocker

The registry-reference gate is cleared: OMTS uploads completed and generated
ChartAssignments reference images in the `arhkp-intrinsic` project registry.
The current blocker is scheduler capacity. Five GPUs are allocated cluster-wide;
the demo inference pod needs an additional GPU. After capacity is available,
verify inference readiness before moving to the scene and app layer.

## Recovery order

1. Free or provision one additional GPU slot without evicting unrelated
   workloads; confirm the inference pod schedules and becomes Ready.
2. Verify all runtime dependencies, including ICON/UR connectivity after
   reset, and check the expected scene resources.
3. Apply the `lab_bb_01` scene updates; verify ICON/UR socket connectivity after
   reset.
4. Run and record one simulation-only app cycle. Do not claim an end-to-end
   pass until `move_to_contact` completes and the simulator state changes.
