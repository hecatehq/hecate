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

## Commit shape

Maintenance commits use the normal Conventional Commits format. Agent-doc-only
changes use `chore(agent):`; general upkeep can use `chore:`, `docs:`, or the
more specific scope that matches the edited surface. Pure Markdown changes may
append `[skip ci]`.
