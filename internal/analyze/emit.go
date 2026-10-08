package analyze

import (
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/pratik-mahalle/otelkit/internal/build"
	"github.com/pratik-mahalle/otelkit/internal/diff"
	"github.com/pratik-mahalle/otelkit/internal/model"
	"github.com/pratik-mahalle/otelkit/internal/source"
)

// EmitTarget is a loaded config to turn into a fleet target.
type EmitTarget struct {
	Name     string
	Root     *yaml.Node
	Deployed source.Deployed
}

// Emit writes base.yaml, overrides/<target>.yaml and fleet.yaml into dir so that building the
// fleet reproduces every target. It verifies that first, writes nothing on failure, and refuses
// a non-empty dir so an existing fleet is never overwritten.
func Emit(dir string, targets []EmitTarget) error {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty", dir)
	}
	ts := make([]Target, len(targets))
	seen := map[string]bool{}
	for i, t := range targets {
		if seen[t.Name] {
			return fmt.Errorf("two targets are named %q; nothing written", t.Name)
		}
		seen[t.Name] = true
		ts[i] = Target{Name: t.Name, Root: t.Root}
	}
	base := Base(ts)
	fl := build.Fleet{Otelcol: "otelcol-contrib", Base: "base.yaml", Targets: map[string]build.TargetSpec{}}
	files := map[string]*yaml.Node{"base.yaml": base}
	for _, t := range targets {
		rel := "overrides/" + t.Name + ".yaml"
		ov := overrideFor(base, t.Root)
		m, err := build.Merge([]build.Layer{
			{File: "base.yaml", Kind: build.Base, Root: base},
			{File: rel, Kind: build.Override, Root: ov},
		})
		if err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
		if ch := diff.Compare(diff.Flatten(t.Root), diff.Flatten(m.Root)); len(ch) > 0 {
			return fmt.Errorf("emitted fleet would not reproduce %s (first difference at %s); nothing written", t.Name, ch[0].Path)
		}
		spec := build.TargetSpec{Overrides: rel}
		if t.Deployed.File != "" || t.Deployed.K8s != nil {
			d := t.Deployed
			spec.Deployed = &d
		}
		fl.Targets[t.Name] = spec
		files[rel] = ov
	}
	for rel, node := range files {
		data, err := model.Marshal(node)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, rel), data); err != nil {
			return err
		}
	}
	data, err := yaml.Marshal(fl)
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, "fleet.yaml"), data)
}

// overrideFor returns what target adds beyond base. Base is the intersection of all
// targets, so every base key exists in target with the same value; the round-trip check in
// Emit catches it if that ever stops holding.
func overrideFor(base, target *yaml.Node) *yaml.Node {
	out := model.NewMap()
	bm, tm := asMap(base), asMap(target)
	for i := 0; i+1 < len(tm.Content); i += 2 {
		k, tv := tm.Content[i], tm.Content[i+1]
		bv := model.Lookup(bm, k.Value)
		switch {
		case bv == nil && model.IsNull(tv):
			out.Content = append(out.Content, model.Clone(k), model.NewMap()) // null would delete in an override
		case bv == nil:
			out.Content = append(out.Content, model.Clone(k), model.Clone(tv))
		case asMap(bv) != nil && asMap(tv) != nil:
			if sub := overrideFor(bv, tv); len(sub.Content) > 0 {
				out.Content = append(out.Content, model.Clone(k), sub)
			}
		}
	}
	return out
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
