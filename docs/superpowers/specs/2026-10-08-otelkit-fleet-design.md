# otelkit fleet — design

Date: 2026-10-08
Status: approved design, pending spec review

## Problem

Teams running OpenTelemetry Collectors across Kubernetes clusters and VMs end up
with many near-identical configs that drift apart: copy-paste edits, no single
source of truth, and accidental differences (a missing `memory_limiter`, a stale
exporter endpoint, a different `batch` timeout) that nobody notices.

## Goal

A Go CLI, `otelkit fleet`, for engineers who operate Collector fleets in real
environments. Correctness and zero false surprises matter more than polish.

1. `analyze` — read the configs that exist today and report the common base,
   per-target deviations, and likely-accidental drift.
2. `build` — keep configs in sync going forward from one source of truth
   (base + fragments + per-target overrides), rendering plain Collector YAML.
3. `diff` — show how rendered configs differ from what is deployed.

Output is always plain Collector YAML. Existing deployment tooling (Helm,
Operator, Ansible, systemd) is unchanged, and the tool can be dropped at any time.

## Non-goals (v1)

- Grafana Alloy config syntax (standard Collector YAML only; vendor
  distributions that use Collector YAML are supported).
- Pushing configs to targets, OpAMP, a UI, SSH access to VMs.
- Fragment-level processor positioning (`before:`/`after:`).
- Automatic fragment extraction in `analyze`.

## Architecture

Single Go module, single binary `otelkit`, subcommand `fleet` with `analyze`,
`build`, `diff`. CLI uses the standard library `flag` package.

Dependencies: a YAML library (with `yaml.Node` for line numbers) and
`k8s.io/client-go`. The tool does **not** import `otelcol` or contrib;
component validation is delegated to the user's own Collector binary.

