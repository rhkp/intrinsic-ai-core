# AWS VM + RHOAI serving smoke test

## Result

On 2026-10-01, a Triton 26.07 inference smoke test passed in the `arhkp-intrinsic`
project. The `triton-cifar10-smoke` KServe `InferenceService` became Ready and
returned a Triton V2 prediction for model `cifar10`, version `1`, with output
shape `[1, 10]`.

The RHOAI UI-created runtime is a global OpenShift `Template`, not a
project-scoped KServe `ServingRuntime`. For direct KServe YAML deployment, the
template's embedded `ServingRuntime` was copied into the project and referenced
as `triton-kserve-rest`. This follows the RHOAI procedure for creating a
project-scoped runtime. The project runtime retains the configured immutable
Triton image digest, v2/gRPC-v2 protocols, and one-GPU limit.

The test uses KServe's public CIFAR10 TorchScript repository at
`gs://kfserving-examples/models/torchscript`. Its `model.pt` is 259,172 bytes;
the model configuration specifies `KIND_CPU`. The service therefore reserved
one GPU for runtime/scheduling validation, while this sample's prediction ran
on CPU. This test does **not** prove GPU-accelerated inference. The pod needed
the `g5-gpu=true:NoSchedule` toleration to schedule on the A10G pool. The
The initial InferenceService used Standard deployment mode, one replica, and a
ClusterIP service; its first request was sent through a temporary
localhost-only `oc port-forward`. A later auth-enabled update caused the RHOAI
controller to add a proxy-enabled pod and an admitted external Route. The route
and VM-client checks are recorded in [`../README.md`](../README.md).

At test time the public model objects had these GCS metadata values:

| Object | Size | Generation | MD5 |
| --- | ---: | ---: | --- |
| `models/torchscript/cifar10/config.pbtxt` | 300 bytes | `1602405851161266` | `fkTy1dzYLD7vcYSfk/VEwQ==` |
| `models/torchscript/cifar10/1/model.pt` | 259,172 bytes | `1602376861898773` | `cGU34Hyc/V3Km5ERznq3Hg==` |

The KServe `storageUri` references a mutable public bucket prefix; it does not
pin those GCS object generations. For a durable or production deployment,
mirror the sample into an approved versioned model store and pin/verify its
digest. Do not put model-storage credentials, endpoint addresses, certificates,
or request payloads in this repository.

## Runtime and support notes

