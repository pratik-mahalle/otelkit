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

var (
	secretKey   = regexp.MustCompile(`(?i)token|password|secret|key|authorization|api[-_]?key|headers|inline|connection_string|dsn|datasource|credential`)
	urlUserinfo = regexp.MustCompile(`://[^/\s@:]+:[^/\s@]+@`)
	placeholder = regexp.MustCompile(`^\$\{(env|file):[^}:]*\}$`) // no ":-default", which could hold a secret
)

// Mask hides values whose path looks like a credential. A value that is only a placeholder is
// kept: it holds no secret. ponytail: secrets under key names not in secretKey are not detected;
// extend it when one shows up.
func Mask(path, text string) string {
	if text == Present || placeholder.MatchString(text) {
		return text
	}
	if (secretKey.MatchString(path) && !isAttributeName(path)) || urlUserinfo.MatchString(text) {
		return "****"
	}
	return text
}

// isAttributeName reports a `key` field inside an attributes/actions list, which names an attribute
// rather than holding a secret (e.g. processors.attributes.actions.0.key).
func isAttributeName(path string) bool {
	segs := Split(path)
	if len(segs) < 3 || segs[len(segs)-1] != "key" {
		return false
	}
	list := segs[len(segs)-3]
	return list == "attributes" || list == "actions"
}

// Match reports whether path matches glob, where * matches any characters including dots.
func Match(glob, path string) bool {
	re := "^" + strings.ReplaceAll(regexp.QuoteMeta(glob), `\*`, ".*") + "$"
	ok, _ := regexp.MatchString(re, path)
	return ok
}
