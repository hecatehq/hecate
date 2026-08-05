# Release

Cutting a public release tag. Companion to [`../../docs/contributor/release.md`](../../docs/contributor/release.md), which is the operator-facing version (release notes format, verification gate, image build). This doc is the agent-side procedure with the footguns earlier release cycles earned the hard way.

## When this fires

- Operator says "cut a release" / "tag vX.Y.Z" / "ship" / similar.
- Master is in a stable state worth tagging.
- The change set since the previous tag is meaningful (a release with one typo fix is not worth the operational ceremony).

Default to producing a written plan first ([`../skills/architect/SKILL.md`](../skills/architect/SKILL.md)): version pick, gate posture, recovery path, what's in/out of the release notes.

## Pre-flight

Before running the release script, verify:

1. **The clean checkout is on `master` and exactly matches `origin/master`.** The release helper fetches origin and fails closed before it can stamp or tag a feature-only or stale commit.
2. **`just verify` exits 0** — full gate: `docs-env-check`, race suite, docker-smoke, UI unit + e2e. See [`../core/verification.md`](../core/verification.md). Mandatory; calling out a skip in release notes is acceptable only when the risk is named.
3. **`goreleaser` is installed.** `which goreleaser`. Install via `go install github.com/goreleaser/goreleaser/v2@latest` if missing.
4. **Docker is reachable unless `--skip-snapshot` is intentional.** `just release` runs this check before the expensive verify gate because the Goreleaser snapshot builds local Docker images.
5. **Curated notes are reviewed and committed.** Copy `docs/releases/template.md`
   to `docs/releases/vX.Y.Z.md`, replace the placeholder title exactly, keep
   `## Highlights` to one through six bullets, and name any security or
   breaking/risky impact. The helper requires `--notes` and refuses an
   untracked, oversized, or malformed file.

## Cut the release

Use the release recipe. It runs the release script in preflight-only mode,
then `just verify`, then the full release script. The script checks a clean,
current default branch, refreshes origin and tags, checks tag uniqueness,
checks goreleaser on PATH and Docker availability for the snapshot, fires a
snapshot dry-run, then prompts before tagging:

```bash
just release vX.Y.Z --notes docs/releases/vX.Y.Z.md
```

To skip the snapshot dry-run (e.g. already ran it manually):

```bash
just release v0.5.1 --notes docs/releases/v0.5.1.md --skip-snapshot
```

The helper validates the committed Markdown before the expensive gate and
again immediately before tagging. It creates the annotated tag with
`--cleanup=verbatim -F`, preserving Markdown headings, then CI passes those
exact bytes to GoReleaser as the public GitHub Release body only after comparing
them byte-for-byte with `docs/releases/vX.Y.Z.md` in the tagged commit.
Lightweight tags, version-only annotations, missing/mismatched audit files, and
generated changelog fallbacks fail closed.

## Tauri desktop app

The native desktop app (`tauri/`) **is built and uploaded by CI** as part of the release pipeline — no manual `just tauri-build` step is required when cutting a tag. Bundle architecture and the per-platform build details live in [`../skills/tauri/SKILL.md`](../skills/tauri/SKILL.md); the operator-facing distribution + roadmap view is at [`../../docs/operator/desktop-app.md`](../../docs/operator/desktop-app.md); this section is the release-time view.

### What CI does

`.github/workflows/release.yml` runs two jobs on a `v*` tag push:

1. `goreleaser` — Linux/macOS binary tarballs containing `hecate`, multi-arch Docker images on GHCR, GitHub Release entry.
2. `tauri` (`needs: goreleaser`) — three-platform matrix (macOS arm64, Linux x86_64, Windows x86_64) calls the reusable `_tauri-shared.yml` workflow with `tagName: ${{ github.ref_name }}`. Each leg builds the hecate sidecar, the Tauri bundle, and uploads platform-native artifacts (`.dmg` / `.deb` + `.AppImage` / `.msi`) to the existing release.

End state of a successful tag: the GitHub Release page has goreleaser tarballs + Docker images + four desktop bundles, all attached.

### Version stamping

`just release` / `bun scripts/release.ts` handles the stamp automatically: after confirmation it calls `scripts/stamp-version.ts` with `TAURI_VERSION=<semver>`, commits the changed desktop and mobile version files (`Cargo.toml`, `package.json`, the base and platform Tauri configs, plus generated Apple version metadata), then creates the annotated tag on that commit. CI re-runs the stamp from the tag name as a belt-and-suspenders measure (`stamp-version.ts` is idempotent).

