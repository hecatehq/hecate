package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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

func cairnlineMigrationBackupPath(dbPath string) string {
	return dbPath + ".pre-migration.bak"
}

func cairnlineMigrationRecordPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "migration.json")
}

// renameCairnlineSQLiteFiles atomically moves an SQLite database triplet
// (main + optional -wal/-shm sidecars) from src to dst within the same
// directory. The main file rename is required when src exists so the swap
// cannot silently drop the database; the -wal/-shm sidecars may legitimately
// be absent after a clean close, so their missing rename is not an error.
func renameCairnlineSQLiteFiles(src, dst string) error {
	// Clear any stale destination triplet first so rename cannot collide with
	// leftover files from a previous swap.
	if err := removeCairnlineSQLiteFiles(dst); err != nil {
		return err
	}
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Rename(src+suffix, dst+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
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

func (h *Handler) HandleMigrateProjectsToCairnline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
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
	backupPath := cairnlineMigrationBackupPath(dbPath)
	rollbackBackupPath := ""
	liveExists := false
	if _, statErr := os.Stat(dbPath); statErr == nil {
		liveExists = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, statErr.Error())
		return
	}
	if liveExists {
		if err := renameCairnlineSQLiteFiles(dbPath, backupPath); err != nil {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		rollbackBackupPath = backupPath
	}
	if err := renameCairnlineSQLiteFiles(stagingPath, dbPath); err != nil {
		// Best-effort restore of the backup so the live database is not lost when
		// the swap fails after the backup rename.
		if rollbackBackupPath != "" {
			_ = renameCairnlineSQLiteFiles(backupPath, dbPath)
		}
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
		Parity:             item,
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineMigrationResponse{
		Object: "project_cairnline_migration",
		Data:   report,
	})
}

func (h *Handler) HandleRollbackProjectsCairnlineMigration(w http.ResponseWriter, r *http.Request) {
	dbPath := h.cairnlineEmbeddedDatabasePath()
	backupPath := cairnlineMigrationBackupPath(dbPath)

	if _, err := os.Stat(backupPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, ProjectCairnlineMigrationRollbackResponse{
			Object: "project_cairnline_migration_rollback",
			Data: ProjectCairnlineMigrationRollbackResult{
				Restored: false,
				Reason:   "no_backup",
				Target:   dbPath,
			},
		})
		return
	}

	if err := renameCairnlineSQLiteFiles(backupPath, dbPath); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	// The authoritative migration was rolled back, so the operator is back to the
	// pre-migration embedded state; delete the migration record rather than
	// leaving stale evidence pointing at a database that no longer exists.
	if err := os.Remove(cairnlineMigrationRecordPath(dbPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
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
			Detail: "Renamed the existing live embedded database to a pre-migration backup before the swap; skipped when no prior live database existed.",
		},
		{
			ID:     "atomic-swap",
			Status: cairnlineMigrationStepStatus(verified, swapped),
			Detail: "Atomically renamed the verified staged database into the live embedded path.",
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