| Package | Responsibility |
|---|---|
| `internal/model` | Parse raw YAML into a normalized Collector config: components keyed by kind (`receivers`, `processors`, `exporters`, `connectors`, `extensions`) and ID (`type[/name]`); `service.pipelines` as ordered lists of IDs; `${...}` placeholders kept verbatim; source file + line tracked per value. |
| `internal/source` | Load configs into named targets. `file` (path or glob; target name = file base name) and `k8s` (`k8s://<context>/<namespace>/configmap/<name>[#key]` or `k8s://<context>/<namespace>/otelcol/<name>` reading CR `spec.config`, `opentelemetry.io/v1beta1`). The URL is parsed from the right so contexts containing `/` (EKS ARNs) work. Default ConfigMap key: `config.yaml`, else `relay` (the Helm chart's key), else the only key. Colliding file base names (many `config.yaml`) fall back to the path with `/` replaced by `-`. |
| `internal/diff` | Flatten a model into `path → value` and compute semantic differences (added / removed / changed). Shared by `analyze`, `build --check` and `diff`. |
| `internal/analyze` | Base computation, per-target deviations, majority-based drift rules, `--emit-fleet`. |
| `internal/build` | Read `fleet.yaml`, merge layers, substitute vars, run post-merge checks, write output. |
| `internal/validate` | Run `<otelcol> validate --config <file>` per rendered target. |
| `cmd/otelkit` | Flag parsing and wiring. |

## Flattening and comparison

- Every leaf becomes a path, e.g. `processors.batch.timeout = 5s`.
- `service.pipelines.<name>.receivers` and `.exporters` compare as sets.
- `service.pipelines.<name>.processors` compares as an ordered list.
- Other lists compare as ordered lists.
- Values whose key matches `(?i)token|password|secret|key|authorization|api[-_]?key|headers`
  are masked in all human and JSON output. `${env:...}`/`${file:...}`
  placeholders are printed verbatim (they contain no secret).

## `fleet.yaml` format

```
fleet/
  fleet.yaml
  base.yaml            # normal Collector YAML
  fragments/*.yaml     # partial Collector YAML
  overrides/*.yaml     # optional per-target partial Collector YAML
  out/                 # generated
```

```yaml
otelcol: otelcol-contrib          # binary used for validate
base: base.yaml
targets:
  prod-eu:
    fragments: [k8s-logs, k8s-attributes]   # resolved to fragments/<name>.yaml
    vars: { cluster: prod-eu, backend: otlp.eu.example.com:4317 }
    overrides: overrides/prod-eu.yaml
    deployed: { k8s: { context: prod-eu, namespace: observability, configmap: otel-collector, key: config.yaml } }
  vm-payments-01:
    fragments: [host-metrics]
    vars: { cluster: none, backend: otlp.eu.example.com:4317 }
    deployed: { file: ./snapshots/vm-payments-01.yaml }
```

- `deployed` is optional; `k8s` accepts either `configmap` (+ optional `key`)
  or `otelcol` (CR name). Used only by `diff`.
- Output path defaults to `out/<target>.yaml`.
- Paths in `fleet.yaml` are relative to `fleet.yaml`.

## Merge rules

Layer order: base → fragments (listed order) → overrides.

1. Maps deep-merge.
2. If two **fragments** set the same leaf to different values, `build` fails
   for that target and names both files and lines. Base → fragment and
   any → override may overwrite.
3. Pipeline `receivers`/`processors`/`exporters` lists append in layer order,
   de-duplicated, first occurrence keeps its position.
4. An override list tagged `!replace` replaces the merged list.
5. In an **override**, a value of `null` deletes that key (JSON merge-patch
   semantics). In base and fragments `null` keeps its Collector meaning (a
   component with default settings, e.g. `batch:`) and never deletes.
6. `${var:name}` is substituted from the target's `vars`; an unknown var is an
   error. `${env:...}` and `${file:...}` pass through unchanged.

## Post-merge checks (warnings)

- `memory_limiter` is not the first processor of a pipeline, or `batch` is not
  the last.
- A component is defined but used by no pipeline.
- A pipeline references an undefined component (reported with origin file).

## `analyze`

`otelkit fleet analyze [--group name=<source>...] [--vary <path-glob>...] [--format text|json] [--fail-on-drift] [--emit-fleet <dir>] <source>...`

1. Load and flatten every target.
2. Base = paths with an identical value in every target of the group.
3. Deviations = each target's diff against the base.
4. For every non-base path:
   - **Expected to vary** (not flagged): built-in globs for `*.endpoint`,
     `*.headers.*`, `*.auth.*`, `*.tls.*_file`, `processors.resource*.attributes.*.value`,
     plus any value that is a `${env:...}`/`${file:...}` placeholder, plus user
     `--vary` globs.
   - **Likely drift** (flagged): a strict majority (> 50%) of the group's
     targets share one value (or presence) and the rest differ. Reported as
     `<path>: N targets use <value>; <targets> use <other>`, ranked by majority
     size. Missing components and processor-order differences are reported the
     same way.
5. Groups: each `--group` computes its own base and drift; a cross-group base
   (paths identical across all targets) is also reported. With no `--group`,
   all sources form one group.

Output: text report (summary with target count and % of paths shared, base,
drift, per-target deviations) or JSON. Exit 0, or 1 with `--fail-on-drift`
when any drift is found.

`--emit-fleet <dir>` writes `base.yaml` (the settings every target shares),
`overrides/<target>.yaml` (what each target adds beyond the base), and
`fleet.yaml` (no fragments, no vars, `deployed` set from where each config was
loaded). It refuses a non-empty directory. Rendering that
fleet must reproduce every input config with an empty semantic diff; analyze
verifies this before writing and fails otherwise.

## `build`

`otelkit fleet build [-f fleet.yaml] [--target name...] [--check] [--require-validate]`

- Writes `out/<target>.yaml` with header
  `# generated by otelkit from fleet.yaml — do not edit`, deterministic key
  order (base order first, then keys in the order layers introduce them).
- Writes via temp file + rename; a failed target leaves its previous output
  untouched.
- Validation per target: `<otelcol> validate --config <file>`.
  - Each referenced `${env:X}` that is unset in the environment is set to
    `otelkit-placeholder` for the validation process only.
  - If a referenced `${file:...}` does not exist locally, the target is
    reported as `not validated` (not a failure).
  - Missing `otelcol` binary: warning, continue; failure with
    `--require-validate`.
- `--check`: render in memory, write nothing; exit 1 if committed `out/`
  differs from the render or any validation fails.

## `diff`

`otelkit fleet diff [-f fleet.yaml] [--target name...] [--format text|json] [--fail-on-diff]`

Semantic diff of each rendered target against its `deployed` source, secrets
masked. Targets without `deployed` are skipped with a note. Exit 1 with
`--fail-on-diff` when any target differs.

## Error handling

- Errors carry source file and YAML line:
  `fragments/k8s-logs.yaml:14 processors.batch.timeout conflicts with fragments/tuning.yaml:3`.
- Targets are processed independently; one failing (bad merge, unreachable
  k8s context, missing ConfigMap) does not stop others. A summary is printed
  at the end and the exit code is non-zero if any target failed.
- Invalid YAML in a source is an error for that target only.

## Testing

- Unit tests per merge rule (deep merge, fragment conflict, list append/dedupe,
  `!replace`, `null` delete, var substitution incl. unknown var, `${env}` pass-through).
- Unit tests for flatten/diff (set vs ordered list semantics, masking).
- Golden tests: `testdata/<case>/` with input fleet and expected `out/`;
  `go test ./... -update` regenerates.
- Drift tests: fixture config sets with planted drift; assert each planted case
  is flagged and expected-to-vary differences are not.
- Round-trip test: `analyze --emit-fleet` then `build` reproduces inputs with an
  empty diff, over realistic configs (contrib examples; the user's sanitized
  configs when available).
- External dependencies faked: a fake `otelcol` script in `testdata`;
  `client-go` fake clientset for the k8s source.
