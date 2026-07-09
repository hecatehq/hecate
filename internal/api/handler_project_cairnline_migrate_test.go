package api

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

// migrationSeed captures the identifiers a seeded project exposes so migration
// tests can assert against reconstructed embedded state.
type migrationSeed struct {
	projectID    string
	rootID       string
	assignmentID string
	memoryID     string
}

func newCairnlineMigrationHandler(t *testing.T, cfg config.Config) (*Handler, apiTestClient) {
	t.Helper()
	handler := NewHandler(cfg, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetProjectAssistantProposalStore(projectassistant.NewMemoryProposalStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	client := newAPITestClient(t, server)
	return handler, client
}

func seedCairnlineMigrationProject(t *testing.T, handler *Handler, client apiTestClient) migrationSeed {
	t.Helper()
	root := t.TempDir()
	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name":           "Cairnline Migration",
		"workspace_path": root,
		"workspace_kind": "git",
	}))
	projectID := project.Data.ID
	rootID := project.Data.Roots[0].ID
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                  "migrate_profile",
		Name:                "Migrate profile",
		Surface:             agentprofiles.SurfaceHecateTask,
		ProjectMemoryPolicy: agentprofiles.MemoryInclude,
		ContextSourcePolicy: agentprofiles.ContextIncludeEnabled,
		ProviderHint:        "openai",
		ModelHint:           "gpt-5",
	}); err != nil {
		t.Fatalf("Create profile: %v", err)
	}
	if _, err := handler.projectSkills.UpsertDiscovered(t.Context(), projectID, []projectskills.Skill{{
		ID:          "backend",
		ProjectID:   projectID,
		Title:       "Backend",
		Path:        "docs-ai/skills/backend/SKILL.md",
		RootID:      rootID,
		Format:      projectskills.FormatSkillMD,
		Enabled:     true,
		Status:      projectskills.StatusAvailable,
		TrustLabel:  projectskills.TrustWorkspaceSkill,
		Description: "Backend guidance.",
	}}); err != nil {
		t.Fatalf("Upsert skills: %v", err)
	}
	role := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                    "migrate_developer",
		"name":                  "Migrate Developer",
		"default_agent_profile": "migrate_profile",
		"default_driver_kind":   projectwork.AssignmentDriverHecateTask,
		"skill_ids":             []string{"backend"},
	}))
	work := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":            "work_migrate",
		"title":         "Migrate Projects to Cairnline",
		"brief":         "Prove Hecate can migrate a durable all-project Cairnline DB.",
		"owner_role_id": role.Data.ID,
		"root_id":       rootID,
	}))
	assignment := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/"+work.Data.ID+"/assignments", projectJourneyJSON(t, map[string]any{
		"id":          "asgn_migrate",
		"role_id":     role.Data.ID,
		"driver_kind": projectwork.AssignmentDriverHecateTask,
		"root_id":     rootID,
	}))
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:         "mem_migrate",
		Scope:      memory.ScopeProject,
		ProjectID:  projectID,
		Title:      "Migration replacement gate",
		Body:       "Migration should preserve accepted project memory.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		SourceID:   assignment.Data.ID,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create memory: %v", err)
	}
	return migrationSeed{
		projectID:    projectID,
		rootID:       rootID,
		assignmentID: assignment.Data.ID,
		memoryID:     "mem_migrate",
	}
}

