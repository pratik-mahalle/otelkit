package build

import (
	"slices"
	"strings"
	"testing"

	"github.com/pratik-mahalle/otelkit/internal/model"
)

func check(t *testing.T, in string, origins map[string]string) []string {
	t.Helper()
	root, err := model.Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	return Check(Merged{Root: root, Origins: origins})
}

func TestCheckProcessorOrder(t *testing.T) {
	w := check(t, `
receivers: {otlp: {}}
processors: {batch: {}, memory_limiter: {}, k8sattributes: {}}
exporters: {debug: {}}
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch, memory_limiter, k8sattributes]
      exporters: [debug]
`, nil)
	want := []string{
		"pipeline traces: memory_limiter should be the first processor",
		"pipeline traces: batch should be the last processor",
	}
	if !slices.Equal(w, want) {
		t.Errorf("got %q", w)
	}
}

func TestCheckUnusedAndUndefined(t *testing.T) {
	w := check(t, `
receivers: {otlp: {}, prometheus: {}}
exporters: {debug: {}}
extensions: {health_check: {}}
service:
  extensions: [health_check, pprof]
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [debug, otlp]
`, map[string]string{"service.pipelines.traces.exporters": "fragments/x.yaml"})
	for _, want := range []string{
		`pipeline traces references undefined exporter "otlp" (set in fragments/x.yaml)`,
		`service references undefined extension "pprof"`,
		`receiver "prometheus" is defined but not used`,
	} {
		if !slices.Contains(w, want) {
			t.Errorf("missing %q in %q", want, w)
		}
	}
}

func TestCheckConnectorsAreUsed(t *testing.T) {
	w := check(t, `
receivers: {otlp: {}}
connectors: {spanmetrics: {}}
exporters: {debug: {}}
service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [spanmetrics]
    metrics:
      receivers: [spanmetrics]
      exporters: [debug]
`, nil)
	if len(w) != 0 {
		t.Errorf("connectors must count as defined and used, got %q", w)
	}
}

func TestCheckCleanConfig(t *testing.T) {
	w := check(t, `
receivers: {otlp: {}}
processors: {memory_limiter: {}, batch/2: {}}
exporters: {debug: {}}
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch/2]
      exporters: [debug]
`, nil)
	if len(w) != 0 {
		t.Errorf("got %q", strings.Join(w, "; "))
	}
}
