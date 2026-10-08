# otelkit fleet — live Kubernetes test report

Date: 2026-10-08 · otelkit `main` · result: **25 / 25 checks passed** after one product fix found by this run.

## What was tested

A dedicated kind cluster (`kind-otelkit-test`, Kubernetes v1.34.0) with Collectors deployed the three ways teams actually do it, each with drift planted on purpose:

| Source | Targets | Deployed with | Planted problem |
|---|---|---|---|
| Helm chart `opentelemetry-collector` 0.175.1 | team-a, team-b, team-c | `otel/opentelemetry-collector-contrib:0.161.0`, ConfigMap key `relay` | team-c: no `memory_limiter`, batch timeout 10s |
| OpenTelemetry Operator 0.160.0 (`OpenTelemetryCollector` v1beta1) | gateway-eu, gateway-ap, gateway-us | structured `spec.config` | gateway-us: OTLP HTTP receiver missing |
| Hand-written ConfigMap | legacy/agent-config | key `config.yaml`, YAML anchors + `<<` merge key | anchor held in an extra top-level key `x-limits` |
| VM files | vms/vm-a, vms/vm-b (both named `config.yaml`) | local files | vm-b: scrape interval 10s, unused `basicauth` extension holding a password |

All 6 in-cluster Collectors were running. Validation used the real `otelcol-contrib` 0.161.0 binary (checksum-verified download).

## Results

### `analyze` — 9 targets from 4 groups, ~2 s

| Group | Shared | Drift found |
|---|---|---|
| helm (3) | 80% | `processors.memory_limiter` present in 2/3, missing in team-c · `processors.batch.timeout` 2/3 use 5s, team-c uses 10s · traces/metrics/logs processor lists differ in team-c |
| operator (3) | 94% | `receivers.otlp.protocols.http.endpoint` present in 2/3, missing in gateway-us |
| raw (1) | 100% | — |
| vms (2) | 88% | none (two targets can't form a majority; the differences are listed as deviations) |

Also verified: the Helm chart's `relay` key was picked automatically; the Operator CR's structured `spec.config` loaded; anchors and `<<` were expanded with no `<<` in output; the two `config.yaml` files became `vms-vm-a-config` / `vms-vm-b-config`; endpoints were not reported as drift; the `Bearer` header and the htpasswd password never appeared (shown as `****`); `--format json` is valid; `--fail-on-drift` exits 1.

### `--emit-fleet` — round trip verified for all 9 live configs

### `build` with real `otelcol-contrib` 0.161.0

- 8 of 9 rendered configs passed `otelcol validate`, including the Helm configs with the chart's `${env:MY_POD_IP}` placeholders.
- **legacy/agent-config was rejected by the real Collector**: `'otelcol.configSettings' has invalid keys: x-limits`. The ConfigMap is accepted by Kubernetes but would crash a Collector on start — otelkit caught it before rollout.
- Warning raised: `extension "basicauth/server" is defined but not used` (vm-b).
- `--check` flagged only the invalid target.

### `diff` against the live cluster

- Right after emit: all 9 targets `no differences`.
- After patching gateway-eu's batch timeout to 7s in the cluster: exit 1, exactly one target differs, `~ processors.batch.timeout: 7s -> 5s`. The patch was reverted afterwards.

## Bug found and fixed during this run

**Missing settings hidden by the "expected to vary" rule.** gateway-us had no HTTP receiver, but `analyze` reported 0 drift: the built-in glob `*.endpoint` skipped the path entirely, so a missing protocol looked like a normal endpoint difference. Fixed so an expected-to-vary path still reports when it is **absent** in a minority (values may still differ freely). Tests: `TestVaryPathMissingInSomeTargetsIsDrift` (failed first), `TestVaryPathWithDifferentValuesIsNotDrift`.

## Usability findings (not fixed)

1. **One root cause, several drift lines.** Removing `memory_limiter` on team-c produced 5 lines (component + 3 pipeline lists + timeout). Accurate, but a reader has to connect them.
2. **Long target names.** `kind-otelkit-test-team-c-opentelemetry-collector` dominates every line; a way to name sources (e.g. `name=k8s://...`) would help.
3. **Groups of two never show drift** — with no majority possible, differences only appear under deviations. Worth saying in the report header.
4. "1 targets" grammar in the summary line.

## Not covered

EKS/GKE/AKS auth plugins and large clusters (only kind); DaemonSet-mode Helm charts (ConfigMap name `-agent`); Operator v1alpha1 CRs with string `spec.config`; Grafana Alloy (out of scope for v1).

## Reproduce

```bash
cd test/live
./setup.sh      # kind cluster + Helm/Operator/raw collectors + otelcol-contrib download (~5 min first run)
./run.sh        # 25 checks; outputs in out/
./teardown.sh   # deletes the cluster
```
