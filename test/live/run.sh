#!/usr/bin/env bash
# Runs every otelkit command against the cluster from setup.sh and checks the results.
# Writes command output to out/. The only cluster write is a temporary edit to gateway-eu, reverted at the end.
set -uo pipefail
cd "$(dirname "$0")"

CTX=kind-otelkit-test
K="k8s://$CTX"
rm -rf out && mkdir -p out
go build -o out/otelkit ../../cmd/otelkit || exit 1
OK=out/otelkit
pass=0; fail=0
check() { # check <name> <command...>: PASS when the command succeeds
  local name=$1; shift
  if "$@" >/dev/null 2>&1; then echo "PASS  $name"; pass=$((pass+1)); else echo "FAIL  $name"; fail=$((fail+1)); fi
}
has() { grep -qF -- "$2" "$1"; }
lacks() { ! grep -qF -- "$2" "$1"; }

HELM=(--group "helm=$K/team-a/configmap/opentelemetry-collector" --group "helm=$K/team-b/configmap/opentelemetry-collector" --group "helm=$K/team-c/configmap/opentelemetry-collector")
OPER=(--group "operator=$K/obs/otelcol/gateway-eu" --group "operator=$K/obs/otelcol/gateway-ap" --group "operator=$K/obs/otelcol/gateway-us")
RAW=(--group "raw=$K/legacy/configmap/agent-config")
VMS=(--group "vms=vms/vm-a/config.yaml" --group "vms=vms/vm-b/config.yaml")

echo "== analyze (9 targets: 3 Helm, 3 Operator, 1 raw ConfigMap, 2 VM files)"
start=$(date +%s)
$OK fleet analyze "${HELM[@]}" "${OPER[@]}" "${RAW[@]}" "${VMS[@]}" > out/analyze.txt 2> out/analyze.err; rc=$?
echo "analyze took $(( $(date +%s) - start ))s, exit $rc"
check "analyze exits 0"                               test $rc -eq 0
check "analyze loads all 9 targets"                   test "$(grep -E '^group ' out/analyze.txt | grep -oE '[0-9]+ targets' | awk '{s+=$1} END{print s}')" -eq 9
check "Helm: missing memory_limiter on team-c"        has out/analyze.txt "processors.memory_limiter: present in 2/3 targets, missing in kind-otelkit-test-team-c-opentelemetry-collector"
check "Helm: batch timeout 10s on team-c"             has out/analyze.txt "processors.batch.timeout: 2/3 targets use 5s; kind-otelkit-test-team-c-opentelemetry-collector uses 10s"
check "Operator: gateway-us missing HTTP receiver"    has out/analyze.txt "receivers.otlp.protocols.http.endpoint: present in 2/3 targets, missing in kind-otelkit-test-obs-gateway-us"
check "Helm ConfigMap key 'relay' found by default"   lacks out/analyze.err "has no key"
check "anchors expanded (raw ConfigMap)"              has out/analyze.txt "processors.memory_limiter.check_interval = 1s"
check "no merge key leaks into output"                lacks out/analyze.txt "<<"
check "VM files named apart (vms-vm-a-config)"        has out/analyze.txt "vms-vm-a-config"
check "endpoints not reported as drift"               lacks out/analyze.txt "exporters.otlp.endpoint: "
check "secret header masked"                          lacks out/analyze.txt "fake-token-for-masking-test"
check "htpasswd password masked"                      lacks out/analyze.txt "SuperSecretPw"
$OK fleet analyze --format json "${HELM[@]}" "${OPER[@]}" > out/analyze.json 2>/dev/null
check "analyze --format json is valid JSON"           python3 -m json.tool out/analyze.json
check "--fail-on-drift exits 1 when drift exists"     bash -c "! $OK fleet analyze --fail-on-drift ${HELM[*]@Q} >/dev/null 2>&1"

echo "== emit a fleet from the live configs"
$OK fleet analyze --emit-fleet out/fleet "${HELM[@]}" "${OPER[@]}" "${RAW[@]}" "${VMS[@]}" > out/emit.txt 2>&1; rc=$?
check "--emit-fleet succeeds (round trip verified)"   test $rc -eq 0
check "fleet has 9 targets"                           test "$(grep -cE '^ +overrides: ' out/fleet/fleet.yaml)" -eq 9

echo "== build with the real otelcol-contrib"
$OK fleet build -f out/fleet/fleet.yaml --otelcol bin/otelcol-contrib --require-validate > out/build.txt 2>&1; rc=$?
# planted error: the legacy ConfigMap holds its YAML anchor in an extra top-level key, which the Collector rejects
check "build exits 1 (one invalid config)"            test $rc -eq 1
check "real otelcol rejects legacy x-limits key"      has out/build.txt "has invalid keys: x-limits"
check "the other 8 configs pass otelcol validate"     test "$(grep -c 'validate: ok' out/build.txt)" -eq 8
check "warns about the unused basicauth extension"    has out/build.txt 'extension "basicauth/server" is defined but not used'
$OK fleet build -f out/fleet/fleet.yaml --otelcol bin/otelcol-contrib --check > out/build-check.txt 2>&1
check "build --check flags only the invalid config"   test "$(grep -c FAILED out/build-check.txt)" -eq 1

echo "== diff rendered vs live cluster"
$OK fleet diff -f out/fleet/fleet.yaml --fail-on-diff > out/diff-clean.txt 2>&1; rc=$?
check "diff against live cluster is clean"            test $rc -eq 0

echo "== change gateway-eu in the cluster, then diff"
kubectl --context $CTX -n obs patch otelcol gateway-eu --type merge -p '{"spec":{"config":{"processors":{"batch":{"timeout":"7s"}}}}}' >/dev/null
$OK fleet diff -f out/fleet/fleet.yaml --fail-on-diff > out/diff-changed.txt 2>&1; rc=$?
kubectl --context $CTX -n obs patch otelcol gateway-eu --type merge -p '{"spec":{"config":{"processors":{"batch":{"timeout":"5s"}}}}}' >/dev/null
check "diff exits 1 after a live change"              test $rc -eq 1
check "diff names the changed setting"                has out/diff-changed.txt "~ processors.batch.timeout: 7s -> 5s"
check "only gateway-eu differs"                       test "$(grep -cE ': [0-9]+ differences$' out/diff-changed.txt)" -eq 1

echo "== $pass passed, $fail failed (outputs in out/)"
[ $fail -eq 0 ]
