#!/usr/bin/env bash
# Deletes the test cluster. Leaves bin/ (the downloaded collector) for the next run.
set -euo pipefail
kind delete cluster --name otelkit-test