The stamp commit remains on the default branch. The script pushes both that
branch and the annotated tag, keeping visible Tauri version metadata aligned
with the latest release. Release CI then uploads one bounded delivery proposal
containing refreshed release references. `v0.5.0` also carries the one-time
manifest bridge for alpha.28+ desktop installs. After a maintainer applies it
on current `master`, opens the PR, and the proposal is reviewed, checked, and merged, run
`git pull --ff-only origin master` to pick up the post-release commit.

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
- **Stable release versions use the plain `X.Y.Z` form.** The release helper and
  workflow reject prerelease suffixes so GitHub's native latest-release route
  remains the one updater channel.
- **macOS bundles are signed + notarized only on release-workflow runs; PR-validation builds are unsigned by design.** "Release-workflow run" = a tag push or a `workflow_dispatch` whose selected ref is an existing `v*` tag. Branch-based manual dispatches fail before build work, so every accepted invocation passes a release tag to the reusable workflow. Two protections in series:
  - **Caller-side (load-bearing):** PR validation in `test.yml` and manual `tauri-build.yml` runs do NOT use `secrets: inherit` when calling the reusable workflow. The called workflow's `secrets.APPLE_*` references therefore resolve to empty unconditionally during PR/manual validation — the secret values are not in the calling job's context, so even a same-repo PR that rewrites the called workflow can't read them. `release.yml` does inherit (it needs the credentials to actually sign).
  - **Called-side (defense in depth):** the env block in `_tauri-shared.yml` gates each Apple secret on `matrix.os == 'macos-latest' && inputs.tagName != ''`. Belt-and-suspenders against future misconfiguration where some new caller might inherit secrets unintentionally.
  - The shared workflow uses `${{ github.token }}` instead of `${{ secrets.GITHUB_TOKEN }}` so it works in both modes — `github.token` is the per-job-run token, available in every workflow run without needing secrets-inherit.

  With the secrets configured on the repo, a release-workflow `.dmg` is signed with Developer ID Application and notarized — first launch on a clean Mac shows no Gatekeeper warning. Notarization adds ~5–15 minutes to the macOS leg per release (longer if Apple's notary service is backed up). PR builds always produce unsigned bundles (intentional — they're throwaway artifacts for "does the build still produce a `.dmg`?" verification, not for distribution). Operator setup checklist for the secrets is in [`../../docs/operator/macos-signing.md`](../../docs/operator/macos-signing.md). Windows code signing (Authenticode + EV cert) is not yet configured at all, and Linux/Windows desktop bundles are still CI-built-only; document that they are untested and likely buggy until real-machine smoke coverage lands.

- **Auto-update emits `latest.json` per release.** The packaging and protected-branch delivery pipeline keeps platform signing and manifest publication separate:
  1. Each matrix leg gives `tauri-action` the gated `TAURI_UPDATER_PRIVATE_KEY` + `TAURI_UPDATER_PRIVATE_KEY_PASSWORD`, signs its platform payload, and uploads its `.sig` file plus macOS `Hecate.app.tar.gz`.
  2. `publish-updater-manifest` downloads those signatures, stitches a `latest.json` manifest that references the signed platform payloads, fetches bounded GitHub Release body/date as advisory `notes` / `pub_date`, and uploads it to the GitHub Release without logging the full notes body.
  3. `release-delivery.yml` downloads that canonical release asset, validates its version, signatures, and referenced assets, refreshes the release-linked docs, and uploads an allowlisted patch plus provenance as a review artifact. `v0.5.0` also updates the alpha.28+ compatibility bridge once. The repository intentionally prevents the built-in Actions token from creating pull requests, so a maintainer applies the patch on current `master` and opens the delivery PR. No App/PAT secret or branch-rules bypass is required.

  Stable releases use GitHub's `/releases/latest/download/latest.json` directly. Existing desktop installs check the manifest on launch; a newer release illuminates the compact status-bar **Updates** control, and an explicit check opens its details dialog. Manifest notes are advisory metadata—the downloaded payload is what Hecate verifies before installation. The pipeline requires `bundle.createUpdaterArtifacts: "v1Compatible"` and both `TAURI_UPDATER_*` secrets; otherwise the stitch job fails at `missing updater signature(s)`. Maintainer-side keypair custody and rotation: [`../../docs/operator/desktop-updater-signing.md`](../../docs/operator/desktop-updater-signing.md).

