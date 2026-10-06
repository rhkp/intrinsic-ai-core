package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

const smokeNamespace = "arhkp-intrinsic"

func TestRunRendersInlineChartThroughOpenShiftPolicy(t *testing.T) {
	archive := inlineTestChart(t)
	input := "apiVersion: apps.cloudrobotics.com/v1alpha1\n" +
		"kind: ChartAssignment\n" +
		"metadata:\n  name: render-smoke\n  namespace: arhkp-intrinsic\n" +
		"spec:\n  namespaceName: arhkp-intrinsic\n  chart:\n" +
		"    name: render-smoke\n    version: 0.0.1\n" +
		"    inline: " + base64.StdEncoding.EncodeToString(archive) + "\n" +
		"    values: {}\n"
	var output bytes.Buffer
	if err := run(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "kind: ConfigMap") ||
		!strings.Contains(output.String(), "namespace: arhkp-intrinsic") ||
		!strings.Contains(output.String(), "name: render-smoke-result") {
		t.Fatalf("rendered output missing expected OpenShift-scoped ConfigMap:\n%s", output.String())
	}
}

func TestRunRejectsRemoteChart(t *testing.T) {
	input := `{"apiVersion":"apps.cloudrobotics.com/v1alpha1","kind":"ChartAssignment","metadata":{"name":"remote"},"spec":{"namespaceName":"arhkp-intrinsic","chart":{"name":"remote","version":"1.0.0","repository":"https://example.invalid"}}}`
	err := run(strings.NewReader(input), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "remote chart retrieval is disabled") {
		t.Fatalf("expected remote chart to be rejected, got %v", err)
	}
}

func TestRunRejectsNamespaceEscape(t *testing.T) {
	archive := inlineTestChart(t)
	input := "apiVersion: apps.cloudrobotics.com/v1alpha1\n" +
		"kind: ChartAssignment\n" +
		"metadata:\n  name: render-smoke\n  namespace: arhkp-intrinsic\n" +
		"spec:\n  namespaceName: another-project\n  chart:\n" +
		"    name: render-smoke\n    version: 0.0.1\n" +
		"    inline: " + base64.StdEncoding.EncodeToString(archive) + "\n" +
		"    values: {}\n"
	err := run(strings.NewReader(input), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "does not match target namespace") {
		t.Fatalf("expected namespace escape to be rejected, got %v", err)
	}
}

func TestRunRendersDynamicResourceChartThroughOpenShiftPolicy(t *testing.T) {
	setSmokeRouting(t)
	values := `resource_instances:
  - name: smoke-resource
    instance_name: smoke-resource
    context_id: smoke-context
    runtime_context_pb_base64: c21va2U=
    spec: |-
      automountServiceAccountToken: true
      serviceAccountName: default
      securityContext:
        runAsUser: 65532
      containers:
        - name: smoke-resource
          image: quay.io/rhkp/intrinsic/http_gateway_service_jwhjwootqbghfz5q@sha256:bb59153ea8f1c31893e105918ccafcf76c0384a0fb36083ca3cf5029cf562fba
          securityContext:
            runAsUser: 65532
            runAsGroup: 65532
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
    service:
      port: 8080
      host_name: rs-smoke-resource
      host_port: 8080
      proto_prefixes:
        - /intrinsic_proto.demo.v1.DemoService/
      supports_service_state: true
`
	output := runTemplateChart(t, "resource-smoke", "resources.yaml", values)
	objects := parseRenderedObjects(t, output)
	statefulSet := findRenderedObject(t, objects, "StatefulSet", "rs-smoke-resource")
	annotations, found, err := unstructured.NestedStringMap(statefulSet, "spec", "template", "metadata", "annotations")
	if err != nil || !found || annotations["sidecar.istio.io/inject"] != "true" {
		t.Fatalf("Gateway-routed resource sidecar annotation = %v, found=%t, err=%v", annotations, found, err)
	}
	podSpec, _, _ := unstructured.NestedMap(statefulSet, "spec", "template", "spec")
	if podSpec["serviceAccountName"] != "intrinsic-runtime" || podSpec["automountServiceAccountToken"] != false {
		t.Fatalf("resource identity was not adapted: %#v", podSpec)
	}
	if securityContext, _, _ := unstructured.NestedMap(podSpec, "securityContext"); securityContext["runAsUser"] != nil {
		t.Fatalf("fixed pod UID was retained: %#v", securityContext)
	}
	containers, _, _ := unstructured.NestedSlice(podSpec, "containers")
	container := containers[0].(map[string]interface{})
	if securityContext, _, _ := unstructured.NestedMap(container, "securityContext"); securityContext["runAsUser"] != nil || securityContext["runAsGroup"] != nil {
		t.Fatalf("fixed container UID/GID was retained: %#v", securityContext)
	}
	virtualService := findRenderedObject(t, objects, "VirtualService", "rs-smoke-resource")
	gateways, _, _ := unstructured.NestedStringSlice(virtualService, "spec", "gateways")
	exported, _, _ := unstructured.NestedStringSlice(virtualService, "spec", "exportTo")
	if len(gateways) != 1 || gateways[0] != smokeNamespace+"/intrinsic-grpc-internal" || len(exported) != 1 || exported[0] != "." {
		t.Fatalf("resource VirtualService scope = gateways %v, exportTo %v", gateways, exported)
	}
	if strings.Contains(output, "app-ingress") || strings.Contains(output, "app-intrinsic-base") {
		t.Fatal("resource chart retained an upstream namespace or ingress reference")
	}
}

