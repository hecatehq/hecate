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
- Do not publish a release from a dirty worktree.

## What a release produces

Every `v*` tag fires `.github/workflows/release.yml`, which runs three jobs:

1. **`goreleaser`** (~5–10 min) — multi-arch Go binary tarballs for
   `linux+darwin × amd64+arm64`; each tarball includes `hecate` and
   `hecate-acp`. It also publishes multi-arch Docker images on
   `ghcr.io/hecatehq/hecate`, source tarball, checksums, GitHub Release
   entry.
2. **`tauri`** (matrix, ~10–15 min, runs after goreleaser) — three legs
   building native desktop bundles and uploading them to the same Release
   entry: `.dmg` (macOS arm64), `.deb` + `.AppImage` (Linux x86_64), `.msi`
   (Windows x86_64). Wall-clock total ~15–25 min.
3. **`update-release-docs`** — reads the actual uploaded Release assets and
   refreshes the README Desktop app table plus pinned Docker/tarball examples.
   This runs only after the Tauri matrix succeeds, so it never links to bundles
   that were not published.

Acceptance after the run:

- Both jobs green.
- Release entry marked **Pre-release** for `-alpha.N` tags.
- Goreleaser-side artifacts attached: tarballs for each goos/goarch + checksums.
  Each tarball contains both `hecate` and `hecate-acp`.
- Tauri-side bundles attached: 1 `.dmg`, 1 `.deb`, 1 `.AppImage`, 1 `.msi`.
  If any is missing, the matrix leg silently skipped upload — open the run
  to see what failed.
- README Desktop app table and pinned install examples point at the release
  tag. The workflow commits this docs-only refresh to `master` with `[skip ci]`.
- `docker pull ghcr.io/hecatehq/hecate:X.Y.Z` succeeds (no `v` prefix —
  goreleaser uses bare semver as the docker tag). The image contains both
  `/usr/local/bin/hecate` and `/usr/local/bin/hecate-acp`; the entrypoint is
  `/usr/local/bin/hecate`.
- `docker run --rm -p 8765:8765 ghcr.io/hecatehq/hecate:X.Y.Z` then
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
5. ACP bridge smoke test
6. Docker smoke test
7. UI unit tests
8. UI e2e tests
9. production binary build with embedded UI

If a check is intentionally skipped, call it out in the release notes with the
reason and the risk. Docker smoke and UI e2e are allowed to be slow; they are
not optional for a public alpha build.

The gate does **not** exercise the Tauri matrix. PR validation
(`tauri-build.yml`) covers that on every PR touching the desktop pipeline;
post-tag, the release matrix is the next opportunity to catch regressions.

## Cut the release

The canonical entry point is the `just release` recipe, which runs
`just verify` first and then delegates to `scripts/release.ts`:

```bash
just release vX.Y.Z
```

It performs, in order: the full project verification gate,
clean-worktree check, tag-uniqueness check, goreleaser-on-PATH check,
goreleaser snapshot dry-run, interactive confirmation prompt, Tauri
version stamp commit (Cargo.toml, package.json, tauri.conf.json),
annotated tag, push.

Pass `--skip-snapshot` to skip the dry-run when you've already validated
locally:

```bash
just release vX.Y.Z --skip-snapshot
```

The script's annotated tag message is just the version string. For
substantive release notes, tag manually instead so the message becomes
the canonical release notes (what `git show vX.Y.Z` and the GitHub
Releases page surface):

```bash
bun scripts/stamp-version.ts                   # stamps Tauri version files
git add tauri/src-tauri/Cargo.toml tauri/src-tauri/Cargo.lock \
        tauri/src-tauri/tauri.conf.json tauri/package.json
git commit -m "chore(tauri): stamp version X.Y.Z"
git push origin master
git tag -a vX.Y.Z -F /tmp/release-notes.txt    # message from a file
git push origin vX.Y.Z
```

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

## Post-release docs refresh

`scripts/release.ts` automatically stamps Tauri version files
(`Cargo.toml`, `package.json`, `tauri.conf.json`). After the Release bundles
are uploaded, `.github/workflows/release.yml` automatically refreshes release
docs via `scripts/update-release-links.ts`.

The post-release updater covers:

- [`README.md`](../README.md) — Desktop app download table and pinned Docker
  image example.
- [`docs/deployment.md`](deployment.md) — image-pinning example, tarball URLs,
  and the "Available tarballs for `vX.Y.Z`" list.
- [`docs/desktop-app.md`](desktop-app.md) — current-state release heading.

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
created before the failure step). Goreleaser's release pipeline is mostly
idempotent — a clean retag at a fixed commit produces the same artifacts.

For Tauri-side failures, the `.dmg` / `.deb` / `.AppImage` / `.msi` may be
partially uploaded. `tauri-action` uploads with `--clobber`, so a retag
re-uploads cleanly without manual cleanup.

## Image build

The published image is built by goreleaser in CI using `Dockerfile.release`.
For local validation:

```bash
docker compose build hecate
just test-docker-smoke
```

`docker compose` uses the development `Dockerfile`, not `Dockerfile.release`.
Any new `ENV` var or runtime default needs to land in both files; otherwise
local dev and the published image diverge silently.

For published images, pin by tag in deployment examples and release notes.
Avoid recommending `latest` for anything beyond quick experiments.

## Release Notes

Each release note should include:

- **Highlights** — the most important operator-visible changes.
- **Breaking or risky changes** — config, storage, API, auth, provider, or UI
  behavior changes that can surprise an operator.
- **Migration notes** — storage/schema considerations and any manual steps.
- **Verification** — the exact gate that passed, normally `just verify`.
- **Known limitations** — link to [`known-limitations.md`](known-limitations.md)
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
