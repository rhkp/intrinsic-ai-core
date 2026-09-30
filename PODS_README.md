# Deployed pods: quick reference

[Back to the deployment journal](README.md)

Snapshot checked on **2026-09-30**: **51 pods, all ready**. This describes our
`lab_bb_01` OMTS deployment in **simulation mode**, using Intrinsic Core and OMTS
release `20260922.0`. Readiness is a snapshot, not a guarantee of current health.

A pod runs one or more related containers. Kubernetes-generated name suffixes
are omitted below; resource pods retain their stable `-0` suffix. Skill-group
numbers are deployment groupings, and their contents can change after redeploying.

## Robot, simulation, and perception

Namespace: `app-resources` — **13 pods**.

| Pod | Purpose |
| --- | --- |
| `rs-gazebo-simulator-0` | Runs Gazebo: simulated robot, environment, physics, and sensors. |
| `rs-icon-0` | Intrinsic's robot controller; executes motion and reports robot state. |
| `rs-ur-module-0` | Connects ICON to the simulated UR3e robot through a Gazebo hardware adapter. |
| `rs-motion-planner-service-0` | Calculates robot paths while considering robot limits and collisions. |
| `rs-gripper-0` | Provides the gripper command API used by skills. |
| `rs-hande-gripper-0` | Runs the simulated Robotiq Hand-E gripper driver. |
| `rs-orbbec-camera-0` | Provides Intrinsic's camera API for image acquisition. |
| `rs-orbbec-gemini-driver-0` | ROS driver component for Orbbec Gemini camera integration; its presence does not imply a physical camera is connected. |
| `rs-inference-service-0` | Runs perception models using the GPU, including NVIDIA Triton. |
| `rs-pose-estimator-service-0` | Estimates an object's 3D position and orientation from camera observations. |
| `rs-train-service-0` | Registers/prepares object models for pose estimation. |
| `rs-calibration-service-0` | Supports calibration, such as locating the camera relative to the robot. |
| `rs-flowstate-ros-bridge-0` | Bridges Intrinsic world data into ROS, enabling the RViz workcell view. |

## Reusable skills

Namespace: `skills` — **8 pods**. These mappings reflect the containers present
in this deployment.

| Pod | Skills hosted |
| --- | --- |
| `skill-group-00` | Attach an object to the robot's world model; set digital outputs; estimate object pose from multiple views. |
| `skill-group-01` | Move the robot. |
| `skill-group-02` | Initialize calibration; update the world model. |
| `skill-group-03` | Clear the motion-planning cache; create a world-model object. |
| `skill-group-04` | Collect calibration data; read digital inputs; preplan motion; sample calibration poses. |
| `skill-group-06` | Calibrate camera-to-robot alignment; capture images. |
| `skill-group-07` | Detach a world-model object; command the gripper. |
| `skill-group-09` | Move until contact is detected. |

## Application execution

Namespace: `app-intrinsic-app-chart` — **4 pods**.

| Pod | Purpose |
| --- | --- |
| `executive` | Runs the application's behavior tree, coordinating skills and execution state. |
| `code-execution` | Runs application code; includes the code-execution service and a Jupyter server. |
| `gzserver` | Provides proxy APIs for simulation services; the actual simulator is `rs-gazebo-simulator-0`. |
| `proto-registry` | Supplies message-type definitions so services can interpret application and asset data. |

The two Gazebo-related pods have distinct roles, as shown in the upstream
[Gazebo proxy deployment](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic/simulation/templates/gzserver_core.yaml).

## Shared Intrinsic platform services

Namespace: `app-intrinsic-base` — **17 pods**.

| Pod | Purpose |
| --- | --- |
| `artifacts-deployment` | Handles software artifacts used in deployments. |
| `data-store` | Provides shared state and content-addressed storage for data blobs. |
| `geomservice` | Provides geometry processing used by world and planning services. |
| `http-gateway` | Translates HTTP/JSON API calls into internal gRPC service calls. |
| `kvstore-service` | Exposes Zenoh's key-value storage through a gRPC API. |
| `operations` | Provides status and management APIs for long-running operations. |
| `proto-builder` | Compiles message schemas into definitions services can use. |
| `resource-registry` | Catalogs available resources, such as robots, cameras, and service endpoints. |
| `runtime-db` | Stores application resources' runtime data. |
| `scene-object-import` | Imports scene objects and their geometry into the platform. |
| `simulation-service` | Coordinates simulation lifecycle and controls such as reset, pause, and resume. |
| `skill-registry` | Catalogs available skills, their parameters, and execution endpoints. |
| `solution-service` | Provides running-solution status and manages its behavior trees. |
| `workcell-cluster-service` | Coordinates deploying and stopping applications on the cluster. |
| `world` | Maintains the robot's world model: objects, frames, geometry, and relationships. |
| `world-updater` | Updates the world model from robot-state reports, including joint positions. |
| `zenoh-router` | Routes publish/subscribe messages between components; also includes introspection tooling. |

The upstream [runtime database API](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic/resources/proto/runtime_db.proto)
describes resource-data storage, and the
[simulation service API](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic/simulation/service/proto/first_party/simulation_service.proto)
documents controls for running simulations.

## Kubernetes, deployment, and networking

Across three namespaces — **9 pods**.

| Namespace | Pod | Purpose |
| --- | --- | --- |
| `app-ingress` | `istio-ingressgateway` | Routes incoming application traffic to the appropriate internal service. |
| `app-ingress` | `istiod` | Configures Istio routing and manages service discovery and certificates. |
| `default` | `chart-assignment-controller` | Reconciles Helm chart assignments to install and maintain deployed components. |
| `kube-system` | `coredns` | Resolves internal service names so pods can find one another. |
| `kube-system` | `local-path-provisioner` | Supplies persistent storage backed by directories on the VM. |
| `kube-system` | `metrics-server` | Collects CPU and memory metrics used by commands such as `kubectl top`. |
| `kube-system` | `nvidia-device-plugin` | Makes the NVIDIA GPU available to Kubernetes workloads. |
| `kube-system` | `svclb-istio-ingressgateway` | Forwards VM service ports into the Istio gateway. |
| `kube-system` | `svclb-zenoh-router-host` | Forwards the host-facing Zenoh port into the cluster. |

See the [Istio architecture](https://istio.io/latest/docs/ops/deployment/architecture/)
and [K3s networking documentation](https://docs.k3s.io/networking/networking-services)
for these infrastructure roles. The `svclb-*` pods do not themselves change AWS
security-group rules.

## How the pieces connect

A typical motion request passes through the executive and a skill, uses the
motion planner to calculate a path, and uses ICON and the UR module to execute
it in Gazebo. The world service tracks the workcell model, while the ROS bridge
makes that model available to RViz.

The browser window shows **RViz**, with Gazebo running the simulation behind it.
RViz, the desktop, and the browser viewer run separately under the VM's
`intrinsic-viewer.service`; they are not included in these 51 pods.

## Read-only inspection commands

Run these in an SSH session on the VM:

```sh
# List all pods and their readiness across namespaces.
kubectl get pods -A

# Watch pod status; Ctrl+C exits the watch.
kubectl get pods -A --watch

# Focus on robot, simulation, and perception resources.
kubectl get pods -n app-resources

# Show CPU and memory use, when metrics are available.
kubectl top pods -A

# See which containers are grouped into each skill pod.
kubectl get pods -n skills \
  -o custom-columns='POD:.metadata.name,CONTAINERS:.spec.containers[*].name'
```

This reference intentionally excludes VM addresses, connection credentials,
keys, certificates, and raw logs.
