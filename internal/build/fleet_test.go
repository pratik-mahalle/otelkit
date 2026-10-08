package build

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pratikmahalle/otelkit/internal/model"
	"github.com/pratikmahalle/otelkit/internal/validate"
)

var update = flag.Bool("update", false, "rewrite golden files")

// copyFleet copies testdata/<name> into a temp dir so Run can write out/.
func copyFleet(t *testing.T, name string) *Fleet {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("testdata", name))); err != nil {
		t.Fatal(err)
	}
	f, err := Load(filepath.Join(dir, "fleet.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRunGolden(t *testing.T) {
	f := copyFleet(t, "basic")
	results := Run(f, Options{})
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("%s: %v", r.Name, r.Err)
		}
		if r.Validation.Status != validate.Skipped {
			t.Errorf("%s: missing binary must be skipped, got %+v", r.Name, r.Validation)
		}
		got, err := os.ReadFile(r.Output)
		if err != nil {
			t.Fatal(err)
		}
		golden := filepath.Join("testdata", "basic", "expected", r.Name+".yaml")
		if *update {
			os.MkdirAll(filepath.Dir(golden), 0o755)
			os.WriteFile(golden, got, 0o644)
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run go test ./internal/build -update)", err)
		}
		if string(got) != string(want) {
			t.Errorf("%s differs from golden\n--- got\n%s\n--- want\n%s", r.Name, got, want)
		}
	}
	prod := results[0]
	if prod.Name != "prod-eu" || !slices.Contains(prod.Warnings, "pipeline traces: batch should be the last processor") {
		t.Errorf("prod-eu warnings = %q", prod.Warnings)
	}
}

func TestRenderSemantics(t *testing.T) {
	f := copyFleet(t, "basic")
	m, err := f.Render("prod-eu")
	if err != nil {
		t.Fatal(err)
	}
	if got := model.Get(m.Root, "processors", "batch", "timeout").Value; got != "10s" {
		t.Errorf("override not applied: %s", got)
	}
	if got := model.Get(m.Root, "processors", "resource", "attributes").Content[0].Content[3].Value; got != "prod-eu" {
		t.Errorf("var not substituted: %s", got)
	}
	if got := model.Get(m.Root, "exporters", "otlp", "endpoint").Value; got != "${env:BACKEND_ENDPOINT}" {
		t.Errorf("env placeholder must pass through: %s", got)
	}
	b, _ := Bytes(m.Root)
	if !strings.HasPrefix(string(b), Header) {
		t.Error("missing header")
	}
}

func TestRunCheckDetectsStaleOutput(t *testing.T) {
	f := copyFleet(t, "basic")
	Run(f, Options{})
	if rs := Run(f, Options{Check: true}); rs[0].Err != nil || rs[1].Err != nil {
		t.Fatalf("fresh output must pass --check: %+v", rs)
	}
	os.WriteFile(f.OutputPath("vm-a"), []byte("stale\n"), 0o644)
	rs := Run(f, Options{Check: true, Targets: []string{"vm-a"}})
	if rs[0].Err == nil || !strings.Contains(rs[0].Err.Error(), "stale") {
		t.Fatalf("want stale error, got %+v", rs[0])
	}
	if got, _ := os.ReadFile(f.OutputPath("vm-a")); string(got) != "stale\n" {
		t.Error("--check must not write")
	}
}

func TestRunFailuresAreIndependent(t *testing.T) {
	f := copyFleet(t, "basic")
	f.Targets["broken"] = TargetSpec{Fragments: []string{"missing"}}
	rs := Run(f, Options{})
	if len(rs) != 3 || rs[0].Err == nil || !strings.Contains(rs[0].Err.Error(), "fragments/missing.yaml") ||
		rs[1].Err != nil || rs[2].Err != nil {
		t.Fatalf("got %+v", rs)
	}
	if _, err := os.Stat(f.OutputPath("prod-eu")); err != nil {
		t.Error("other targets must still be written")
	}
}

func TestRunRequireValidateWritesNothing(t *testing.T) {
	f := copyFleet(t, "basic")
	rs := Run(f, Options{RequireValidate: true})
	if rs[0].Err == nil {
		t.Fatal("missing binary must fail with --require-validate")
	}
	if _, err := os.Stat(f.OutputPath("prod-eu")); !os.IsNotExist(err) {
		t.Error("a target that failed validation must not be written")
	}
}

func TestRunValidatesWithBinary(t *testing.T) {
	f := copyFleet(t, "basic")
	bin, _ := filepath.Abs("../validate/testdata/fake-otelcol")
	f.Otelcol = bin
	for _, r := range Run(f, Options{}) {
		if r.Validation.Status != validate.OK {
			t.Errorf("%s: %+v", r.Name, r.Validation)
		}
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "fleet.yaml"), []byte("base: base.yaml\ntargets:\n  a:\n    fragment: [x]\n"), 0o644)
	if _, err := Load(filepath.Join(dir, "fleet.yaml")); err == nil {
		t.Fatal("typo 'fragment' must be rejected")
	}
}

// fleet.yaml is often changed in pull requests; it must not be able to point build at a script to execute.
func TestLoadRejectsOtelcolPath(t *testing.T) {
	dir := t.TempDir()
	for _, bin := range []string{"./evil.sh", "/tmp/evil.sh", "tools/otelcol"} {
		os.WriteFile(filepath.Join(dir, "fleet.yaml"), []byte("otelcol: "+bin+"\nbase: base.yaml\n"), 0o644)
		if _, err := Load(filepath.Join(dir, "fleet.yaml")); err == nil || !strings.Contains(err.Error(), "--otelcol") {
			t.Errorf("otelcol %q: want error pointing at --otelcol, got %v", bin, err)
		}
	}
}
