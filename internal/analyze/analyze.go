// Package analyze finds what a set of Collector configs share and where they drift.
package analyze

import (
	"maps"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/pratik-mahalle/otelkit/internal/diff"
	"github.com/pratik-mahalle/otelkit/internal/model"
)

// Absent marks a path a target does not have.
const Absent = "(absent)"

// DefaultVary lists paths that legitimately differ between targets.
var DefaultVary = []string{"*.endpoint", "*.headers.*", "*.auth.*", "*.tls.*_file", "processors.resource*.attributes.*.value"}

// Target is one config being analyzed.
type Target struct {
	Name string
	Root *yaml.Node
}

// GroupInput is a named set of targets analyzed together.
type GroupInput struct {
	Name    string
	Targets []Target
}

// Options tune Analyze.
type Options struct {
	Vary []string // extra path globs expected to differ
}

// Drift is a path where a strict majority agrees and the rest differ.
type Drift struct {
	Path     string            `json:"path"`
	Majority string            `json:"majority"`
	Agree    int               `json:"agree"`
	Total    int               `json:"total"`
	Outliers map[string]string `json:"outliers"`
}

// Group is the analysis of one group. All values are masked.
type Group struct {
	Name       string                       `json:"name"`
	Targets    []string                     `json:"targets"`
	Shared     float64                      `json:"shared"`
	Base       map[string]string            `json:"base"`
	Drift      []Drift                      `json:"drift"`
	Deviations map[string]map[string]string `json:"deviations"`
}

// Report is the full analysis.
type Report struct {
	Groups    []Group           `json:"groups"`
	CrossBase map[string]string `json:"cross_group_base,omitempty"`
}

// HasDrift reports whether any group has drift.
func (r Report) HasDrift() bool {
	return slices.ContainsFunc(r.Groups, func(g Group) bool { return len(g.Drift) > 0 })
}

// Analyze computes base, deviations and drift per group, plus a cross-group base when there are several groups.
func Analyze(groups []GroupInput, opt Options) Report {
	vary := append(slices.Clone(DefaultVary), opt.Vary...)
	var r Report
	var all []Target
	for _, g := range groups {
		r.Groups = append(r.Groups, analyzeGroup(g, vary))
		all = append(all, g.Targets...)
	}
	if len(groups) > 1 {
		r.CrossBase = masked(diff.Flatten(Base(all)))
	}
	return r
}

// Base returns what every target has in common.
func Base(targets []Target) *yaml.Node {
	if len(targets) == 0 {
		return model.NewMap()
	}
	b := model.Clone(targets[0].Root)
	for _, t := range targets[1:] {
		b = Intersect(nil, b, t.Root)
	}
	return b
}

// Intersect keeps shared mapping keys recursively and other values only when equal.
// Null and {} count as an empty mapping. It returns nil when nothing is shared.
func Intersect(path []string, a, b *yaml.Node) *yaml.Node {
	am, bm := asMap(a), asMap(b)
	if am != nil && bm != nil {
		// below component level an empty key and a populated one are different values
		component := len(path) == 2 && slices.Contains(model.Kinds, path[0])
		if len(path) > 0 && !component && (len(am.Content) == 0) != (len(bm.Content) == 0) {
			return nil
		}
		out := model.NewMap()
		for i := 0; i+1 < len(am.Content); i += 2 {
			k := am.Content[i]
			bv := model.Lookup(bm, k.Value)
			if bv == nil {
				continue
			}
			if v := Intersect(append(path[:len(path):len(path)], k.Value), am.Content[i+1], bv); v != nil {
				out.Content = append(out.Content, model.Clone(k), v)
			}
		}
		return out
	}
	if am == nil && bm == nil && diff.Text(path, a) == diff.Text(path, b) {
		return model.Clone(a)
	}
	return nil
}

func asMap(n *yaml.Node) *yaml.Node {
	switch {
	case model.IsNull(n):
		return model.NewMap()
	case n.Kind == yaml.MappingNode:
		return n
	}
	return nil
}

func analyzeGroup(g GroupInput, vary []string) Group {
	base := diff.Flatten(Base(g.Targets))
	out := Group{Name: g.Name, Base: masked(base), Deviations: map[string]map[string]string{}}
	flats := map[string]diff.Flat{}
	union := map[string]bool{}
	for _, t := range g.Targets {
		out.Targets = append(out.Targets, t.Name)
		f := diff.Flatten(t.Root)
		flats[t.Name] = f
		dev := map[string]string{}
		for p, v := range f {
			union[p] = true
			if _, ok := base[p]; !ok {
				dev[p] = diff.Mask(p, v.Text)
			}
		}
		if len(dev) > 0 {
			out.Deviations[t.Name] = dev
		}
	}
	out.Shared = 1
	if len(union) > 0 {
		out.Shared = float64(len(base)) / float64(len(union))
	}
	for _, p := range slices.Sorted(maps.Keys(union)) {
		if _, ok := base[p]; ok {
			continue
		}
		if d, ok := drift(p, out.Targets, flats, vary); ok {
			out.Drift = append(out.Drift, d)
		}
	}
	sort.SliceStable(out.Drift, func(i, j int) bool { return out.Drift[i].Agree > out.Drift[j].Agree })
	return out
}

// drift judges one path. Settings inside a component are judged only among targets that have the
// component, so a missing component is reported once, at the component.
func drift(p string, names []string, flats map[string]diff.Flat, vary []string) (Drift, bool) {
	// an expected-to-vary path may differ in value, but whether it is there at all still counts
	presenceOnly := slices.ContainsFunc(vary, func(g string) bool { return diff.Match(g, p) })
	comp := componentPath(p)
	values := map[string]string{}
	for _, n := range names {
		f := flats[n]
		if comp != "" && comp != p {
			if _, ok := f[comp]; !ok {
				continue
			}
		}
		text := Absent
		if v, ok := f[p]; ok {
			text = v.Text
		}
		if strings.Contains(text, "${env:") || strings.Contains(text, "${file:") {
			presenceOnly = true // a placeholder means the value is set per environment
		}
		values[n] = text
	}
	if presenceOnly {
		for n, v := range values {
			if v != Absent {
				values[n] = diff.Present
			}
		}
	}
	counts := map[string]int{}
	for _, v := range values {
		counts[v]++
	}
	top, best := "", 0
	for v, c := range counts {
		if c > best || (c == best && v < top) {
			top, best = v, c
		}
	}
	if best*2 <= len(values) || best == len(values) {
		return Drift{}, false
	}
	d := Drift{Path: p, Majority: diff.Mask(p, top), Agree: best, Total: len(values), Outliers: map[string]string{}}
	for n, v := range values {
		if v != top {
			d.Outliers[n] = diff.Mask(p, v)
		}
	}
	return d, true
}

func componentPath(p string) string {
	segs := diff.Split(p)
	if len(segs) >= 2 && slices.Contains(model.Kinds, segs[0]) {
		return diff.Join(segs[:2])
	}
	return ""
}

func masked(f diff.Flat) map[string]string {
	out := make(map[string]string, len(f))
	for p, v := range f {
		out[p] = diff.Mask(p, v.Text)
	}
	return out
}
