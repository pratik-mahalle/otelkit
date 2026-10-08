// Package build merges fleet layers into per-target Collector configs and writes them.
package build

import (
	"bytes"
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/pratikmahalle/otelkit/internal/diff"
	"github.com/pratikmahalle/otelkit/internal/model"
)

// Kind is a layer's role in the merge; it decides which overwrites are allowed.
type Kind int

const (
	Base Kind = iota
	Fragment
	Override
)

// Layer is one parsed config file in a target's merge order.
type Layer struct {
	File string
	Kind Kind
	Root *yaml.Node
}

// Merged is a merged config plus, for every path, the file that last set it.
type Merged struct {
	Root    *yaml.Node
	Origins map[string]string
}

type origin struct {
	file string
	line int
	kind Kind
}

// Merge folds layers in order: maps deep-merge, pipeline component lists append,
// fragments may not disagree with each other, and null in an override deletes.
func Merge(layers []Layer) (Merged, error) {
	m := merger{origins: map[string]origin{}}
	root := model.NewMap()
	for _, l := range layers {
		if err := m.merge(root, l.Root, nil, l); err != nil {
			return Merged{}, err
		}
	}
	files := make(map[string]string, len(m.origins))
	for p, o := range m.origins {
		files[p] = o.file
	}
	return Merged{Root: root, Origins: files}, nil
}

type merger struct{ origins map[string]origin }

func (m merger) merge(dst, src *yaml.Node, path []string, l Layer) error {
	for i := 0; i+1 < len(src.Content); i += 2 {
		key, val := src.Content[i].Value, src.Content[i+1]
		p := append(path[:len(path):len(path)], key)
		cur := model.Lookup(dst, key)
		switch {
		case model.IsNull(val) && l.Kind == Override:
			model.Delete(dst, key)
		case model.IsNull(val) && cur != nil:
			// a null body (e.g. `batch:`) adds nothing to an existing entry
		case cur == nil || model.IsNull(cur):
			model.Set(dst, key, clean(val))
			m.mark(p, l, val)
		case cur.Kind == yaml.MappingNode && val.Kind == yaml.MappingNode:
			if err := m.merge(cur, val, p, l); err != nil {
				return err
			}
		case isPipelineList(p) && cur.Kind == yaml.SequenceNode && val.Kind == yaml.SequenceNode && val.Tag != "!replace":
			appendUnique(cur, val)
			m.mark(p, l, val)
		default:
			if same(cur, val) {
				continue
			}
			if o := m.origins[diff.Join(p)]; l.Kind == Fragment && o.kind == Fragment {
				return fmt.Errorf("%s:%d %s conflicts with %s:%d", l.File, val.Line, diff.Join(p), o.file, o.line)
			}
			model.Set(dst, key, clean(val))
			m.mark(p, l, val)
		}
	}
	return nil
}

// mark records l as the origin of p and everything below it.
func (m merger) mark(p []string, l Layer, n *yaml.Node) {
	m.origins[diff.Join(p)] = origin{file: l.File, line: n.Line, kind: l.Kind}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			m.mark(append(p[:len(p):len(p)], n.Content[i].Value), l, n.Content[i+1])
		}
	}
}

func isPipelineList(p []string) bool {
	return len(p) == 4 && p[0] == "service" && p[1] == "pipelines" &&
		(p[3] == "receivers" || p[3] == "processors" || p[3] == "exporters")
}

func appendUnique(dst, src *yaml.Node) {
	have := map[string]bool{}
	for _, v := range model.Scalars(dst) {
		have[v] = true
	}
	for _, n := range src.Content {
		if !have[n.Value] {
			dst.Content = append(dst.Content, model.Clone(n))
			have[n.Value] = true
		}
	}
}

// clean deep-copies n and drops the !replace tag so it never reaches the output.
func clean(n *yaml.Node) *yaml.Node {
	c := model.Clone(n)
	var strip func(*yaml.Node)
	strip = func(x *yaml.Node) {
		if x.Tag == "!replace" {
			x.Tag = "!!seq"
		}
		for _, ch := range x.Content {
			strip(ch)
		}
	}
	strip(c)
	return c
}

// same compares values ignoring quoting style and comments.
func same(a, b *yaml.Node) bool {
	x, err1 := model.Marshal(plain(a))
	y, err2 := model.Marshal(plain(b))
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

func plain(n *yaml.Node) *yaml.Node {
	c := clean(n)
	var reset func(*yaml.Node)
	reset = func(x *yaml.Node) {
		x.Style, x.HeadComment, x.LineComment, x.FootComment = 0, "", "", ""
		if x.Kind == yaml.ScalarNode && x.Tag == "!!str" {
			x.Tag = "" // let "1s" and 1s compare equal
		}
		for _, ch := range x.Content {
			reset(ch)
		}
	}
	reset(c)
	return c
}
