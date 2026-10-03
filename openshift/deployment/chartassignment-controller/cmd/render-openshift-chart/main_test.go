package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"strings"
	"testing"
)

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

func inlineTestChart(t *testing.T) []byte {
	t.Helper()
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(compressed)
	files := map[string]string{
		"render-smoke/Chart.yaml":            "apiVersion: v1\nname: render-smoke\nversion: 0.0.1\n",
		"render-smoke/values.yaml":           "{}\n",
		"render-smoke/templates/result.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: render-smoke-result\ndata:\n  result: passed\n",
	}
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
