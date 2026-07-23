# Maintenance

Keep routine upkeep small, repeatable, and separated from feature work.
Maintenance changes should make future work safer without smuggling in
behavior changes.

## Default cadence

### Before a maintenance PR

Start from current `master` in a separate worktree or branch. Do not layer
maintenance commits on top of an active feature branch unless the cleanup is
strictly required by that feature.

Run:

```bash
just branches-report
```

Use the report to spot merged worktrees, local-only branches, and open PRs
before deleting anything.

### Cheap recurring gate

Run:

```bash
just maintenance
```

This is the fast health pass for routine upkeep: docs checks, formatting checks,
Go vet/unit tests, UI lint/unit tests, and website lint. It intentionally skips
the expensive PR/release surfaces such as race tests, Docker smoke, UI e2e, and
the embedded binary build.

### Full PR or release gate

Run:

```bash
just verify
```

Use this before merging broad runtime changes, release prep, or anything that
touches startup, config loading, Docker, UI e2e journeys, task execution, or
the embedded runtime binary.

### Nightly scheduled gate

The GitHub Actions nightly maintenance workflow runs:

```bash
just maintenance-nightly
```

It writes `.maintenance/maintenance-report.md`, appends the same report to the
GitHub Actions job summary, and uploads the whole `.maintenance/` directory as
a short-lived artifact. Deterministic checks (`just maintenance` and
`just test-race`) fail the workflow. External link rot is informational and is
captured in the report without turning the run red.

## Branch and worktree hygiene

Use `just branches-report` before pruning. Treat its output as a report, not an
auto-delete command.

Safe candidates usually have all three properties:

- the worktree is clean
- the branch has no unique patch content relative to `origin/master`
- any associated PR is merged or intentionally closed

Do not delete worktrees or branches with open PRs, uncommitted changes, or
commits that are not already represented on `origin/master`.

## Docs drift

Maintenance is the right time to remove stale product language and obsolete
rules from agent docs, README sections, UI copy, and long-form docs.

Check especially for:

- old gateway-first positioning where the local runtime console framing is
  intended
- obsolete auth, tenant, or postgres guidance
- stale release links and screenshots
- dead anchors after headings are renamed
- Mermaid diagrams that no longer match the runtime flow

Run `just docs-check` after docs edits. Use `just check-links-external` when
you specifically want to inspect external URL rot; it is informational because
remote hosts and badges fail transiently.

## Dependency drift

Dependency maintenance should be its own PR unless the bump is required by the
change at hand. Keep one ecosystem per commit when practical:

- Go modules: `go.mod` / `go.sum`
- UI: `ui/package.json` / `ui/bun.lock`
- Website: `website/package.json` / `website/bun.lock`
- Tauri/Rust: `tauri/src-tauri/Cargo.toml` / `Cargo.lock`
- GitHub Actions and Docker images

After dependency changes, run the narrow checks for that ecosystem first, then
the broader gate that matches the risk.

### Cursor Agent artifact pins

Cursor Agent is not updated from its mutable installer during Docker builds.
Run:

```bash
just cursor-agent-update
```

The updater treats the installer as bounded metadata, downloads and validates
the official versioned Linux x64 and arm64 archives, prints their exact hashes,
and changes only `Dockerfile` and `Dockerfile.release`. It refuses an older
advertised release date, refuses an unordered same-date transition by default,
and refuses to normalize changed bytes under an already pinned version. Use
`--allow-same-date-transition` only after confirming that opaque suffix change
is intentional; scheduled automation never overrides the guard. Never replace
those failures with an unreviewed checksum.

`.github/workflows/cursor-agent-update.yml` runs the same check weekly and on
manual dispatch. A repository-scoped GitHub App opens or refreshes one
deterministic review PR; the workflow never approves or merges it, and the
normal PR event runs the full affected CI surface. Review the advertised version,
installer digest, both artifact URLs and hashes, and CI before merging.
An open proposal is a review boundary: if its version's bytes mutate or the
installer advances to another version, the workflow fails instead of replacing
the reviewed release evidence. Merge or close the proposal before asking the
automation to publish another version. If the proposal, its master parent, and
its open PR are unchanged, the workflow leaves the commit alone so it does not
churn CI or dismiss review state.

Do not install the updater App or store its key until `master` has an active
ruleset requiring a reviewed PR, approval of the latest push, strict status
checks through `Required checks`, and deletion/force-push protection. The App
must have no bypass. The workflow verifies effective rules before minting its
token, but the read-only workflow token cannot audit private bypass metadata.

## Commit shape

Maintenance commits use the normal Conventional Commits format. Agent-doc-only
changes use `chore(agent):`; general upkeep can use `chore:`, `docs:`, or the
more specific scope that matches the edited surface. Pure Markdown changes may
append `[skip ci]`.
