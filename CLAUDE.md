# CLAUDE.md

This is the Hecate repo — an open-source local AI runtime console. Shared,
provider-neutral agent guidance lives in [`docs-ai/`](docs-ai/README.md). This
file is only the Claude Code adapter; it must not carry a parallel copy of the
project rules.

## Start Here

1. Read [`docs-ai/README.md`](docs-ai/README.md).
2. Load the relevant Hecate skill from [`docs-ai/skills/`](docs-ai/skills/).
3. Use [`docs-ai/core/agent-guidance.md`](docs-ai/core/agent-guidance.md) when
   changing any agent-facing instruction file.

## Claude Code Adapters

- [`.claude/skills/`](.claude/skills/) contains symlinks to
  [`docs-ai/skills/`](docs-ai/skills/) so Claude Code can discover the same
  skills every other agent reads. Add or edit skill content under `docs-ai/`,
  then mirror only the symlink if a new skill is introduced.
- [`.claude/commands/`](.claude/commands/) contains Claude Code slash-command
  wrappers. Command behavior delegates to canonical verification guidance in
  [`docs-ai/core/verification.md`](docs-ai/core/verification.md).
- [`.claude/settings.json`](.claude/settings.json) is a shared permission
  baseline. Local `settings.local.json` files are allowed for per-machine
  permissions or session state, but they are not repository guidance.

## Useful Slash Commands

| Command          | What it does                                                                         |
| ---------------- | ------------------------------------------------------------------------------------ |
| `/race`          | Full Go race suite — the canonical ready-to-commit check for runtime/backend changes |
| `/test-affected` | Focused tests for packages touched in the working tree                               |

## Repo Policy

If a rule belongs in agent context, it lives in [`docs-ai/`](docs-ai/README.md)
in the open, under version control. Tool-specific files may adapt discovery or
commands, but they must not define their own project policy.
