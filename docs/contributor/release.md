# Release

Hecate is pre-1.0. Releases should be boring, repeatable, and explicit about
what is alpha-grade versus production-shaped.

## Versioning

- Use semantic-version tags: `v0.x.y` until the public API and storage schema
  reach a v1 stability bar.
- Use patch releases for bug fixes, docs corrections, and small UI polish.
- Use minor releases for API additions, storage changes, provider/runtime
  behavior changes, or operator workflow changes.
- For pre-release tags use the dotted-suffix semver form: `v0.1.0-alpha.1`,
  `v0.1.0-alpha.2`, `v0.1.0-rc.1`. Goreleaser and tauri-action handle them
  the same as stable tags; consumers can opt out via semver tooling that
  recognizes pre-release tags.
- Keep shipping `v0.1.0-alpha.N` while the
  [alpha-to-beta roadmap](beta-roadmap.md) is open. The first beta tag is a
  quality gate (`v0.1.0-beta.1`), not the next release by default.
- Do not publish a release from a dirty worktree.

## Pre-release policy

Every release stays marked as a GitHub **pre-release** until `v1.0.0`
stable. The semver suffix (`-alpha.N`, `-beta.N`, `-rc.N`) already
signals pre-stability to humans; the GitHub pre-release flag makes
that signal machine-readable for package managers, mirrors, and any
downstream automation that respects it.

Practical consequences:

- `https://github.com/hecatehq/hecate/releases/latest/` will resolve
  to **nothing** while every release is a pre-release — GitHub's
  "latest" semantics explicitly skip pre-releases.
- In-app auto-update therefore cannot rely on
  `/releases/latest/download/latest.json`. The desktop app instead
  reads `https://hecate.sh/releases/alpha/latest.json`, which CI
  publishes after every successful release via the
  `publish-updater-website` job in `_tauri-shared.yml`. See
  [`desktop-updater-signing.md`](../operator/desktop-updater-signing.md) for the
  pipeline and troubleshooting.
- Once `v1.0.0` ships, that release lands without the pre-release
  flag. The decision to drop the flag will be deliberate — the
  release before it (`v1.0.0-rc.N` or equivalent) is the last
  gate. The dedicated `hecate.sh/releases/alpha/latest.json`
  channel can then be retired in favor of GitHub's native endpoint,
  or kept as the canonical multi-channel surface; that's a v1
  decision.

Historical context for the channel switch. `v0.1.0-alpha.27` was
the last release built with the GitHub-native updater endpoint
(`/releases/latest/download/latest.json`) baked into the bundle.
`v0.1.0-alpha.28` introduced the new
`hecate.sh/releases/alpha/latest.json` endpoint and was briefly used
to validate the bridge path. Because Hecate had no real installed
alpha cohort to migrate, alpha.28 was flipped back to pre-release
after validation and old alpha.21–27 installs are expected to
reinstall manually from the current alpha. Alpha.29+ ship as
pre-releases by default; auto-update routing no longer depends on
GitHub's `latest` semantics once a bundle contains the `hecate.sh`
channel endpoint.

## What a release produces

Every `v*` tag fires `.github/workflows/release.yml`, which expands into
five Actions jobs:

1. **`goreleaser`** (~5–10 min) — multi-arch Go binary tarballs for
   `linux+darwin × amd64+arm64`; each tarball includes `hecate`. It also
   publishes multi-arch Docker images on
   `ghcr.io/hecatehq/hecate`, source tarball, checksums, GitHub Release
   entry.
2. **`tauri / build`** (matrix, ~10–15 min, runs after goreleaser) —
   three legs building native desktop bundles and uploading them to the same
   Release entry: `.dmg` (macOS arm64), `.deb` + `.AppImage` (Linux x86_64),
   `.msi` (Windows x86_64). This is packaging validation, not platform
   confidence: maintainers currently launch-test the macOS Apple Silicon
   desktop path only. Linux and Windows desktop bundles are CI-built but not
   manually exercised on real machines yet.
3. **`tauri / publish updater manifest`** — stitches signed updater payloads
   into `latest.json` and uploads the GitHub Release copy.
4. **`tauri / publish updater manifest to website`** — commits the same
   manifest to `website/public/releases/alpha/`, dispatches the website
   deploy, and blocks until `https://hecate.sh/releases/alpha/latest.json`
   serves the new version.
5. **`update-release-docs`** — reads the actual uploaded Release assets and
   refreshes the README Desktop app table plus pinned Docker/tarball examples.
   This runs only after the Tauri matrix succeeds, so it never links to bundles
   that were not published.

Acceptance after the run:

- All release jobs green.
- Release entry marked **Pre-release** for `-alpha.N` tags.
- Goreleaser-side artifacts attached: tarballs for each goos/goarch + checksums.
  Each tarball contains `hecate`.
- Tauri-side bundles attached: 1 `.dmg`, 1 `.deb`, 1 `.AppImage`, 1 `.msi`.
  If any is missing, the matrix leg silently skipped upload — open the run
  to see what failed.
