# OpenShift demo dependency ladder

This is the critical path for the `lab_bb_01` simulation demo. Kubernetes
Services use TCP; the API calls listed below are gRPC over TCP unless noted.
Check live Service ports and ChartAssignment conditions before each run. A
running Pod alone does not pass a layer.

| Layer | Services and transport | Gate before moving up | dev01 observation (2026-10-08) |
| --- | --- | --- | --- |
| 0. Workspace ingress | `intrinsic-grpc-gateway`: Service TCP 80 (workspace client), target 443 (Gateway TLS / HTTP/2 gRPC); `image-registry.openshift-image-registry.svc:5000`: OCI registry HTTPS | Workspace can call Core through the Gateway and authenticate to registry | OMTS workspace is running; solution images uploaded successfully and use project-registry references |
| 1. Artifact storage | `artifacts-deployment`: gRPC TCP 8080, metrics TCP 9101; remote writes to project registry | ArtifactService is Ready; one image upload is visible by digest in the project registry | ArtifactService Pod is 3/3 Running and listens on 8080; OMTS upload completed |
| 2. Core control plane | `content-addressable-storage` gRPC 9747; `hot-shared-state` gRPC 9777; `operations` gRPC 8080; `resource-registry` gRPC 8080; `skill-registry` gRPC 8080; `runtime-db` TCP 8080; `proto-registry` gRPC 8080; `solution-service` gRPC 8082; `solution-deployment-v1` gRPC Service 8080 -> target 9769; `workcell-cluster-service` gRPC 9957 | Core service Pods Ready and `intrinsic-base` ChartAssignment Ready / ResourceSet Settled | `intrinsic-base` generation 4 is Settled; base services needed by the demo are available |
| 3. Solution deployment | `asset-artifacts-v1`, `asset-deployment`, `asset-instances-v1` gRPC 8080; Workcell deploy path above; namespaced ChartAssignment controller | New solution-generated ChartAssignments use project-registry image refs and their ResourceSets settle | `resources` generation 10, `skills` generation 3, and `intrinsic-app-chart` generation 10 are Ready; generated image refs use the project registry |
| 4. Simulation runtime | `simulation-service` gRPC/TCP 8088; `rs-icon` gRPC/TCP 9090 and TCP 9091; `rs-ur-module` gRPC/TCP 9090; OpenShift adapter declares the seven upstream Gazebo gRPC ports on headless `simulation-server` for Istio mTLS | Runtime Pods Ready; ICON reports the HWM active and remains connected to `ur_module` after reset; Gazebo stays healthy; inference has a GPU | Gazebo 2/2, ICON 3/3, UR 2/2, gripper 2/2, and motion planner 3/3 are Running. Inference is 3/3 Ready with an A10G GPU; the RViz/noVNC viewer is 1/1 Running |
| 5. Scene and app | `world` Service TCP 8080/8082; `world-updater` TCP 8080; scene update and solution clients use Gateway gRPC | Apply all four `configs/lab_bb_01/*.updates.pbtxt` files in the documented order, verify expected scene, then complete one bounded simulation-only app cycle including `move_to_contact` | The documented scene updates and simulator reset succeeded; ICON returned to `kMotionEnabled`. A one-cycle app reached motion planning, then stopped on collision between `gripper.gripper_finger2` and `raw_stock_2x3x5.base_link`. The pick did not complete |

## Current controlling blocker

The deployment, project-registry image path, runtime dependencies, inference,
scene reset, and namespace-local viewer are working. The remaining demo gate is
successful completion of the pick cycle. The motion planner stops on the
gripper/stock collision also observed on AWS; this is a known upstream behavior,
not an OpenShift deployment failure.

## Recovery order

1. Keep the private viewer available with the loopback-only port-forward in
   [`viewer/README.md`](deployment/viewer/README.md).
2. Before another app run, apply the documented `lab_bb_01` scene updates and
   verify ICON reconnects and reports `kMotionEnabled` after reset.
3. Run and record one simulation-only app cycle. Track the known collision
   separately; do not claim a completed pick until `move_to_contact` completes
   and the simulator state changes.
