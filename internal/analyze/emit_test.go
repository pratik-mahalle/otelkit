package analyze

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pratik-mahalle/otelkit/internal/build"
	"github.com/pratik-mahalle/otelkit/internal/diff"
	"github.com/pratik-mahalle/otelkit/internal/model"
	"github.com/pratik-mahalle/otelkit/internal/source"
)

func emitTargets(t *testing.T) []EmitTarget {
	var out []EmitTarget
	for _, tg := range fleet(t) {
		out = append(out, EmitTarget{Name: tg.Name, Root: tg.Root, Deployed: source.Deployed{File: "/configs/" + tg.Name + ".yaml"}})
	}
	// a null-body component inside a section the base has, and a top-level section only one target has
	out[0].Root, _ = model.Parse([]byte(strings.Replace(cfgA, "processors:\n", "processors:\n  transform:\n", 1) + "connectors:\n  spanmetrics:\n"))
	return out
}

func TestEmitRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fleet")
	ts := emitTargets(t)
	if err := Emit(dir, ts); err != nil {
		t.Fatal(err)
	}
	f, err := build.Load(filepath.Join(dir, "fleet.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Join(f.Dir, f.Targets["a"].Deployed.File); got != "/configs/a.yaml" {
		t.Errorf("deployed not recorded: %+v", f.Targets["a"])
	}
	for _, tg := range ts {
		m, err := f.Render(tg.Name)
		if err != nil {
			t.Fatal(err)
		}
		if ch := diff.Compare(diff.Flatten(tg.Root), diff.Flatten(m.Root)); len(ch) != 0 {
			t.Errorf("%s does not round-trip at %s", tg.Name, ch[0].Path)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "base.yaml")); err != nil {
		t.Error(err)
	}
}

func TestEmitRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644)
	if err := Emit(dir, emitTargets(t)); err == nil {
		t.Fatal("must refuse to write into a non-empty dir")
	}
}

func TestEmitRejectsDuplicateNames(t *testing.T) {
	ts := emitTargets(t)
	ts[1].Name = ts[0].Name
	dir := filepath.Join(t.TempDir(), "fleet")
	if err := Emit(dir, ts); err == nil {
		t.Fatal("duplicate target names would silently drop a config")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("nothing may be written on failure")
	}
}

func TestEmitWritesRelativeDeployedPaths(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "configs", "a.yaml")
	os.MkdirAll(filepath.Dir(cfg), 0o755)
	os.WriteFile(cfg, []byte(cfgA), 0o644)
	ts := emitTargets(t)[:1]
	ts[0].Deployed = source.Deployed{File: cfg}
	if err := Emit(filepath.Join(root, "fleet"), ts); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "fleet", "fleet.yaml"))
	if !strings.Contains(string(data), "file: ../configs/a.yaml") {
		t.Errorf("a committed fleet must work on other machines; want a relative path:\n%s", data)
	}
}

func TestEmitLeavesNoTempDirs(t *testing.T) {
	root := t.TempDir()
	if err := Emit(filepath.Join(root, "fleet"), emitTargets(t)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != "fleet" {
		t.Errorf("only the fleet dir may remain, got %v", entries)
	}
}
