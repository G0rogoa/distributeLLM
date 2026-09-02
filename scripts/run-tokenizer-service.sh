#!/usr/bin/env bash
set -euo pipefail

HOST="${HOST:-127.0.0.1}"
PORT="${PORT:-18091}"
TOKENIZER_PATH="${TOKENIZER_PATH:?TOKENIZER_PATH is required}"
MODEL_ID="${MODEL_ID:?MODEL_ID is required}"
MODEL_REVISION="${MODEL_REVISION:?MODEL_REVISION is required}"
TOKENIZER_ID="${TOKENIZER_ID:?TOKENIZER_ID is required}"
TOKENIZER_REVISION="${TOKENIZER_REVISION:?TOKENIZER_REVISION is required}"
CHAT_TEMPLATE_VERSION="${CHAT_TEMPLATE_VERSION:?CHAT_TEMPLATE_VERSION is required}"
MAX_BYTES="${MAX_BYTES:-1048576}"
MAX_TOKENS="${MAX_TOKENS:-262144}"
PYTHON="${PYTHON:-python}"

exec "${PYTHON}" tools/tokenizer_service/server.py \
  --host "${HOST}" \
  --port "${PORT}" \
  --tokenizer-path "${TOKENIZER_PATH}" \
  --model-id "${MODEL_ID}" \
  --model-revision "${MODEL_REVISION}" \
  --tokenizer-id "${TOKENIZER_ID}" \
  --tokenizer-revision "${TOKENIZER_REVISION}" \
  --chat-template-version "${CHAT_TEMPLATE_VERSION}" \
  --max-bytes "${MAX_BYTES}" \
  --max-tokens "${MAX_TOKENS}"
