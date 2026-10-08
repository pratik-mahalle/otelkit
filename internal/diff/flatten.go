// Package diff flattens Collector configs into path → value maps and compares them.
package diff

import (
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/pratikmahalle/otelkit/internal/model"
)

// Present is the value of a component's presence entry.
const Present = "(present)"

// Value is one flattened leaf.
type Value struct {
	Text string // canonical text used for comparison
	Line int
}

// Flat maps a path to its value.
type Flat map[string]Value

// Flatten turns a config into leaf paths plus a presence entry for every component.
func Flatten(root *yaml.Node) Flat {
	f := Flat{}
	walk(f, nil, root)
	return f
}

func walk(f Flat, path []string, n *yaml.Node) {
	if len(path) == 2 && slices.Contains(model.Kinds, path[0]) {
		f[Join(path)] = Value{Text: Present, Line: n.Line}
	}
	switch {
	case model.IsNull(n):
		// null and {} add nothing beyond presence
	case n.Kind == yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			walk(f, append(path[:len(path):len(path)], n.Content[i].Value), n.Content[i+1])
		}
	case n.Kind == yaml.SequenceNode && hasCollections(n):
		for i, ch := range n.Content {
			walk(f, append(path[:len(path):len(path)], strconv.Itoa(i)), ch)
		}
	default:
		f[Join(path)] = Value{Text: Text(path, n), Line: n.Line}
	}
}

// Text is the canonical comparison text of a non-mapping node at path.
func Text(path []string, n *yaml.Node) string {
	switch {
	case n.Kind == yaml.ScalarNode:
		return n.Value
	case n.Kind == yaml.SequenceNode && !hasCollections(n):
		items := model.Scalars(n)
		if isSet(path) {
			items = slices.Sorted(slices.Values(items))
		}
		return "[" + strings.Join(items, ", ") + "]"
	default:
		b, _ := model.Marshal(n)
		return string(b)
	}
}

func hasCollections(seq *yaml.Node) bool {
	return slices.ContainsFunc(seq.Content, func(n *yaml.Node) bool { return n.Kind != yaml.ScalarNode })
}

// isSet reports lists whose order the Collector ignores.
func isSet(p []string) bool {
	if len(p) == 2 && p[0] == "service" && p[1] == "extensions" {
		return true
	}
	return len(p) == 4 && p[0] == "service" && p[1] == "pipelines" && (p[3] == "receivers" || p[3] == "exporters")
}

// Join builds a path, quoting segments that contain dots.
func Join(segs []string) string {
	q := make([]string, len(segs))
	for i, s := range segs {
		if strings.Contains(s, ".") {
			s = `"` + s + `"`
		}
		q[i] = s
	}
	return strings.Join(q, ".")
}

// Split is the inverse of Join.
func Split(path string) []string {
	var segs []string
	var cur strings.Builder
	quoted := false
	for _, r := range path {
		switch {
		case r == '"':
			quoted = !quoted
		case r == '.' && !quoted:
			segs = append(segs, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(segs, cur.String())
}
