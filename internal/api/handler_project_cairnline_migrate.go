package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
)

const projectCairnlineMigrateURL = "/hecate/v1/projects/cairnline/migrate"
const projectCairnlineMigrateRollbackURL = "/hecate/v1/projects/cairnline/migrate/rollback"

// cairnlineMigrationRecord is durable evidence that an authoritative one-way
// migration into the embedded Cairnline database has been executed and
// verified. It is written as a sidecar JSON file next to the embedded
// projects.db. This is embedded-runtime-local operational metadata, consistent
// with the embedded projects.db itself being a local, non-tiered file; it is
// NOT Hecate app data, so it deliberately does not flow through the
// memory/sqlite/postgres storage tiers.
type cairnlineMigrationRecord struct {
	MigratedAt         time.Time `json:"migrated_at"`
	Verified           bool      `json:"verified"`
	ParityMatch        bool      `json:"parity_match"`
	Target             string    `json:"target"`
	SourceAuthority    []string  `json:"source_authority"`
	RollbackBackupPath string    `json:"rollback_backup_path"`
	ProjectCount       int       `json:"project_count"`
}

func cairnlineMigrationStagingPath(dbPath string) string {
	return dbPath + ".migrating"
}

// cairnlineMigrationBackupPath builds a timestamped, colon-free backup filename
// so repeated migrations keep distinct backup generations instead of clobbering
// the true pre-migration original. Colons are avoided so the name is safe on
// Windows and macOS filesystems. migratedAt is the same timestamp recorded in
// migration.json, so the backup name and the durable record always agree.
func cairnlineMigrationBackupPath(dbPath string, migratedAt time.Time) string {
	ts := migratedAt.UTC().Format("20060102T150405.000000000Z")
	return dbPath + ".pre-migration-" + ts + ".bak"
}

func cairnlineMigrationRecordPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "migration.json")
}

// copyFileSync copies src to dst and fsyncs the written file so the copy is
// durable before a caller relies on it (e.g. as a pre-migration backup a crash
// must not lose).
func copyFileSync(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// copyCairnlineSQLiteFiles copies an SQLite database (main file plus any present
// -wal/-shm sidecars) from src to dst, fsyncing each written file. The main file
// must exist; sidecars are legitimately absent after a clean close, so a missing
// sidecar is not an error but any stale destination sidecar is cleared so the
// destination triplet matches the source.
func copyCairnlineSQLiteFiles(src, dst string) error {
	if err := copyFileSync(src, dst); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := copyFileSync(src+suffix, dst+suffix); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// Source has no such sidecar; drop any stale destination one so the
				// backup/restore triplet is exactly what the source holds.
				if rmErr := os.Remove(dst + suffix); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
					return rmErr
				}
				continue
			}
			return err
		}
	}
	return nil
}

