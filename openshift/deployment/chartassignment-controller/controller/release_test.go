package chartassignment

import (
	"context"
	"testing"

	apps "github.com/googlecloudrobotics/core/src/go/pkg/apis/apps/v1alpha1"
	"github.com/googlecloudrobotics/core/src/go/pkg/kubetest"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/record"
	"k8s.io/helm/pkg/chartutil"

	"example.invalid/intrinsic-openshift/chartassignment-controller/synk"
)

const ChartName = "testchart"

type fakeSynk struct {
	applyName       string
	applyOptions    *synk.ApplyOptions
	deleteNamespace string
	deleteName      string
}

func (*fakeSynk) Init() error { return nil }

func (f *fakeSynk) Delete(_ context.Context, namespace, name string) error {
	f.deleteNamespace = namespace
	f.deleteName = name
	return nil
}

func (f *fakeSynk) Apply(_ context.Context, name string, opts *synk.ApplyOptions, _ ...*unstructured.Unstructured) (*apps.ResourceSet, error) {
	f.applyName = name
	f.applyOptions = opts
	return &apps.ResourceSet{}, nil
}

func newTestAssignment(t *testing.T) *apps.ChartAssignment {
	t.Helper()
	var as apps.ChartAssignment
	unmarshalYAML(t, &as, `
metadata:
  name: test-assignment-1
  namespace: arhkp-intrinsic
spec:
  namespaceName: arhkp-intrinsic
  chart:
    values:
`)
	as.Spec.Chart.Inline = kubetest.BuildInlineChart(t, ChartName, "", `foo: 1`)
	return &as
}

func verifyValues(t *testing.T, have string, wantValues chartutil.Values) {
	t.Helper()
	want, err := wantValues.YAML()
	if err != nil {
		t.Fatal(err)
	}
	if want != have {
		t.Fatalf("config values do not match: want\n%s\n\ngot\n%s\n", want, have)
	}
}

func Test_loadChart_mergesValues(t *testing.T) {
	as := newTestAssignment(t)
	as.Spec.Chart.Values = apps.ConfigValues{
		"bar1": 4,
		"bar2": map[string]interface{}{"baz2": "test"},
	}
	_, vals, err := loadChart(&as.Spec.Chart)
	if err != nil {
		t.Fatal(err)
	}
	wantValues := chartutil.Values{
		"bar1": 4,
		"bar2": chartutil.Values{"baz2": "test"},
		"foo":  1,
	}
	verifyValues(t, vals, wantValues)
}

func Test_updateSynkUsesAssignmentNamespace(t *testing.T) {
	as := newTestAssignment(t)
	fake := &fakeSynk{}
	r := &release{synk: fake, recorder: &record.FakeRecorder{}}

	r.update(as)

	if fake.applyName != as.Name {
		t.Fatalf("Apply name = %q, want %q", fake.applyName, as.Name)
	}
	if fake.applyOptions == nil || fake.applyOptions.Namespace != "arhkp-intrinsic" || !fake.applyOptions.EnforceNamespace {
		t.Fatalf("Apply options = %#v, want namespace-bound apply", fake.applyOptions)
	}
}

func Test_deleteSynkUsesAssignmentNamespace(t *testing.T) {
	as := newTestAssignment(t)
	fake := &fakeSynk{}
	r := &release{synk: fake, recorder: &record.FakeRecorder{}}

	r.delete(as)

	if fake.deleteNamespace != "arhkp-intrinsic" || fake.deleteName != as.Name {
		t.Fatalf("Delete target = %q/%q, want arhkp-intrinsic/%q", fake.deleteNamespace, fake.deleteName, as.Name)
	}
}
