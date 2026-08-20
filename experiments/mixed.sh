#!/usr/bin/env bash
set -euo pipefail
# Run 400 short and 100 long requests with the same deterministic seed.
go run ./cmd/loadgen -target=http://127.0.0.1:8080 -requests=400 -concurrency=16 -input-min=32 -input-max=64 -output-min=32 -output-max=32 -seed=2 -format=json
go run ./cmd/loadgen -target=http://127.0.0.1:8080 -requests=100 -concurrency=16 -input-min=32 -input-max=64 -output-min=512 -output-max=512 -seed=3 -format=json
