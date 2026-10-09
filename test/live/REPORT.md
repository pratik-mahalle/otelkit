# otelkit fleet — live Kubernetes test report

Date: 2026-10-09 · otelkit `main` · result: **30 / 30 checks passed**, after one product bug and four usability issues found by the first run were fixed.

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
| helm (3) | 80% | `processors.memory_limiter` present in 2/3, missing in team-c (also missing from pipelines logs, metrics, traces) · `processors.batch.timeout` 2/3 use 5s, team-c uses 10s |
| operator (3) | 94% | `receivers.otlp.protocols.http.endpoint` present in 2/3, missing in gateway-us |
| raw (1) | 100% | — |
| vms (2) | 88% | none — the report says so: "drift needs a majority (3+ targets); see deviations below" |

Target names are short (`team-c-opentelemetry-collector`, `obs-gateway-us`); `--name team-c=k8s://…` gives an alias. Also verified: the Helm chart's `relay` key was picked automatically; the Operator CR's structured `spec.config` loaded; anchors and `<<` were expanded with no `<<` in output; the two `config.yaml` files became `vms-vm-a-config` / `vms-vm-b-config`; endpoints were not reported as drift; the `Bearer` header and the htpasswd password never appeared (shown as `****`); `--format json` is valid; `--fail-on-drift` exits 1.

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

## Usability issues found by the first run — fixed

1. **One root cause, several drift lines.** Removing `memory_limiter` on team-c produced 5 lines. Pipeline-list drift caused only by a missing component now folds into that component's line ("also missing from pipelines logs, metrics, traces"); a pipeline list that differs in any other way still gets its own line. JSON: `also_in_pipelines`.
2. **Long target names.** `kind-otelkit-test-team-c-opentelemetry-collector` → `team-c-opentelemetry-collector`; the context is added only when two contexts hold the same namespace/object. `--name <alias>=<source>` sets any name.
3. **Groups of two never show drift** — the group header now says so and points at deviations.
4. "1 targets" → "1 target".

## Real-world configs: the Helm chart's own examples

The 21 Collector configs rendered in `opentelemetry-helm-charts` (`charts/opentelemetry-collector/examples/*/rendered`), analyzed as two groups (7 DaemonSet agents, 14 Deployments/StatefulSets):

- All 21 loaded, analyzed in under a second, round-tripped through `--emit-fleet` exactly, and `diff` was clean.
- **Real upstream bug found:** the `kubernetesAttributes` example's traces pipeline uses a `resource` processor that is never defined. `otelcol-contrib` 0.161.0 rejects it (`references processor "resource" which is not configured`); otelkit's own check flagged it too. Still present on upstream `main` (last changed 2026-10-05).
- One false validation failure: `hostmetrics root_path is supported on linux only`, because validation ran on macOS. Run `build` in Linux CI.
- The first pass printed 46 drift lines, several showing `****` for a value that was simply missing. After the fixes below: **28 lines, no misleading masks.** These examples differ on purpose, so most remaining lines are intended features; on a real fleet, mark known variants with `--vary`.

Noise fixes from this run: missing values are never masked; "6/7 use (absent)" now reads "only in X"; an added component folds its pipeline-list changes into one line ("also added to pipelines …"); a target that is the lone outlier on 5+ lines is summarized as "X: differs from the group in N settings" (JSON keeps every line).

## Not covered

EKS/GKE/AKS auth plugins and large clusters (only kind); DaemonSet-mode Helm charts (ConfigMap name `-agent`); Operator v1alpha1 CRs with string `spec.config`; Grafana Alloy (out of scope for v1).

## Reproduce

```bash
cd test/live
./setup.sh      # kind cluster + Helm/Operator/raw collectors + otelcol-contrib download (~5 min first run)
./run.sh        # 30 checks; outputs in out/
./teardown.sh   # deletes the cluster
```
