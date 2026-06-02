---
applyTo: "cmd/**/*.go,internal/**/*.go,pkg/**/*.go,e2e/**/*.go,go.mod,go.sum"
---

# Backend Runtime

For Go backend/runtime work, read `AGENTS.md` plus
`docs-ai/skills/backend/SKILL.md`. Use `docs-ai/core/verification.md` for the
verification ladder.

High-signal rules:

- Workspace-bound file/search/write behavior goes through WorkspaceFS;
  subprocesses go through ProcessRunner, GitRunner, or the sandbox seams.
- Mirror persisted behavior across memory and sqlite backends.
- Run events are append-only and use the runtime event seams; document new event
  types in `docs/events.md`.
- Store money as `int64` micro-USD, never floats.
- Add OTel spans/attributes for new request or runtime paths.
- Do not introduce auth, tenant, or remote-multi-user assumptions; Hecate is a
  local operator console.
- Add unit tests and e2e tests for backend behavior changes.
