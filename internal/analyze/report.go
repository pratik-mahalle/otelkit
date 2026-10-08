package analyze

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/pratik-mahalle/otelkit/internal/diff"
)

// Line renders a drift finding in one line.
func (d Drift) Line() string {
	outs := slices.Sorted(maps.Keys(d.Outliers))
	if d.Majority == diff.Present {
		return fmt.Sprintf("%s: present in %d/%d targets, missing in %s", d.Path, d.Agree, d.Total, strings.Join(outs, ", "))
	}
	parts := make([]string, len(outs))
	for i, n := range outs {
		parts[i] = n + " uses " + d.Outliers[n]
	}
	return fmt.Sprintf("%s: %d/%d targets use %s; %s", d.Path, d.Agree, d.Total, d.Majority, strings.Join(parts, ", "))
}

// WriteText prints the human report: summary, drift, base and per-target deviations.
func (r Report) WriteText(w io.Writer) {
	for _, g := range r.Groups {
		fmt.Fprintf(w, "group %s: %d targets, %.0f%% of settings shared\n", g.Name, len(g.Targets), g.Shared*100)
		fmt.Fprintf(w, "  targets: %s\n\n", strings.Join(g.Targets, ", "))
		fmt.Fprintf(w, "  likely drift (%d):\n", len(g.Drift))
		for _, d := range g.Drift {
			fmt.Fprintf(w, "    %s\n", d.Line())
		}
		fmt.Fprintf(w, "\n  base (%d settings):\n", len(g.Base))
		writeMap(w, "    ", g.Base)
		fmt.Fprintln(w, "\n  deviations from base:")
		for _, n := range g.Targets {
			if dev := g.Deviations[n]; len(dev) > 0 {
				fmt.Fprintf(w, "    %s:\n", n)
				writeMap(w, "      ", dev)
			}
		}
		fmt.Fprintln(w)
	}
	if len(r.CrossBase) > 0 {
		fmt.Fprintf(w, "cross-group base (%d settings):\n", len(r.CrossBase))
		writeMap(w, "  ", r.CrossBase)
	}
}

func writeMap(w io.Writer, indent string, m map[string]string) {
	for _, p := range slices.Sorted(maps.Keys(m)) {
		fmt.Fprintf(w, "%s%s = %s\n", indent, p, m[p])
	}
}