Intrinsic Core tag `20260922.0` selects
`nvcr.io/nvidia/tritonserver:26.07-py3` for `linux/amd64` in
[`MODULE.bazel`](https://github.com/intrinsic-ai/intrinsic-core/blob/20260922.0/MODULE.bazel#L708-L714).
The registered runtime uses this immutable amd64 image digest:

```text
nvcr.io/nvidia/tritonserver@sha256:551599c0812bb706121410a71abda07f9920d25e6c66edf1ec07a375e49f5f90
```

Triton 26.07 is based on CUDA 13.3.4.1. NVIDIA's CUDA compatibility guide
describes minor-version compatibility and its feature limitations. Since the
sample model is CPU-only, this test does not establish CUDA driver compatibility
for GPU inference. See the [Triton 26.07 release
notes](https://docs.nvidia.com/deeplearning/triton-inference-server/release-notes/rel-26-07.html)
and [NVIDIA CUDA compatibility guide](https://docs.nvidia.com/deploy/cuda-compatibility/minor-version-compatibility.html).

RHOAI 3.4 lists Triton in its separate Tested and verified category for Standard
(Raw) serving. This does not make this custom image configuration or the
Intrinsic hybrid integration a Red Hat-supported solution; the runtime owner
remains responsible for image policy, licensing, configuration, and upkeep.
See the [RHOAI 3.4 model-serving runtime guide](https://docs.redhat.com/en/documentation/red_hat_openshift_ai_self-managed/3.4/html/configuring_your_model-serving_platform/configuring_model_servers).

## Reproduce

Prerequisites: `oc` is logged in to the intended cluster, `jq`, `curl`, and
Python 3 are installed, and the user can read the global RHOAI runtime template
and create a project-scoped `ServingRuntime` and `InferenceService`.

1. Materialize the global runtime template in the target project. The query
   fails unless it finds exactly one matching runtime and image digest:

   ```bash
   oc get templates -n redhat-ods-applications -o json |
     jq -e '[.items[] as $t | $t.objects[]? |
       select(.kind == "ServingRuntime" and
              .metadata.name == "triton-kserve-rest" and
              any(.spec.containers[]; .image == "nvcr.io/nvidia/tritonserver@sha256:551599c0812bb706121410a71abda07f9920d25e6c66edf1ec07a375e49f5f90")) |
       .metadata.namespace = "arhkp-intrinsic"] |
       if length == 1 then .[0] else error("Expected one matching global runtime template") end' |
     oc apply -f -
   ```

2. Create the internal one-replica model service:

   ```bash
   oc apply -f approaches/tried-not-feasible-aws-vm-with-rhoai/openshift/serving/manifests/triton-cifar10-smoke.yaml
   oc wait --for=condition=Ready inferenceservice/triton-cifar10-smoke \
     -n arhkp-intrinsic --timeout=10m
   ```

3. Before enabling external visibility, check Ready state, GPU request, service
   type, and that no matching Route exists. These commands do not print the
   internal service address:

   ```bash
   oc get isvc triton-cifar10-smoke -n arhkp-intrinsic -o json |
     jq -r '.status.conditions[] | select(.type == "Ready") | .status'
   oc get pods -n arhkp-intrinsic \
     -l serving.kserve.io/inferenceservice=triton-cifar10-smoke -o json |
     jq -r '.items[] | [.status.phase, .spec.containers[0].resources.limits["nvidia.com/gpu"]] | @tsv'
   oc get svc triton-cifar10-smoke-predictor -n arhkp-intrinsic -o json |
     jq -r '.spec.type'
   oc get routes -n arhkp-intrinsic -o json |
     jq '[.items[] | select(.metadata.name | contains("triton-cifar10-smoke"))] | length'
   ```

4. In one terminal, open a temporary local port-forward:

   ```bash
   oc port-forward -n arhkp-intrinsic \
     service/triton-cifar10-smoke-predictor 18080:80
   ```

   In another terminal, send a deterministic zero tensor and validate the V2
   response:

   ```bash
   python3 -c 'import json; print(json.dumps({"inputs":[{"name":"INPUT__0","shape":[1,3,32,32],"datatype":"FP32","data":[0.0]*3072}]}))' |
     curl --fail --silent --show-error -H 'Content-Type: application/json' \
       --data-binary @- http://localhost:18080/v2/models/cifar10/infer |
     jq -e 'if .model_name == "cifar10" and .outputs[0].shape == [1,10] and (.outputs[0].data|length) == 10 then {result:"PASS",model:.model_name,version:.model_version,output_shape:.outputs[0].shape} else error("Unexpected inference response") end'
   ```

5. Stop the port-forward with `Ctrl-C`. To remove the smoke workload later:

   ```bash
   oc delete -f approaches/tried-not-feasible-aws-vm-with-rhoai/openshift/serving/manifests/triton-cifar10-smoke.yaml
   oc delete servingruntime triton-kserve-rest -n arhkp-intrinsic
   oc delete -f approaches/tried-not-feasible-aws-vm-with-rhoai/openshift/serving/manifests/triton-cifar10-grpc-smoke.yaml
   oc delete -f approaches/tried-not-feasible-aws-vm-with-rhoai/openshift/serving/manifests/triton-kserve-grpc-smoke-runtime.yaml
   ```

For the current synthetic smoke, the RHOAI controller created an HTTPS
reencrypt Route after token authentication was enabled. A dedicated
least-privilege service-account token was minted with `oc create token`, stored
in a protected file, and used for the request from the AWS VM. Missing and
invalid tokens returned `401`; the valid token returned `200` with model
`cifar10` and output shape `1x10`. TLS verification remained enabled. The
external ingress plus token authentication is accepted for this bounded demo;
source-network allowlisting was not tested. No token, route hostname,
certificate, or CA bundle is recorded here. See [`../README.md`](../README.md)
for the CLI reproduction and [`../../TEST_PLAN.md`](../../TEST_PLAN.md) for the
current integration gates.

This REST smoke remains the control. The runtime advertises `v2` and
`grpc-v2`, with Triton HTTP and gRPC enabled; the original InferenceService
selects `protocolVersion: v2` and exposes the HTTPS proxy port. A separate
CPU-only gRPC smoke now uses a port-9000 `h2c` ServingRuntime and
`protocolVersion: grpc-v2`. Its InferenceService is Ready, and backend gRPC
metadata, model readiness, and CIFAR-10 inference passed over a temporary
localhost-only port-forward (output shape `[1, 10]`). The gRPC smoke manifests
are [`triton-kserve-grpc-smoke-runtime.yaml`](manifests/triton-kserve-grpc-smoke-runtime.yaml)
and [`triton-cifar10-grpc-smoke.yaml`](manifests/triton-cifar10-grpc-smoke.yaml).

External gRPC is not yet proven through the model-deployment workflow. The
earlier ALPN observation applies only to the Route generated for our
CLI-created gRPC smoke resource; it is not a requirement to add a custom Route
or certificate. RHOAI 3.4's [model deployment guide](https://docs.redhat.com/en/documentation/red_hat_openshift_ai_self-managed/3.4/html-single/deploying_models/deploying_models)
documents external Triton gRPC on port 443 and bearer-token `grpcurl` checks.
Next, use **Models → Model deployments** to select the gRPC runtime, enable
**Model access** and **Require token authentication** for the dedicated
service account, then test the UI-created endpoint from the AWS VM with TLS
verification enabled. Confirm gRPC metadata, readiness, and synthetic
inference before deploying perception artifacts. Preserve the VM-local
inference path for rollback.

## References

- [RHOAI 3.4 project-scoped resources](https://docs.redhat.com/en/documentation/red_hat_openshift_ai_self-managed/3.4/html-single/managing_openshift_ai/index#creating-project-scoped-resources_managing-rhoai)
- [RHOAI 3.4 model deployment](https://docs.redhat.com/en/documentation/red_hat_openshift_ai_self-managed/3.4/html/deploying_models/deploying_models)
- [OpenShift 4.21 routes and TLS termination](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html-single/ingress_and_load_balancing/index)
- [KServe Triton TorchScript model repository and V2 inference example](https://kserve.github.io/website/docs/model-serving/predictive-inference/frameworks/triton/torchscript)