- **Stable update policy.** Every release is a normal GitHub Release; see [`../../docs/contributor/release.md#stable-update-channel`](../../docs/contributor/release.md#stable-update-channel). Do not manually edit its prerelease/latest flags.
- **Alpha migration bridge.** Alpha.28+ moved to `https://hecate.sh/releases/alpha/latest.json`; the `v0.5.0` delivery proposal updates that one file to the stable manifest so those installs can migrate. Later releases intentionally leave it at `v0.5.0`; current desktop builds use GitHub directly.
- **`tauri/src-tauri/target/` is large** (~1–2 GB after a release build). Don't accidentally `git add` it — it's gitignored, but be specific with paths anyway.
- **Icons must be format-correct.** A `.png` renamed to `.ico` will pass macOS but fail Windows `RC.EXE`. Regenerate via `bunx @tauri-apps/cli icon source.png` if changing artwork.

## Watch CI

Push triggers `.github/workflows/release.yml` with these jobs:

1. `goreleaser` (~5–10 min, Docker buildx multi-arch dominates) — multi-arch binaries + Docker images on `ghcr.io/hecatehq/hecate` + GitHub Release entry.
2. `tauri / build` (`needs: goreleaser`, ~10–15 min, three platforms in parallel) — desktop bundles attached to the same release entry. Cold rust-cache adds ~5 min on first run; subsequent runs at the same dep set are warm.
3. `tauri / publish updater manifest` (`needs: build`) — stitches per-platform `.sig` files into `latest.json` and uploads to the GitHub Release. Seconds.
4. `Prepare release delivery proposal` (`needs: goreleaser, tauri`) — verifies the release body and canonical manifest, refreshes release docs, and uploads `release-delivery-<tag>` with an allowlisted patch and provenance. `v0.5.0` also carries the alpha.28+ migration bridge. A maintainer applies it on current `master` and opens the PR, which triggers normal Required checks and Links workflows.

Packaging plus proposal preparation typically takes ~20–30 minutes. The
GitHub Release asset becomes the updater source as soon as the release run is green.

Acceptance:

- The release workflow is green and reports one release-delivery proposal
  artifact (or that the exact delivery is already present on `master`).
- The release-delivery PR has an approving review of its latest push, Required
  checks and Links are green, and it is merged normally.
- GitHub Releases page has the latest stable entry and the reviewed curated
  Markdown from the tag. The Tauri matrix must attach bundles through
  `releaseId`; passing only `tagName` to tauri-action v1 rewrites the existing
  release body.
- Goreleaser-side artifacts attached: tarballs for each `goos/goarch`, source tarball, checksums. Each binary tarball contains `hecate`.
- Tauri-side artifacts attached: one `.dmg`, one `.deb`, one `.AppImage`, one `.msi`. If any is missing, the matrix leg silently skipped upload — open the run, find the leg, see what failed.
- `latest.json` is attached as a release asset (the auto-updater manifest, GitHub Release copy). Missing means the `publish-updater-manifest` job failed — most likely on its `missing updater signature(s)` check. Look there first; common causes are `bundle.createUpdaterArtifacts` being unset in `tauri.conf.json` (bundler produced no sigs) or the `TAURI_UPDATER_*` secrets having been removed from repo settings.
- `v0.5.0`'s `https://hecate.sh/releases/alpha/latest.json` bridge byte-matches the published manifest and the Website workflow verifies it after the delivery PR merges. Later releases leave it unchanged; the GitHub Release asset is the canonical updater source.
- Bundle sizes look right: `.dmg` ~20–40 MB, `.deb` ~15–25 MB, `.AppImage` ~80–120 MB (bundles its own libs), `.msi` ~15–25 MB. A 1 MB `.dmg` means the sidecar didn't embed — investigate before announcing.
- `docker pull ghcr.io/hecatehq/hecate:X.Y.Z` succeeds (no `v` prefix — see footgun below).
- `docker run --rm -p 8765:8765 ghcr.io/hecatehq/hecate:X.Y.Z` then `curl :8765/healthz` returns `version: "X.Y.Z"`.
- Download the `.dmg` and verify it launches: window opens, splash → gateway
  UI, auto-logged in (no token paste), `cmd+Q` leaves no orphan `gateway`
  process. ~10 min and catches most desktop-side regressions.

## Footguns

- **`{{ .Version }}` strips the `v` prefix.** Docker tags are `0.5.0`, **not**
  `v0.5.0`. The git tag itself keeps the `v`. Same applies to tarball names.
  The `/healthz` `version` field also reports the bare semver.
- **`.env_file` in compose overrides Dockerfile `ENV`.** The compose stack pins `HECATE_DATA_DIR=/data` and `HECATE_SQLITE_PATH=/data/hecate.db` in the service `environment:` block — which wins over `env_file:` — so a developer's relative `.data` source-dev path can't be carried into the container and break `docker compose cp /data/...`. Any new Dockerfile `ENV` that conflicts with a source-dev default in `.env.example` needs the same treatment in `docker-compose.yml`, or this footgun recurs.
- **Public notes never come from the generated changelog.** The versioned,
  reviewed Markdown file is mandatory because raw commit dumps are noisy in
  both GitHub and the desktop updater.
- **Don't run snapshot from a clean checkout, then `git add -A`.** The snapshot writes ~50 MB of binaries into `./dist`; a sweeping `git add` will pick them up if `dist/` isn't gitignored.
- **`ui/dist/.gitkeep` must be tracked.** The `//go:embed all:ui/dist` directive in `embed.go` fails at compile time if `ui/dist` is completely absent from the tree. `.gitignore` keeps `ui/dist/*` but un-ignores `.gitkeep` via negation — the negation only works if `/dist/` (not `dist/`) is the rule anchoring the goreleaser output directory. If `go build` fails with `pattern all:ui/dist: no matching files found`, check that `ui/dist/.gitkeep` is tracked (`git ls-files ui/dist/`) and that `.gitignore` anchors the root dist rule with a leading `/`.
- **`Dockerfile.release` is what goreleaser uses, not `Dockerfile`.** `Dockerfile` is the source-build image used by `docker compose up --build`; `Dockerfile.release` copies the prebuilt binary into the published GHCR image. They are two build paths for the same runtime shape, so any runtime package, bundled External Agent CLI/adapter, `ENV` var, volume, or default must land in both.
- **Bundled External Agent versions are deliberate.** The Dockerfiles pin the top-level vendor CLI packages for Codex, Claude Code, and Grok Build. The Codex and Claude Code ACP adapters are Go module dependencies compiled into Hecate, so adapter updates land through `go.mod` / `go.sum` and must keep the registry `SupportedRange` compatible. Cursor Agent uses official versioned Linux archives with separate x64 and arm64 SHA-256 pins; the mutable installer is parsed only as bounded release metadata and is never executed. Use `just cursor-agent-update`, review its JSON evidence and two-file diff, and require normal CI. An in-place artifact mutation or older advertised release date is a security failure, not a checksum-refresh task. Distinct same-date suffixes have no reliable ordering and fail closed; use `--allow-same-date-transition` only after explicit review. Scheduled automation never overrides this guard or replaces an open proposal with another advertised version.
- **CI's `e2e-ollama` job runs under `-tags 'e2e ollama'`** — `just verify` only covers `-tags 'e2e docker'` locally, so an ollama-only regression sails through the local gate. The `v0.1.0-alpha.7` cut hit this with the env-PRECONFIGURED gate: `gateway_test.go` was patched but `ollama_test.go` was missed. Before tagging, also run `OLLAMA_BASE_URL=http://127.0.0.1:11434 OLLAMA_MODEL=smollm2:135m go test -tags 'e2e ollama' -count=1 ./e2e/...` if any e2e helper has changed.
- **Lychee link-check runs only on master pushes**, not on tag pushes — a broken markdown link in `AGENTS.md` / `docs-ai/**` won't block a release, but it'll blink red on the next master push. Run `just check-links` (or grep for the suspected dangling target) before tagging when the change set is doc-heavy.

## Recovery

If artifact packaging or signing fails before the canonical `latest.json` is
valid, clean up and retag:

```bash
git push --delete origin vX.Y.Z
git tag -d vX.Y.Z
# fix root cause, re-tag, re-push
```

Tag deletion on GitHub also clears the dangling Release entry (if one was created before the failure step). Goreleaser's release pipeline is mostly idempotent — a clean retag at a fixed commit produces the same artifacts.

Do **not** delete or move a valid published tag merely because protected-branch
delivery failed. If the GitHub Release, platform assets, signatures, and
`latest.json` are complete, recover from current `master`:

```bash
gh workflow run release-delivery.yml \
  --repo hecatehq/hecate \
  --ref master \
  -f tag=vX.Y.Z
```

Download the `release-delivery-vX.Y.Z` artifact from that run, verify
`provenance.json`, apply `release-delivery.patch` on current `master`, and open
the delivery PR. This recovery reuses the canonical release asset and does not
rebuild, delete, or retag the release. The exact checksum, apply, and PR handoff
commands live in
[`../../docs/contributor/release.md`](../../docs/contributor/release.md).
