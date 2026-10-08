# Live Kubernetes test

Runs otelkit against a real cluster: Collectors deployed with the Helm chart, the OpenTelemetry
Operator and a hand-written ConfigMap, plus VM-style files, each with drift planted on purpose.
Needs Docker, kind, helm, kubectl and Go. Touches nothing outside the `kind-otelkit-test` cluster.

```bash
./setup.sh      # create cluster, deploy collectors, download otelcol-contrib into bin/
./run.sh        # run analyze / emit / build / diff and check results (outputs in out/)
./teardown.sh   # delete the cluster
```

Latest results: [REPORT.md](REPORT.md).
