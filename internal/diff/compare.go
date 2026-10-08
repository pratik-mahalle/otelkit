package diff

import (
	"regexp"
	"sort"
	"strings"
)

// Change is one difference between two flattened configs; From or To is nil when the path is absent on that side.
type Change struct {
	Path     string
	From, To *Value
}

// Compare lists differences from a to b, sorted by path.
func Compare(a, b Flat) []Change {
	var out []Change
	for p, av := range a {
		bv, ok := b[p]
		switch {
		case !ok:
			out = append(out, Change{Path: p, From: &av})
		case av.Text != bv.Text:
			out = append(out, Change{Path: p, From: &av, To: &bv})
		}
	}
	for p, bv := range b {
		if _, ok := a[p]; !ok {
			out = append(out, Change{Path: p, To: &bv})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

var secretKey = regexp.MustCompile(`(?i)token|password|secret|key|authorization|api[-_]?key|headers`)

// Mask hides values whose path looks like a credential. Placeholders are kept: they hold no secret.
func Mask(path, text string) string {
	if text == Present || strings.Contains(text, "${") || !secretKey.MatchString(path) {
		return text
	}
	return "****"
}

// Match reports whether path matches glob, where * matches any characters including dots.
func Match(glob, path string) bool {
	re := "^" + strings.ReplaceAll(regexp.QuoteMeta(glob), `\*`, ".*") + "$"
	ok, _ := regexp.MatchString(re, path)
	return ok
}
