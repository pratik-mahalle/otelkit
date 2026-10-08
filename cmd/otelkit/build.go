package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/pratik-mahalle/otelkit/internal/build"
)

func runBuild(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("f", "fleet.yaml", "path to fleet.yaml")
	var targets multi
	fs.Var(&targets, "target", "build only this target; repeatable")
	check := fs.Bool("check", false, "write nothing; fail if out/ is stale or validation fails")
	requireValidate := fs.Bool("require-validate", false, "fail when the otelcol binary is missing")
	otelcol := fs.String("otelcol", "", "otelcol binary path; overrides fleet.yaml, which may only name a command on PATH")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	f, err := build.Load(*file)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if *otelcol != "" {
		if f.Otelcol, err = filepath.Abs(*otelcol); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	}
	results := build.Run(f, build.Options{Targets: targets, Check: *check, RequireValidate: *requireValidate})
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Fprintf(stdout, "%s: FAILED: %v\n", r.Name, r.Err)
		} else {
			fmt.Fprintf(stdout, "%s: ok, validate: %s", r.Name, r.Validation.Status)
			if r.Validation.Detail != "" {
				fmt.Fprintf(stdout, " (%s)", r.Validation.Detail)
			}
			fmt.Fprintln(stdout)
		}
		for _, w := range r.Warnings {
			fmt.Fprintf(stdout, "  warning: %s\n", w)
		}
	}
	fmt.Fprintf(stdout, "%d targets, %d failed\n", len(results), failed)
	if failed > 0 {
		return 1
	}
	return 0
}
