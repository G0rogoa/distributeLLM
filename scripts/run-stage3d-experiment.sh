#!/usr/bin/env bash
set -euo pipefail

ROOT=${ROOT:-$(pwd)}
EXPERIMENT_ID=${EXPERIMENT_ID:-stage3d-local-$(date -u +%Y%m%dT%H%M%SZ)}
START_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
ARTIFACT_DIR=${ARTIFACT_DIR:-"$ROOT/artifacts/$EXPERIMENT_ID"}
CONTROLLER_BIN=${CONTROLLER_BIN:-"$ROOT/controller"}
WORKERAGENT_BIN=${WORKERAGENT_BIN:-"$ROOT/workeragent"}
LOADGEN_BIN=${LOADGEN_BIN:-"$ROOT/loadgen"}
MODEL=${MODEL:-mock-llm}
SCHEDULER=${SCHEDULER:-ect}
CONTROLLER_ADDR=${CONTROLLER_ADDR:-127.0.0.1:18080}
WORKLOAD=${WORKLOAD:-}
REQUESTS=${REQUESTS:-100}
CONCURRENCY=${CONCURRENCY:-4}
STREAM=${STREAM:-true}
TOKENIZER_MODE=${TOKENIZER_MODE:-disabled}
TOKENIZER_URL=${TOKENIZER_URL:-}
TOKENIZER_TIMEOUT=${TOKENIZER_TIMEOUT:-2s}
MODEL_REVISION=${MODEL_REVISION:-}
MODEL_IDENTITY_HASH=${MODEL_IDENTITY_HASH:-}
TOKENIZER_ID=${TOKENIZER_ID:-}
TOKENIZER_REVISION=${TOKENIZER_REVISION:-}
CHAT_TEMPLATE_VERSION=${CHAT_TEMPLATE_VERSION:-}
CACHE_FORMAT_VERSION=${CACHE_FORMAT_VERSION:-}
KV_LAYOUT=${KV_LAYOUT:-}
COST_PROFILE=${COST_PROFILE:-}
WORKER_IDS=${WORKER_IDS:-}
WORKER_URLS=${WORKER_URLS:-}
GPU_INDICES=${GPU_INDICES:-}
WORKER_CAPACITY=${WORKER_CAPACITY:-32}
PREFIX_TOKENS=${PREFIX_TOKENS:-}
OUTPUT_TOKENS=${OUTPUT_TOKENS:-}
WARMUP_REQUESTS=${WARMUP_REQUESTS:-0}
SEED=${SEED:-}
REPETITION=${REPETITION:-}
VLLM_VERSION=${VLLM_VERSION:-}
TORCH_VERSION=${TORCH_VERSION:-}
CUDA_BUILD_VERSION=${CUDA_BUILD_VERSION:-}
DRIVER_VERSION=${DRIVER_VERSION:-}
SHADOW_CONFIDENCE=${SHADOW_CONFIDENCE:-0.5}
ONLINE_COST_LEARNING=${ONLINE_COST_LEARNING:-false}
CONGESTION_LEVEL=${CONGESTION_LEVEL:-0}

if [ -z "$WORKER_IDS" ] || [ -z "$WORKER_URLS" ]; then
  echo "WORKER_IDS and WORKER_URLS are required; vLLM must already be started manually" >&2
  exit 2
fi
required=(MODEL_IDENTITY_HASH MODEL_REVISION TOKENIZER_ID TOKENIZER_REVISION CHAT_TEMPLATE_VERSION CACHE_FORMAT_VERSION KV_LAYOUT COST_PROFILE WORKLOAD GPU_INDICES PREFIX_TOKENS OUTPUT_TOKENS SEED REPETITION VLLM_VERSION TORCH_VERSION CUDA_BUILD_VERSION DRIVER_VERSION)
for name in "${required[@]}"; do
  if [ -z "${!name}" ]; then
    echo "$name is required for a validated experiment manifest" >&2
    exit 2
  fi
done

mkdir -p "$ARTIFACT_DIR/logs" "$ARTIFACT_DIR/vllm-metrics-before" "$ARTIFACT_DIR/vllm-metrics-after"
: > "$ARTIFACT_DIR/worker-samples.jsonl"

