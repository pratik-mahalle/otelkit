package analyze

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pratik-mahalle/otelkit/internal/model"
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
		"processors.memory_limiter: present in 2/3 targets, missing in c (also missing from pipelines traces)",
		"processors.batch.timeout: 2/3 targets use 5s; c uses 10s",
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
	for _, want := range []string{"group all: 3 targets", "likely drift (2):", "base (", "deviations from base:", "    c:"} {
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

func TestVaryPathMissingInSomeTargetsIsDrift(t *testing.T) {
	both := "receivers:\n  otlp:\n    protocols:\n      grpc: {endpoint: 0.0.0.0:4317}\n      http: {endpoint: 0.0.0.0:4318}\n"
	grpcOnly := "receivers:\n  otlp:\n    protocols:\n      grpc: {endpoint: 0.0.0.0:4317}\n"
	g := Analyze([]GroupInput{{Name: "all", Targets: []Target{target(t, "eu", both), target(t, "ap", both), target(t, "us", grpcOnly)}}}, Options{}).Groups[0]
	if len(g.Drift) != 1 || g.Drift[0].Line() != "receivers.otlp.protocols.http.endpoint: present in 2/3 targets, missing in us" {
		t.Errorf("an endpoint may vary in value, but a missing one is drift; got %+v", g.Drift)
	}
}

func TestVaryPathWithDifferentValuesIsNotDrift(t *testing.T) {
	ts := []Target{
		target(t, "a", "exporters: {otlp: {endpoint: a:4317}}\n"),
		target(t, "b", "exporters: {otlp: {endpoint: a:4317}}\n"),
		target(t, "c", "exporters: {otlp: {endpoint: c:4317}}\n"),
	}
	if d := Analyze([]GroupInput{{Name: "all", Targets: ts}}, Options{}).Groups[0].Drift; len(d) != 0 {
		t.Errorf("differing endpoint values are expected: %+v", d)
	}
}

const threePipelines = `
processors:
  memory_limiter: {check_interval: 1s}
  batch: {}
service:
  pipelines:
    traces: {processors: [memory_limiter, batch]}
    metrics: {processors: [memory_limiter, batch]}
    logs: {processors: [memory_limiter, batch]}
`

func TestDriftFoldsPipelineListsIntoMissingComponent(t *testing.T) {
	c := strings.NewReplacer("  memory_limiter: {check_interval: 1s}\n", "", "[memory_limiter, batch]", "[batch]").Replace(threePipelines)
	g := Analyze([]GroupInput{{Name: "all", Targets: []Target{target(t, "a", threePipelines), target(t, "b", threePipelines), target(t, "c", c)}}}, Options{}).Groups[0]
	if len(g.Drift) != 1 {
		t.Fatalf("one root cause must be one drift line, got %d: %+v", len(g.Drift), g.Drift)
	}
	if want := "processors.memory_limiter: present in 2/3 targets, missing in c (also missing from pipelines logs, metrics, traces)"; g.Drift[0].Line() != want {
		t.Errorf("got  %q\nwant %q", g.Drift[0].Line(), want)
	}
}

func TestDriftKeepsPipelineLineWithOtherDifferences(t *testing.T) {
	// k8sattributes is defined everywhere, so using it only in c's traces is not explained by any component line
	all := strings.Replace(threePipelines, "  batch: {}\n", "  batch: {}\n  k8sattributes: {}\n", 1)
	c := strings.NewReplacer("  memory_limiter: {check_interval: 1s}\n", "", "traces: {processors: [memory_limiter, batch]}", "traces: {processors: [k8sattributes, batch]}", "[memory_limiter, batch]", "[batch]").Replace(all)
	g := Analyze([]GroupInput{{Name: "all", Targets: []Target{target(t, "a", all), target(t, "b", all), target(t, "c", c)}}}, Options{}).Groups[0]
	var lines []string
	for _, d := range g.Drift {
		lines = append(lines, d.Line())
	}
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "service.pipelines.traces.processors: 2/3 targets use [memory_limiter, batch]; c uses [k8sattributes, batch]") {
		t.Errorf("traces differs beyond the missing component and must stay:\n%s", got)
	}
	if !strings.Contains(got, "(also missing from pipelines logs, metrics)") {
		t.Errorf("logs and metrics only lack memory_limiter and must fold:\n%s", got)
	}
}

