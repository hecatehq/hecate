# Release

Cutting a public release tag. Companion to [`../../docs/contributor/release.md`](../../docs/contributor/release.md), which is the operator-facing version (release notes format, verification gate, image build). This doc is the agent-side procedure with the footguns the v0.1.0-alpha.1 cycle earned the hard way.

## When this fires

- Operator says "cut a release" / "tag vX.Y.Z" / "ship the alpha" / similar.
- Master is in a stable state worth tagging.
- The change set since the previous tag is meaningful (a release with one typo fix is not worth the operational ceremony).

Default to producing a written plan first ([`../skills/architect/SKILL.md`](../skills/architect/SKILL.md)): version pick, gate posture, recovery path, what's in/out of the release notes.

## Pre-flight

Before running the release script, verify:

1. **`just verify` exits 0** — full gate: `docs-env-check`, race suite, docker-smoke, UI unit + e2e. See [`../core/verification.md`](../core/verification.md). Mandatory; calling out a skip in release notes is acceptable only when the risk is named.
2. **`goreleaser` is installed.** `which goreleaser`. Install via `go install github.com/goreleaser/goreleaser/v2@latest` if missing.
3. **Docker is reachable unless `--skip-snapshot` is intentional.** `just release` runs this check before the expensive verify gate because the Goreleaser snapshot builds local Docker images.

## Cut the release

Use the release recipe. It runs the release script in preflight-only mode,
then `just verify`, then the full release script. The script checks clean
worktree, tag uniqueness, goreleaser on PATH, Docker availability for the
snapshot, fires a snapshot dry-run, then prompts before tagging:

```bash
just release vX.Y.Z
```

For pre-release tags:

```bash
just release v0.1.0-alpha.7
```

To skip the snapshot dry-run (e.g. already ran it manually):

```bash
just release v0.2.0 --skip-snapshot
```

The release helper creates an annotated tag whose message is only the version,
so ordinary releases use GoReleaser's generated changelog. For a substantive
release, create an annotated tag manually from a reviewed Markdown file. The
release workflow detects that non-version annotation and passes it to
GoReleaser as the public GitHub Release body. Create that tag with
`git tag -a --cleanup=verbatim vX.Y.Z -F /tmp/release-notes.md`; without
`--cleanup=verbatim`, Git treats Markdown headings as comments and silently
strips them from the annotation.

## Tauri desktop app

The native desktop app (`tauri/`) **is built and uploaded by CI** as part of the release pipeline — no manual `just tauri-build` step is required when cutting a tag. Bundle architecture and the per-platform build details live in [`../skills/tauri/SKILL.md`](../skills/tauri/SKILL.md); the operator-facing distribution + roadmap view is at [`../../docs/operator/desktop-app.md`](../../docs/operator/desktop-app.md); this section is the release-time view.

### What CI does

`.github/workflows/release.yml` runs two jobs on a `v*` tag push:

1. `goreleaser` — Linux/macOS binary tarballs containing `hecate`, multi-arch Docker images on GHCR, GitHub Release entry.
2. `tauri` (`needs: goreleaser`) — three-platform matrix (macOS arm64, Linux x86_64, Windows x86_64) calls the reusable `_tauri-shared.yml` workflow with `tagName: ${{ github.ref_name }}`. Each leg builds the hecate sidecar, the Tauri bundle, and uploads platform-native artifacts (`.dmg` / `.deb` + `.AppImage` / `.msi`) to the existing release.

End state of a successful tag: the GitHub Release page has goreleaser tarballs + Docker images + four desktop bundles, all attached.

### Version stamping

`just release` / `bun scripts/release.ts` handles the stamp automatically: after confirmation it calls `scripts/stamp-version.ts` with `TAURI_VERSION=<semver>`, commits the changed files (`Cargo.toml`, `package.json`, `tauri.conf.json`), then creates the annotated tag on that commit. CI re-runs the stamp from the tag name as a belt-and-suspenders measure (`stamp-version.ts` is idempotent).

The stamp commit remains on the release branch. The script pushes both that
branch and the annotated tag, keeping visible Tauri version metadata aligned
with the latest release. Release CI may then add updater-manifest and release
reference commits to `master`; after CI completes, run
`git pull --ff-only origin master` to pick up those post-release commits.

The Tauri matrix doesn't need any local action — pushing the tag fires the workflow.

### Pre-tag validation

The main `.github/workflows/test.yml` workflow owns PR-time desktop validation.
It path-filters desktop-impacting changes (`tauri/**`, `cmd/hecate/**`,
`Justfile`, `just/**`, Tauri version scripts, release
packaging files, and the workflows themselves), then starts the
`Tauri desktop bundles` matrix only after the cheaper Go, TypeScript, e2e,
Docker smoke, and Tauri Rust jobs pass or skip. The PR matrix proves the
macOS, Linux, and Windows bundles build, but does not upload unsigned bundle
artifacts. Treat that as packaging validation only: macOS is the only desktop
bundle maintainers currently launch-test, and release notes must not imply the
Linux `.deb` / `.AppImage` or Windows `.msi` have been manually tested until
that smoke run happens on real machines.