controller_pid=""
sampler_pid=""
agent_pids=()

cleanup() {
  if [ -n "$sampler_pid" ] && ps -p "$sampler_pid" >/dev/null 2>&1; then
    kill "$sampler_pid" || true
    wait "$sampler_pid" || true
  fi
  for pid in "${agent_pids[@]:-}"; do
    if ps -p "$pid" -o command= | grep -q "$WORKERAGENT_BIN"; then
      kill "$pid" || true
    fi
  done
  if [ -n "$controller_pid" ] && ps -p "$controller_pid" -o command= | grep -q "$CONTROLLER_BIN"; then
    kill "$controller_pid" || true
  fi
  nvidia-smi > "$ARTIFACT_DIR/nvidia-after.txt" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

nvidia-smi > "$ARTIFACT_DIR/nvidia-before.txt" 2>/dev/null || true

git_commit=$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)
dirty=false
if [ -n "$(git -C "$ROOT" status --porcelain --untracked-files=normal 2>/dev/null)" ]; then
  dirty=true
fi
if [ "$dirty" = true ]; then
  echo "formal experiments require a clean committed worktree" >&2
  exit 2
fi

controller_flags=(-listen="$CONTROLLER_ADDR" -model="$MODEL" -scheduler="$SCHEDULER" -tokenizer-mode="$TOKENIZER_MODE")
if [ "$TOKENIZER_MODE" = "remote" ]; then
  if [ -z "$TOKENIZER_URL" ]; then
    echo "TOKENIZER_URL is required when TOKENIZER_MODE=remote" >&2
    exit 2
  fi
  controller_flags+=(-tokenizer-url="$TOKENIZER_URL" -tokenizer-timeout="$TOKENIZER_TIMEOUT")
fi
if [ -n "$MODEL_REVISION" ]; then controller_flags+=(-model-revision="$MODEL_REVISION"); fi
if [ -n "$TOKENIZER_ID" ]; then controller_flags+=(-tokenizer-id="$TOKENIZER_ID"); fi
if [ -n "$TOKENIZER_REVISION" ]; then controller_flags+=(-tokenizer-revision="$TOKENIZER_REVISION"); fi
if [ -n "$CHAT_TEMPLATE_VERSION" ]; then controller_flags+=(-chat-template-version="$CHAT_TEMPLATE_VERSION"); fi
if [ -n "$CACHE_FORMAT_VERSION" ]; then controller_flags+=(-cache-format-version="$CACHE_FORMAT_VERSION"); fi
if [ -n "$KV_LAYOUT" ]; then controller_flags+=(-kv-layout="$KV_LAYOUT"); fi
if [ -n "$COST_PROFILE" ]; then
  controller_flags+=(-cost-profile="$COST_PROFILE")
fi
controller_flags+=(-online-cost-learning="$ONLINE_COST_LEARNING")
IFS=',' read -r -a ids <<< "$WORKER_IDS"
IFS=',' read -r -a urls <<< "$WORKER_URLS"
IFS=',' read -r -a gpus <<< "$GPU_INDICES"
if [ "${#ids[@]}" -ne "${#urls[@]}" ]; then
  echo "WORKER_IDS and WORKER_URLS must have the same length" >&2
  exit 2
fi
if [ "${#ids[@]}" -ne "${#gpus[@]}" ]; then
  echo "WORKER_IDS and GPU_INDICES must have the same length" >&2
  exit 2
fi

