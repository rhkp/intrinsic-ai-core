# Offline OpenShift chart renderer
> [!NOTE]
> This project was developed with assistance from AI tools.

> Deployment examples use generic placeholders for cluster context, project namespace, registry, storage class, and GPU settings. Replace them with approved environment values before use; scripts may read `OPENSHIFT_CONTEXT`, `OPENSHIFT_NAMESPACE`, `OPENSHIFT_API_SERVER_URL`, and `OPENSHIFT_GPU_TAINT_KEY` from the environment.


`render-openshift-chart` accepts one inline `ChartAssignment`, runs the same
Helm rendering and OpenShift adaptation as the pilot controller, and writes
the resulting YAML to standard output. It does not call `oc`, connect to a
cluster, retrieve remote charts, or apply resources. Remote chart references
are rejected; the inline chart and values must be supplied explicitly.

From `openshift/deployment/chartassignment-controller`, render into a private
temporary file for inspection:

```bash
umask 077
go run ./cmd/render-openshift-chart < /path/to/inline-chart-assignment.yaml \
  > /private/tmp/intrinsic-rendered.yaml
python3 ../../validate_rendered_manifests.py \
  --namespace your-project-namespace /private/tmp/intrinsic-rendered.yaml
```

The input must use the `apps.cloudrobotics.com/v1alpha1` ChartAssignment shape,
with `metadata.namespace` and `spec.namespaceName` set to the same pre-created
target project and `spec.chart.inline` containing a base64-encoded Helm
archive. Keep any
rendered output private; Helm values may contain operational configuration.
This tool is a render-and-policy gate, not an apply command or a replacement
for readiness, rollback, or lifecycle handling.
