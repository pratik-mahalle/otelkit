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
	if d.Majority == Absent {
		parts := make([]string, len(outs))
		for i, n := range outs {
			parts[i] = n
			if v := d.Outliers[n]; v != diff.Present {
				parts[i] += " = " + v
			}
		}
		line := fmt.Sprintf("%s: only in %s (%d/%d targets don't have it)", d.Path, strings.Join(parts, ", "), d.Agree, d.Total)
		if len(d.AlsoInPipelines) > 0 {
			line += " (also added to pipelines " + strings.Join(d.AlsoInPipelines, ", ") + ")"
		}
		return line
	}
	if d.Majority == diff.Present {
		line := fmt.Sprintf("%s: present in %d/%d targets, missing in %s", d.Path, d.Agree, d.Total, strings.Join(outs, ", "))
		if len(d.AlsoInPipelines) > 0 {
			line += " (also missing from pipelines " + strings.Join(d.AlsoInPipelines, ", ") + ")"
		}
		return line
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
		noun := "targets"
		if len(g.Targets) == 1 {
			noun = "target"
		}
		fmt.Fprintf(w, "group %s: %d %s, %.0f%% of settings shared\n", g.Name, len(g.Targets), noun, g.Shared*100)
		fmt.Fprintf(w, "  targets: %s\n", strings.Join(g.Targets, ", "))
		if len(g.Targets) == 2 {
			fmt.Fprintln(w, "  note: drift needs a majority (3+ targets); see deviations below")
		}
		fmt.Fprintln(w)
		lines := driftLines(g)
		fmt.Fprintf(w, "  likely drift (%d):\n", len(lines))
		for _, l := range lines {
			fmt.Fprintf(w, "    %s\n", l)
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

// loneOutlierSummary is how many lines a target may be the only outlier on before the
// text report summarizes them as one; JSON output keeps every line.
const loneOutlierSummary = 5

// driftLines renders drift, collapsing a target that is the lone outlier on many lines into one line.
func driftLines(g Group) []string {
	lone := map[string]int{}
	for _, d := range g.Drift {
		if len(d.Outliers) == 1 {
			for n := range d.Outliers {
				lone[n]++
			}
		}
	}
	var out []string
	for _, d := range g.Drift {
		if len(d.Outliers) == 1 {
			if n := slices.Collect(maps.Keys(d.Outliers))[0]; lone[n] >= loneOutlierSummary {
				continue
			}
		}
		out = append(out, d.Line())
	}
	for _, n := range g.Targets {
		if lone[n] >= loneOutlierSummary {
			out = append(out, fmt.Sprintf("%s: differs from the group in %d settings (see deviations below)", n, lone[n]))
		}
	}
	return out
}

func writeMap(w io.Writer, indent string, m map[string]string) {
	for _, p := range slices.Sorted(maps.Keys(m)) {
		fmt.Fprintf(w, "%s%s = %s\n", indent, p, m[p])
	}
}