func TestRunRendersDynamicSkillChartThroughOpenShiftPolicy(t *testing.T) {
	setSmokeRouting(t)
	values := `pods:
  - name: smoke-skill-group
    service_account_name: default
    containers:
      - name: smoke-skill
        asset: smoke.skill
        registry: quay.io/rhkp/intrinsic
        image_ref: http_gateway_service_jwhjwootqbghfz5q@sha256:bb59153ea8f1c31893e105918ccafcf76c0384a0fb36083ca3cf5029cf562fba
        port: 8003
        metrics_port: 9101
skills: []
`
	output := runTemplateChart(t, "skill-smoke", "skills.yaml", values)
	objects := parseRenderedObjects(t, output)
	deployment := findRenderedObject(t, objects, "Deployment", "smoke-skill-group")
	annotations, found, err := unstructured.NestedStringMap(deployment, "spec", "template", "metadata", "annotations")
	if err != nil || !found || annotations["sidecar.istio.io/inject"] != "true" {
		t.Fatalf("Gateway-routed skill sidecar annotation = %v, found=%t, err=%v", annotations, found, err)
	}
	podSpec, _, _ := unstructured.NestedMap(deployment, "spec", "template", "spec")
	if podSpec["serviceAccountName"] != "intrinsic-runtime" || podSpec["automountServiceAccountToken"] != false {
		t.Fatalf("skill identity was not adapted: %#v", podSpec)
	}
	if securityContext, _, _ := unstructured.NestedMap(podSpec, "securityContext"); securityContext["runAsUser"] != nil || securityContext["runAsGroup"] != nil {
		t.Fatalf("fixed skill UID/GID was retained: %#v", securityContext)
	}
	containers, _, _ := unstructured.NestedSlice(podSpec, "containers")
	image, _, _ := unstructured.NestedString(containers[0].(map[string]interface{}), "image")
	if image != "quay.io/rhkp/intrinsic/http_gateway_service_jwhjwootqbghfz5q@sha256:bb59153ea8f1c31893e105918ccafcf76c0384a0fb36083ca3cf5029cf562fba" {
		t.Fatalf("skill image = %q, want digest-locked Quay image", image)
	}
	if strings.Contains(output, "app-ingress") || strings.Contains(output, "app-intrinsic-base") {
		t.Fatal("skill chart retained an upstream namespace reference")
	}
	policy := findRenderedObject(t, objects, "NetworkPolicy", "smoke-skill-group")
	egress, _, _ := unstructured.NestedSlice(policy, "spec", "egress")
	peers, _, _ := unstructured.NestedSlice(egress[0].(map[string]interface{}), "to")
	podSelector, found, _ := unstructured.NestedMap(peers[0].(map[string]interface{}), "podSelector", "matchLabels")
	if !found || podSelector["app.kubernetes.io/name"] != "intrinsic-grpc-gateway" || podSelector["istio"] != "intrinsic-grpc-gateway" {
		t.Fatalf("skill ingress egress peer is not a valid, restricted LabelSelector: %#v", peers[0])
	}
	if !strings.Contains(output, "kubernetes.io/metadata.name: istio-system") ||
		!strings.Contains(output, "istio.io/rev: data-science-smcp") || !strings.Contains(output, "port: 15012") {
		t.Fatal("skill egress policy does not allow the verified Service Mesh xDS control plane")
	}
	if !strings.Contains(output, "kubernetes.io/metadata.name: openshift-dns") ||
		!strings.Contains(output, "dns.operator.openshift.io/daemonset-dns: default") {
		t.Fatal("skill DNS egress policy still targets the upstream kube-dns pods")
	}
	if !strings.Contains(output, "port: dns") || !strings.Contains(output, "port: dns-tcp") {
		t.Fatal("skill DNS egress policy does not allow both named OpenShift DNS endpoint ports")
	}
}

