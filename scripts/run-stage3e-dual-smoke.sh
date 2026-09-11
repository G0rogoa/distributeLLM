#!/usr/bin/env bash
set -euo pipefail

ROOT=${ROOT:-/data/zhangshenqiang/distserve-stage3e-701e1f9}
BIN_DIR=${BIN_DIR:-/tmp/distserve-stage3e-701e1f9-bin}
CONDA=${CONDA:-/opt/anaconda3/bin/conda}
MODEL_PATH=${MODEL_PATH:-/data/zhangshenqiang/models/Qwen2.5-0.5B-Instruct}
MODEL=${MODEL:-stage3-qwen0.5b}
GPU_INDICES=${GPU_INDICES:-0,1}
VLLM_PORTS=${VLLM_PORTS:-19000,19100}
TOKENIZER_PORT=${TOKENIZER_PORT:-19191}
CONTROLLER_PORT=${CONTROLLER_PORT:-18180}
ARTIFACT_ROOT=${ARTIFACT_ROOT:-/data/zhangshenqiang/distserve-stage3e-701e1f9-artifacts}
EXPERIMENT_ID=${EXPERIMENT_ID:-dual-smoke-$(date -u +%Y%m%dT%H%M%SZ)}
OUT=$ARTIFACT_ROOT/$EXPERIMENT_ID

IFS=',' read -r -a gpus <<< "$GPU_INDICES"
IFS=',' read -r -a ports <<< "$VLLM_PORTS"
if [ "${#gpus[@]}" -ne 2 ] || [ "${#ports[@]}" -ne 2 ]; then
  echo "dual smoke requires exactly two GPU indices and two vLLM ports" >&2
  exit 2
fi
if [ -n "$(git -C "$ROOT" status --porcelain)" ]; then
  echo "dual smoke requires a clean worktree" >&2
  exit 2
fi
for binary in controller workeragent loadgen; do
  test -x "$BIN_DIR/$binary"
done
for gpu in "${gpus[@]}"; do
  test "$(nvidia-smi -i "$gpu" --query-compute-apps=pid --format=csv,noheader | sed '/^$/d' | wc -l)" -eq 0
done
for port in "${ports[@]}" "$TOKENIZER_PORT" "$CONTROLLER_PORT"; do
  if ss -ltn "sport = :$port" | tail -n +2 | grep -q .; then
    echo "port $port is already in use" >&2
    exit 2
  fi
done

mkdir -p "$OUT/logs" "$OUT/pids" "$OUT/workloads"
pids=()
cleanup() {
  set +e
  for pgid in "${pids[@]}"; do
    kill -TERM -- "-$pgid" 2>/dev/null || true
  done
  for _ in $(seq 1 60); do
    alive=0
    for pgid in "${pids[@]}"; do
      kill -0 -- "-$pgid" 2>/dev/null && alive=$((alive + 1))
    done
    [ "$alive" -eq 0 ] && break
    sleep 1
  done
  nvidia-smi > "$OUT/nvidia-after.txt" 2>&1 || true
}
trap cleanup EXIT INT TERM

start_group() {
  name=$1
  shift
  setsid "$@" > "$OUT/logs/$name.log" 2>&1 < /dev/null &
  pgid=$!
  pids+=("$pgid")
  echo "$pgid" > "$OUT/pids/$name.pgid"
}

wait_http() {
  url=$1
  for _ in $(seq 1 180); do
    curl -fsS --max-time 2 "$url" >/dev/null 2>&1 && return 0
    sleep 2
  done
  return 1
}

nvidia-smi > "$OUT/nvidia-before.txt"
cd "$ROOT"
start_group tokenizer "$CONDA" run --no-capture-output -n zsq python tools/tokenizer_service/server.py --host 127.0.0.1 --port "$TOKENIZER_PORT" --tokenizer-path "$MODEL_PATH" --model-id "$MODEL" --model-revision local-qwen2.5-0.5b --tokenizer-id qwen2.5-tokenizer --tokenizer-revision local-qwen2.5-0.5b --chat-template-version qwen2.5-instruct
for index in 0 1; do
  gpu=${gpus[$index]}
  port=${ports[$index]}
  start_group "vllm-gpu$gpu" env CUDA_VISIBLE_DEVICES="$gpu" "$CONDA" run --no-capture-output -n zsq python -m vllm.entrypoints.openai.api_server --host 127.0.0.1 --port "$port" --model "$MODEL_PATH" --served-model-name "$MODEL" --dtype bfloat16
