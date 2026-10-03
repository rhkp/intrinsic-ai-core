# Offline OpenShift chart renderer

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
  --namespace arhkp-intrinsic /private/tmp/intrinsic-rendered.yaml
```

The input must use the `apps.cloudrobotics.com/v1alpha1` ChartAssignment shape,
with `metadata.namespace` and `spec.namespaceName` set to the same pre-created
target project and `spec.chart.inline` containing a base64-encoded Helm
archive. Keep any
rendered output private; Helm values may contain operational configuration.
This tool is a render-and-policy gate, not an apply command or a replacement
for readiness, rollback, or lifecycle handling.
