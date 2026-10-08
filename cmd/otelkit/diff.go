package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/pratik-mahalle/otelkit/internal/build"
	"github.com/pratik-mahalle/otelkit/internal/diff"
)

type change struct {
	Path     string  `json:"path"`
	Deployed *string `json:"deployed"`
	Rendered *string `json:"rendered"`
}

type diffResult struct {
	Target  string   `json:"target"`
	Changes []change `json:"changes"`
	Skipped bool     `json:"skipped,omitempty"`
	Error   string   `json:"error,omitempty"`
}

func runDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("f", "fleet.yaml", "path to fleet.yaml")
	var targets multi
	fs.Var(&targets, "target", "diff only this target; repeatable")
	format := fs.String("format", "text", "text or json")
	failOnDiff := fs.Bool("fail-on-diff", false, "exit 1 when any target differs")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	f, err := build.Load(*file)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	names := []string(targets)
	if len(names) == 0 {
		names = f.Names()
	}
	var results []diffResult
	failed, differs := false, false
	for _, n := range names {
		r := diffResult{Target: n}
		switch spec, ok := f.Targets[n]; {
		case !ok:
			r.Error = "unknown target"
		case spec.Deployed == nil:
			r.Skipped = true
		default:
			r.Changes, err = diffTarget(f, n)
			if err != nil {
				r.Error = err.Error()
			}
		}
		failed = failed || r.Error != ""
		differs = differs || len(r.Changes) > 0
		results = append(results, r)
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	} else {
		for _, r := range results {
			switch {
			case r.Error != "":
				fmt.Fprintf(stdout, "%s: FAILED: %s\n", r.Target, r.Error)
			case r.Skipped:
				fmt.Fprintf(stdout, "%s: skipped (no deployed source)\n", r.Target)
			case len(r.Changes) == 0:
				fmt.Fprintf(stdout, "%s: no differences\n", r.Target)
			default:
				fmt.Fprintf(stdout, "%s: %d differences\n", r.Target, len(r.Changes))
				for _, c := range r.Changes {
					switch {
					case c.Deployed == nil:
						fmt.Fprintf(stdout, "  + %s: %s\n", c.Path, *c.Rendered)
					case c.Rendered == nil:
						fmt.Fprintf(stdout, "  - %s: %s\n", c.Path, *c.Deployed)
					default:
						fmt.Fprintf(stdout, "  ~ %s: %s -> %s\n", c.Path, *c.Deployed, *c.Rendered)
					}
				}
			}
		}
	}
	if failed || (*failOnDiff && differs) {
		return 1
	}
	return 0
}

// diffTarget compares what is deployed (From) with what the fleet renders (To), masked.
func diffTarget(f *build.Fleet, name string) ([]change, error) {
	m, err := f.Render(name)
	if err != nil {
		return nil, err
	}
	dep, err := loader.FromDeployed(context.Background(), f.Dir, *f.Targets[name].Deployed)
	if err != nil {
		return nil, err
	}
	var out []change
	for _, c := range diff.Compare(diff.Flatten(dep), diff.Flatten(m.Root)) {
		ch := change{Path: c.Path}
		if c.From != nil {
			s := diff.Mask(c.Path, c.From.Text)
			ch.Deployed = &s
		}
		if c.To != nil {
			s := diff.Mask(c.Path, c.To.Text)
			ch.Rendered = &s
		}
		out = append(out, ch)
	}
	return out, nil
}
