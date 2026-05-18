---
description: Run tests only for the Go packages touched in the working tree
---

Run tests for just the packages affected by the current changes — much faster than the full `/race` when you're iterating.

Steps:

1. Run `git status --short` to find changed `.go` files
2. Map each to its package path: `dirname` of each file gives the package directory; convert to a `./internal/...` import path
3. Deduplicate
4. Run `go test -race -count=1` against the union

Example: changes in `internal/providers/anthropic.go` and `internal/api/handler_chat.go` →

```
go test -race -count=1 ./internal/providers/ ./internal/api/
```

For UI changes, run `cd ui && bun run test` (vitest already runs only what's touched in watch mode; for one-shot, full run is fast enough).

For e2e changes (`e2e/*.go`), run `go test -tags e2e -count=1 ./e2e/...` instead — the e2e build tag matters.

Skip this and use `/race` when:

- changes touch shared types in `pkg/types/` (every consumer transitively affected)
- you're at "ready to commit" and want full confidence
