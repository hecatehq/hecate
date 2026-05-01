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

The native desktop app (`tauri/`) is **not yet part of the goreleaser pipeline**. It is built and distributed separately, out-of-band from the Docker images and binary tarballs.

### Version stamping

`bun scripts/release.ts` handles the stamp automatically: after confirmation it calls `scripts/stamp-version.ts` with `TAURI_VERSION=<semver>`, commits the changed files (`Cargo.toml`, `package.json`, `tauri.conf.json`), then creates the annotated tag on that commit. No manual stamp step needed when using `release.ts`.

`make tauri-build` also depends on `make tauri-version` (`scripts/stamp-version.ts`), which resolves the version from: `TAURI_VERSION` env var → latest git tag → existing `Cargo.toml`. This means the tag must exist before `make tauri-build` runs — `release.ts` guarantees this since it tags before you run the build.

```bash
# Full flow:
bun scripts/release.ts vX.Y.Z   # stamps + commits + tags + pushes to CI
make tauri-build                 # builds desktop bundles; picks up vX.Y.Z from tag
# upload tauri/src-tauri/target/release/bundle/* to the GitHub Release manually
```

Or stamp and build without going through `release.ts`:

```bash
TAURI_VERSION=X.Y.Z make tauri-build
```

### Build artifacts

`make tauri-build` outputs to `tauri/src-tauri/target/release/bundle/`. Platform-specific:

| Platform | Output |
|---|---|
| macOS | `.app` bundle + `.dmg` installer |
| Windows | `.msi` installer + `.exe` setup |
| Linux | `.deb`, `.rpm`, `.AppImage` |

Upload these to the GitHub Release entry manually until CI automation is added.

### Tauri-specific footguns

- **Tag before build, not after.** `make tauri-version` reads the git tag at script run time. If you build first and tag second, the bundle version will be the previous release.
- **`0.1.0-alpha.N` is valid semver for Tauri**, but macOS `CFBundleShortVersionString` strips the pre-release suffix in the About dialog. That's expected — Tauri handles it internally.
- **`tauri/src-tauri/binaries/hecate-{triple}` must exist before `tauri-build`.** `make tauri-build` runs `make tauri-sidecar` which calls `make build` (Go binary) first. If Go fails to compile, the Tauri build fails. Run `make build` independently first if you want to isolate the Go failure.
- **Code signing is not configured.** macOS will show a Gatekeeper warning on first launch for unsigned builds. Windows will show a SmartScreen warning. Document this for users until signing is wired.
- **`tauri/src-tauri/target/` is large** (~1–2 GB after a release build). Don't accidentally `git add` it — it's gitignored but `git add -A` from the repo root will not descend into it. If you're adding Tauri files manually, use specific paths.

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
- **CI's `e2e-ollama` job runs under `-tags 'e2e ollama'`** — `make verify-alpha` only covers `-tags 'e2e docker'` locally, so an ollama-only regression sails through the local gate. The `v0.1.0-alpha.7` cut hit this with the env-PRECONFIGURED gate: `gateway_test.go` was patched but `ollama_test.go` was missed. Before tagging, also run `OLLAMA_BASE_URL=http://127.0.0.1:11434 OLLAMA_MODEL=smollm2:135m go test -tags 'e2e ollama' -count=1 ./e2e/...` if any e2e helper has changed.
- **Lychee link-check runs only on master pushes**, not on tag pushes — a broken markdown link in `AGENTS.md` / `ai/**` won't block a release, but it'll blink red on the next master push. Run `make check-links` (or grep for the suspected dangling target) before tagging when the change set is doc-heavy.

## Recovery

If CI fails:

```bash
git push --delete origin vX.Y.Z
git tag -d vX.Y.Z
# fix root cause, re-tag, re-push
```

Tag deletion on GitHub also clears the dangling Release entry (if one was created before the failure step). Goreleaser's release pipeline is mostly idempotent — a clean retag at a fixed commit produces the same artifacts.