- Release notes and README copy keep the desktop-platform caveat honest:
  macOS Apple Silicon is launch-tested; Linux and Windows desktop bundles are
  experimental until real-machine smoke coverage exists.
- README Desktop app table and pinned install examples point at the release
  tag. The workflow commits this docs-only refresh to `master` with `[skip ci]`.
- `https://hecate.sh/releases/alpha/latest.json` serves the same version as
  the release tag. This is the updater endpoint bundled into alpha.28+ desktop
  apps; the GitHub Release `latest.json` asset is a backup/source artifact.
- `docker pull ghcr.io/hecatehq/hecate:X.Y.Z` succeeds (no `v` prefix —
  goreleaser uses bare semver as the docker tag). The image contains
  `/usr/local/bin/hecate`; the entrypoint is `/usr/local/bin/hecate`.
- `docker run --rm -p 127.0.0.1:8765:8765 ghcr.io/hecatehq/hecate:X.Y.Z` then
  `curl :8765/healthz` returns `version: "X.Y.Z"`.

## Alpha gate

Run the full local gate before cutting a public alpha tag:

```bash
just verify
```

The target runs the non-destructive launch checks in order:

1. docs/env drift check
2. Go unit tests
3. `go vet`
4. Go race tests
5. Docker smoke test
6. UI unit tests
7. UI e2e tests
8. production binary build with embedded UI

If a check is intentionally skipped, call it out in the release notes with the
reason and the risk. Docker smoke and UI e2e are allowed to be slow; they are
not optional for a public alpha build.

The local gate does **not** exercise the Tauri desktop bundle matrix. PR
validation covers that inside `test.yml` for changes that touch the desktop
pipeline, after the cheaper CI checks pass or skip. Post-tag, the release
matrix is the next opportunity to catch regressions.

## Cut the release

The canonical entry point is the `just release` recipe, which first runs a
fast release preflight, then `just verify`, then delegates to
`scripts/release.ts`:

```bash
just release vX.Y.Z
```

It performs, in order: clean-worktree check, tag-uniqueness check,
goreleaser-on-PATH check, Docker-daemon check when the snapshot is enabled,
the full project verification gate, goreleaser snapshot dry-run, interactive
confirmation prompt, Tauri version stamp commit (Cargo.toml, package.json,
tauri.conf.json), annotated tag, and one push containing both the stamped branch
and the tag.

Pass `--skip-snapshot` to skip the dry-run when you've already validated
locally:

```bash
just release vX.Y.Z --skip-snapshot
```

Pass `--yes` only for non-interactive release automation after the preflight
and verification gate have passed:

```bash
bun scripts/release.ts vX.Y.Z --skip-snapshot --yes
```

The script's annotated tag message is just the version string. For
substantive release notes, tag manually instead so the message becomes
the canonical release notes (what `git show vX.Y.Z` and the GitHub
Releases page surface):

```bash
TAURI_VERSION=X.Y.Z bun scripts/stamp-version.ts
git add tauri/src-tauri/Cargo.toml tauri/src-tauri/Cargo.lock \
        tauri/src-tauri/tauri.conf.json tauri/package.json
git commit -m "chore(tauri): stamp version X.Y.Z"
git tag -a vX.Y.Z -F /tmp/release-notes.txt    # message from a file
git push origin HEAD:master vX.Y.Z
```

The version stamp commit stays on `master`. This keeps the visible Tauri
metadata (`tauri/package.json`, `Cargo.toml`, `tauri.conf.json`) aligned with
the latest release instead of hiding the release version on the tag only.

## Snapshot dry-run

Reproduces what CI's goreleaser job does, locally, without publishing:

```bash
goreleaser release --snapshot --clean
```

Builds Go binaries for `linux+darwin × amd64+arm64` and per-arch Docker
images into `./dist`, skips publishing to GHCR, skips the GitHub release.
~2–3 minutes; surfaces almost every config issue you'd otherwise hit on
the real tag push. **Does not exercise the Tauri matrix** — that's
GitHub-Actions-only.

`bun scripts/release.ts` runs this for you unless you pass
`--skip-snapshot`.

**Inspect the auto-generated changelog.** The first tag in the repo lists
every commit since the dawn of git history; subsequent tags list only commits
since the previous tag. If the changelog is unusable, tune
`.goreleaser.yaml`'s `changelog.filters` or use `--release-notes <file>` to
override before tagging.

Pre-flight checks before the snapshot run (the script enforces these):

- `git status` is clean. Goreleaser refuses to release from a dirty worktree.
- `dist/` is gitignored at repo root. The snapshot writes binaries and
  tarballs into `./dist`; if the directory is tracked, those artifacts can
  leak into a follow-up commit and break the next release on `--clean`. The
  `ui/dist/` entry in `.gitignore` does **not** cover repo-root `dist/`.