func openMigratedCairnlineService(t *testing.T, dbPath string) *cairnline.Service {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("Open migrated Cairnline DB %q: %v", dbPath, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return service
}

func TestHandler_MigrateProjectsToCairnline_HappyPathVerifiesAndSwaps(t *testing.T) {
	handler, client := newCairnlineMigrationHandler(t, config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}})
	seed := seedCairnlineMigrationProject(t, handler, client)

	resp := mustRequestJSON[ProjectCairnlineMigrationResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate", "")
	if resp.Object != "project_cairnline_migration" || resp.Data.Object != "project_cairnline_migration_report" {
		t.Fatalf("migrate envelope = %+v, want project_cairnline_migration report", resp)
	}
	if !resp.Data.Verified || !resp.Data.ParityMatch {
		t.Fatalf("migrate report = %+v, want verified parity match", resp.Data)
	}
	if !resp.Data.Parity.Match || len(resp.Data.Parity.Differences) != 0 || len(resp.Data.Parity.IDDifferences) != 0 || len(resp.Data.Parity.ContentDifferences) != 0 {
		t.Fatalf("migrate parity = %+v, want exact match", resp.Data.Parity)
	}
	if resp.Data.ProjectCount != 1 || len(resp.Data.SourceAuthority) == 0 || resp.Data.SourceAuthority[0] != "memory" {
		t.Fatalf("migrate report = %+v, want single project migrated from memory tier", resp.Data)
	}
	// No prior live database existed, so no backup is recorded on the first run.
	if resp.Data.RollbackBackupPath != "" {
		t.Fatalf("rollback backup path = %q, want empty on first migration", resp.Data.RollbackBackupPath)
	}
	if len(resp.Data.Rollback) == 0 {
		t.Fatalf("rollback plan = %+v, want operator rollback steps", resp.Data.Rollback)
	}
	for _, id := range []string{"load-hecate-stores", "staged-rebuild", "parity-verify", "backup-live-store", "atomic-swap", "migration-record-written"} {
		found := false
		for _, check := range resp.Data.Checklist {
			if check.ID == id {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("checklist = %+v, want step %q", resp.Data.Checklist, id)
		}
	}

	dbPath := handler.cairnlineEmbeddedDatabasePath()
	if resp.Data.Target != dbPath {
		t.Fatalf("target = %q, want embedded db path %q", resp.Data.Target, dbPath)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("stat live db: %v", err)
	}
	if _, err := os.Stat(cairnlineMigrationStagingPath(dbPath)); !os.IsNotExist(err) {
		t.Fatalf("staging db stat err = %v, want removed after swap", err)
	}
	rec, ok, err := readCairnlineMigrationRecord(dbPath)
	if err != nil || !ok || rec == nil {
		t.Fatalf("migration record ok=%v err=%v rec=%+v, want durable record", ok, err, rec)
	}
	if !rec.Verified || !rec.ParityMatch || rec.Target != dbPath || rec.ProjectCount != 1 {
		t.Fatalf("migration record = %+v, want verified record for one project", rec)
	}

	service := openMigratedCairnlineService(t, dbPath)
	if _, err := service.GetProject(t.Context(), seed.projectID); err != nil {
		t.Fatalf("GetProject from migrated DB: %v", err)
	}
	entries, err := service.ListMemoryEntries(t.Context(), seed.projectID, true)
	if err != nil {
		t.Fatalf("ListMemoryEntries from migrated DB: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != seed.memoryID {
		t.Fatalf("migrated memory = %+v, want seeded entry", entries)
	}
}

func TestHandler_MigrateProjectsToCairnline_IdempotentAndBacksUpPriorLiveStore(t *testing.T) {
	handler, client := newCairnlineMigrationHandler(t, config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}})
	seedCairnlineMigrationProject(t, handler, client)

	first := mustRequestJSON[ProjectCairnlineMigrationResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate", "")
	if !first.Data.Verified || !first.Data.ParityMatch || first.Data.RollbackBackupPath != "" {
		t.Fatalf("first migrate = %+v, want verified with no backup", first.Data)
	}
	second := mustRequestJSON[ProjectCairnlineMigrationResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate", "")
	if !second.Data.Verified || !second.Data.ParityMatch {
		t.Fatalf("second migrate = %+v, want still verified parity match", second.Data)
	}
	// The second run had a prior live database, so it must back it up to a
	// timestamped generation recorded in the report.
	dbPath := handler.cairnlineEmbeddedDatabasePath()
	backupPath := second.Data.RollbackBackupPath
	if backupPath == "" || !strings.Contains(backupPath, ".pre-migration-") || !strings.HasPrefix(backupPath, dbPath+".pre-migration-") {
		t.Fatalf("second rollback backup = %q, want timestamped pre-migration backup for %q", backupPath, dbPath)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("stat backup after idempotent rerun: %v", err)
	}
}

func TestHandler_MigrateProjectsToCairnline_DeletionFaithfulByReconstruction(t *testing.T) {
	handler, client := newCairnlineMigrationHandler(t, config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}})
	seed := seedCairnlineMigrationProject(t, handler, client)

	first := mustRequestJSON[ProjectCairnlineMigrationResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate", "")
	if !first.Data.Verified {
		t.Fatalf("first migrate = %+v, want verified", first.Data)
	}
	dbPath := handler.cairnlineEmbeddedDatabasePath()
	before := openMigratedCairnlineService(t, dbPath)
	entriesBefore, err := before.ListMemoryEntries(t.Context(), seed.projectID, true)
	if err != nil || len(entriesBefore) != 1 {
		t.Fatalf("pre-delete migrated memory = %+v err=%v, want seeded entry", entriesBefore, err)
	}

	// Delete a native record; Hecate stores keep no tombstone, so the absence
	// must propagate through a rebuild-from-source migration.
	if err := handler.memory.Delete(t.Context(), seed.projectID, seed.memoryID); err != nil {
		t.Fatalf("Delete native memory: %v", err)
	}

	second := mustRequestJSON[ProjectCairnlineMigrationResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate", "")
	if !second.Data.Verified || !second.Data.ParityMatch {
		t.Fatalf("second migrate = %+v, want verified parity match against the new native snapshot", second.Data)
	}
	after := openMigratedCairnlineService(t, dbPath)
	entriesAfter, err := after.ListMemoryEntries(t.Context(), seed.projectID, true)
	if err != nil {
		t.Fatalf("post-delete ListMemoryEntries: %v", err)
	}
	if len(entriesAfter) != 0 {
		t.Fatalf("migrated memory = %+v, want deleted native entry absent after reconstruction", entriesAfter)
	}
}

func TestHandler_MigrateProjectsToCairnline_EmptySourcesVerifyAndSwap(t *testing.T) {
	handler, client := newCairnlineMigrationHandler(t, config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}})

	resp := mustRequestJSON[ProjectCairnlineMigrationResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate", "")
	if !resp.Data.Verified || !resp.Data.ParityMatch || resp.Data.ProjectCount != 0 {
		t.Fatalf("empty migrate = %+v, want verified empty parity match", resp.Data)
	}
	dbPath := handler.cairnlineEmbeddedDatabasePath()
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("stat live db after empty migrate: %v", err)
	}
	service := openMigratedCairnlineService(t, dbPath)
	projectsList, err := service.ListProjects(t.Context())
	if err != nil {
		t.Fatalf("ListProjects from empty migrated DB: %v", err)
	}
	if len(projectsList) != 0 {
		t.Fatalf("migrated projects = %+v, want empty", projectsList)
	}
}

