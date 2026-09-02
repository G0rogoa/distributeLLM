#!/usr/bin/env bash
set -euo pipefail

GPU_INDEX="${GPU_INDEX:-0}"
MODEL="${MODEL:-example-model}"
PORT="${PORT:-8100}"
HOST="${HOST:-127.0.0.1}"

echo "Before running this script, inspect nvidia-smi and confirm GPU ${GPU_INDEX} has no other user's process."
echo "Starting one externally managed vLLM OpenAI server on ${HOST}:${PORT} for model ${MODEL}."

CUDA_VISIBLE_DEVICES="${GPU_INDEX}" \
python -m vllm.entrypoints.openai.api_server \
  --host "${HOST}" \
  --port "${PORT}" \
  --model "${MODEL}"