func TestWriteTextSmallGroupsAndGrammar(t *testing.T) {
	var buf bytes.Buffer
	Analyze([]GroupInput{
		{Name: "one", Targets: []Target{target(t, "a", cfgA)}},
		{Name: "two", Targets: []Target{target(t, "a", cfgA), target(t, "b", cfgA)}},
	}, Options{}).WriteText(&buf)
	out := buf.String()
	for _, want := range []string{"group one: 1 target,", "group two: 2 targets,", "note: drift needs a majority (3+ targets); see deviations below"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func lines(g Group) string {
	var out []string
	for _, d := range g.Drift {
		out = append(out, d.Line())
	}
	return strings.Join(out, "\n")
}

func TestAbsentIsNeverMasked(t *testing.T) {
	plain := "exporters: {otlp: {endpoint: x}}\n"
	withHeader := "exporters: {otlp: {endpoint: x, headers: {authorization: secret}}}\n"
	g := Analyze([]GroupInput{{Name: "all", Targets: []Target{target(t, "a", plain), target(t, "b", plain), target(t, "c", withHeader)}}}, Options{}).Groups[0]
	if got := lines(g); strings.Contains(got, "****") || strings.Contains(got, "secret") {
		t.Errorf("a missing value must read as missing, and a secret must stay hidden:\n%s", got)
	}
}

func TestOnlyInPhrasing(t *testing.T) {
	base := "receivers: {otlp: {}}\nservice: {pipelines: {logs: {receivers: [otlp]}}}\n"
	extra := "receivers: {otlp: {}, filelog: {include: [/var/log/*.log]}}\nservice: {pipelines: {logs: {receivers: [otlp, filelog]}}}\n"
	g := Analyze([]GroupInput{{Name: "all", Targets: []Target{target(t, "a", base), target(t, "b", base), target(t, "c", extra)}}}, Options{}).Groups[0]
	got := lines(g)
	if want := "receivers.filelog: only in c (2/3 targets don't have it) (also added to pipelines logs)"; got != want {
		t.Errorf("one extra component must be one line\ngot  %q\nwant %q", got, want)
	}
}

func TestOnlyInShowsValue(t *testing.T) {
	a := "service: {telemetry: {logs: {level: info}}}\n"
	c := "service: {telemetry: {logs: {level: info, encoding: json}}}\n"
	g := Analyze([]GroupInput{{Name: "all", Targets: []Target{target(t, "a", a), target(t, "b", a), target(t, "c", c)}}}, Options{}).Groups[0]
	if want := "service.telemetry.logs.encoding: only in c = json (2/3 targets don't have it)"; lines(g) != want {
		t.Errorf("got %q want %q", lines(g), want)
	}
}

func TestWriteTextSummarizesOddTarget(t *testing.T) {
	odd := "receivers: {k8sobjects: {}}\nprocessors: {k8sattributes: {}}\nexporters: {otlp: {endpoint: x}}\nextensions: {pprof: {}}\nconnectors: {count: {}}\nservice: {telemetry: {logs: {level: debug}}}\n"
	var buf bytes.Buffer
	Analyze([]GroupInput{{Name: "all", Targets: []Target{target(t, "a", cfgA), target(t, "b", cfgA), target(t, "c", cfgA), target(t, "odd", odd)}}}, Options{}).WriteText(&buf)
	out := buf.String()
	if !strings.Contains(out, "odd: differs from the group in ") || strings.Contains(out, "missing in odd") {
		t.Errorf("a target that is the lone outlier on many lines must be summarized once:\n%s", out)
	}
}
