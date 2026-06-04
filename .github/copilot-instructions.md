# Hecate Copilot Instructions

Hecate is an open-source local AI operations console for supervised agent work.
It combines a model gateway, Hecate Chat, queued `agent_loop` tasks, External
Agent supervision, projects, memory, approvals, artifacts, usage, and
OpenTelemetry traces into one operator surface.

This file is a GitHub Copilot adapter shim. The canonical provider-neutral
guidance lives in `AGENTS.md` and `docs-ai/`; read those before changing code.
Do not copy long-form rules into this file.

Universal rules:

- Keep changes scoped to the user request and the existing architecture.
- Follow `docs-ai/core/engineering-standards.md` and
  `docs-ai/core/workflow.md`.
- Use the relevant `docs-ai/skills/*/SKILL.md` for the area being changed.
- Do not create Claude-, Cursor-, Codex-, or Copilot-specific copies of shared
  guidance.
- Do not auto-commit. Suggest a Conventional Commit message when useful.
- Report the verification you ran, and call out anything you could not run.

Verification starts at `docs-ai/core/verification.md`. Common gates:

- Backend/runtime: focused `go test`, then `go test -race -timeout 10m ./...`
  or `just test-race` for broad runtime changes.
- E2E: `go test -tags e2e ./e2e/...`.
- UI: `cd ui && bun run typecheck && bun run test`; never use `bun test`.
- Agent docs: `just agent-docs-check`, `just docs-format-check`, and
  `just check-links` when links change.
