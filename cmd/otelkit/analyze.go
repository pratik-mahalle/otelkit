package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/pratikmahalle/otelkit/internal/analyze"
)

func runAnalyze(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var groups, vary multi
	fs.Var(&groups, "group", "name=<source>: analyze this source as part of group name; repeatable")
	fs.Var(&vary, "vary", "path glob expected to differ between targets; repeatable")
	format := fs.String("format", "text", "text or json")
	failOnDrift := fs.Bool("fail-on-drift", false, "exit 1 when drift is found")
	emit := fs.String("emit-fleet", "", "write a fleet that reproduces the inputs into this empty dir")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var srcs, order []string
	groupOf := map[string]string{}
	add := func(group, src string) {
		if _, seen := groupOf[src]; !seen {
			srcs = append(srcs, src)
		}
		groupOf[src] = group
		if !slices.Contains(order, group) {
			order = append(order, group)
		}
	}
	for _, g := range groups {
		name, src, ok := strings.Cut(g, "=")
		if !ok {
			fmt.Fprintf(stderr, "error: --group %q: want name=<source>\n", g)
			return 2
		}
		add(name, src)
	}
	for _, s := range fs.Args() {
		add("all", s)
	}
	if len(srcs) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}

	targets, errs := loader.Load(context.Background(), srcs)
	for _, err := range errs {
		fmt.Fprintln(stderr, "error:", err)
	}
	byGroup := map[string]*analyze.GroupInput{}
	for _, g := range order {
		byGroup[g] = &analyze.GroupInput{Name: g}
	}
	var emitTargets []analyze.EmitTarget
	for _, t := range targets {
		g := byGroup[groupOf[t.Source]]
		g.Targets = append(g.Targets, analyze.Target{Name: t.Name, Root: t.Root})
		emitTargets = append(emitTargets, analyze.EmitTarget{Name: t.Name, Root: t.Root, Deployed: t.Deployed})
	}
	var inputs []analyze.GroupInput
	for _, g := range order {
		if len(byGroup[g].Targets) > 0 {
			inputs = append(inputs, *byGroup[g])
		}
	}

	rep := analyze.Analyze(inputs, analyze.Options{Vary: vary})
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	} else {
		rep.WriteText(stdout)
	}

	if *emit != "" {
		if len(errs) > 0 {
			fmt.Fprintln(stderr, "error: not emitting a fleet while some sources failed to load")
			return 1
		}
		if err := analyze.Emit(*emit, emitTargets); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		fmt.Fprintf(stdout, "wrote fleet to %s\n", *emit)
	}
	if len(errs) > 0 || (*failOnDrift && rep.HasDrift()) {
		return 1
	}
	return 0
}
