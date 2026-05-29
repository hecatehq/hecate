---
description: Run the full Go race suite from the canonical verification ladder
---

Run the runtime/backend race-suite floor from
[`docs-ai/core/verification.md`](../../docs-ai/core/verification.md):

```sh
go test -race -timeout 10m ./...
```

Report whether it passed cleanly. If anything fails, surface the package and
test name plus the first error line; don't dump the full trace unless asked.