manifest_args=(
  --output="$ARTIFACT_DIR/manifest.json" --experiment-id="$EXPERIMENT_ID"
  --git-commit="$git_commit" --start-time="$START_TIME" --end-time="$START_TIME"
  --model-id="$MODEL" --model-identity-hash="$MODEL_IDENTITY_HASH" --model-revision="$MODEL_REVISION" --tokenizer-id="$TOKENIZER_ID"
  --tokenizer-revision="$TOKENIZER_REVISION" --chat-template-version="$CHAT_TEMPLATE_VERSION"
  --vllm-version="$VLLM_VERSION" --torch-version="$TORCH_VERSION"
  --cuda-build-version="$CUDA_BUILD_VERSION" --driver-version="$DRIVER_VERSION"
  --scheduler="$SCHEDULER" --cost-profile="$COST_PROFILE" --workload="$WORKLOAD"
  --prefix-tokens="$PREFIX_TOKENS" --output-tokens="$OUTPUT_TOKENS" --requests="$REQUESTS"
  --concurrency="$CONCURRENCY" --warmup-requests="$WARMUP_REQUESTS" --seed="$SEED"
  --repetition="$REPETITION" --shadow-confidence="$SHADOW_CONFIDENCE" --congestion-level="$CONGESTION_LEVEL"
)
for gpu in "${gpus[@]}"; do manifest_args+=(--gpu-index="$gpu"); done
for value in "${controller_flags[@]}"; do manifest_args+=(--controller-flag="$value"); done
for index in "${!ids[@]}"; do manifest_args+=(--worker-flag="worker_id=${ids[$index]},gpu_index=${gpus[$index]},capacity=$WORKER_CAPACITY"); done
if [ "$ONLINE_COST_LEARNING" = true ]; then manifest_args+=(--online-learning); fi
python3 "$ROOT/tools/write_manifest.py" "${manifest_args[@]}"

"$CONTROLLER_BIN" "${controller_flags[@]}" > "$ARTIFACT_DIR/logs/controller.log" 2>&1 &
controller_pid=$!

for index in "${!ids[@]}"; do
  gpu_arg=()
  if [ "${gpus[$index]:-}" != "" ]; then
    gpu_arg=(-gpu-index="${gpus[$index]}")
  fi
  "$WORKERAGENT_BIN" -controller-url="http://$CONTROLLER_ADDR" -worker-id="${ids[$index]}" "${gpu_arg[@]}" -model="$MODEL" -backend-url="${urls[$index]}" -capacity="$WORKER_CAPACITY" > "$ARTIFACT_DIR/logs/agent-${ids[$index]}.log" 2>&1 &
  agent_pids+=("$!")
  curl -fsS "${urls[$index]%/}/metrics" > "$ARTIFACT_DIR/vllm-metrics-before/${ids[$index]}.txt" 2>/dev/null || true
done

for _ in $(seq 1 30); do
  if curl -fsS "http://$CONTROLLER_ADDR/health" >/dev/null; then
    break
  fi
  sleep 1
done

python3 "$ROOT/tools/sample_workers.py" --controller-url="http://$CONTROLLER_ADDR" --output="$ARTIFACT_DIR/worker-samples.jsonl" > "$ARTIFACT_DIR/logs/worker-sampler.log" 2>&1 &
sampler_pid=$!
sleep 1

curl -fsS "http://$CONTROLLER_ADDR/metrics" > "$ARTIFACT_DIR/controller-metrics-before.txt"

loadgen_args=(-target="http://$CONTROLLER_ADDR" -model="$MODEL" -requests="$REQUESTS" -concurrency="$CONCURRENCY" -stream="$STREAM" -format=json -output="$ARTIFACT_DIR/requests.jsonl")
if [ -n "$WORKLOAD" ]; then
  loadgen_args+=(-workload="$WORKLOAD")
fi
"$LOADGEN_BIN" "${loadgen_args[@]}" > "$ARTIFACT_DIR/summary.json"

curl -fsS "http://$CONTROLLER_ADDR/metrics" > "$ARTIFACT_DIR/controller-metrics-after.txt"
curl -fsS "http://$CONTROLLER_ADDR/internal/debug/decisions" > "$ARTIFACT_DIR/decisions.json"
for index in "${!ids[@]}"; do
  curl -fsS "${urls[$index]%/}/metrics" > "$ARTIFACT_DIR/vllm-metrics-after/${ids[$index]}.txt" 2>/dev/null || true
done

kill "$sampler_pid"
wait "$sampler_pid"
sampler_pid=""

for index in "${!manifest_args[@]}"; do
  if [[ "${manifest_args[$index]}" == --end-time=* ]]; then
    manifest_args[$index]="--end-time=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  fi
done
python3 "$ROOT/tools/write_manifest.py" "${manifest_args[@]}"

python3 "$ROOT/tools/analyze_experiment.py" "$ARTIFACT_DIR" --output "$ARTIFACT_DIR/analysis.json"
