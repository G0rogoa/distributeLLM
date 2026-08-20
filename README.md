# DistServe

DistServe is a learning-oriented control plane for distributed LLM serving. The
current Phase 1 implementation provides an OpenAI-compatible streaming gateway,
standalone heterogeneous mock workers, discovery and health tracking, round-robin and
least-loaded scheduling, reservations, bounded admission, limited retry, metrics, and a
Go load generator. It does not run a real model or require a GPU.

## Run

Requires Go 1.23 or newer. Start the controller first:

```bash
go run ./cmd/controller
```

Then start a worker:

```bash
go run ./cmd/mockworker
```

Additional workers need unique IDs, ports, and advertised URLs. For example:

```bash
go run ./cmd/mockworker -listen=:9002 -id=worker-2 -advertise=http://127.0.0.1:9002
```

Send a streaming request:

```bash
curl -N http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":4,"stream":true}'
```

Inspect the registry with `curl http://127.0.0.1:8080/internal/workers`.

Switch scheduling with `-scheduler=round-robin` or `-scheduler=least-loaded`. Enable
one safe pre-response retry with `-retry`. Metrics are at `/metrics`; recent bounded
request lifecycle records are at `/internal/debug/requests`.

With Docker installed, `docker compose -f deployments/docker-compose.yml up --build`
starts the Controller, three heterogeneous Workers, and Prometheus.

## Test

```bash
go test ./...
go vet ./...
go test -race ./...
```

Run load generation with:

```bash
go run ./cmd/loadgen -requests=100 -concurrency=8 -stream -format=json
```

Reproducible benchmark commands are in `experiments/`; methodology is documented in
`docs/benchmark-methodology.md`. No performance numbers are checked in because this
environment has no Docker installation and benchmark results must be measured.

The API supports only `model`, `messages`, `max_tokens`, `temperature`, and `stream`.
Registry and lifecycle state are in memory; internal APIs have no authentication;
metrics use a small standard-library Prometheus exposition implementation; the mock
worker is a timing simulator, not a model server. See `docs/future-phases.md` for KV
cache-aware scheduling, real vLLM/SGLang adapters, prefill/decode separation, SLOs,
and Kubernetes evolution.
