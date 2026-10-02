# Historical OpenShift resources for the AWS VM + RHOAI experiment

**Status:** archived; these steps are not the current deployment plan. The team
has set aside the hybrid path in favor of a full OpenShift deployment. See
[the active work plan](../../../OPENSHIFT_PLAN.md).

This directory is the separate implementation track for the hybrid approach.
The target is **dev01 only** in project `arhkp-intrinsic`; dev02 is excluded
because it lacks the needed resources. No dev02 inventory or smoke result counts
as dev01 evidence. On dev01, the UI-created gRPC model is Ready and internal
inference passed through a local port-forward. From the AWS VM, DNS and TLS to
the route work, but ALPN `h2` was not negotiated; external gRPC inference has
not passed. Token-authenticated REST inference from the VM is also untested.
Re-inventory dev01's product versions, runtime workflow, and capacity before
further deployment work. This does not alter the VM Only deployment or its
local inference path.

## Historical dev02 inventory (out of scope)

The following dev02 observations and instructions are retained as history only.
Do not use them to claim dev01 readiness or copy them to dev01 without
revalidating product versions, UI behavior, permissions, capacity, and resource
state.

Read-only checks on 2026-10-01 recorded OpenShift `4.21.16`, RHOAI `3.4.1`,
and KServe in `Managed` state. The versions are a supported pairing according
to Red Hat's [RHOAI 3.x supported configurations](https://access.redhat.com/articles/rhoai-supported-configs-3.x).
The cluster had two `ServingRuntime` resources and no Triton runtime. The
`arhkp-intrinsic` project has since been created and verified. The latest
read-only inventory found 9 allocatable A10G GPUs with 3 requested and 3
allocatable L40S GPUs with 2 requested (an estimated 7 free across both
products). This is cluster-wide capacity, not a reservation; re-run preflight
before scheduling. Project GPU quota, model storage, and production-model
artifacts remain unverified. Source-network allowlisting is also unverified,
but external ingress with token authentication has been accepted for this
bounded synthetic demo. Available
StorageClasses are `gp2-csi` and `gp3-csi`. Namespace-scoped create checks
currently allow ServingRuntime, InferenceService, PVC, ResourceQuota, and
NetworkPolicy resources; this does not establish RHOAI dashboard administrator
privileges or authorize serving runtime enablement.

