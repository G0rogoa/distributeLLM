#!/usr/bin/env bash
set -euo pipefail
# Start this load, stop one worker during the run, then retain both this output and /metrics.
go run ./cmd/loadgen -target=http://127.0.0.1:8080 -duration=60s -arrival=fixed-rate -rate=20 -concurrency=32 -input-min=32 -input-max=128 -output-min=32 -output-max=128 -seed=4 -format=json
