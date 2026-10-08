// Package validate runs the user's own otelcol binary to validate rendered configs.
package validate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Status is the outcome of validating one config.
type Status string

const (
	OK           Status = "ok"
	Failed       Status = "failed"
	NotValidated Status = "not validated"
	Skipped      Status = "skipped"
)

// Result is a validation outcome with otelcol's output or the reason it did not run.
type Result struct {
	Status Status
	Detail string
}

var (
	envRef  = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)`)
	fileRef = regexp.MustCompile(`\$\{file:([^}]+)\}`)
)

// Run validates file with `<bin> validate --config <file>`, running in dir.
func Run(bin, dir, file string) Result {
	if strings.Contains(bin, "/") && !filepath.IsAbs(bin) {
		bin = filepath.Join(dir, bin)
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return Result{Skipped, fmt.Sprintf("otelcol binary %q not found", bin)}
	}
	file, err = filepath.Abs(file)
	if err != nil {
		return Result{Failed, err.Error()}
	}
	content, err := os.ReadFile(file)
	if err != nil {
		return Result{Failed, err.Error()}
	}
	for _, m := range fileRef.FindAllStringSubmatch(string(content), -1) {
		p := m[1]
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if _, err := os.Stat(p); err != nil {
			return Result{NotValidated, m[1] + " does not exist locally"}
		}
	}
	cmd := exec.Command(path, "validate", "--config", file)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for _, m := range envRef.FindAllStringSubmatch(string(content), -1) {
		if _, ok := os.LookupEnv(m[1]); !ok {
			cmd.Env = append(cmd.Env, m[1]+"=otelkit-placeholder")
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return Result{Failed, strings.TrimSpace(string(out))}
	}
	return Result{Status: OK}
}