If the change set touches the desktop pipeline, prefer landing it via PR so the
matrix runs before the tag — it's the only way to find out a Windows-only or
Linux-only regression without burning a release. Use the manual
`.github/workflows/tauri-build.yml` workflow from the Actions tab only for an
explicit desktop rebuild/debug run.
If a reviewer needs to test-launch a bundle before merge, dispatch
`.github/workflows/tauri-build.yml` manually from the PR branch.

### Manual local build (rarely needed)

```bash
TAURI_VERSION=X.Y.Z just tauri-build
```

Outputs land in `tauri/src-tauri/target/release/bundle/`. Use this for iterating on Tauri-specific issues that the cargo-cache hides on rebuilds; for shipping, let CI do it.

### Tauri-specific footguns

- **Don't build manually then expect CI artifacts to match.** The CI matrix produces bundles signed differently (or unsigned) from a local build. Local artifacts are for debugging, not distribution.
- **`0.1.0-alpha.N` is valid semver for Tauri**, but macOS `CFBundleShortVersionString` strips the pre-release suffix in the About dialog. That's expected — Tauri handles it internally.
- **macOS bundles are signed + notarized only on release-workflow runs; PR-validation builds are unsigned by design.** "Release-workflow run" = a tag push or a `workflow_dispatch` whose selected ref is an existing `v*` tag. Branch-based manual dispatches fail before build work, so every accepted invocation passes a release tag to the reusable workflow. Two protections in series:
  - **Caller-side (load-bearing):** PR validation in `test.yml` and manual `tauri-build.yml` runs do NOT use `secrets: inherit` when calling the reusable workflow. The called workflow's `secrets.APPLE_*` references therefore resolve to empty unconditionally during PR/manual validation — the secret values are not in the calling job's context, so even a same-repo PR that rewrites the called workflow can't read them. `release.yml` does inherit (it needs the credentials to actually sign).
  - **Called-side (defense in depth):** the env block in `_tauri-shared.yml` gates each Apple secret on `matrix.os == 'macos-latest' && inputs.tagName != ''`. Belt-and-suspenders against future misconfiguration where some new caller might inherit secrets unintentionally.
  - The shared workflow uses `${{ github.token }}` instead of `${{ secrets.GITHUB_TOKEN }}` so it works in both modes — `github.token` is the per-job-run token, available in every workflow run without needing secrets-inherit.

  With the secrets configured on the repo, a release-workflow `.dmg` is signed with Developer ID Application and notarized — first launch on a clean Mac shows no Gatekeeper warning. Notarization adds ~5–15 minutes to the macOS leg per release (longer if Apple's notary service is backed up). PR builds always produce unsigned bundles (intentional — they're throwaway artifacts for "does the build still produce a `.dmg`?" verification, not for distribution). Operator setup checklist for the secrets is in [`../../docs/operator/macos-signing.md`](../../docs/operator/macos-signing.md). Windows code signing (Authenticode + EV cert) is not yet configured at all, and Linux/Windows desktop bundles are still CI-built-only; document that they are untested and likely buggy until real-machine smoke coverage lands.

