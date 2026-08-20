#!/usr/bin/env bash
set -euo pipefail
go run ./cmd/loadgen -target=http://127.0.0.1:8080 -requests=500 -concurrency=16 -input-min=64 -input-max=64 -output-min=64 -output-max=64 -seed=1 -format=json
