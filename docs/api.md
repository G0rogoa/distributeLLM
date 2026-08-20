# API

## Public

`POST /v1/chat/completions` accepts `model`, `messages`, `max_tokens`,
`temperature`, and `stream`. The only model is `mock-llm`; `max_tokens` is 1–4096.
Streaming responses use `text/event-stream` and finish with `data: [DONE]`.
`X-Request-ID` is accepted or generated and returned. `GET /health` reports process
health and `GET /metrics` returns Prometheus text exposition.

## Internal experimental API

`POST /internal/workers/register`, `POST /internal/workers/{id}/heartbeat`,
`POST /internal/workers/{id}/drain`, `GET /internal/workers`, and
`GET /internal/debug/requests` are unauthenticated lab endpoints. Production use must
place them on an isolated listener with authentication and authorization.

Errors use an OpenAI-like `{ "error": { "message", "type", "code" } }` envelope at
the Gateway. Important status codes are 400 invalid input/model, 429 controller
admission rejection, 503 no eligible worker/worker queue full, 504 deadline, and 502
worker transport failure.