- **Auto-update emits `latest.json` per release.** Three-step pipeline in `_tauri-shared.yml`. (1) Per matrix leg: tauri-action receives `TAURI_UPDATER_PRIVATE_KEY` + `TAURI_UPDATER_PRIVATE_KEY_PASSWORD` (gated on `inputs.tagName != ''` — same caller-side / called-side protection model as the Apple secrets) and signs the platform bundles; a follow-up `Upload updater payloads to release` step uploads the `.sig` files and macOS `Hecate.app.tar.gz` to the release (tauri-action only uploads the platform installers, not the updater payloads alongside them). (2) Post-matrix: the `publish-updater-manifest` job downloads every `.sig`, stitches them into `latest.json`, and uploads the manifest to the GitHub Release. tauri-action can't do this itself in matrix mode because each leg only sees its own signature. (3) Post-manifest: the `publish-updater-website` job drops `latest.json` into `website/public/releases/alpha/`, commits to master with a non-FF retry loop, then explicitly dispatches `website.yml` via `workflow_dispatch` (pushes made with `github.token` deliberately don't trigger downstream workflows on push events; `workflow_dispatch` IS allowed for `github.token`, which is the documented workaround). `website.yml` rebuilds Astro and deploys to Pages. The job's final step polls `https://hecate.sh/releases/alpha/latest.json` and only exits green once the new version is live (10-min cap). The dispatch runs unconditionally so a re-run can recover from a stuck Pages deploy. This second publish target exists because every release stays a GitHub pre-release until v1.0.0 — see `Pre-release policy` below — and GitHub's `/releases/latest/` shortcut refuses to resolve to pre-releases, so the bundled updater endpoint points at the website channel instead. Existing installs check `latest.json` on launch and surface a banner when a newer version is published. Maintainer-side keypair custody and rotation: [`../../docs/operator/desktop-updater-signing.md`](../../docs/operator/desktop-updater-signing.md). Two prerequisites — `bundle.createUpdaterArtifacts: "v1Compatible"` in `tauri.conf.json` (without it the bundler produces no updater artifacts to sign) and the two `TAURI_UPDATER_*` secrets. If either is missing, the stitch job fails loudly on `missing updater signature(s)` rather than shipping a half-broken manifest.
- **Pre-release policy.** Every release is a GitHub pre-release until v1.0.0; see [`../../docs/contributor/release.md#pre-release-policy`](../../docs/contributor/release.md#pre-release-policy). Consequence: do **not** run `gh release edit <tag> --prerelease=false --latest` on a regular release — `release.yml` and the `publish-updater-website` job route auto-update through `hecate.sh` independent of GitHub's "latest" semantics, so the flag has no effect on auto-update routing.
- **Historical auto-update channel switch.** Alpha.28 moved the bundled updater endpoint from `https://github.com/.../releases/latest/download/latest.json` to `https://hecate.sh/releases/alpha/latest.json`. Because Hecate had no real installed alpha cohort to migrate, alpha.28 is now a pre-release like the rest of the alpha line. Old alpha.21–27 installs are expected to reinstall manually from the current alpha; alpha.28+ installs update through `hecate.sh`.
- **`tauri/src-tauri/target/` is large** (~1–2 GB after a release build). Don't accidentally `git add` it — it's gitignored, but be specific with paths anyway.
- **Icons must be format-correct.** A `.png` renamed to `.ico` will pass macOS but fail Windows `RC.EXE`. Regenerate via `bunx @tauri-apps/cli icon source.png` if changing artwork.

## Watch CI

Push triggers `.github/workflows/release.yml` with these jobs:

1. `goreleaser` (~5–10 min, Docker buildx multi-arch dominates) — multi-arch binaries + Docker images on `ghcr.io/hecatehq/hecate` + GitHub Release entry.
2. `tauri / build` (`needs: goreleaser`, ~10–15 min, three platforms in parallel) — desktop bundles attached to the same release entry. Cold rust-cache adds ~5 min on first run; subsequent runs at the same dep set are warm.
3. `tauri / publish updater manifest` (`needs: build`) — stitches per-platform `.sig` files into `latest.json` and uploads to the GitHub Release. Seconds.
4. `tauri / publish updater manifest to website` (`needs: publish-updater-manifest`) — commits `latest.json` to `website/public/releases/alpha/` on master (retries on non-FF if master moved), then dispatches `website.yml` via `workflow_dispatch` (because `github.token` pushes don't auto-trigger downstream workflows on push events), then polls `https://hecate.sh/releases/alpha/latest.json` until the new version is live. Total time including the website rebuild + Pages redeploy is typically 2–5 min, capped at 10 min before the job fails.

Total wall-clock: ~20–35 min (the website publish + verification adds 2–5 min to the post-matrix tail).

Acceptance:

- Both workflow jobs are green.
- GitHub Releases page has the entry, is marked **Pre-release** for `-alpha.N`
  tags, and has non-empty notes from either a substantive tag annotation or
  GoReleaser's generated changelog. The Tauri matrix must attach bundles through
  `releaseId`; passing only `tagName` to tauri-action v1 rewrites the existing
  release body.
- Goreleaser-side artifacts attached: tarballs for each `goos/goarch`, source tarball, checksums. Each binary tarball contains `hecate`.
- Tauri-side artifacts attached: one `.dmg`, one `.deb`, one `.AppImage`, one `.msi`. If any is missing, the matrix leg silently skipped upload — open the run, find the leg, see what failed.
- `latest.json` is attached as a release asset (the auto-updater manifest, GitHub Release copy). Missing means the `publish-updater-manifest` job failed — most likely on its `missing updater signature(s)` check. Look there first; common causes are `bundle.createUpdaterArtifacts` being unset in `tauri.conf.json` (bundler produced no sigs) or the `TAURI_UPDATER_*` secrets having been removed from repo settings.
- `https://hecate.sh/releases/alpha/latest.json` serves the new version's manifest. This is the URL bundles actually read; the GitHub Release copy is a backup. If `publish-updater-website` succeeded, this is automatic — the job blocks until propagation completes. If `publish-updater-website` failed at "Verify manifest is live at hecate.sh", check the website workflow run for the master commit `publish updater manifest for vX.Y.Z`.
- Bundle sizes look right: `.dmg` ~20–40 MB, `.deb` ~15–25 MB, `.AppImage` ~80–120 MB (bundles its own libs), `.msi` ~15–25 MB. A 1 MB `.dmg` means the sidecar didn't embed — investigate before announcing.
- `docker pull ghcr.io/hecatehq/hecate:X.Y.Z` succeeds (no `v` prefix — see footgun below).
- `docker run --rm -p 8765:8765 ghcr.io/hecatehq/hecate:X.Y.Z` then `curl :8765/healthz` returns `version: "X.Y.Z"`.
- (Optional but recommended for `-alpha.N`) Download the `.dmg` and verify it launches: window opens, splash → gateway UI, auto-logged in (no token paste), `cmd+Q` leaves no orphan `gateway` process. ~10 min and catches >90% of desktop-side regressions.

## Footguns

- **`{{ .Version }}` strips the `v` prefix.** Docker tags are `0.1.0-alpha.1`, **not** `v0.1.0-alpha.1`. The git tag itself keeps the `v`. Same applies to tarball names. The `/healthz` `version` field also reports the bare semver.
- **`.env_file` in compose overrides Dockerfile `ENV`.** The compose stack pins `HECATE_DATA_DIR=/data` and `HECATE_SQLITE_PATH=/data/hecate.db` in the service `environment:` block — which wins over `env_file:` — so a developer's relative `.data` source-dev path can't be carried into the container and break `docker compose cp /data/...`. Any new Dockerfile `ENV` that conflicts with a source-dev default in `.env.example` needs the same treatment in `docker-compose.yml`, or this footgun recurs.
- **First-tag changelog is all-history.** Goreleaser builds the auto-changelog from git log between previous and current tags; if there's no previous tag, it includes every commit since the initial commit. Inspect the snapshot output before tagging.
- **Don't run snapshot from a clean checkout, then `git add -A`.** The snapshot writes ~50 MB of binaries into `./dist`; a sweeping `git add` will pick them up if `dist/` isn't gitignored.
- **`ui/dist/.gitkeep` must be tracked.** The `//go:embed all:ui/dist` directive in `embed.go` fails at compile time if `ui/dist` is completely absent from the tree. `.gitignore` keeps `ui/dist/*` but un-ignores `.gitkeep` via negation — the negation only works if `/dist/` (not `dist/`) is the rule anchoring the goreleaser output directory. If `go build` fails with `pattern all:ui/dist: no matching files found`, check that `ui/dist/.gitkeep` is tracked (`git ls-files ui/dist/`) and that `.gitignore` anchors the root dist rule with a leading `/`.
- **`Dockerfile.release` is what goreleaser uses, not `Dockerfile`.** `Dockerfile` is the source-build image used by `docker compose up --build`; `Dockerfile.release` copies the prebuilt binary into the published GHCR image. They are two build paths for the same runtime shape, so any runtime package, bundled External Agent CLI/adapter, `ENV` var, volume, or default must land in both.
- **Bundled External Agent versions are deliberate.** The Dockerfiles pin the top-level vendor CLI packages for Codex, Claude Code, and Grok Build. The Codex and Claude Code ACP adapters are Go module dependencies compiled into Hecate, so adapter updates land through `go.mod` / `go.sum` and must keep the registry `SupportedRange` compatible. Cursor Agent comes from Cursor's official installer; because that script has no version flag, the Dockerfiles pin the installer SHA-256 and fail the build when Cursor rotates it. Review the new script, confirm the hardcoded Cursor package it installs, then update both Dockerfiles together.
- **CI's `e2e-ollama` job runs under `-tags 'e2e ollama'`** — `just verify` only covers `-tags 'e2e docker'` locally, so an ollama-only regression sails through the local gate. The `v0.1.0-alpha.7` cut hit this with the env-PRECONFIGURED gate: `gateway_test.go` was patched but `ollama_test.go` was missed. Before tagging, also run `OLLAMA_BASE_URL=http://127.0.0.1:11434 OLLAMA_MODEL=smollm2:135m go test -tags 'e2e ollama' -count=1 ./e2e/...` if any e2e helper has changed.
- **Lychee link-check runs only on master pushes**, not on tag pushes — a broken markdown link in `AGENTS.md` / `docs-ai/**` won't block a release, but it'll blink red on the next master push. Run `just check-links` (or grep for the suspected dangling target) before tagging when the change set is doc-heavy.

## Recovery

If CI fails:

```bash
git push --delete origin vX.Y.Z
git tag -d vX.Y.Z
# fix root cause, re-tag, re-push
```

Tag deletion on GitHub also clears the dangling Release entry (if one was created before the failure step). Goreleaser's release pipeline is mostly idempotent — a clean retag at a fixed commit produces the same artifacts.
