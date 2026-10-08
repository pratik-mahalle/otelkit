package validate

import (
	"os"
	"path/filepath"
	"testing"
)

func fake(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("testdata/fake-otelcol")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func write(t *testing.T, content string) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, file
}

func TestRunOK(t *testing.T) {
	dir, file := write(t, "receivers: {}\n")
	if r := Run(fake(t), dir, file); r.Status != OK {
		t.Fatalf("got %+v", r)
	}
}

func TestRunFailedIncludesOutput(t *testing.T) {
	dir, file := write(t, "INVALID: true\n")
	r := Run(fake(t), dir, file)
	if r.Status != Failed || r.Detail != "invalid config: INVALID found" {
		t.Fatalf("got %+v", r)
	}
}

func TestRunSetsPlaceholderForUnsetEnv(t *testing.T) {
	t.Setenv("OTELKIT_TEST_VAR", "")
	os.Unsetenv("OTELKIT_TEST_VAR")
	dir, file := write(t, "x: ${env:OTELKIT_TEST_VAR}\n")
	if r := Run(fake(t), dir, file); r.Status != OK {
		t.Fatalf("unset env var must get a placeholder, got %+v", r)
	}
}

func TestRunMissingFileRefIsNotValidated(t *testing.T) {
	dir, file := write(t, "x: ${file:certs/ca.pem}\n")
	r := Run(fake(t), dir, file)
	if r.Status != NotValidated || r.Detail != "certs/ca.pem does not exist locally" {
		t.Fatalf("got %+v", r)
	}
}

func TestRunMissingBinaryIsSkipped(t *testing.T) {
	dir, file := write(t, "receivers: {}\n")
	r := Run("otelkit-no-such-otelcol", dir, file)
	if r.Status != Skipped {
		t.Fatalf("got %+v", r)
	}
}