done
wait_http "http://127.0.0.1:$TOKENIZER_PORT/health"
for port in "${ports[@]}"; do wait_http "http://127.0.0.1:$port/v1/models"; done

start_group controller "$BIN_DIR/controller" -listen="127.0.0.1:$CONTROLLER_PORT" -model="$MODEL" -scheduler=ect -request-timeout=120s -tokenizer-mode=remote -tokenizer-url="http://127.0.0.1:$TOKENIZER_PORT" -tokenizer-timeout=10s -model-revision=local-qwen2.5-0.5b -tokenizer-id=qwen2.5-tokenizer -tokenizer-revision=local-qwen2.5-0.5b -chat-template-version=qwen2.5-instruct -cache-block-size=16 -cache-format-version=vllm-0.10.2-prefix-cache -kv-layout=bfloat16-a100 -shadow-affinity-ttl=5m -online-cost-learning=false
for index in 0 1; do
  gpu=${gpus[$index]}
  port=${ports[$index]}
  start_group "agent-gpu$gpu" "$BIN_DIR/workeragent" -controller-url="http://127.0.0.1:$CONTROLLER_PORT" -worker-id="worker-gpu$gpu" -gpu-index="$gpu" -model="$MODEL" -backend-url="http://127.0.0.1:$port" -capacity=32 -heartbeat-interval=1s
done
wait_http "http://127.0.0.1:$CONTROLLER_PORT/health"
for _ in $(seq 1 30); do
  healthy=$(curl -fsS "http://127.0.0.1:$CONTROLLER_PORT/internal/workers" | grep -o '"status":"healthy"' | wc -l)
  [ "$healthy" -eq 2 ] && break
  sleep 1
done
[ "$healthy" -eq 2 ]

start_group sampler python3 tools/sample_workers.py --controller-url="http://127.0.0.1:$CONTROLLER_PORT" --output="$OUT/worker-samples.jsonl" --interval=1
curl -fsS "http://127.0.0.1:$CONTROLLER_PORT/metrics" > "$OUT/controller-metrics-before.txt"
for index in 0 1; do curl -fsS "http://127.0.0.1:${ports[$index]}/metrics" > "$OUT/vllm-gpu${gpus[$index]}-metrics-before.txt"; done

for prefix in 2048 4096 8192; do
  python3 tools/generate_stage3d_workload.py --kind shared --prefix-tokens "$prefix" --requests 30 --output-tokens 32 --seed 20260911 -o "$OUT/workloads/shared-$prefix.jsonl"
  "$BIN_DIR/loadgen" -target="http://127.0.0.1:$CONTROLLER_PORT" -model="$MODEL" -requests=30 -concurrency=1 -stream=true -timeout=120s -workload="$OUT/workloads/shared-$prefix.jsonl" -format=json -output="$OUT/requests-$prefix.jsonl" > "$OUT/summary-$prefix.json"
done

curl -fsS "http://127.0.0.1:$CONTROLLER_PORT/internal/debug/decisions" > "$OUT/decisions.json"
curl -fsS "http://127.0.0.1:$CONTROLLER_PORT/metrics" > "$OUT/controller-metrics-after.txt"
for index in 0 1; do curl -fsS "http://127.0.0.1:${ports[$index]}/metrics" > "$OUT/vllm-gpu${gpus[$index]}-metrics-after.txt"; done

for prefix in 2048 4096 8192; do
  grep -q '"success_rate": 1' "$OUT/summary-$prefix.json"
done
matches=$(awk '$1 == "distserve_shadow_affinity_matches_total" {print int($2)}' "$OUT/controller-metrics-after.txt")
[ "${matches:-0}" -ge 80 ]
[ "$(wc -l < "$OUT/worker-samples.jsonl")" -ge 3 ]
echo "dual Stage 3E smoke passed: artifact=$OUT shadow_matches=$matches"
