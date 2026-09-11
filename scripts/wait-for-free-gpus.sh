#!/usr/bin/env bash
set -euo pipefail

GPU_INDICES=${GPU_INDICES:-0,1}
POLL_SECONDS=${POLL_SECONDS:-30}
STABLE_SECONDS=${STABLE_SECONDS:-300}
MAX_WAIT_SECONDS=${MAX_WAIT_SECONDS:-86400}
MAX_IDLE_MEMORY_MIB=${MAX_IDLE_MEMORY_MIB:-16}
RUNNER=${RUNNER:-}
LOG=${LOG:-/dev/stdout}

if [ -z "$RUNNER" ] || [ ! -x "$RUNNER" ]; then
  echo "RUNNER must name an executable experiment script" >&2
  exit 2
fi
for value in "$POLL_SECONDS" "$STABLE_SECONDS" "$MAX_WAIT_SECONDS" "$MAX_IDLE_MEMORY_MIB"; do
  if ! [[ "$value" =~ ^[0-9]+$ ]] || [ "$value" -lt 1 ]; then
    echo "wait intervals and memory threshold must be positive integers" >&2
    exit 2
  fi
done

IFS=',' read -r -a gpus <<< "$GPU_INDICES"
if [ "${#gpus[@]}" -lt 1 ]; then
  echo "GPU_INDICES must not be empty" >&2
  exit 2
fi

started=$(date +%s)
stable_since=0
trap 'echo "$(date -Iseconds) watcher stopped" >> "$LOG"; exit 130' INT TERM

while true; do
  now=$(date +%s)
  if [ $((now - started)) -ge "$MAX_WAIT_SECONDS" ]; then
    echo "$(date -Iseconds) timed out waiting for GPUs $GPU_INDICES" >> "$LOG"
    exit 124
  fi

  free=true
  states=()
  for gpu in "${gpus[@]}"; do
    memory=$(nvidia-smi -i "$gpu" --query-gpu=memory.used --format=csv,noheader,nounits | tr -d ' ')
    processes=$(nvidia-smi -i "$gpu" --query-compute-apps=pid --format=csv,noheader | sed '/^$/d' | wc -l)
    states+=("gpu${gpu}:memory=${memory}MiB,processes=${processes}")
    if [ "$processes" -ne 0 ] || [ "$memory" -gt "$MAX_IDLE_MEMORY_MIB" ]; then
      free=false
    fi
  done

  if [ "$free" = true ]; then
    if [ "$stable_since" -eq 0 ]; then
      stable_since=$now
    fi
    stable_for=$((now - stable_since))
  else
    stable_since=0
    stable_for=0
  fi
  echo "$(date -Iseconds) ${states[*]} stable_for=${stable_for}s" >> "$LOG"

  if [ "$free" = true ] && [ "$stable_for" -ge "$STABLE_SECONDS" ]; then
    echo "$(date -Iseconds) GPUs $GPU_INDICES stable and free; starting $RUNNER" >> "$LOG"
    exec "$RUNNER"
  fi
  sleep "$POLL_SECONDS"
done
