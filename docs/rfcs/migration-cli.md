# Migration CLI

> **Status:** proposed; not implemented.
> **Current source of truth:** [Deployment](../deployment.md) and
> [Known limitations](../known-limitations.md) for today's backup/upgrade
> guidance.
> **Next action:** design `hecate migrate` around the current per-package SQLite
> migration pattern.

`docs/known-limitations.md` flags two related gaps under "API And
Schema Stability":

> - Persisted SQLite schemas are young. Back up data before upgrading.
> - There is not yet a dedicated migration CLI or rollback workflow.

Today an operator who's about to pull a new release does it by hand:
stop the gateway, copy `.data/hecate.db` somewhere, replace the
binary, start it back up, and if anything looks wrong, swap the
file back. That works but it's manual, error-prone, and doesn't
surface what changed during the upgrade.

This RFC scopes the smallest CLI that closes both gaps without
inventing a parallel migration system.

## What exists today

Eight SQLite-backed packages, each owning its own schema:

| Package                                      | What it stores                                            |
| -------------------------------------------- | --------------------------------------------------------- |
| `internal/agentadapters/approvals_sqlite.go` | Adapter approvals + grants                                |
| `internal/chat/sqlite.go`                    | Chat sessions and messages                                |
| `internal/controlplane/store_sqlite.go`      | Configured providers, policy rules, secrets, audit events |
| `internal/governor/usage_sqlite.go`          | Usage totals and usage events                             |
| `internal/orchestrator/queue_sqlite.go`      | Task run queue                                            |
| `internal/providers/history_sqlite.go`       | Provider health-state transitions                         |
| `internal/retention/history_sqlite.go`       | Retention sweep run records                               |
| `internal/taskstate/sqlite.go`               | Task runs, steps, artifacts, approvals                    |

All eight share a single SQLite file (`.data/hecate.db` by default,
overridable via `GATEWAY_SQLITE_PATH`). Each package's `migrate(ctx)`
runs lazily on first store use:

- New tables come from `CREATE TABLE IF NOT EXISTS` — idempotent.
- New columns come from `ensureSessionColumn`-style helpers that
  check `PRAGMA table_info` before issuing `ALTER TABLE ADD COLUMN`.
  Also idempotent.
- All migrations are additive. Hecate has never shipped a destructive
  migration in alpha.

There is no central `schema_version` table. No package tracks how
many migrations it has run. The contract is "the boot path runs
every package's migrate, and migrate is idempotent, so the database
ends up in the right shape regardless of what state it started in."

This is fine for upgrades. It does not help with anything else an
operator might want to do around upgrades.

## What operators actually need

In rough priority order:

1. **A safe backup before upgrading.** "Stop the gateway, `cp` the
   db" works but is racy if the gateway is busy with a long-running
   task. Operators want a quiesced point-in-time copy.
2. **A rollback path that doesn't require shell archeology.** "Last
   thing I tried broke; revert to yesterday's snapshot" should be
   one command, not five.