- `goreleaser` itself is on PATH (`go install github.com/goreleaser/goreleaser/v2@latest`).
- Docker is reachable when the snapshot is enabled. The snapshot builds local
  Docker images, so `just release` fails early if Docker Desktop is stopped or
  the current Docker context points at a missing socket. If `just verify` has
  already passed and only the local snapshot is blocked, rerun with
  `--skip-snapshot`.
- Non-interactive shells can pass `--yes` after reviewing the release target.
  This only answers confirmation prompts; it does not skip preflight,
  stamping, tag creation, or the branch+tag push.

## Post-release docs refresh

`scripts/release.ts` automatically stamps Tauri version files
(`Cargo.toml`, `package.json`, `tauri.conf.json`). After the Release bundles
are uploaded, `.github/workflows/release.yml` automatically refreshes release
docs via `scripts/update-release-links.ts`.

The post-release updater covers:

- [`README.md`](../README.md) — Desktop app download table and pinned Docker
  image example.
- [`docs/operator/deployment.md`](../operator/deployment.md) — image-pinning example, tarball URLs,
  and the "Available tarballs for `vX.Y.Z`" list.
- [`docs/operator/desktop-app.md`](../operator/desktop-app.md) — current-state release heading.

If new docs add copy-pasted release tags, extend `scripts/update-release-links.ts`
in the same change so future releases do not drift.

## Recovery

If the CI run fails after pushing the tag:

```bash
git push --delete origin vX.Y.Z
git tag -d vX.Y.Z
# fix root cause, retag, retry
```

Tag deletion on GitHub also clears the dangling Release entry (if one was
created before the failure step). The Tauri version stamp commit remains on
`master`; that is fine when retrying the same version because the release script
will find the Tauri files already stamped and skip creating a duplicate stamp
commit. If the version is abandoned entirely, revert or supersede the stamp with
the next release version before tagging again. Goreleaser's release pipeline is
mostly idempotent — a clean retag at a fixed commit produces the same artifacts.

For Tauri-side failures, the `.dmg` / `.deb` / `.AppImage` / `.msi` may be
partially uploaded. `tauri-action` uploads with `--clobber`, so a retag
re-uploads cleanly without manual cleanup.

If the macOS leg fails during Apple notarization with HTTP 403 and wording like
"a required agreement is missing or has expired", the repository state is not
the root cause. An Apple Developer account holder must accept the current
agreements in Apple Developer/App Store Connect, then the same version can be
retagged. When GitHub has already created a pre-release entry, clean it up with:

```bash
gh release delete vX.Y.Z --cleanup-tag --yes
git tag -d vX.Y.Z 2>/dev/null || true
```

Leave the version stamp commit on `master` when retrying the same version; the
release script will detect the files are already stamped.

## Image build

The published image is built by goreleaser in CI using `Dockerfile.release`.
The release Dockerfile copies the prebuilt binary into the same runtime shape as
the source-build `Dockerfile`: embedded UI, git/ssh, supported External Agent
CLIs/ACP adapters, `/data`, and `/workspace`. For local validation:

```bash
docker compose build hecate
just test-docker-smoke
```

`docker compose` uses the source-build `Dockerfile`, not `Dockerfile.release`.
Any new runtime package, `ENV` var, volume, or default needs to land in both
files; otherwise local dev and the published image diverge silently.

The bundled External Agent packages are pinned with Docker build args in both
Dockerfiles. When bumping Codex, Claude Code, Cursor Agent, or Grok Build
support, update the package version args in both files. Cursor's official
installer does not expose a version flag, so the Dockerfiles verify the
installer script against `CURSOR_INSTALL_SHA256` before running it; refresh the
checksum only after reviewing the new script and confirming the hardcoded
Cursor Agent package it installs.

For published images, pin by tag in deployment examples and release notes.
Avoid recommending `latest` for anything beyond quick experiments.

## Release Notes

Each release note should include:

- **Highlights** — the most important operator-visible changes.
- **Breaking or risky changes** — config, storage, API, auth, provider, or UI
  behavior changes that can surprise an operator.
- **Migration notes** — storage/schema considerations and any manual steps.
- **Verification** — the exact gate that passed, normally `just verify`.
- **Known limitations** — link to [`known-limitations.md`](../operator/known-limitations.md)
  and call out any release-specific caveats.

## Alpha Limitations

The public alpha is credible for early technical users, but not a production
SLA. Keep these expectations visible:

- APIs and persisted schemas can still change before v1.
- The gateway/provider path is more mature than the task runtime.
- The sandbox is a per-call subprocess with env sanitisation, output cap, wall-clock timeout, and an auto-detected `bwrap` / `sandbox-exec` wrapper where available. It is not hardened OS isolation or container-level isolation.
- Multi-node deployments are not the primary tested path yet.
- Provider lifecycle covers preset and OpenAI-compatible custom-endpoint adds, plus
  persisted settings edits; broader provider workflows are still evolving.
