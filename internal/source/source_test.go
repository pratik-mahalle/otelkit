package source

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/pratikmahalle/otelkit/internal/model"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(ts []Target) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Name)
	}
	slices.Sort(out)
	return out
}

func TestLoadFilesDirsAndGlobs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "vms", "a.yaml"), "receivers: {otlp: {}}\n")
	writeFile(t, filepath.Join(dir, "vms", "b.yml"), "receivers: {otlp: {}}\n")
	writeFile(t, filepath.Join(dir, "c.yaml"), "receivers: {otlp: {}}\n")
	ts, errs := Loader{}.Load(context.Background(), []string{filepath.Join(dir, "vms"), filepath.Join(dir, "c*.yaml")})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if got := names(ts); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("names = %v", got)
	}
	if !filepath.IsAbs(ts[0].Deployed.File) {
		t.Errorf("deployed file must be absolute: %q", ts[0].Deployed.File)
	}
}

func TestLoadFileNameCollisions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "vm-a", "config.yaml"), "{}\n")
	writeFile(t, filepath.Join(dir, "vm-b", "config.yaml"), "{}\n")
	t.Chdir(dir)
	ts, errs := Loader{}.Load(context.Background(), []string{"vm-*/config.yaml"})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if got := names(ts); !slices.Equal(got, []string{"vm-a-config", "vm-b-config"}) {
		t.Errorf("names = %v", got)
	}
}

func TestLoadErrorsDoNotStopOthers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ok.yaml"), "{}\n")
	writeFile(t, filepath.Join(dir, "bad.yaml"), "a: [\n")
	ts, errs := Loader{}.Load(context.Background(), []string{
		filepath.Join(dir, "ok.yaml"), filepath.Join(dir, "bad.yaml"), filepath.Join(dir, "none-*.yaml"),
	})
	if len(ts) != 1 || len(errs) != 2 {
		t.Fatalf("targets=%d errs=%v", len(ts), errs)
	}
}

func TestParseK8sContextWithSlashes(t *testing.T) {
	r, err := ParseK8s("k8s://arn:aws:eks:eu-west-1:123:cluster/prod-eu/observability/configmap/otel-collector#relay")
	if err != nil {
		t.Fatal(err)
	}
	want := K8sRef{Context: "arn:aws:eks:eu-west-1:123:cluster/prod-eu", Namespace: "observability", ConfigMap: "otel-collector", Key: "relay"}
	if r != want {
		t.Errorf("got %+v", r)
	}
	if r.name() != "prod-eu-observability-otel-collector" {
		t.Errorf("name = %s", r.name())
	}
	if _, err := ParseK8s("k8s://ctx/ns/deployment/x"); err == nil {
		t.Error("want error for unknown kind")
	}
}

func fakeClients(objs []runtime.Object, dyn ...runtime.Object) Clients {
	return func(string) (kubernetes.Interface, dynamic.Interface, error) {
		dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
			map[schema.GroupVersionResource]string{CollectorGVR: "OpenTelemetryCollectorList"}, dyn...)
		return fake.NewClientset(objs...), dc, nil
	}
}

func TestLoadConfigMapDefaultsToRelayKey(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "otel-collector", Namespace: "obs"},
		Data:       map[string]string{"relay": "receivers: {otlp: {}}\n", "other": "x"},
	}
	l := Loader{Clients: fakeClients([]runtime.Object{cm})}
	ts, errs := l.Load(context.Background(), []string{"k8s://prod/obs/configmap/otel-collector"})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if model.Get(ts[0].Root, "receivers", "otlp") == nil || ts[0].Name != "prod-obs-otel-collector" {
		t.Errorf("got %+v", ts[0])
	}
}

func TestLoadCollectorCR(t *testing.T) {
	cr := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "opentelemetry.io/v1beta1",
		"kind":       "OpenTelemetryCollector",
		"metadata":   map[string]any{"name": "main", "namespace": "obs"},
		"spec": map[string]any{"config": map[string]any{
			"receivers": map[string]any{"otlp": map[string]any{}},
		}},
	}}
	l := Loader{Clients: fakeClients(nil, cr)}
	root, err := l.FromDeployed(context.Background(), "", Deployed{K8s: &K8sRef{Context: "prod", Namespace: "obs", Otelcol: "main"}})
	if err != nil {
		t.Fatal(err)
	}
	if model.Get(root, "receivers", "otlp") == nil {
		t.Error("spec.config not loaded")
	}
}

func TestFromDeployedRelativeFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "snap", "vm.yaml"), "receivers: {otlp: {}}\n")
	root, err := Loader{}.FromDeployed(context.Background(), dir, Deployed{File: "snap/vm.yaml"})
	if err != nil || model.Get(root, "receivers", "otlp") == nil {
		t.Fatalf("root=%v err=%v", root, err)
	}
}
