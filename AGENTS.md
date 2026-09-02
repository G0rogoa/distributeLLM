# DistServe contributor notes

- The maintainer comes from C++ and is learning Go; keep code direct and readable.
- Avoid abstractions unless there are multiple implementations or a real test boundary.
- Run `go test -race ./...` for every concurrency change.
- Every goroutine must have an explicit exit condition.
- Every reservation introduced in later milestones must be released on every exit path.
- Do not refactor unrelated modules.
- Never invent benchmark results.
- Read `docs/architecture.md` and `docs/concurrency-model.md` before changing core design.
- Keep design documentation synchronized with core design changes.
- Keep third-party dependencies few and justified.
- Preserve existing user changes.

## Remote development and test environment

- Develop in this WSL checkout. Treat Git as the source of truth; do not edit the remote checkout independently unless explicitly requested.
- Use SSH host alias `a100-lab`. Its hop and key details live in `~/.ssh/config`; do not copy credentials, private keys, or passwords into this repository.
- The remote project directory is `/data/zhangshenqiang/distserve`.
- The remote Conda environment is `zsq` under `/opt/anaconda3`.
- For non-interactive commands, use `/opt/anaconda3/bin/conda run -n zsq ...` instead of relying on `conda activate`.
- Before every real GPU test, inspect `nvidia-smi`. A GPU with any process owned by another user is unavailable even when its utilization is zero.
- Never terminate, signal, inspect the files of, or otherwise interfere with another user's processes. Do not automatically choose occupied GPUs.
- Default to one vLLM instance per GPU. Only GPUs 0 and 1 are candidates for tensor-parallel experiments, and only after confirming both are free.
- Start with one free GPU and expand gradually. Do not reserve more GPUs than a test needs, and release all project processes after the test.
- Do not expose services publicly, create new tunnels, or alter the existing relay. Bind test services to loopback unless explicitly instructed otherwise.
- Do not automatically push, deploy, start long-running GPU jobs, or modify the remote environment without explicit confirmation.
