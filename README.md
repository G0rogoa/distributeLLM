# DistServe

DistServe is an elastic LLM inference control plane for a shared, single-node,
multi-GPU server. It combines resource-level Worker lifecycle scheduling with
request-level KV Cache-aware routing. When an authorized GPU must be returned to a
primary research workload, the planned resource layer coordinates draining, cache
invalidation, admission reduction, and prompt release of DistServe-owned resources.

The target environment is one shared Ubuntu 24.04 LTS KVM virtual machine with
5× NVIDIA A100 80GB GPUs. Users run processes directly and coordinate access
manually; Slurm, Kubernetes, and container orchestration are not the resource
allocator. This remains an LLM serving project, not a general cluster scheduler or
GPU monitoring wrapper.

## Scheduling model

```text
GPU observation + cooperative authorization
                  |
                  v
Resource Scheduler: Worker start, drain, stop, cooldown
                  |
                  v
Registry: the currently usable Worker set
                  |
                  v
Request Scheduler: cache locality, load, queue, stability
```

The resource layer operates over seconds to minutes; request routing operates over
milliseconds to seconds. One independent Worker per GPU keeps failure, lifecycle,
cache ownership, and resource release explicit. Resource scheduling changes the
Worker set; it does not replace request scheduling.

## Current status and roadmap

- Stage 1, complete: streaming Gateway, Mock Workers, Registry, health tracking,
  round-robin and least-loaded scheduling, reservations, bounded admission, retry,
  metrics, lifecycle records, and load generation.
- Stage 2, complete in mock mode: prompt identity, token blocks, prefix hashing,
  bounded Cache Index, Mock Worker LRU, cache events, prefix-aware scheduling,
  eviction, and Cache Fill Reservations.
- Stage 3, planned: real tokenizer and vLLM adapter, one to five Workers on the single
  node, real TTFT/TPOT and prefix-cache observation, and static scaling.
- Stage 4, planned: Node Agent, GPU Observer, cooperative Lease, Resource Policy,
  Elasticity Manager, reclaim/cooldown, Interference Guard, Trace Replay,
  reclaim-risk-aware routing, and dynamic scaling experiments.
- Stage 5, optional: single-node prefill/decode pools and local KV transfer. Cross-node
  RDMA and multi-node serving are not current goals.

Stage 4 will be validated with Mock GPU snapshots, sanitized Trace Replay, and only
then a real observer in an authorized experiment window. Planned experiments cover
static 1→2→3→4→5 scaling, 5→4→3→2 contraction, and 1→3→5 expansion. No unmeasured
performance result is claimed.

## Resource safety boundary

Automatic resource use is planned and disabled by default. Planned safe defaults are
`resource_policy.enabled: false` and `allowed_gpu_indices: []`. Enabling it requires
an explicit allowed set and a valid, group-approved Lease; momentary idleness is not
authorization.

DistServe observes only aggregate resource signals, manages only Workers it created,
and never signals or inspects another user's process beyond detecting the presence of
a foreign compute process. Primary research workloads have priority. Policy is slow
to enter and fast to reclaim, and every lifecycle decision is auditable. “Low
interference” means staying below agreed impact thresholds and releasing resources
quickly and verifiably after a reclaim signal.

The repository contains no server address, credentials, Wi-Fi details, contact data,
or sensitive shared-directory information.

## Run the current mock control plane

Requires Go 1.23 or newer:

```bash
go run ./cmd/controller
go run ./cmd/mockworker
go run ./cmd/mockworker -listen=:9002 -id=worker-2 -advertise=http://127.0.0.1:9002
```

Send a streaming request:

```bash
curl -N http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"mock-llm","messages":[{"role":"user","content":"hello"}],"max_tokens":4,"stream":true}'
```

Prefix-aware scheduling is the default; use `-scheduler=round-robin` or
`-scheduler=least-loaded` for baselines. Metrics are at `/metrics`; see `docs/api.md`
for internal inspection endpoints.

With Docker installed, `docker compose -f deployments/docker-compose.yml up --build`
starts the Controller, three heterogeneous Mock Workers, and Prometheus.

## Test and benchmark

```bash
go test ./...
go vet ./...
go test -race ./...
```

Run the load generator with:

```bash
go run ./cmd/loadgen -requests=100 -concurrency=8 -stream -format=json
```

Request experiment scripts are under `experiments/`. See
`docs/benchmark-methodology.md` and `docs/stage4-benchmark-plan.md`. The current system
uses timing-simulator Workers: it does not run a real model, allocate GPU KV tensors,
automatically claim GPUs, provide authenticated control APIs, or implement Stage 3–5.
The public mock API supports only `model`, `messages`, `max_tokens`, `temperature`, and
`stream`; Controller state remains in memory and internal APIs are unauthenticated.
