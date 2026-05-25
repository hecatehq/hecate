# CLAUDE.md

This is the Hecate repo — an open-source local AI runtime console (Go runtime with embedded React UI plus companion integration entrypoints). Shared agent guidance lives in [`docs-ai/`](docs-ai/README.md). This file is a thin Claude-specific adapter; the substance lives there.

## Start here

[`docs-ai/README.md`](docs-ai/README.md) is the entry point and map.

## Working rules

**Rule 1 — Think Before Coding.** No silent assumptions. State what you're assuming. Surface tradeoffs. Ask before guessing. Push back when a simpler approach exists.

**Rule 2 — Simplicity First.** Minimum code that solves the problem. No speculative features. No abstractions for single-use code. If a senior engineer would call it overcomplicated — simplify.

**Rule 3 — Surgical Changes.** Touch only what you must. Don't "improve" adjacent code, comments, or formatting. Don't refactor what isn't broken. Match existing style.

**Rule 4 — Goal-Driven Execution.** Define success criteria. Loop until verified. Don't tell Claude what steps to follow, tell it what success looks like and let it iterate.

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

| Command          | What it does                                                                           |
| ---------------- | -------------------------------------------------------------------------------------- |
| `/race`          | Full Go race suite — the canonical "ready to commit" check for runtime/backend changes |
| `/test-affected` | Tests only for packages touched in the working tree                                    |

## Repo policy

Shared agent guidance is repository-owned and committed. There is no `.local` override layer and no personal customization tier. If a rule belongs in agent context, it lives in [`docs-ai/`](docs-ai/README.md), in the open, under version control.

## Note on `AGENTS.md`

[`AGENTS.md`](AGENTS.md) at the repo root, [`ui/AGENTS.md`](ui/AGENTS.md), and [`internal/providers/AGENTS.md`](internal/providers/AGENTS.md) are the codebase map and the Codex-discoverable entry points. They stay scoped to map-and-recipes; conventions and longer guidance live in [`docs-ai/`](docs-ai/README.md).