func TestHandler_RollbackProjectsCairnlineMigration_RestoresBackupAndClearsRecord(t *testing.T) {
	handler, client := newCairnlineMigrationHandler(t, config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}})
	seed := seedCairnlineMigrationProject(t, handler, client)

	// Two migrations so a pre-migration backup exists to roll back to.
	mustRequestJSON[ProjectCairnlineMigrationResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate", "")
	second := mustRequestJSON[ProjectCairnlineMigrationResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate", "")

	dbPath := handler.cairnlineEmbeddedDatabasePath()
	backupPath := second.Data.RollbackBackupPath
	if backupPath == "" {
		t.Fatalf("second migrate = %+v, want recorded rollback backup path", second.Data)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("expected backup before rollback: %v", err)
	}

	resp := mustRequestJSON[ProjectCairnlineMigrationRollbackResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate/rollback", "")
	if resp.Object != "project_cairnline_migration_rollback" || !resp.Data.Restored || resp.Data.RestoredFrom != backupPath || resp.Data.Target != dbPath {
		t.Fatalf("rollback = %+v, want restored from backup", resp)
	}
	if _, err := os.Stat(cairnlineMigrationRecordPath(dbPath)); !os.IsNotExist(err) {
		t.Fatalf("migration record stat err = %v, want removed after rollback", err)
	}
	// The restored live database still contains the pre-migration content.
	service := openMigratedCairnlineService(t, dbPath)
	entries, err := service.ListMemoryEntries(t.Context(), seed.projectID, true)
	if err != nil {
		t.Fatalf("ListMemoryEntries from restored DB: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != seed.memoryID {
		t.Fatalf("restored memory = %+v, want pre-migration content", entries)
	}
}

func TestHandler_RollbackProjectsCairnlineMigration_NoBackupReportsNoOp(t *testing.T) {
	handler, client := newCairnlineMigrationHandler(t, config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}})
	_ = handler

	resp := mustRequestJSON[ProjectCairnlineMigrationRollbackResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate/rollback", "")
	if resp.Data.Restored || resp.Data.Reason != "no_backup" {
		t.Fatalf("rollback = %+v, want no-op with no_backup reason", resp.Data)
	}
}

func TestHandler_MigrateProjectsToCairnline_BackendStatusReportsMigratedVerified(t *testing.T) {
	handler, client := newCairnlineMigrationHandler(t, config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			Backend:             "memory",
			CoordinationBackend: "cairnline",
			CairnlineConnector:  "embedded",
			CairnlineReadSource: "embedded",
		},
	})
	seedCairnlineMigrationProject(t, handler, client)

	migrate := mustRequestJSON[ProjectCairnlineMigrationResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate", "")
	if !migrate.Data.Verified {
		t.Fatalf("migrate = %+v, want verified before checking backend status", migrate.Data)
	}

	status := handler.projectCoordinationBackendStatusWithContext(t.Context())
	if status.MigrationCutover == nil || status.MigrationCutover.Status != "migrated_verified" || !status.MigrationCutover.Migrated || !status.MigrationCutover.Verified || !status.MigrationCutover.ParityMatch {
		t.Fatalf("migration cutover = %+v, want migrated_verified", status.MigrationCutover)
	}
	if status.MigrationCutover.Endpoint != projectCairnlineMigrateURL || status.MigrationCutover.RollbackEndpoint != projectCairnlineMigrateRollbackURL {
		t.Fatalf("migration cutover endpoints = %+v, want migrate + rollback urls", status.MigrationCutover)
	}
	if containsString(status.WriteAdapterGaps, "migration-cutover") {
		t.Fatalf("write adapter gaps = %+v, want migration-cutover cleared after verified migration", status.WriteAdapterGaps)
	}
	if len(status.MigrationBlockers) != 0 {
		t.Fatalf("migration blockers = %+v, want none after verified migration", status.MigrationBlockers)
	}
	if point := findWriteSwitchpoint(status.WriteSwitchpoints, "migration-cutover"); point == nil || point.CairnlineState != "migrated_verified" || !point.LiveMirror {
		t.Fatalf("migration switchpoint = %+v, want migrated_verified live-mirror state", point)
	}
}

// buildCairnlineStoreWithProject writes a real Cairnline SQLite store at dbPath
// containing a single named project so swap assertions are content-meaningful.
func buildCairnlineStoreWithProject(t *testing.T, dbPath, projectID, projectName string) {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteService(%q): %v", dbPath, err)
	}
	if _, err := service.CreateProject(t.Context(), cairnline.Project{ID: projectID, Name: projectName}); err != nil {
		_ = store.Close()
		t.Fatalf("CreateProject: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}
}

// cairnlineStoreHasProject opens a Cairnline store read-only-ish and reports
// whether the given project id is present, so swap tests can prove which bytes
// survived a crash.
func cairnlineStoreHasProject(t *testing.T, dbPath, projectID string) bool {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("open store %q: %v", dbPath, err)
	}
	defer func() { _ = store.Close() }()
	if _, err := service.GetProject(t.Context(), projectID); err != nil {
		return false
	}
	return true
}

// TestCairnlineSwapMigrationStore_CrashBeforeRenameKeepsOriginal proves the
// crash-safe swap: a crash injected AFTER the backup copy but BEFORE the atomic
// rename must leave the live database intact with its original content, and the
// backup must hold that same original. A successful swap then replaces the live
// content while the backup preserves the original.
func TestCairnlineSwapMigrationStore_CrashBeforeRenameKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/projects.db"
	stagingPath := cairnlineMigrationStagingPath(dbPath)
	migratedAt := timeParseUTC(t, "2026-07-09T13:18:18.123456789Z")
	backupPath := cairnlineMigrationBackupPath(dbPath, migratedAt)

	buildCairnlineStoreWithProject(t, dbPath, "proj_original", "Original")
	buildCairnlineStoreWithProject(t, stagingPath, "proj_staging", "Staging")

	injected := errors.New("simulated crash before rename")
	if err := swapCairnlineMigrationStore(dbPath, stagingPath, backupPath, func() error { return injected }); !errors.Is(err, injected) {
		t.Fatalf("swap with crash hook err = %v, want injected error", err)
	}
	// dbPath must still exist and still hold the ORIGINAL content: the swap must
	// never leave the live path absent or empty.
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("live db missing after injected crash: %v", err)
	}
	if !cairnlineStoreHasProject(t, dbPath, "proj_original") {
		t.Fatal("live db lost original content after injected crash")
	}
	if cairnlineStoreHasProject(t, dbPath, "proj_staging") {
		t.Fatal("live db unexpectedly holds staging content after injected crash")
	}
	if !cairnlineStoreHasProject(t, backupPath, "proj_original") {
		t.Fatal("backup missing original content after injected crash")
	}

	// A clean swap (no crash hook) now completes: live holds staging content,
	// backup still holds the original.
	if err := swapCairnlineMigrationStore(dbPath, stagingPath, backupPath, nil); err != nil {
		t.Fatalf("clean swap: %v", err)
	}
	if !cairnlineStoreHasProject(t, dbPath, "proj_staging") {
		t.Fatal("live db missing staging content after clean swap")
	}
	if !cairnlineStoreHasProject(t, backupPath, "proj_original") {
		t.Fatal("backup lost original content after clean swap")
	}
	if _, err := os.Stat(stagingPath); !os.IsNotExist(err) {
		t.Fatalf("staging stat err = %v, want removed after clean swap", err)
	}
}

