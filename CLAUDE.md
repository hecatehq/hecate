# CLAUDE.md

This is the Hecate repo — an open-source AI gateway and agent-task runtime (Go gateway with embedded React UI plus companion integration entrypoints). Shared agent guidance lives in [`docs-ai/`](docs-ai/README.md). This file is a thin Claude-specific adapter; the substance lives there.

## Start here

[`docs-ai/README.md`](docs-ai/README.md) is the entry point and map.

## When working on this repo

Pick the right skill for the change:

- **Backend** (anything outside `ui/` and `tauri/`) — [`hecate-backend`](docs-ai/skills/backend/SKILL.md).
- **UI** (`ui/`) — [`hecate-ui`](docs-ai/skills/ui/SKILL.md).
- **Native desktop app** (`tauri/`) — [`hecate-tauri`](docs-ai/skills/tauri/SKILL.md). Tauri 2.x Rust layer, sidecar lifecycle, platform bundling, gateway↔webview integration.
- **Provider adapters** (`internal/providers/`, anything that crosses the api↔providers boundary) — [`hecate-providers`](docs-ai/skills/providers/SKILL.md). Owns the canonical seven-step "add a wire field" chain.
- **Planning a substantial change** — [`hecate-architect`](docs-ai/skills/architect/SKILL.md). Produces a plan, not code.
- **Test strategy / coverage audit / verification report** — [`hecate-tester`](docs-ai/skills/tester/SKILL.md).
- **Delivery review** (env vars, schema migrations, CI/CD, observability surfaces) — [`hecate-devops`](docs-ai/skills/devops/SKILL.md).

## Useful slash commands

| Command | What it does |
|---|---|
| `/race` | Full Go race suite — the canonical "ready to commit" check for runtime/backend changes |
| `/test-affected` | Tests only for packages touched in the working tree |

## Repo policy

Shared agent guidance is repository-owned and committed. There is no `.local` override layer and no personal customization tier. If a rule belongs in agent context, it lives in [`docs-ai/`](docs-ai/README.md), in the open, under version control.

## Note on `AGENTS.md`

[`AGENTS.md`](AGENTS.md) at the repo root, [`ui/AGENTS.md`](ui/AGENTS.md), and [`internal/providers/AGENTS.md`](internal/providers/AGENTS.md) are the codebase map and the Codex-discoverable entry points. They stay scoped to map-and-recipes; conventions and longer guidance live in [`docs-ai/`](docs-ai/README.md).
