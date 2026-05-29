---
description: Run tests only for the Go packages touched in the working tree
---

Run focused tests for the Go packages touched in the working tree. This is a
Claude Code convenience wrapper; the canonical verification ladder remains
[`docs-ai/core/verification.md`](../../docs-ai/core/verification.md).

Steps:

1. Run `git status --short` to find changed `.go` files.
2. Map each file to its package path: the directory name becomes the package,
   for example `internal/providers/openai.go` becomes `./internal/providers`.
3. Deduplicate package paths.
4. Run `go test -race -count=1` against the union.

Example: changes in `internal/providers/anthropic.go` and
`internal/api/handler_chat.go`:

```sh
go test -race -count=1 ./internal/providers ./internal/api
```

For UI changes, use the UI ladder in
[`docs-ai/core/verification.md`](../../docs-ai/core/verification.md). For e2e
changes (`e2e/*.go`), run `go test -tags e2e -count=1 ./e2e/...` instead; the
e2e build tag matters.

Skip this and use `/race` when shared types in `pkg/types/` changed or when
you are ready for the full runtime/backend confidence check.
