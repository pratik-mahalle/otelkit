package model

import (
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestParseEmptyAndCommentOnly(t *testing.T) {
	for _, in := range []string{"", "# only a comment\n", "---\n"} {
		root, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if len(Keys(root)) != 0 {
			t.Fatalf("Parse(%q) keys = %v, want none", in, Keys(root))
		}
	}
}

func TestParseRejectsNonMapping(t *testing.T) {
	if _, err := Parse([]byte("- a\n- b\n")); err == nil {
		t.Fatal("want error for top-level sequence")
	}
}

func TestParseExpandsAliasesAndMergeKeys(t *testing.T) {
	in := `
x-defaults: &defaults
  timeout: 5s
  send_batch_size: 100
processors:
  batch:
    <<: *defaults
    timeout: 1s
  batch/2: *defaults
`
	root, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	b := Get(root, "processors", "batch")
	if got := Lookup(b, "timeout").Value; got != "1s" {
		t.Errorf("explicit key must win over merge key: timeout = %s", got)
	}
	if got := Lookup(b, "send_batch_size").Value; got != "100" {
		t.Errorf("merged send_batch_size = %s, want 100", got)
	}
	if Lookup(b, "<<") != nil {
		t.Error("merge key must not survive")
	}
	if got := Get(root, "processors", "batch/2", "timeout").Value; got != "5s" {
		t.Errorf("alias not expanded: timeout = %s", got)
	}
	out, err := Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(out); err != nil {
		t.Fatalf("marshalled output must re-parse (no duplicate anchors): %v\n%s", err, out)
	}
}

func TestHelpers(t *testing.T) {
	root, _ := Parse([]byte("a: 1\nb:\nc: {d: 2}\n"))
	if !IsNull(Lookup(root, "b")) {
		t.Error("b should be null")
	}
	Set(root, "a", &yaml.Node{Kind: yaml.ScalarNode, Value: "2"})
	if Lookup(root, "a").Value != "2" {
		t.Error("Set did not replace a")
	}
	Delete(root, "b")
	if !slices.Equal(Keys(root), []string{"a", "c"}) {
		t.Errorf("keys = %v", Keys(root))
	}
	c := Clone(root)
	Lookup(c, "c").Content[1].Value = "9"
	if Get(root, "c", "d").Value != "2" {
		t.Error("Clone must deep-copy")
	}
}

func TestPipelines(t *testing.T) {
	root, _ := Parse([]byte(`
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [otlp, debug]
`))
	p := Pipelines(root)["traces"]
	if !slices.Equal(p.Processors, []string{"memory_limiter", "batch"}) || !slices.Equal(p.Exporters, []string{"otlp", "debug"}) {
		t.Errorf("got %+v", p)
	}
}