func setSmokeRouting(t *testing.T) {
	t.Helper()
	t.Setenv("INTRINSIC_INGRESS_ADDRESS", "intrinsic-grpc-gateway."+smokeNamespace+".svc.cluster.local:80")
	t.Setenv("INTRINSIC_INGRESS_GATEWAY", smokeNamespace+"/intrinsic-grpc-internal")
	t.Setenv("INTRINSIC_INGRESS_POD_SELECTOR", `{"app.kubernetes.io/name":"intrinsic-grpc-gateway","istio":"intrinsic-grpc-gateway"}`)
}

func runTemplateChart(t *testing.T, releaseName, templateName, values string) string {
	t.Helper()
	chartArchive := inlineTemplateChart(t, releaseName, templateName, values)
	input := fmt.Sprintf("apiVersion: apps.cloudrobotics.com/v1alpha1\nkind: ChartAssignment\nmetadata:\n  name: %s\n  namespace: arhkp-intrinsic\nspec:\n  namespaceName: arhkp-intrinsic\n  chart:\n    name: %s\n    version: 0.0.1\n    inline: %s\n    values:\n", releaseName, releaseName, base64.StdEncoding.EncodeToString(chartArchive))
	for _, line := range strings.Split(strings.TrimSuffix(values, "\n"), "\n") {
		input += "      " + line + "\n"
	}
	var output bytes.Buffer
	if err := run(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func inlineTemplateChart(t *testing.T, chartName, templateName, values string) []byte {
	t.Helper()
	assets := filepath.Join("..", "..", "..", "intrinsic-core", "intrinsic", "assets", "deploy")
	template, err := os.ReadFile(filepath.Join(assets, templateName))
	if err != nil {
		t.Fatal(err)
	}
	return inlineChartArchive(t, map[string]string{
		chartName + "/Chart.yaml":                "apiVersion: v1\nname: " + chartName + "\nversion: 0.0.1\n",
		chartName + "/values.yaml":               values,
		chartName + "/templates/" + templateName: string(template),
	})
}

func parseRenderedObjects(t *testing.T, rendered string) []map[string]interface{} {
	t.Helper()
	var objects []map[string]interface{}
	for _, document := range strings.Split(rendered, "---\n") {
		if strings.TrimSpace(document) == "" {
			continue
		}
		var object map[string]interface{}
		if err := yaml.Unmarshal([]byte(document), &object); err != nil {
			t.Fatalf("unmarshal rendered object: %v", err)
		}
		objects = append(objects, object)
	}
	return objects
}

func findRenderedObject(t *testing.T, objects []map[string]interface{}, kind, name string) map[string]interface{} {
	t.Helper()
	for _, object := range objects {
		metadata, _ := object["metadata"].(map[string]interface{})
		if object["kind"] == kind && metadata["name"] == name {
			return object
		}
	}
	t.Fatalf("rendered %s/%s not found", kind, name)
	return nil
}

func inlineTestChart(t *testing.T) []byte {
	t.Helper()
	files := map[string]string{
		"render-smoke/Chart.yaml":            "apiVersion: v1\nname: render-smoke\nversion: 0.0.1\n",
		"render-smoke/values.yaml":           "{}\n",
		"render-smoke/templates/result.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: render-smoke-result\ndata:\n  result: passed\n",
	}
	return inlineChartArchive(t, files)
}

func inlineChartArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(compressed)
	for name, content := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o444, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}
