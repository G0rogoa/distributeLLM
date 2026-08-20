# Architecture

## Current slice

Milestone 1 is one vertical request path:

```text
client -> controller/gateway -> mock worker -> controller/gateway -> client
```

The controller is a modular monolith. The gateway owns HTTP validation, request IDs,
request deadlines, backend forwarding, and streaming proxy behavior. It depends only
on an HTTP backend address, not on the mock worker implementation. The mock worker is
a separate process and implements the same small OpenAI-compatible endpoint.

API wire types live separately from gateway and worker behavior. This prevents mock
simulation details from leaking into the public API and leaves a thin seam where a
vLLM/SGLang backend adapter can be added later.

Milestone 2 adds a registry beside the gateway, not inside it. A worker registers its
stable ID plus a process-specific instance ID, then reports load every two seconds.
The controller sweeps heartbeat age every second. Schedulers in later milestones will
consume immutable worker snapshots; they will not access registry maps.

The internal experimental endpoints are:

- `POST /internal/workers/register`
- `POST /internal/workers/{id}/heartbeat`
- `POST /internal/workers/{id}/drain`
- `GET /internal/workers`

They intentionally have no authentication yet and must not be exposed publicly.

## Request sequence

1. Gateway validates the JSON request and model.
2. Gateway creates or forwards an `X-Request-ID` and derives a timeout context.
3. The HTTP request to the worker uses that context.
4. For streaming responses the gateway copies each SSE event and flushes immediately.
5. Completion, error, timeout, or client cancellation closes the upstream response.
6. A structured completion log records TTFT and total duration without logging prompts.

The modular monolith keeps state ownership and failure behavior visible while the
project is small. Registry, scheduling, and admission modules can later become service
boundaries without changing the external API.

## Completed Phase 1 modules

- Gateway owns validation, admission, timeout, retry boundary, streaming proxy, and
  lifecycle completion.
- Registry owns Worker identity, health, load reports, and local reservations.
- Scheduler consumes copied snapshots and returns explainable decisions. It neither
  mutates Registry nor performs network I/O.
- Mock Worker owns its running semaphore, queue semaphore, timing simulation, random
  source, and heartbeat client.
- Telemetry owns bounded counters/observations and renders Prometheus exposition.
- Loadgen is a separate client process and has no control-plane dependency.

Dependency direction is `cmd -> gateway -> registry/scheduler`, while `scheduler` only
depends on Registry snapshot types. Selection is followed by a separate atomic Reserve
commit so a stale snapshot cannot create work on a changed or full instance.

## Phase 2 prefix preparation

`internal/cache` now provides a request-local preparation pipeline without changing
Phase 1 scheduling behavior:

```text
messages -> deterministic PromptBuilder -> Tokenizer -> TokenBlock builder
         -> versioned CacheIdentity -> SHA-256 Prefix Hash Chain
```

Tokenization and hashing run before any future Cache Index lock is acquired. Only full
blocks enter the reusable prefix chain. The mock Tokenizer is deterministic test
infrastructure and does not claim compatibility with a real model.
