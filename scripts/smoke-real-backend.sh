#!/usr/bin/env bash
set -euo pipefail

MODEL="${MODEL:-example-model}"
VLLM_URL="${VLLM_URL:-http://127.0.0.1:8100}"
CONTROLLER_URL="${CONTROLLER_URL:-http://127.0.0.1:8080}"
PROMPT="${PROMPT:-Say hello in one short sentence.}"

BODY="$(printf '{"model":%q,"messages":[{"role":"user","content":%q}],"max_tokens":16,"stream":false}' "${MODEL}" "${PROMPT}")"
STREAM_BODY="$(printf '{"model":%q,"messages":[{"role":"user","content":%q}],"max_tokens":16,"stream":true}' "${MODEL}" "${PROMPT}")"

echo "Direct vLLM non-streaming request"
curl -sS "${VLLM_URL}/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d "${BODY}"
echo

echo "Controller non-streaming request"
curl -sS "${CONTROLLER_URL}/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -H 'X-Request-ID: smoke-nonstream' \
  -d "${BODY}"
echo

echo "Controller streaming request"
curl -N "${CONTROLLER_URL}/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -H 'Accept: text/event-stream' \
  -H 'X-Request-ID: smoke-stream' \
  -d "${STREAM_BODY}"
echo
