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
bun scripts/release.ts vX.Y.Z
```

For pre-release tags:

```bash
bun scripts/release.ts v0.1.0-alpha.7
```

To skip the snapshot dry-run (e.g. already ran it manually):

```bash
bun scripts/release.ts v0.2.0 --skip-snapshot
```

The annotated tag message becomes the canonical release notes — it's what `git show vX.Y.Z` and the GitHub Releases page surface. Write it before tagging; the script prompts for confirmation but doesn't prompt for the message (pass it as the annotation when the script creates the tag, or edit via `git tag -a -f` before pushing if needed).

## Tauri desktop app

The native desktop app (`tauri/`) **is built and uploaded by CI** as part of the release pipeline — no manual `make tauri-build` step is required when cutting a tag. Bundle architecture and the per-platform build details live in [`../skills/tauri/SKILL.md`](../skills/tauri/SKILL.md); the operator-facing distribution + roadmap view is at [`../../docs/desktop-app.md`](../../docs/desktop-app.md); this section is the release-time view.

### What CI does

`.github/workflows/release.yml` runs two jobs on a `v*` tag push:

1. `goreleaser` — Linux/macOS binary tarballs containing `hecate` and `hecate-acp`, multi-arch Docker images on GHCR, GitHub Release entry.
2. `tauri` (`needs: goreleaser`) — three-platform matrix (macOS arm64, Linux x86_64, Windows x86_64) calls the reusable `_tauri-shared.yml` workflow with `tagName: ${{ github.ref_name }}`. Each leg builds the hecate sidecar, the Tauri bundle, and uploads platform-native artifacts (`.dmg` / `.deb` + `.AppImage` / `.msi`) to the existing release.

End state of a successful tag: the GitHub Release page has goreleaser tarballs + Docker images + four desktop bundles, all attached.

### Version stamping

`bun scripts/release.ts` handles the stamp automatically: after confirmation it calls `scripts/stamp-version.ts` with `TAURI_VERSION=<semver>`, commits the changed files (`Cargo.toml`, `package.json`, `tauri.conf.json`), then creates the annotated tag on that commit. CI re-runs the stamp from the tag name as a belt-and-suspenders measure (`stamp-version.ts` is idempotent).

The Tauri matrix doesn't need any local action — pushing the tag fires the workflow.

### Pre-tag validation

`.github/workflows/tauri-build.yml` runs the same matrix on PRs (path-filtered to changes that could break it: `tauri/**`, `cmd/hecate/**`, `ui/**`, `Makefile`, `scripts/stamp-version.ts`, the workflows themselves). Bundles persist as workflow artifacts (14-day retention) so reviewers can download and test-launch from the run page.

If the change set touches the desktop pipeline, prefer landing it via PR so the matrix runs before the tag — it's the only way to find out a Windows-only or Linux-only regression without burning a release.

### Manual local build (rarely needed)

```bash
TAURI_VERSION=X.Y.Z make tauri-build
```

Outputs land in `tauri/src-tauri/target/release/bundle/`. Use this for iterating on Tauri-specific issues that the cargo-cache hides on rebuilds; for shipping, let CI do it.

### Tauri-specific footguns

- **Don't build manually then expect CI artifacts to match.** The CI matrix produces bundles signed differently (or unsigned) from a local build. Local artifacts are for debugging, not distribution.
- **`0.1.0-alpha.N` is valid semver for Tauri**, but macOS `CFBundleShortVersionString` strips the pre-release suffix in the About dialog. That's expected — Tauri handles it internally.
- **Code signing is not configured.** macOS shows Gatekeeper warning on first launch; Windows MSI shows SmartScreen. Document this for users in release notes until signing secrets land. tauri-action picks up `APPLE_CERTIFICATE` / `TAURI_SIGNING_PRIVATE_KEY` / etc. from env when added — no workflow rewrite needed.
- **`tauri/src-tauri/target/` is large** (~1–2 GB after a release build). Don't accidentally `git add` it — it's gitignored, but be specific with paths anyway.
- **Icons must be format-correct.** A `.png` renamed to `.ico` will pass macOS but fail Windows `RC.EXE`. Regenerate via `bunx @tauri-apps/cli icon source.png` if changing artwork.

## Watch CI

Push triggers `.github/workflows/release.yml` with two jobs:

1. `goreleaser` (~5–10 min, Docker buildx multi-arch dominates) — multi-arch binaries + Docker images on `ghcr.io/chicoxyzzy/hecate` + GitHub Release entry.
2. `tauri` (`needs: goreleaser`, ~10–15 min, three platforms in parallel) — desktop bundles attached to the same release entry. Cold rust-cache adds ~5 min on first run; subsequent runs at the same dep set are warm.

Total wall-clock: ~15–25 min.

Acceptance:

- Both workflow jobs are green.
- GitHub Releases page has the entry, marked **Pre-release** for `-alpha.N` tags.
- Goreleaser-side artifacts attached: tarballs for each `goos/goarch`, source tarball, checksums. Each binary tarball contains `hecate` and `hecate-acp`.
- Tauri-side artifacts attached: one `.dmg`, one `.deb`, one `.AppImage`, one `.msi`. If any is missing, the matrix leg silently skipped upload — open the run, find the leg, see what failed.
- Bundle sizes look right: `.dmg` ~20–40 MB, `.deb` ~15–25 MB, `.AppImage` ~80–120 MB (bundles its own libs), `.msi` ~15–25 MB. A 1 MB `.dmg` means the sidecar didn't embed — investigate before announcing.
- `docker pull ghcr.io/chicoxyzzy/hecate:X.Y.Z` succeeds (no `v` prefix — see footgun below).
- `docker run --rm -p 8765:8765 ghcr.io/chicoxyzzy/hecate:X.Y.Z` then `curl :8765/healthz` returns `version: "X.Y.Z"`.
- (Optional but recommended for `-alpha.N`) Download the `.dmg` and verify it launches: window opens, splash → gateway UI, auto-logged in (no token paste), `cmd+Q` leaves no orphan `gateway` process. ~10 min and catches >90% of desktop-side regressions.

## Footguns

- **`{{ .Version }}` strips the `v` prefix.** Docker tags are `0.1.0-alpha.1`, **not** `v0.1.0-alpha.1`. The git tag itself keeps the `v`. Same applies to tarball names. The `/healthz` `version` field also reports the bare semver.
- **`.env_file` in compose overrides Dockerfile `ENV`.** If your local `.env` has `GATEWAY_DATA_DIR=.data` (relative), it'll override the Dockerfile's absolute `/data` and break `docker compose cp /data/...`. The current `.env.example` comments these out specifically; old developer-machine `.env` copies may still have the override and will fail `make test-docker-smoke` locally even though CI passes.
- **First-tag changelog is all-history.** Goreleaser builds the auto-changelog from git log between previous and current tags; if there's no previous tag, it includes every commit since the initial commit. Inspect the snapshot output before tagging.
- **Don't run snapshot from a clean checkout, then `git add -A`.** The snapshot writes ~50 MB of binaries into `./dist`; a sweeping `git add` will pick them up if `dist/` isn't gitignored.
- **`ui/dist/.gitkeep` must be tracked.** The `//go:embed all:ui/dist` directive in `embed.go` fails at compile time if `ui/dist` is completely absent from the tree. `.gitignore` keeps `ui/dist/*` but un-ignores `.gitkeep` via negation — the negation only works if `/dist/` (not `dist/`) is the rule anchoring the goreleaser output directory. If `go build` fails with `pattern all:ui/dist: no matching files found`, check that `ui/dist/.gitkeep` is tracked (`git ls-files ui/dist/`) and that `.gitignore` anchors the root dist rule with a leading `/`.
- **`Dockerfile.release` is what goreleaser uses, not `Dockerfile`.** Changes to `Dockerfile` only affect `docker compose up --build` (local dev). The GHCR release image is built from `Dockerfile.release`. Any new `ENV` var or runtime default must go in both.
- **CI's `e2e-ollama` job runs under `-tags 'e2e ollama'`** — `make verify-alpha` only covers `-tags 'e2e docker'` locally, so an ollama-only regression sails through the local gate. The `v0.1.0-alpha.7` cut hit this with the env-PRECONFIGURED gate: `gateway_test.go` was patched but `ollama_test.go` was missed. Before tagging, also run `OLLAMA_BASE_URL=http://127.0.0.1:11434 OLLAMA_MODEL=smollm2:135m go test -tags 'e2e ollama' -count=1 ./e2e/...` if any e2e helper has changed.
- **Lychee link-check runs only on master pushes**, not on tag pushes — a broken markdown link in `AGENTS.md` / `docs-ai/**` won't block a release, but it'll blink red on the next master push. Run `make check-links` (or grep for the suspected dangling target) before tagging when the change set is doc-heavy.

## Recovery

If CI fails:

```bash
git push --delete origin vX.Y.Z
git tag -d vX.Y.Z
# fix root cause, re-tag, re-push
```

Tag deletion on GitHub also clears the dangling Release entry (if one was created before the failure step). Goreleaser's release pipeline is mostly idempotent — a clean retag at a fixed commit produces the same artifacts.
