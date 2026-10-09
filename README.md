# otelkit

Keep OpenTelemetry Collector configs consistent across Kubernetes clusters and VMs.

```bash
go install github.com/pratik-mahalle/otelkit/cmd/otelkit@latest
```

## 1. See how your configs drift

```bash
otelkit fleet analyze --group vms=./vm-configs --group k8s=k8s://prod-eu/observability/configmap/otel-collector ./more/*.yaml
```

Reports the settings every target shares, likely-accidental drift (a majority agrees, a few differ),
and each target's deviations. Endpoints, headers, auth and TLS file paths are expected to differ;
add more with `--vary 'exporters.*.compression'`. Name a target with `--name edge-1=<source>`. Secrets are masked. `--format json`, `--fail-on-drift` for CI.

## 2. Turn them into one source of truth

```bash
otelkit fleet analyze --emit-fleet ./fleet ./vm-configs
```

Writes `base.yaml`, `overrides/<target>.yaml` and `fleet.yaml`, verified to reproduce your configs exactly.
Move shared pieces into `fragments/<name>.yaml` and list them per target.

## 3. Build and check

```bash
otelkit fleet build -f fleet/fleet.yaml          # writes fleet/out/<target>.yaml, runs `otelcol-contrib validate`
otelkit fleet build -f fleet/fleet.yaml --check  # CI: fail if out/ is stale or invalid
otelkit fleet diff  -f fleet/fleet.yaml          # rendered vs deployed
```

Merge rules: maps deep-merge; pipeline receiver/processor/exporter lists append; two fragments
setting the same value differently is an error; in overrides `null` deletes a key and `!replace`
replaces a list; `${var:name}` comes from the target's `vars`; `${env:...}`/`${file:...}` are left for the Collector.

`otelcol:` in `fleet.yaml` may only name a collector command on PATH (its name must contain `otel`); pass a binary path with `--otelcol`
(so a change to `fleet.yaml` can never make CI execute a script from the repo).

Flags go before sources. Design: `docs/superpowers/specs/2026-10-08-otelkit-fleet-design.md`.
