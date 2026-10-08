package build

import (
	"fmt"
	"regexp"

	"go.yaml.in/yaml/v3"
)

var varRef = regexp.MustCompile(`\$\{var:([^}]*)\}`)

// Substitute replaces ${var:name} in scalar values. ${env:...} and ${file:...} are left for the Collector.
func Substitute(n *yaml.Node, vars map[string]string, file string) error {
	if n.Kind != yaml.ScalarNode {
		for _, c := range n.Content {
			if err := Substitute(c, vars, file); err != nil {
				return err
			}
		}
		return nil
	}
	if !varRef.MatchString(n.Value) {
		return nil
	}
	var missing string
	n.Value = varRef.ReplaceAllStringFunc(n.Value, func(m string) string {
		name := varRef.FindStringSubmatch(m)[1]
		v, ok := vars[name]
		if !ok && missing == "" {
			missing = name
		}
		return v
	})
	if missing != "" {
		return fmt.Errorf("%s:%d unknown var %q", file, n.Line, missing)
	}
	if n.Style == 0 {
		n.Tag = "" // plain scalars re-resolve, so `port: ${var:port}` stays an int
	}
	return nil
}
