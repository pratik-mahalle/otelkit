package build

import (
	"strings"
	"testing"

	"github.com/pratikmahalle/otelkit/internal/diff"
	"github.com/pratikmahalle/otelkit/internal/model"
)

func layer(t *testing.T, kind Kind, file, in string) Layer {
	t.Helper()
	root, err := model.Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	return Layer{File: file, Kind: kind, Root: root}
}

func assertFlat(t *testing.T, m Merged, want string) {
	t.Helper()
	w, err := model.Parse([]byte(want))
	if err != nil {
		t.Fatal(err)
	}
	if ch := diff.Compare(diff.Flatten(m.Root), diff.Flatten(w)); len(ch) != 0 {
		out, _ := model.Marshal(m.Root)
		t.Fatalf("merged config differs at %s\n%s", ch[0].Path, out)
	}
}

func TestMergeDeepMergesMaps(t *testing.T) {
	m, err := Merge([]Layer{
		layer(t, Base, "base.yaml", "processors:\n  batch:\n    timeout: 5s\n"),
		layer(t, Fragment, "f.yaml", "processors:\n  batch:\n    send_batch_size: 100\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFlat(t, m, "processors:\n  batch:\n    timeout: 5s\n    send_batch_size: 100\n")
}

func TestMergeFragmentsMayOverwriteBase(t *testing.T) {
	m, err := Merge([]Layer{
		layer(t, Base, "base.yaml", "processors:\n  batch:\n    timeout: 5s\n"),
		layer(t, Fragment, "f.yaml", "processors:\n  batch:\n    timeout: 1s\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFlat(t, m, "processors:\n  batch:\n    timeout: 1s\n")
}

func TestMergeFragmentConflictIsError(t *testing.T) {
	_, err := Merge([]Layer{
		layer(t, Base, "base.yaml", "processors:\n  batch:\n    timeout: 5s\n"),
		layer(t, Fragment, "fragments/a.yaml", "processors:\n  batch:\n    timeout: 1s\n"),
		layer(t, Fragment, "fragments/b.yaml", "processors:\n  batch:\n    timeout: 2s\n"),
	})
	if err == nil || !strings.Contains(err.Error(), "fragments/b.yaml:3 processors.batch.timeout conflicts with fragments/a.yaml:3") {
		t.Fatalf("want conflict naming both files and lines, got %v", err)
	}
}

func TestMergeFragmentsAgreeingIsFine(t *testing.T) {
	_, err := Merge([]Layer{
		layer(t, Fragment, "a.yaml", "processors:\n  batch:\n    timeout: 1s\n"),
		layer(t, Fragment, "b.yaml", "processors:\n  batch:\n    timeout: \"1s\"\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMergeOverrideWins(t *testing.T) {
	m, err := Merge([]Layer{
		layer(t, Fragment, "a.yaml", "processors:\n  batch:\n    timeout: 1s\n"),
		layer(t, Override, "o.yaml", "processors:\n  batch:\n    timeout: 9s\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFlat(t, m, "processors:\n  batch:\n    timeout: 9s\n")
}

func TestMergePipelineListsAppendAndDedupe(t *testing.T) {
	m, err := Merge([]Layer{
		layer(t, Base, "base.yaml", "service:\n  pipelines:\n    traces:\n      processors: [memory_limiter, batch]\n"),
		layer(t, Fragment, "f.yaml", "service:\n  pipelines:\n    traces:\n      processors: [batch, k8sattributes]\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := model.Scalars(model.Get(m.Root, "service", "pipelines", "traces", "processors"))
	if strings.Join(got, ",") != "memory_limiter,batch,k8sattributes" {
		t.Errorf("processors = %v", got)
	}
}

func TestMergeReplaceTag(t *testing.T) {
	m, err := Merge([]Layer{
		layer(t, Base, "base.yaml", "service:\n  pipelines:\n    traces:\n      processors: [memory_limiter, batch]\n"),
		layer(t, Override, "o.yaml", "service:\n  pipelines:\n    traces:\n      processors: !replace [batch]\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	seq := model.Get(m.Root, "service", "pipelines", "traces", "processors")
	if strings.Join(model.Scalars(seq), ",") != "batch" {
		t.Errorf("processors = %v", model.Scalars(seq))
	}
	out, _ := model.Marshal(m.Root)
	if strings.Contains(string(out), "!replace") {
		t.Errorf("!replace tag must not reach the output:\n%s", out)
	}
}

func TestMergeNullDeletesOnlyInOverride(t *testing.T) {
	m, err := Merge([]Layer{
		layer(t, Base, "base.yaml", "processors:\n  batch:\n    timeout: 5s\n  memory_limiter:\n    limit_mib: 100\n"),
		layer(t, Fragment, "f.yaml", "processors:\n  batch:\n"),
		layer(t, Override, "o.yaml", "processors:\n  memory_limiter: null\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFlat(t, m, "processors:\n  batch:\n    timeout: 5s\n")
}

func TestMergeDoesNotMutateLayers(t *testing.T) {
	base := layer(t, Base, "base.yaml", "service:\n  pipelines:\n    traces:\n      processors: [batch]\n")
	if _, err := Merge([]Layer{base, layer(t, Fragment, "f.yaml", "service:\n  pipelines:\n    traces:\n      processors: [x]\n")}); err != nil {
		t.Fatal(err)
	}
	if got := model.Scalars(model.Get(base.Root, "service", "pipelines", "traces", "processors")); len(got) != 1 {
		t.Errorf("base layer mutated: %v", got)
	}
}

func TestSubstitute(t *testing.T) {
	root, _ := model.Parse([]byte("a: ${var:cluster}\nport: ${var:port}\nq: \"${var:port}\"\nb: ${env:TOKEN}\n"))
	if err := Substitute(root, map[string]string{"cluster": "prod-eu", "port": "4317"}, "base.yaml"); err != nil {
		t.Fatal(err)
	}
	out, _ := model.Marshal(root)
	for _, want := range []string{"a: prod-eu", "port: 4317\n", `q: "4317"`, "b: ${env:TOKEN}"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestSubstituteUnknownVar(t *testing.T) {
	root, _ := model.Parse([]byte("a: 1\nb: ${var:nope}\n"))
	err := Substitute(root, nil, "base.yaml")
	if err == nil || err.Error() != `base.yaml:2 unknown var "nope"` {
		t.Fatalf("got %v", err)
	}
}

func TestMergeFragmentConflictWhenFirstMatchesBase(t *testing.T) {
	_, err := Merge([]Layer{
		layer(t, Base, "base.yaml", "processors:\n  batch:\n    timeout: 5s\n"),
		layer(t, Fragment, "fragments/a.yaml", "processors:\n  batch:\n    timeout: 5s\n"),
		layer(t, Fragment, "fragments/b.yaml", "processors:\n  batch:\n    timeout: 10s\n"),
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with fragments/a.yaml") {
		t.Fatalf("two fragments disagree; want conflict, got %v", err)
	}
}
