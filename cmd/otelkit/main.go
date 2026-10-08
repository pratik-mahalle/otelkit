// Command otelkit manages OpenTelemetry Collector configs across a fleet.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pratik-mahalle/otelkit/internal/source"
)

const usage = `usage: otelkit fleet <command> [flags] [args]

commands:
  analyze [--group name=<source>]... [--vary <glob>]... [--format text|json] [--fail-on-drift] [--emit-fleet <dir>] <source>...
  build   [-f fleet.yaml] [--target name]... [--check] [--require-validate] [--otelcol <path>]
  diff    [-f fleet.yaml] [--target name]... [--format text|json] [--fail-on-diff]

sources: a file, a directory of .yaml files, a glob, k8s://<context>/<namespace>/configmap/<name>[#key]
or k8s://<context>/<namespace>/otelcol/<name>. Flags must come before sources.`

var loader = source.Loader{Clients: source.DefaultClients}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "fleet" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[1] {
	case "analyze":
		return runAnalyze(args[2:], stdout, stderr)
	case "build":
		return runBuild(args[2:], stdout, stderr)
	case "diff":
		return runDiff(args[2:], stdout, stderr)
	}
	fmt.Fprintln(stderr, usage)
	return 2
}

// multi is a repeatable string flag.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }
