// Package model parses Collector YAML into yaml.Node trees and provides helpers for walking them.
package model

import (
	"bytes"
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"
)

// Kinds are the top-level sections that hold components.
var Kinds = []string{"receivers", "processors", "exporters", "connectors", "extensions"}

// Parse decodes a Collector config into its root mapping with aliases and merge keys (<<)
// expanded. Empty or comment-only input yields an empty mapping.
func Parse(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || IsNull(doc.Content[0]) {
		return NewMap(), nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: top level must be a mapping", root.Line)
	}
	e := expander{active: map[*yaml.Node]bool{}}
	out := e.expand(root)
	return out, e.err
}

// maxNodes bounds alias expansion so a "billion laughs" document cannot exhaust memory.
const maxNodes = 1_000_000

type expander struct {
	active map[*yaml.Node]bool // anchors being expanded, to catch self-reference
	nodes  int
	err    error
}

// expand returns a copy of n with aliases replaced by copies of their targets and merge
// keys folded into their parent mapping; explicit keys win over merged ones.
func (e *expander) expand(n *yaml.Node) *yaml.Node {
	if e.err != nil {
		return n
	}
	if e.nodes++; e.nodes > maxNodes {
		e.err = fmt.Errorf("too many nodes after expanding aliases (limit %d)", maxNodes)
		return n
	}
	if n.Kind == yaml.AliasNode {
		if e.active[n.Alias] {
			e.err = fmt.Errorf("line %d: alias *%s refers to itself", n.Line, n.Value)
			return n
		}
		e.active[n.Alias] = true
		defer delete(e.active, n.Alias)
		return e.expand(n.Alias)
	}
	if n.Anchor != "" {
		e.active[n] = true
		defer delete(e.active, n)
	}
	c := *n
	c.Anchor = "" // copies of an anchored node must not repeat the anchor
	c.Content = nil
	if n.Kind != yaml.MappingNode {
		for _, ch := range n.Content {
			c.Content = append(c.Content, e.expand(ch))
		}
		return &c
	}
	var merged []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], e.expand(n.Content[i+1])
		if k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" {
			merged = append(merged, v)
			continue
		}
		c.Content = append(c.Content, k, v)
	}
	for _, m := range merged {
		srcs := []*yaml.Node{m}
		if m.Kind == yaml.SequenceNode {
			srcs = m.Content
		}
		for _, s := range srcs {
			for i := 0; i+1 < len(s.Content); i += 2 {
				if Lookup(&c, s.Content[i].Value) == nil {
					c.Content = append(c.Content, s.Content[i], s.Content[i+1])
				}
			}
		}
	}
	return &c
}

// NewMap returns an empty mapping node.
func NewMap() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }

// Lookup returns the value for key in mapping m, or nil if m is not a mapping or lacks key.
func Lookup(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// Get walks nested mapping keys.
func Get(m *yaml.Node, keys ...string) *yaml.Node {
	for _, k := range keys {
		m = Lookup(m, k)
	}
	return m
}

// Set replaces the value for key in mapping m, or appends key.
func Set(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
}

// Delete removes key from mapping m.
func Delete(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = slices.Delete(m.Content, i, i+2)
			return
		}
	}
}

// Keys returns the keys of mapping m in order.
func Keys(m *yaml.Node) []string {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	var out []string
	for i := 0; i+1 < len(m.Content); i += 2 {
		out = append(out, m.Content[i].Value)
	}
	return out
}

// Scalars returns the scalar values of a sequence.
func Scalars(seq *yaml.Node) []string {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	var out []string
	for _, n := range seq.Content {
		out = append(out, n.Value)
	}
	return out
}

// IsNull reports whether n is missing or an explicit/implicit null.
func IsNull(n *yaml.Node) bool {
	return n == nil || (n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null")
}

// Clone deep-copies n.
func Clone(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	c := *n
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, ch := range n.Content {
		c.Content[i] = Clone(ch)
	}
	return &c
}

// Marshal encodes root with a 2-space indent.
func Marshal(root *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Pipeline lists the component IDs one pipeline uses.
type Pipeline struct {
	Receivers, Processors, Exporters []string
}

// Pipelines returns service.pipelines keyed by pipeline name.
func Pipelines(root *yaml.Node) map[string]Pipeline {
	out := map[string]Pipeline{}
	ps := Get(root, "service", "pipelines")
	for _, name := range Keys(ps) {
		p := Lookup(ps, name)
		out[name] = Pipeline{
			Receivers:  Scalars(Lookup(p, "receivers")),
			Processors: Scalars(Lookup(p, "processors")),
			Exporters:  Scalars(Lookup(p, "exporters")),
		}
	}
	return out
}