// fsyncDir flushes a directory's metadata so a freshly created or renamed entry
// survives a crash. On Linux a rename/create is not durable until the containing
// directory is fsynced; on platforms where this is a no-op the call is harmless.
func fsyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// swapCairnlineMigrationStore replaces the live embedded database at dbPath with
// the freshly rebuilt staging database at stagingPath without ever leaving
// dbPath absent. The previous swap did two renames (live->backup, then
// staging->dbPath); a crash between them removed dbPath entirely, so the
// embedded reader silently recreated a fresh empty store and the real data
// survived only in the .bak. This copy-then-single-rename sequence closes that
// window: dbPath always resolves to either the original bytes or the migrated
// bytes, never nothing.
//
// backupPath is "" when there is no live database to back up. beforeRename is an
// optional crash-injection hook (nil in production) that a test uses to simulate
// a crash AFTER the backup copy but BEFORE the atomic rename.
func swapCairnlineMigrationStore(dbPath, stagingPath, backupPath string, beforeRename func() error) error {
	liveExists := false
	if _, err := os.Stat(dbPath); err == nil {
		liveExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	// 1. COPY (not move) the live database and any sidecars to the backup so
	// dbPath stays in place; on any failure below dbPath is still the original.
	if liveExists && backupPath != "" {
		if err := copyCairnlineSQLiteFiles(dbPath, backupPath); err != nil {
			return err
		}
		// 2. Durability: the copy helper fsyncs each written file; fsync the
		// directory so the new backup dir entries survive a crash too.
		if err := fsyncDir(filepath.Dir(backupPath)); err != nil {
			return err
		}
	}

	// 3. Test crash-injection point: return before the rename so dbPath is left
	// as the intact original with no swap performed.
	if beforeRename != nil {
		if err := beforeRename(); err != nil {
			return err
		}
	}

	// 4. Single atomic replace of the destination. The staging store was cleanly
	// closed as a single file, so one os.Rename swaps the whole database in place
	// with no instant where dbPath is absent.
	if err := os.Rename(stagingPath, dbPath); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		stagingSidecar := stagingPath + suffix
		liveSidecar := dbPath + suffix
		if _, err := os.Stat(stagingSidecar); err == nil {
			// Unusual after a clean Close, but if staging still has a sidecar move
			// it over the old one so the swapped database is complete.
			if err := os.Rename(stagingSidecar, liveSidecar); err != nil {
				return err
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// Staging had no sidecar (the expected clean-close case). Remove any stale
		// -wal/-shm left from the OLD database: those sidecars describe different
		// bytes and would corrupt the freshly swapped main file if SQLite replayed
		// them.
		if err := os.Remove(liveSidecar); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	// 5. fsync the directory again so the rename is durable.
	return fsyncDir(filepath.Dir(dbPath))
}

func writeCairnlineMigrationRecord(dbPath string, rec cairnlineMigrationRecord) error {
	rec.MigratedAt = rec.MigratedAt.UTC()
	payload, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	recordPath := cairnlineMigrationRecordPath(dbPath)
	// Write to a temp file in the same directory then rename so a reader never
	// observes a partially written record.
	tmp, err := os.CreateTemp(filepath.Dir(recordPath), "migration-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, recordPath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func readCairnlineMigrationRecord(dbPath string) (*cairnlineMigrationRecord, bool, error) {
	payload, err := os.ReadFile(cairnlineMigrationRecordPath(dbPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var rec cairnlineMigrationRecord
	if err := json.Unmarshal(payload, &rec); err != nil {
		return nil, false, err
	}
	return &rec, true, nil
}

func (h *Handler) cairnlineMigrationSourceAuthority() []string {
	tier := ""
	if h != nil {
		tier = strings.TrimSpace(h.config.Projects.Backend)
	}
	if tier == "" {
		tier = "memory"
	}
	return []string{tier}
}

// cairnlineMigrationRollbackPlan is the operator-facing rollback plan shared by
// the migration report and the migration-and-rollback replacement gate.
func cairnlineMigrationRollbackPlan() []string {
	return []string{
		"Native Hecate stores remain the untouched source of truth; migration only writes the embedded Cairnline database.",
		"To discard the migrated embedded store, POST " + projectCairnlineMigrateRollbackURL + " (restores the pre-migration backup).",
		"To return authority to Hecate, unset HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE (and/or HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY); reads and writes fall back to the native stores with no data loss.",
	}
}

func cairnlineMigrationVerificationNotes() []string {
	// Documents what the parity oracle does and does not compare so operators do
	// not read the timestamp-stripped content digest as a weaker check than it is.
	return []string{
		"Parity verified across all 12 portable write families by count, record-ID set, and content digest.",
		"Content-digest comparison excludes timestamp fields (created_at, updated_at, discovered_at, applied_at) and compares the record-ID intersection; additions and deletions are covered by the record-ID-set layer.",
	}
}

func (h *Handler) HandleMigrateProjectsToCairnline(w http.ResponseWriter, r *http.Request) {
	ctx, span := httpServerTracer.Start(r.Context(), "hecate.projects.cairnline.migrate")
	defer span.End()
	dbPath := h.cairnlineEmbeddedDatabasePath()
	stagingPath := cairnlineMigrationStagingPath(dbPath)

	snapshots, err := cairnlinebridge.LoadSnapshots(ctx, h.cairnlineSnapshotSources())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	// Build the migration target in a staging file so the live embedded database
	// is never mutated until a staged rebuild passes full parity verification.
	// Reconstructing purely from the current native snapshot makes the migration
	// deletion-faithful: any natively-absent row is never written, so deletions
	// propagate for every family even though Cairnline's import path only upserts.
	if err := removeCairnlineSQLiteFiles(stagingPath); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	service, store, err := cairnline.NewSQLiteService(ctx, stagingPath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	// The staging store must be closed before any swap rename so SQLite releases
	// its file handles; guard the defer so the explicit close is not doubled.
	storeClosed := false
	closeStore := func() error {
		if storeClosed {
			return nil
		}
		storeClosed = true
		return store.Close()
	}
	defer func() { _ = closeStore() }()

	if err := cairnlinebridge.SeedSnapshots(ctx, service, snapshots); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	// Verify the staged database before touching the live store. The strict read
	// smoke opens the embedded database through the handler's embedded path, so
	// it is pointed at the staging file while the staged store stays open (a
	// second read connection observes the just-imported rows).
	smokeHandler := h.projectCairnlineStrictEmbeddedProbeHandler()
	smokeHandler.cairnlineEmbeddedPathOverride = stagingPath
	smoke := smokeHandler.projectCairnlineStrictEmbeddedSmoke(ctx, snapshots)
	item, err := projectCairnlineServiceParity(ctx, stagingPath, true, "authoritative_migration", snapshots, service, smoke)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	verified := item.Match && smoke != nil && smoke.Status == "passed"
	span.SetAttributes(
		attribute.Bool("hecate.cairnline.migration.verified", verified),
		attribute.Bool("hecate.cairnline.migration.parity_match", item.Match),
		attribute.Int("hecate.cairnline.migration.project_count", len(snapshots)),
	)

	sourceAuthority := h.cairnlineMigrationSourceAuthority()
	migratedAt := time.Now().UTC()

	if !verified {
		// The staged rebuild failed verification, so the live database is left
		// untouched. Respond 200 with verified=false rather than an error status
		// so the parity diff payload still reaches the operator for diagnosis.
		if err := closeStore(); err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		// The parity item was computed against the staging file, which is deleted
		// below; re-point it at the intended live target so the returned report
		// never references a path that no longer exists.
		item.DatabasePath = dbPath
		if err := removeCairnlineSQLiteFiles(stagingPath); err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		report := ProjectCairnlineMigrationReport{
			Object:          "project_cairnline_migration_report",
			MigratedAt:      migratedAt,
			Verified:        false,
			ParityMatch:     item.Match,
			Target:          dbPath,
			SourceAuthority: sourceAuthority,
			ProjectCount:    len(snapshots),
			Checklist:       cairnlineMigrationChecklist(item.Match, verified, false, false),
			Rollback:        cairnlineMigrationRollbackPlan(),
			Parity:          item,
		}
		WriteJSON(w, http.StatusOK, ProjectCairnlineMigrationResponse{
			Object: "project_cairnline_migration",
			Data:   report,
		})
		return
	}

	// Verified: close the staging store before renaming so SQLite handles are
	// released, then back up any existing live database and atomically swap the
	// staged database into place.
	if err := closeStore(); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	liveExists := false
	if _, statErr := os.Stat(dbPath); statErr == nil {
		liveExists = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, statErr.Error())
		return
	}
	// Timestamped backup generation named from the same migratedAt recorded below
	// so the backup file and migration.json agree; empty when no live DB exists.
	rollbackBackupPath := ""
	if liveExists {
		rollbackBackupPath = cairnlineMigrationBackupPath(dbPath, migratedAt)
	}
	// Crash-safe swap: copy the live DB to the backup (leaving dbPath in place)
	// then a single atomic rename replaces dbPath. On failure dbPath is still the
	// original because the copy did not move it, so no restore dance is needed.
	if err := swapCairnlineMigrationStore(dbPath, stagingPath, rollbackBackupPath, nil); err != nil {
		_ = removeCairnlineSQLiteFiles(stagingPath)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	rec := cairnlineMigrationRecord{
		MigratedAt:         migratedAt,
		Verified:           true,
		ParityMatch:        item.Match,
		Target:             dbPath,
		SourceAuthority:    sourceAuthority,
		RollbackBackupPath: rollbackBackupPath,
		ProjectCount:       len(snapshots),
	}
	if err := writeCairnlineMigrationRecord(dbPath, rec); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	// The staged parity item was computed against the exact bytes now living at
	// dbPath, so it is reused as the live report's parity evidence; re-point it
	// at the live path.
	item.DatabasePath = dbPath
	report := ProjectCairnlineMigrationReport{
		Object:             "project_cairnline_migration_report",
		MigratedAt:         migratedAt,
		Verified:           true,
		ParityMatch:        item.Match,
		Target:             dbPath,
		SourceAuthority:    sourceAuthority,
		RollbackBackupPath: rollbackBackupPath,
		ProjectCount:       len(snapshots),
		Checklist:          cairnlineMigrationChecklist(item.Match, true, liveExists, true),
		Rollback:           cairnlineMigrationRollbackPlan(),
		VerificationNotes:  cairnlineMigrationVerificationNotes(),
		Parity:             item,
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineMigrationResponse{
		Object: "project_cairnline_migration",
		Data:   report,
	})
}

func (h *Handler) HandleRollbackProjectsCairnlineMigration(w http.ResponseWriter, r *http.Request) {
	_, span := httpServerTracer.Start(r.Context(), "hecate.projects.cairnline.migrate.rollback")
	defer span.End()
	dbPath := h.cairnlineEmbeddedDatabasePath()

	noBackup := func() {
		span.SetAttributes(attribute.Bool("hecate.cairnline.migration.restored", false))
		WriteJSON(w, http.StatusOK, ProjectCairnlineMigrationRollbackResponse{
			Object: "project_cairnline_migration_rollback",
			Data: ProjectCairnlineMigrationRollbackResult{
				Restored: false,
				Reason:   "no_backup",
				Target:   dbPath,
			},
		})
	}

	// Roll back to the backup generation recorded in the CURRENT migration.json
	// rather than a fixed path, so timestamped generations restore the latest one.
	rec, ok, err := readCairnlineMigrationRecord(dbPath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	backupPath := ""
	if ok && rec != nil {
		backupPath = strings.TrimSpace(rec.RollbackBackupPath)
	}
	if backupPath == "" {
		noBackup()
		return
	}
	if _, err := os.Stat(backupPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		noBackup()
		return
	}

	// Restore by COPY (not rename) so the backup generation is preserved after
	// rollback for manual recovery. Older backup generations are intentionally
	// left on disk for the same reason.
	if err := copyCairnlineSQLiteFiles(backupPath, dbPath); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if err := fsyncDir(filepath.Dir(dbPath)); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	// The authoritative migration was rolled back, so the operator is back to the
	// pre-migration embedded state; delete the migration record rather than
	// leaving stale evidence pointing at the superseded migrated database.
	if err := os.Remove(cairnlineMigrationRecordPath(dbPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	span.SetAttributes(attribute.Bool("hecate.cairnline.migration.restored", true))
	WriteJSON(w, http.StatusOK, ProjectCairnlineMigrationRollbackResponse{
		Object: "project_cairnline_migration_rollback",
		Data: ProjectCairnlineMigrationRollbackResult{
			Restored:     true,
			RestoredFrom: backupPath,
			Target:       dbPath,
		},
	})
}

func cairnlineMigrationChecklist(parityMatch, verified, backedUp, swapped bool) []ProjectCairnlineMigrationRehearsalCheck {
	return []ProjectCairnlineMigrationRehearsalCheck{
		{
			ID:     "load-hecate-stores",
			Status: "complete",
			Detail: "Loaded Hecate's authoritative project, work, skill, memory, and assistant stores.",
		},
		{
			ID:     "staged-rebuild",
			Status: "complete",
			Detail: "Rebuilt the migration target from scratch in a staging database so absent native rows are never written, making the migration deletion-faithful by reconstruction.",
		},
		{
			ID:     "parity-verify",
			Status: projectCairnlineMigrationParityStatus(true, parityMatch),
			Detail: "Compared counts, IDs, launch packets, and content digests plus the strict embedded read smoke against the staged database before touching the live store.",
		},
		{
			ID:     "backup-live-store",
			Status: cairnlineMigrationStepStatus(verified, backedUp),
			Detail: "Copied the existing live embedded database to a timestamped pre-migration backup generation, leaving the live file in place; skipped when no prior live database existed.",
		},
		{
			ID:     "atomic-swap",
			Status: cairnlineMigrationStepStatus(verified, swapped),
			Detail: "Replaced the live embedded path with the verified staged database via a single atomic rename over the copied backup, so the live path is never absent.",
		},
		{
			ID:     "migration-record-written",
			Status: cairnlineMigrationStepStatus(verified, swapped),
			Detail: "Persisted the durable migration record next to the embedded database.",
		},
	}
}

func cairnlineMigrationStepStatus(verified, executed bool) string {
	if !verified {
		return "skipped"
	}
	if executed {
		return "complete"
	}
	return "skipped"
}

func (h *Handler) projectCairnlineMigrationCutoverStatus(dbPath string) *ProjectCairnlineMigrationCutoverStatus {
	status := &ProjectCairnlineMigrationCutoverStatus{
		Status:           "not_migrated",
		Endpoint:         projectCairnlineMigrateURL,
		RollbackEndpoint: projectCairnlineMigrateRollbackURL,
	}
	rec, ok, err := readCairnlineMigrationRecord(dbPath)
	if err != nil || !ok || rec == nil {
		return status
	}
	migratedAt := rec.MigratedAt
	status.Migrated = true
	status.Verified = rec.Verified
	status.ParityMatch = rec.ParityMatch
	status.MigratedAt = &migratedAt
	status.RollbackBackupPath = rec.RollbackBackupPath
	status.SourceAuthority = append([]string(nil), rec.SourceAuthority...)
	if rec.Verified && rec.ParityMatch {
		status.Status = "migrated_verified"
	} else {
		status.Status = "migrated_unverified"
	}
	return status
}
