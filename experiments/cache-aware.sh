#!/usr/bin/env bash
set -euo pipefail
mode=${1:-cold}
output=${2:-cache-${mode}.json}
case "$mode" in
  cold) args=(-requests=100 -concurrency=8 -input-min=16 -input-max=128 -seed=11) ;;
  hot) args=(-requests=200 -concurrency=16 -input-min=128 -input-max=128 -seed=22) ;;
  zipf) args=(-requests=300 -concurrency=16 -input-min=16 -input-max=256 -seed=33) ;;
  capacity) args=(-requests=300 -concurrency=16 -input-min=128 -input-max=512 -seed=44) ;;
  staleness) args=(-requests=200 -concurrency=8 -input-min=128 -input-max=128 -seed=55) ;;
  block-size) args=(-requests=200 -concurrency=8 -input-min=16 -input-max=512 -seed=66) ;;
  *) echo "mode must be cold|hot|zipf|capacity|staleness|block-size" >&2; exit 2 ;;
esac
echo "Start mode-specific Controller/Workers first; mode=$mode" >&2
go run ./cmd/loadgen -format=json "${args[@]}" >"$output"
