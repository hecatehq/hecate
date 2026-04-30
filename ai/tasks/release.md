# Release

Cutting a public release tag. Companion to [`../../docs/release.md`](../../docs/release.md), which is the operator-facing version (release notes format, alpha gate, image build). This doc is the agent-side procedure with the footguns the v0.1.0-alpha.1 cycle earned the hard way.

## When this fires

- Operator says "cut a release" / "tag vX.Y.Z" / "ship the alpha" / similar.
- Master is in a stable state worth tagging.
- The change set since the previous tag is meaningful (a release with one typo fix is not worth the operational ceremony).

Default to producing a written plan first ([`../skills/architect/SKILL.md`](../skills/architect/SKILL.md)): version pick, gate posture, recovery path, what's in/out of the release notes.

## Pre-flight

Before running the release script, verify:

1. **`make verify-alpha` exits 0** — full gate: `docs-env-check`, race suite, docker-smoke, UI unit + e2e. See [`../core/verification.md`](../core/verification.md). Mandatory; calling out a skip in release notes is acceptable only when the risk is named.
2. **`goreleaser` is installed.** `which goreleaser`. Install via `go install github.com/goreleaser/goreleaser/v2@latest` if missing.

## Cut the release

Use the release script. It checks clean worktree, tag uniqueness, goreleaser on PATH, fires a snapshot dry-run, then prompts before tagging:

```bash
scripts/release.sh vX.Y.Z
```

For pre-release tags:

```bash
scripts/release.sh v0.1.0-alpha.7
```

To skip the snapshot dry-run (e.g. already ran it manually):

```bash
scripts/release.sh v0.2.0 --skip-snapshot
```

The annotated tag message becomes the canonical release notes — it's what `git show vX.Y.Z` and the GitHub Releases page surface. Write it before tagging; the script prompts for confirmation but doesn't prompt for the message (pass it as the annotation when the script creates the tag, or edit via `git tag -a -f` before pushing if needed).

## Watch CI

Push triggers `.github/workflows/release.yml` → goreleaser → multi-arch binaries + Docker images on `ghcr.io/chicoxyzzy/hecate` + GitHub release entry. The full pipeline runs ~5–10 minutes (Docker buildx multi-arch dominates).

Acceptance:

- Workflow run is green.
- GitHub Releases page has the entry, marked **Pre-release** for `-alpha.N` tags.
- `docker pull ghcr.io/chicoxyzzy/hecate:X.Y.Z` succeeds (no `v` prefix — see footgun below).
- `docker run --rm -p 8765:8765 ghcr.io/chicoxyzzy/hecate:X.Y.Z` then `curl :8765/healthz` returns `version: "X.Y.Z"`.

## Footguns

- **`{{ .Version }}` strips the `v` prefix.** Docker tags are `0.1.0-alpha.1`, **not** `v0.1.0-alpha.1`. The git tag itself keeps the `v`. Same applies to tarball names. The `/healthz` `version` field also reports the bare semver.
- **`.env_file` in compose overrides Dockerfile `ENV`.** If your local `.env` has `GATEWAY_DATA_DIR=.data` (relative), it'll override the Dockerfile's absolute `/data` and break `docker compose cp /data/...`. The current `.env.example` comments these out specifically; old developer-machine `.env` copies may still have the override and will fail `make test-docker-smoke` locally even though CI passes.
- **First-tag changelog is all-history.** Goreleaser builds the auto-changelog from git log between previous and current tags; if there's no previous tag, it includes every commit since the initial commit. Inspect the snapshot output before tagging.
- **Don't run snapshot from a clean checkout, then `git add -A`.** The snapshot writes ~50 MB of binaries into `./dist`; a sweeping `git add` will pick them up if `dist/` isn't gitignored.
- **`ui/dist/.gitkeep` must be tracked.** The `//go:embed all:ui/dist` directive in `embed.go` fails at compile time if `ui/dist` is completely absent from the tree. `.gitignore` keeps `ui/dist/*` but un-ignores `.gitkeep` via negation — the negation only works if `/dist/` (not `dist/`) is the rule anchoring the goreleaser output directory. If `go build` fails with `pattern all:ui/dist: no matching files found`, check that `ui/dist/.gitkeep` is tracked (`git ls-files ui/dist/`) and that `.gitignore` anchors the root dist rule with a leading `/`.
- **`Dockerfile.release` is what goreleaser uses, not `Dockerfile`.** Changes to `Dockerfile` only affect `docker compose up --build` (local dev). The GHCR release image is built from `Dockerfile.release`. Any new `ENV` var or runtime default must go in both.

## Recovery

If CI fails:

```bash
git push --delete origin vX.Y.Z
git tag -d vX.Y.Z
# fix root cause, re-tag, re-push
```

Tag deletion on GitHub also clears the dangling Release entry (if one was created before the failure step). Goreleaser's release pipeline is mostly idempotent — a clean retag at a fixed commit produces the same artifacts.
