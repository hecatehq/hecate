# Implementation

How to implement once a plan exists, or when the change is small enough to skip planning.

## Pre-edit checklist

Before touching any file:

1. `go build ./...` passes — establish a clean baseline. Don't modify a broken tree.
2. The file is read and the local patterns are understood — naming, error wrapping, comment density, test layout.
3. The neighboring test file is read — understand what helpers and fixtures exist before writing new ones.
4. The change fits in one logical commit. If it fans out for unrelated reasons, identify the split points now, not mid-edit.

## Make minimal coherent changes

One logical change at a time. If it fans out across unrelated reasons, split into separate commits. The reviewer (human or agent) should be able to hold the whole change in their head; "and while I was here…" diffs defeat that.

## Preserve local style

Read the neighboring code first. Mirror its conventions — naming, error wrapping, comment density, test layout. Don't introduce a new style island. The codebase is internally consistent on purpose; matching what's already there is faster than inventing a new pattern.

## Avoid unrelated edits

Drive-by formatting, renaming, or "while I'm here" cleanups bloat the diff and obscure review. If a cleanup is worth doing, it's worth a follow-up commit.

## Post-edit checklist

After each logical step:

1. `go build ./...` still passes.
2. If platform-specific files changed, cross-compile: `GOOS=linux go build ./...` and `GOOS=darwin go build ./...`.
3. New or updated tests match the local pattern — table-driven where the variant set is obvious, named `TestPackage_Behavior`.
4. Docs updated in the same change (not as a follow-up):

| Change | Doc |
|---|---|
| New env var | `.env.example` AND the relevant `docs/<feature>.md` env-var table |
| New API field | `docs/runtime-api.md` (or wherever the contract lives) |
| New event type | `docs/events.md` with payload shape |
| New built-in tool | `docs/agent-runtime.md` and/or `docs/mcp.md` |
| New behavior on the api↔providers boundary | both sides' tests |
| New isolation / sandbox capability | `docs/sandbox.md` |

5. `git diff --stat` reviewed — confirm the change is cohesive, no accidental drift, no unrelated formatting.

## When to add comments

When the *why* isn't obvious from the code. State the trade-off being accepted, not the mechanic. The reader can see *what* the code does; they can't see what was rejected and why.

Don't add comments that paraphrase identifiers. `// increment counter` ages into noise. If the function name says it, don't say it again.

## The seven-step chain

When the change is "add a passthrough wire field," follow the canonical seven-step chain in [`../skills/providers/SKILL.md`](../skills/providers/SKILL.md). Forgetting to plumb the field into the streaming `wireReq` is the most common bug — the non-stream tests pass, the field silently drops in production for any client using `stream: true`.
