package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const vmA = `receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
processors:
  memory_limiter:
    check_interval: 1s
    limit_percentage: 80
    spike_limit_percentage: 20
  batch:
    timeout: 5s
exporters:
  otlp:
    endpoint: collector-a.example.com:4317
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [otlp]
`

func configs(t *testing.T) string {
	dir := t.TempDir()
	b := strings.Replace(vmA, "collector-a", "collector-b", 1)
	c := strings.NewReplacer(
		"collector-a", "collector-c",
		"  memory_limiter:\n    check_interval: 1s\n    limit_percentage: 80\n    spike_limit_percentage: 20\n", "",
		"[memory_limiter, batch]", "[batch]",
		"timeout: 5s", "timeout: 10s",
	).Replace(vmA)
	for name, content := range map[string]string{"a.yaml": vmA, "b.yaml": b, "c.yaml": c} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func call(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestEndToEnd(t *testing.T) {
	src := configs(t)
	fleetDir := filepath.Join(t.TempDir(), "fleet")

	code, out, errOut := call("fleet", "analyze", "--emit-fleet", fleetDir, src)
	if code != 0 {
		t.Fatalf("analyze exit %d: %s", code, errOut)
	}
	for _, want := range []string{
		"processors.memory_limiter: present in 2/3 targets, missing in c",
		"processors.batch.timeout: 2/3 targets use 5s; c uses 10s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("analyze output missing %q:\n%s", want, out)
		}
	}

	if code, _, _ := call("fleet", "analyze", "--fail-on-drift", src); code != 1 {
		t.Errorf("--fail-on-drift exit = %d, want 1", code)
	}

	fleetFile := filepath.Join(fleetDir, "fleet.yaml")
	if code, out, errOut := call("fleet", "build", "-f", fleetFile); code != 0 {
		t.Fatalf("build exit %d:\n%s%s", code, out, errOut)
	}
	if code, out, _ := call("fleet", "build", "-f", fleetFile, "--check"); code != 0 {
		t.Fatalf("build --check on fresh output exit %d:\n%s", code, out)
	}

	code, out, errOut = call("fleet", "diff", "-f", fleetFile, "--fail-on-diff")
	if code != 0 {
		t.Fatalf("diff after emit must be clean, exit %d:\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "c: no differences") {
		t.Errorf("diff output:\n%s", out)
	}

	// change the deployed file; diff must notice and fail
	os.WriteFile(filepath.Join(src, "c.yaml"), []byte(strings.Replace(vmA, "timeout: 5s", "timeout: 30s", 1)), 0o644)
	code, out, _ = call("fleet", "diff", "-f", fleetFile, "--fail-on-diff", "--target", "c")
	if code != 1 || !strings.Contains(out, "~ processors.batch.timeout: 30s -> 10s") {
		t.Errorf("diff exit %d:\n%s", code, out)
	}
}

func TestBuildOtelcolFlag(t *testing.T) {
	fleetDir := filepath.Join(t.TempDir(), "fleet")
	if code, _, errOut := call("fleet", "analyze", "--emit-fleet", fleetDir, configs(t)); code != 0 {
		t.Fatal(errOut)
	}
	bin, _ := filepath.Abs("../../internal/validate/testdata/fake-otelcol")
	code, out, _ := call("fleet", "build", "-f", filepath.Join(fleetDir, "fleet.yaml"), "--otelcol", bin, "--require-validate")
	if code != 0 || !strings.Contains(out, "a: ok, validate: ok") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestUsage(t *testing.T) {
	if code, _, errOut := call("nope"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestAnalyzeNameFlag(t *testing.T) {
	src := configs(t)
	code, out, errOut := call("fleet", "analyze", "--name", "edge-c="+filepath.Join(src, "c.yaml"), filepath.Join(src, "a.yaml"), filepath.Join(src, "b.yaml"), filepath.Join(src, "c.yaml"))
	if code != 0 || !strings.Contains(out, "missing in edge-c") {
		t.Errorf("exit %d stderr %q:\n%s", code, errOut, out)
	}
}

func TestAnalyzeConfigFile(t *testing.T) {
	src := configs(t)
	dir := t.TempDir()
	cfg := `groups:
  vms:
    - ` + src + `/a.yaml
    - ` + src + `/b.yaml
    - ` + src + `/c.yaml
names:
  edge-c: ` + src + `/c.yaml
vary:
  - processors.batch.*
`
	os.WriteFile(filepath.Join(dir, ".otelkit.yaml"), []byte(cfg), 0o644)
	t.Chdir(dir)
	code, out, errOut := call("fleet", "analyze")
	if code != 0 || !strings.Contains(out, "group vms: 3 targets") || !strings.Contains(out, "missing in edge-c") {
		t.Fatalf("exit %d stderr %q:\n%s", code, errOut, out)
	}
	if strings.Contains(out, "processors.batch.timeout:") {
		t.Errorf("vary from the config file was ignored:\n%s", out)
	}
}

func TestAnalyzeConfigFileRelativePathsAndTypos(t *testing.T) {
	src := configs(t)
	os.WriteFile(filepath.Join(src, "otelkit.yaml"), []byte("groups:\n  vms: [a.yaml, b.yaml, c.yaml]\n"), 0o644)
	if code, out, errOut := call("fleet", "analyze", "-c", filepath.Join(src, "otelkit.yaml")); code != 0 || !strings.Contains(out, "group vms: 3 targets") {
		t.Fatalf("paths must resolve from the config file's folder; exit %d %q\n%s", code, errOut, out)
	}
	os.WriteFile(filepath.Join(src, "bad.yaml"), []byte("group:\n  vms: [a.yaml]\n"), 0o644)
	if code, _, errOut := call("fleet", "analyze", "-c", filepath.Join(src, "bad.yaml")); code != 2 || !strings.Contains(errOut, "group") {
		t.Errorf("a typo in the config file must fail loudly; exit %d %q", code, errOut)
	}
}

func TestVersion(t *testing.T) {
	version = "1.2.3"
	if code, out, _ := call("version"); code != 0 || out != "otelkit 1.2.3\n" {
		t.Errorf("exit %d, out %q", code, out)
	}
}
