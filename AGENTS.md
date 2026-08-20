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
