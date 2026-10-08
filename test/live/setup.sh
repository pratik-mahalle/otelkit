#!/usr/bin/env bash
# Creates a kind cluster with Collectors deployed three ways (Helm chart, Operator CR, raw ConfigMap),
# each with drift planted on purpose, and downloads otelcol-contrib for `build` validation.
set -euo pipefail
cd "$(dirname "$0")"

CLUSTER=otelkit-test
CTX=kind-$CLUSTER
COLLECTOR_VERSION=0.161.0
CHART_VERSION=0.175.1
OPERATOR_CHART_VERSION=0.124.1

kind get clusters 2>/dev/null | grep -qx "$CLUSTER" || kind create cluster --name "$CLUSTER" --wait 120s
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts >/dev/null 2>&1 || true
helm repo update open-telemetry >/dev/null

echo "== Helm chart collectors (team-a, team-b, team-c)"
for team in team-a team-b team-c; do
  helm upgrade --install opentelemetry-collector open-telemetry/opentelemetry-collector \
    --kube-context "$CTX" --version "$CHART_VERSION" -n "$team" --create-namespace \
    -f helm/common.yaml -f "helm/$team.yaml" --wait --timeout 5m
done

echo "== OpenTelemetry Operator + 3 OpenTelemetryCollector CRs"
helm upgrade --install opentelemetry-operator open-telemetry/opentelemetry-operator \
  --kube-context "$CTX" --version "$OPERATOR_CHART_VERSION" -n otel-operator --create-namespace \
  --set manager.collectorImage.repository=otel/opentelemetry-collector-contrib \
  --set admissionWebhooks.certManager.enabled=false \
  --set admissionWebhooks.autoGenerateCert.enabled=true \
  --wait --timeout 5m
kubectl --context "$CTX" apply -f operator/

echo "== Raw ConfigMap with YAML anchors"
kubectl --context "$CTX" apply -f raw/

echo "== otelcol-contrib $COLLECTOR_VERSION for validation"
if [ ! -x bin/otelcol-contrib ]; then
  os=$(uname -s | tr '[:upper:]' '[:lower:]'); arch=$(uname -m); [ "$arch" = x86_64 ] && arch=amd64; [ "$arch" = aarch64 ] && arch=arm64
  asset="otelcol-contrib_${COLLECTOR_VERSION}_${os}_${arch}.tar.gz"
  mkdir -p bin
  curl -fsSL -o "bin/$asset" "https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v${COLLECTOR_VERSION}/${asset}"
  curl -fsSL -o "bin/$asset.sha256" "https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v${COLLECTOR_VERSION}/${asset}.sha256"
  [ "$(cut -d' ' -f1 "bin/$asset.sha256")" = "$(shasum -a 256 "bin/$asset" | cut -d' ' -f1)" ] || { echo "checksum mismatch for $asset" >&2; exit 1; }
  tar -xzf "bin/$asset" -C bin otelcol-contrib
  rm "bin/$asset" "bin/$asset.sha256"
fi
bin/otelcol-contrib --version

echo "== ready: context $CTX"