// TestHandler_MigrateProjectsToCairnline_TimestampedBackupsAndRollbackLatest
// proves repeated migrations produce distinct timestamped backup generations and
// that rollback restores the generation recorded in the current migration.json.
func TestHandler_MigrateProjectsToCairnline_TimestampedBackupsAndRollbackLatest(t *testing.T) {
	handler, client := newCairnlineMigrationHandler(t, config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}})
	seed := seedCairnlineMigrationProject(t, handler, client)

	// First migration: no prior live DB, so no backup generation is created.
	first := mustRequestJSON[ProjectCairnlineMigrationResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate", "")
	if !first.Data.Verified || first.Data.RollbackBackupPath != "" {
		t.Fatalf("first migrate = %+v, want verified with no backup", first.Data)
	}
	// Second migration: backs up the first live DB to a timestamped generation.
	second := mustRequestJSON[ProjectCairnlineMigrationResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate", "")
	if second.Data.RollbackBackupPath == "" {
		t.Fatalf("second migrate = %+v, want a recorded backup generation", second.Data)
	}
	// Third migration: a distinct generation, leaving the second's intact.
	third := mustRequestJSON[ProjectCairnlineMigrationResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate", "")
	if third.Data.RollbackBackupPath == "" {
		t.Fatalf("third migrate = %+v, want a recorded backup generation", third.Data)
	}
	if second.Data.RollbackBackupPath == third.Data.RollbackBackupPath {
		t.Fatalf("backup generations not distinct: %q", third.Data.RollbackBackupPath)
	}
	for _, p := range []string{second.Data.RollbackBackupPath, third.Data.RollbackBackupPath} {
		if !strings.Contains(p, ".pre-migration-") {
			t.Fatalf("backup path %q, want timestamped .pre-migration- generation", p)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("stat backup generation %q: %v", p, err)
		}
	}

	dbPath := handler.cairnlineEmbeddedDatabasePath()
	rec, ok, err := readCairnlineMigrationRecord(dbPath)
	if err != nil || !ok || rec == nil {
		t.Fatalf("migration record ok=%v err=%v, want durable record", ok, err)
	}
	if rec.RollbackBackupPath != third.Data.RollbackBackupPath {
		t.Fatalf("record backup = %q, want latest generation %q", rec.RollbackBackupPath, third.Data.RollbackBackupPath)
	}

	// Rollback restores the latest recorded generation and leaves it on disk.
	rollback := mustRequestJSON[ProjectCairnlineMigrationRollbackResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/migrate/rollback", "")
	if !rollback.Data.Restored || rollback.Data.RestoredFrom != third.Data.RollbackBackupPath {
		t.Fatalf("rollback = %+v, want restored from latest generation %q", rollback.Data, third.Data.RollbackBackupPath)
	}
	// The older generation is intentionally left on disk for manual recovery.
	if _, err := os.Stat(second.Data.RollbackBackupPath); err != nil {
		t.Fatalf("older backup generation removed: %v", err)
	}
	// The restored live database still resolves the seeded project content.
	service := openMigratedCairnlineService(t, dbPath)
	if _, err := service.GetProject(t.Context(), seed.projectID); err != nil {
		t.Fatalf("GetProject from restored DB: %v", err)
	}
}

func timeParseUTC(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed.UTC()
}