3. **Visibility into what state the database is in right now.** Which
   tables exist? When were they last migrated? How many rows in each?
   Useful both before an upgrade ("am I about to migrate something
   big?") and after a failed upgrade ("did the migration actually
   apply?").
4. **A dry-run of a pending migration.** Less critical given Hecate's
   migrations are all additive — there's nothing destructive to
   dry-run today — but the surface should accommodate a future
   destructive migration without redesign.
5. **Restore from backup.** The mirror of (1).

What operators do NOT need (out of scope for this RFC):

- Down-migrations / per-table rollback. Hecate's pattern is
  additive-only; rollback = restore from backup. Trying to walk
  schema history backwards is more code than the actual operator
  workflow needs.
- Cross-backend migration (memory → SQLite, or vice versa). Memory
  is dev-only; operators upgrading production are already on SQLite.
- Cross-database migration (SQLite → Postgres). Postgres is not on
  the alpha-to-beta gate.
- A general-purpose migration framework. Goose / golang-migrate /
  Atlas are mature options if Hecate ever needs one; until then the
  per-package pattern is enough.

## Proposed CLI surface

A new `migrate` subcommand on the existing `hecate` binary, sharing
all the storage code. No new binary.

```text
hecate migrate status         # show schema state per package
hecate migrate apply          # run pending migrations explicitly
hecate migrate snapshot       # safe point-in-time backup
hecate migrate restore <path> # restore from a snapshot
hecate migrate verify         # sanity-check tables (counts, no orphans)
```

### `hecate migrate status`

Reads the live SQLite file (read-only connection, no migrate call)
and emits a human-readable table plus a `--json` flag for machine
consumption. For each of the eight packages, shows:

- Table presence (`exists` / `missing`)
- Row count
- Per-column presence vs. the binary's expected schema
- Anything missing → flagged as "pending migration"

Output something like:

```text
package                  table                       rows      schema
chat                     chat_sessions               42        ok
chat                     chat_messages               318       ok
controlplane             providers                   3         ok
controlplane             policy_rules                7         pending: column 'expires_at' not present
taskstate                task_runs                   124       ok
...
8 packages, 24 tables. 1 pending migration.
```

This is the highest-leverage subcommand because it turns "is my
database OK?" into a single command the operator can run without
restarting the gateway.

### `hecate migrate apply`

Runs every package's `migrate(ctx)` against the configured SQLite
file. Equivalent to what happens implicitly on gateway boot, but
explicit, exit-coded, and runnable without serving requests. Useful
when:

- An operator wants to apply migrations during a maintenance
  window without exposing the new binary's HTTP surface yet.
- A future destructive migration ships and operators want to run it
  detached from boot.

Flags:

- `--dry-run`: open the connection, walk the same code path, but
  swallow every write. Validates that migrations can plan
  themselves without committing. Most useful as a preflight in a
  CI release pipeline.

### `hecate migrate snapshot`

Takes a quiesced backup of the SQLite file. Two implementation
choices, both correct:

- **(a)** Run SQLite's `VACUUM INTO 'path'` against a read-only
  connection. Atomic, consistent, even with concurrent writers.
- **(b)** Use the SQLite Online Backup API (`sqlite3_backup_*`).
  The pure-Go driver in modernc.org/sqlite supports it.

Default output path: `.data/snapshots/hecate-YYYY-MM-DDTHH-MM-SSZ.db`.
Override with `--out`. After write, prints the path for use in
scripted backup pipelines.

Important: `snapshot` MUST work against a running gateway. The
operator workflow is "back up before pulling the new release," not
"shut down everything for an hour."

### `hecate migrate restore <path>`

Replaces the configured SQLite file with the snapshot at `<path>`.
Validates `<path>` is itself a valid SQLite file before touching the
live database.

Restore MUST refuse to run while a gateway is connected. Detection:
look for the `-wal` and `-shm` files; if `-wal` is non-empty,
something is connected. The operator can override with `--force` if
they're confident no gateway is running (and accept they'll lose any
in-flight WAL writes). The default refusal is the safe path.

### `hecate migrate verify`

Sanity-check the database without modifying it. Reports:

