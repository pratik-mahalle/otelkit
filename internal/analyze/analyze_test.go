package analyze

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pratikmahalle/otelkit/internal/model"
)

const cfgA = `
receivers:
  otlp:
    protocols: {grpc: {endpoint: 0.0.0.0:4317}}
processors:
  memory_limiter: {check_interval: 1s, limit_percentage: 80}
  batch: {timeout: 5s}
exporters:
  otlp:
    endpoint: a.example.com:4317
    headers: {authorization: Bearer secret-a}
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [otlp]
`

func target(t *testing.T, name, in string) Target {
	t.Helper()
	root, err := model.Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	return Target{Name: name, Root: root}
}

func fleet(t *testing.T) []Target {
	b := strings.NewReplacer("a.example.com", "b.example.com", "secret-a", "secret-b").Replace(cfgA)
	c := strings.NewReplacer(
		"a.example.com", "c.example.com", "secret-a", "secret-c",
		"  memory_limiter: {check_interval: 1s, limit_percentage: 80}\n", "",
		"[memory_limiter, batch]", "[batch]",
		"timeout: 5s", "timeout: 10s",
	).Replace(cfgA)
	return []Target{target(t, "a", cfgA), target(t, "b", b), target(t, "c", c)}
}

func TestAnalyzeFindsDrift(t *testing.T) {
	r := Analyze([]GroupInput{{Name: "all", Targets: fleet(t)}}, Options{})
	g := r.Groups[0]
	var lines []string
	for _, d := range g.Drift {
		lines = append(lines, d.Line())
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{
		"processors.memory_limiter: present in 2/3 targets, missing in c",
		"processors.batch.timeout: 2/3 targets use 5s; c uses 10s",
		"service.pipelines.traces.processors: 2/3 targets use [memory_limiter, batch]; c uses [batch]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing drift %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "endpoint") || strings.Contains(got, "headers") || strings.Contains(got, "check_interval") {
		t.Errorf("expected-to-vary paths or children of a missing component flagged:\n%s", got)
	}
	if g.Base["processors.batch"] != "(present)" || g.Base["receivers.otlp.protocols.grpc.endpoint"] != "0.0.0.0:4317" {
		t.Errorf("base = %v", g.Base)
	}
	if g.Deviations["a"]["exporters.otlp.headers.authorization"] != "****" {
		t.Errorf("secrets must be masked: %v", g.Deviations["a"])
	}
}

func TestAnalyzeUserVary(t *testing.T) {
	r := Analyze([]GroupInput{{Name: "all", Targets: fleet(t)}}, Options{Vary: []string{"processors.batch.*"}})
	for _, d := range r.Groups[0].Drift {
		if strings.HasPrefix(d.Path, "processors.batch.") {
			t.Errorf("--vary glob ignored: %s", d.Line())
		}
	}
}

func TestAnalyzePlaceholdersAreExpectedToVary(t *testing.T) {
	ts := []Target{
		target(t, "a", "exporters:\n  otlp:\n    compression: gzip\n"),
		target(t, "b", "exporters:\n  otlp:\n    compression: gzip\n"),
		target(t, "c", "exporters:\n  otlp:\n    compression: ${env:COMPRESSION}\n"),
	}
	if d := Analyze([]GroupInput{{Name: "all", Targets: ts}}, Options{}).Groups[0].Drift; len(d) != 0 {
		t.Errorf("placeholder value must not be drift: %+v", d)
	}
}

func TestAnalyzeNoMajorityIsNotDrift(t *testing.T) {
	ts := []Target{
		target(t, "a", "processors: {batch: {timeout: 1s}}\n"),
		target(t, "b", "processors: {batch: {timeout: 2s}}\n"),
	}
	if d := Analyze([]GroupInput{{Name: "all", Targets: ts}}, Options{}).Groups[0].Drift; len(d) != 0 {
		t.Errorf("1 vs 1 has no majority: %+v", d)
	}
}

func TestAnalyzeGroupsAndCrossBase(t *testing.T) {
	ts := fleet(t)
	r := Analyze([]GroupInput{{Name: "k8s", Targets: ts[:2]}, {Name: "vms", Targets: ts[2:]}}, Options{})
	if len(r.Groups) != 2 || r.CrossBase["processors.batch"] != "(present)" {
		t.Fatalf("got %+v", r)
	}
	if _, ok := r.CrossBase["processors.memory_limiter"]; ok {
		t.Error("memory_limiter is not in every group")
	}
}

func TestWriteText(t *testing.T) {
	var buf bytes.Buffer
	r := Analyze([]GroupInput{{Name: "all", Targets: fleet(t)}}, Options{})
	r.WriteText(&buf)
	out := buf.String()
	for _, want := range []string{"group all: 3 targets", "likely drift (3):", "base (", "deviations from base:", "    c:"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-") {
		t.Errorf("secret leaked:\n%s", out)
	}
	if !r.HasDrift() {
		t.Error("HasDrift")
	}
}

func TestBaseIgnoresCommentsInListsOfMaps(t *testing.T) {
	plain := "processors:\n  attributes:\n    actions:\n      - {key: a, value: 1, action: insert}\n"
	commented := "processors:\n  attributes:\n    actions:\n      - {key: \"a\", value: 1, action: insert} # why\n"
	g := Analyze([]GroupInput{{Name: "all", Targets: []Target{target(t, "a", plain), target(t, "b", plain), target(t, "c", commented)}}}, Options{}).Groups[0]
	if g.Shared != 1 || len(g.Deviations) != 0 {
		t.Errorf("a comment or quoting difference must not split the base: shared=%v deviations=%v", g.Shared, g.Deviations)
	}
}