A live check on 2026-10-01 initially found `triton-cifar10-smoke` Ready with no
Route or auth configuration. We then enabled auth first with the
`security.opendatahub.io/enable-auth: "true"` InferenceService annotation and,
after the proxy-enabled pod was Ready, exposed it with the
`networking.kserve.io/visibility: exposed` label. The RHOAI model controller
created the Route; its observed TLS mode was `reencrypt`, its HTTP policy was
`Redirect`, and it targeted the predictor's HTTPS port. The Route was admitted.
These fields match the current [upstream route reconciler](https://github.com/opendatahub-io/odh-model-controller/blob/incubating/internal/controller/serving/reconcilers/kserve_raw_route_reconciler.go).

The client identity is the dedicated `intrinsic-aws-vm` ServiceAccount. Its
project Role grants only `get` on the `triton-cifar10-smoke` InferenceService.
A 15-minute token was minted with `oc create token` into an owner-only local
file. The sanitized external-route smoke test passed from both a local client
and the AWS VM: missing token `401`, invalid token `401`, valid token `200`,
model `cifar10`, output shape `1x10`; TLS verification remained enabled. This
verifies authentication, inference, DNS, and connectivity for the tested VM
request. The Route is on an ingress controller with `External` scope, so the
source-network allowlisting was not tested and is not claimed. The external
Route with token authentication is accepted for this bounded synthetic demo;
review exposure again before non-synthetic data or wider use. No endpoint
hostname, token, certificate, or CA bundle is recorded here.

The auth change required a pod replacement. The default rolling update could
not schedule a second GPU for this workload, despite an earlier cluster-wide
free-GPU estimate. We set
`spec.predictor.deploymentStrategy.type: Recreate` for this smoke service so the
old pod released its GPU before the proxy-enabled pod started. This strategy
causes downtime during replacement; recheck model-specific GPU placement and
capacity before future rollouts.

### Reproduce the route/auth smoke setup

This CLI sequence was verified on dev02 only. It is a reference for the
dev01 test, not a verified dev01 procedure. Recheck permissions, object names,
RHOAI behavior, and resource availability before running it there. It creates
project-scoped caller RBAC and changes the named smoke InferenceService. The
external route label is deliberately applied only after the auth proxy is ready.

```bash
oc apply -f - <<'YAML'
apiVersion: v1
kind: ServiceAccount
metadata:
  name: intrinsic-aws-vm
  namespace: arhkp-intrinsic
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: triton-cifar10-smoke-inference
  namespace: arhkp-intrinsic
rules:
- apiGroups: [serving.kserve.io]
  resources: [inferenceservices]
  resourceNames: [triton-cifar10-smoke]
  verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: triton-cifar10-smoke-inference
  namespace: arhkp-intrinsic
subjects:
- kind: ServiceAccount
  name: intrinsic-aws-vm
  namespace: arhkp-intrinsic
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: triton-cifar10-smoke-inference
YAML

# Use this for the one-GPU smoke workload when a rolling update cannot get
# temporary surge capacity; Recreate introduces downtime during replacement.
oc patch inferenceservice triton-cifar10-smoke -n arhkp-intrinsic \
  --type=merge \
  -p '{"spec":{"predictor":{"deploymentStrategy":{"type":"Recreate"}}}}'

# Enable the proxy first, then wait until its pod is Ready and the predictor
# Service has an HTTPS port before exposing the Route.
oc annotate inferenceservice triton-cifar10-smoke -n arhkp-intrinsic \
  security.opendatahub.io/enable-auth=true --overwrite
oc wait pods -n arhkp-intrinsic \
  -l serving.kserve.io/inferenceservice=triton-cifar10-smoke \
  --for=condition=Ready --timeout=10m
oc label inferenceservice triton-cifar10-smoke -n arhkp-intrinsic \
  networking.kserve.io/visibility=exposed --overwrite

# Mint a short-lived caller token into a protected file; this does not print it.
install -d -m 700 "$HOME/.config/intrinsic"
umask 077
oc create token intrinsic-aws-vm -n arhkp-intrinsic --duration=15m \
  > "$HOME/.config/intrinsic/rhoai-token"
chmod 600 "$HOME/.config/intrinsic/rhoai-token"
```

Check that the generated Route is admitted, uses `reencrypt` TLS with HTTP
redirect, and targets the predictor's HTTPS port. Configure the ignored `.env`
with the route URL, the token-file path, and an approved CA bundle if the local
Python trust store needs one; never place the token value in `.env`. Run
`python3 approaches/tried-not-feasible-aws-vm-with-rhoai/openshift/serving/verify_external_route_auth.py`
from the intended client network. The script suppresses the hostname and token
and tests missing, invalid, and valid-token inference with certificate
verification enabled. Repeat the token mint shortly before later tests because
the example token expires after 15 minutes.

The new project is marked with `opendatahub.io/dashboard=true`, matching the
marker found on existing RHOAI project namespaces in this cluster. Confirm the
project appears in the dashboard UI when available; this metadata does not
install a serving runtime or deploy a model. RHOAI 3.4 describes a project as
an OpenShift namespace with additional annotations in its
[project guide](https://docs.redhat.com/en/documentation/red_hat_openshift_ai_self-managed/3.4/html/working_on_projects/using-projects_projects).

These records deliberately omit the cluster URL, context/user identity, node
names, kubeconfig, endpoint addresses, and credentials. Re-run the commands
below for current state; do not treat a previous GPU estimate as a reservation.

## Safe preflight and project bootstrap

The scripts use a local context-name hint. If that hint matches more than one
cluster or identity, they stop. They do not print the selected context or raw
CLI output. Preflight does not modify cluster state; bootstrap creates only the
named project and does not write the project choice into kubeconfig.

```bash
python3 approaches/tried-not-feasible-aws-vm-with-rhoai/openshift/preflight.py
python3 approaches/tried-not-feasible-aws-vm-with-rhoai/openshift/bootstrap_project.py
python3 approaches/tried-not-feasible-aws-vm-with-rhoai/openshift/bootstrap_project.py --apply
python3 approaches/tried-not-feasible-aws-vm-with-rhoai/openshift/preflight.py
```

Use `--context-fragment` only if the local context names do not contain
`dev01`. Do not paste context lists, kubeconfig output, API URLs, tokens, or
identity details into issue trackers or commit them. Bootstrap has no write
side effect unless `--apply` is supplied.

Only the project and its RHOAI dashboard-discovery marker are bootstrapped
automatically. No GPU quota is imposed here:
the quota owner must confirm an appropriate bound, and existing quotas must be
preserved. The scripts create no `ServingRuntime`, `InferenceService`, PVC,
route, `NetworkPolicy`, registry secret, or endpoint.

## Serving gates

RHOAI 3.4 documentation lists NVIDIA Triton in a separate Tested and verified
category, outside the Supported model-serving runtimes table; it uses Standard
(Raw) mode, with gRPC as the default protocol. Do not describe that listing as
Red Hat support for this Intrinsic integration. The guide explicitly says
custom runtimes are not supported by Red Hat and their owners are responsible
for licensing, configuration, and maintenance. Follow the [RHOAI 3.4 instructions
for adding a tested and verified runtime](https://docs.redhat.com/en/documentation/red_hat_openshift_ai_self-managed/3.4/html/configuring_your_model-serving_platform/configuring_model_servers)
with an RHOAI administrator. Do not silently substitute an ordinary
Deployment for an RHOAI-managed model-serving runtime.

Before an inference workload is applied, record all of the following in
sanitized form:

1. An approved, pinned Triton image digest and its licensing/registry-pull
   process. Never place a registry API key or pull secret in Git.
2. A known-good synthetic model repository and a versioned approved storage
   location that RHOAI can read. The model format and Triton layout must match
   the runtime.
3. A project GPU quota and a worker SKU with sufficient VRAM for the smoke
   model. Current free-GPU estimates are not reservations.
4. Authenticated model access and validated TLS trust. The actual Route was
   checked, and required token authentication passed. External ingress is
   accepted for this bounded synthetic test; do not treat that as a source
   allowlist or approval for non-synthetic inputs.
5. AWS VM-to-ROSA reachability. VM connectivity succeeded for the smoke test.
   Do not open AWS security-group ingress or expose the cluster API for this
   test.

The pinned [Intrinsic inference manifest](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_inference/assets/inference_service/inference_service.manifest.textproto),
[service entry point](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_inference/assets/inference_service/inference_service_main.py),
and [Triton model controller](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/intrinsic_inference/core/model_controller_triton.py)
are the upstream references for the current local inference contract. The
RHOAI platform deploys each model from a dedicated model server. The dev02 REST
smoke is historical and excluded. On dev01, internal gRPC inference passed, but
the external gRPC route did not negotiate ALPN `h2`; REST from the VM and
Intrinsic-to-RHOAI inference remain untested.
The upstream contract uses a local Unix socket/shared-memory path and Intrinsic
model load/unload control. Before switching, validate how the VM-side wrapper
will call the dev01 endpoint with TLS and token authentication and how RF-DETR and
FoundationPose artifact lifecycle maps to RHOAI model deployments. This may
need client configuration or an adapter; confirm against the pinned source
before choosing.

## Ownership and rollback

RHOAI platform/operator namespaces remain cluster-owned. Project-scoped
resources belong in `arhkp-intrinsic`; do not edit operator-managed objects.
Keep the VM's local inference pair intact as the baseline and rollback path.
See the [implementation layout](../IMPLEMENTATION_LAYOUT.md) and
[test plan](../TEST_PLAN.md) for the placement split, gates, and stop criteria.