- Foreign-key integrity (`PRAGMA foreign_key_check`).
- Row counts per table for cross-checking against `status`.
- Orphan row detection per package (e.g., task runs whose `task_id`
  doesn't reference a row in `tasks`).

Each package contributes its own verifiers via a small
`SchemaVerifier` interface, registered alongside the `migrate`
function. Output is plain text by default; `--json` emits a
machine-readable shape.

## Implementation sketch

### CLI wiring

A new `cmd/hecate/migrate.go` (or a small subpackage under
`cmd/hecate/`) that:

1. Parses the subcommand from `os.Args`.
2. Loads `internal/config/config.go` like normal so `GATEWAY_SQLITE_PATH`
   etc. resolve identically to the gateway.
3. Constructs the same SQLite client the gateway constructs at boot
   (`internal/storage/sqlite.go`).
4. Dispatches to a per-subcommand handler.

The flag layout matches Go's `flag` package convention. No new
dependency.

### Per-package wiring

Each SQLite package gains two small interfaces:

```go
// internal/storage/migration.go (new)

type Migrator interface {
    Migrate(ctx context.Context) error
}

type StatusReporter interface {
    Status(ctx context.Context) ([]TableStatus, error)
}

type Verifier interface {
    Verify(ctx context.Context) ([]VerifyFinding, error)
}
```

`Migrate` is the existing per-package `migrate(ctx)` re-exported.
`Status` and `Verify` are new but ~50 lines per package — most of
the work is enumerating expected tables and columns, both of which
the package already knows.

A small registry in `internal/storage/migration.go` lets the CLI
iterate every registered package without import gymnastics.

### Snapshot / restore

These don't need per-package help. They operate on the SQLite file
itself. Implementation goes in `internal/storage/sqlite.go` next to
the existing connection setup.

## Rollback model

This RFC does NOT propose down-migrations. Rollback in Hecate is:

1. `hecate migrate snapshot` before the upgrade.
2. Pull and start the new binary.
3. If anything looks wrong: stop the new binary, run
   `hecate migrate restore <snapshot>`, start the previous binary.

The trade-off is honest. Hecate's additive migrations make
down-migrations nearly free _to write_ but expensive _to maintain_
once they exist. Operators who want a different rollback model will
find that snapshot/restore covers >95% of real upgrade-rollback
workflows: by the time they discover the new version is broken,
they want their data back as it was, not a half-migrated hybrid.

## Backward compatibility

- Existing operators who don't run any `hecate migrate` subcommand
  see zero behavior change. The gateway's boot-time migrate is
  unchanged.
- `migrate apply` produces the same database state as a gateway
  boot. They're interchangeable.
- Snapshots are valid SQLite files — operators can also inspect them
  with `sqlite3` directly.

## Phasing

| PR  | Scope                                                                                                            | Size                                                                      |
| --- | ---------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| 1   | `internal/storage/migration.go` interfaces + registry; per-package wiring for `Migrate` (re-export). No CLI yet. | small (~200 lines)                                                        |
| 2   | `hecate migrate status` + per-package `StatusReporter` implementations                                           | medium (~500 lines, mostly per-package boilerplate)                       |
| 3   | `hecate migrate apply` (with `--dry-run`)                                                                        | small (~150 lines)                                                        |
| 4   | `hecate migrate snapshot` + `hecate migrate restore`                                                             | small-medium (~250 lines, plus integration tests with a real SQLite file) |
| 5   | `hecate migrate verify` + per-package verifiers                                                                  | medium (~400 lines)                                                       |
| 6   | Docs: drop the `known-limitations` bullets, add `docs/migrations.md` operator guide                              | small (~150 lines)                                                        |

Total: ~1650 lines, ~6 PRs. PRs 1–4 close the immediate operator
gap (status + safe backup/restore). PR 5 is polish that catches
schema drift early. PR 6 is the doc surface that makes the CLI
discoverable.

PRs 1 and 4 are the highest-leverage; status and snapshot/restore
together close the explicit known-limitations gap. PRs 2, 3, 5 are
incremental improvements after that.

## Open questions

- **Should `migrate apply` be the _only_ path on boot too?** Today
  the gateway runs migrate implicitly on first store use, scattered
  across nine packages. Centralizing into one boot-time call (and
  letting `migrate apply` be that call) is cleaner but expands this
  RFC's scope.
- **Granularity of snapshot.** A single SQLite file is the simplest
  snapshot unit. Per-package snapshots ("just the providers
  config") are a natural follow-up but introduce cross-table
  consistency questions (foreign keys spanning packages).
- **Snapshot retention.** Should `migrate snapshot` rotate old
  snapshots automatically? The retention worker handles other
  rotation; consistency with that pattern would say yes. Out of
  scope for the first PR.
- **Encryption.** Snapshots are byte-identical SQLite files,
  including any settings encrypted via
  `GATEWAY_CONTROL_PLANE_SECRET_KEY`. Operators who want
  off-machine backup should encrypt the snapshot themselves
  (`age`, `gpg`). Building encryption into the CLI is out of scope.
- **Concurrency.** Multiple `hecate migrate` invocations against
  the same database file are safe (SQLite's locking handles it),
  but multiple `restore --force` calls are not. The CLI should
  acquire a file-level advisory lock for `restore` to prevent
  operators racing themselves.

## Risks

1. **`restore` against a running gateway corrupts WAL state.** The
   refusal to run when `-wal` is non-empty is the primary
   mitigation. `--force` exists for the rare case where the
   operator knows better, but it's documented as a footgun.
2. **`status` falsely reports "ok"** when the binary's expected
   schema drifts from what the package's `migrate` actually applies.
   Mitigation: each package's `Status` implementation reads from
   the same source-of-truth list its `migrate` function uses; if
   they drift, the same kind of test that already pins migrations
   catches it.
3. **`verify` is slow** on large databases (foreign-key check
   walks every row). Mitigation: keep `verify` opt-in and offer
   `--quick` (skip foreign-key check, count rows only) for a
   bounded-time variant.
4. **CLI behavior diverges from gateway boot behavior** as new
   storage packages land. Mitigation: package authors register
   their store via a single registry call; forgetting to register
   means `migrate status` doesn't see the package, which is loud
   on first run.

## Acceptance criteria

When this RFC is implemented, the following are all true:

- An operator can run `hecate migrate snapshot` against a live
  gateway and get a consistent, restorable backup.
- An operator can run `hecate migrate restore <path>` against a
  stopped gateway and end up with the schema and data the snapshot
  captured.
- `hecate migrate status` is the canonical pre-upgrade
  "is my database OK?" check.
- The two `known-limitations.md` bullets ("schemas are young, back
  up data" + "no migration CLI") get rewritten to reflect that
  there's now a documented backup/restore workflow.
- The gateway's boot-time migrate behavior is unchanged.
