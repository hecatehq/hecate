//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestE2E_CairnlineMigrationCutover drives the full one-way migration cutover
// against the real hecate binary: seed native project/work/memory state through
// the public API, migrate into the embedded Cairnline database, verify parity +
// the backend-status cutover gate, prove idempotency and deletion-faithfulness,
// then roll the migration back and confirm the cutover record is cleared.
func TestE2E_CairnlineMigrationCutover(t *testing.T) {
	// Coordination backend must be cairnline so backend-status populates the
	// migration_cutover gate; the embedded connector routes the migration into a
	// local projects.db. Read source is left at the default (auto) and write
	// authority at the default (none) so seeding writes land in the native
	// Hecate stores that the migration reconstructs from.
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_PROJECTS_COORDINATION_BACKEND=cairnline",
		"HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded",
	)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	// 1. Seed native state across several families so parity is meaningful.
	project := postJSONDecodeStatus[e2eMigrationProjectResponse](t, baseURL+"/hecate/v1/projects", fmt.Sprintf(`{
		"name": "cairnline migration e2e %s"
	}`, suffix), http.StatusCreated)
	projectID := project.Data.ID
	if projectID == "" {
		t.Fatal("created project id is empty")
	}

	postJSONDecodeStatus[e2eProjectWorkRoleResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/roles", `{
		"id": "role_migrate",
		"name": "Migration reviewer"
	}`, http.StatusCreated)
	postJSONDecodeStatus[e2eProjectWorkItemResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/work-items", `{
		"id": "work_migrate",
		"title": "Migrate projects to Cairnline",
		"owner_role_id": "role_migrate"
	}`, http.StatusCreated)
	postJSONDecodeStatus[e2eProjectWorkAssignmentResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/work-items/work_migrate/assignments", `{
		"id": "asgn_migrate",
		"role_id": "role_migrate"
	}`, http.StatusCreated)
	memory := postJSONDecodeStatus[e2eProjectMemoryResponse](t, baseURL+"/hecate/v1/projects/"+projectID+"/memory", `{
		"title": "Migration replacement gate",
		"body": "Migration should preserve accepted project memory.",
		"trust_label": "operator-memory",
		"source_kind": "operator"
	}`, http.StatusCreated)
	memoryID := memory.Data.ID
	if memoryID == "" {
		t.Fatal("created memory entry id is empty")
	}

	// 2. Migrate: reconstruct the embedded DB from the native snapshot and swap.
	migrate := postJSONDecode[e2eMigrationResponse](t, baseURL+"/hecate/v1/projects/cairnline/migrate", "")
	if migrate.Object != "project_cairnline_migration" || migrate.Data.Object != "project_cairnline_migration_report" {
		t.Fatalf("migrate envelope = %+v, want project_cairnline_migration report", migrate)
	}
	if !migrate.Data.Verified || !migrate.Data.ParityMatch {
		t.Fatalf("migrate report = %+v, want verified parity match", migrate.Data)
	}
	if migrate.Data.ProjectCount < 1 {
		t.Fatalf("migrate project count = %d, want >= 1", migrate.Data.ProjectCount)
	}
	if len(migrate.Data.Rollback) == 0 {
		t.Fatalf("migrate rollback plan = %+v, want operator rollback steps", migrate.Data.Rollback)
	}

	// 3. Backend status must report the verified cutover and clear the gates.
	assertMigratedVerifiedStatus(t, baseURL)

	// 4. Idempotency: a second migration converges to the same verified state.
	second := postJSONDecode[e2eMigrationResponse](t, baseURL+"/hecate/v1/projects/cairnline/migrate", "")
	if !second.Data.Verified || !second.Data.ParityMatch {
		t.Fatalf("second migrate = %+v, want still verified parity match", second.Data)
	}
	assertMigratedVerifiedStatus(t, baseURL)

	// 5. Deletion-faithfulness: dropping a native record then re-migrating must
	// still verify, proving the reconstructed embedded store drops the absent row.
	deleteJSONExpectStatus(t, baseURL+"/hecate/v1/projects/"+projectID+"/memory/"+memoryID, http.StatusNoContent)
	afterDelete := postJSONDecode[e2eMigrationResponse](t, baseURL+"/hecate/v1/projects/cairnline/migrate", "")
	if !afterDelete.Data.Verified || !afterDelete.Data.ParityMatch {
		t.Fatalf("post-delete migrate = %+v, want verified parity match against the new native snapshot", afterDelete.Data)
	}

	// 6. Rollback: a backup exists after the repeated migrations, so restore it
	// and confirm the cutover record is cleared back to not_migrated.
	rollback := postJSONDecode[e2eMigrationRollbackResponse](t, baseURL+"/hecate/v1/projects/cairnline/migrate/rollback", "")
	if rollback.Object != "project_cairnline_migration_rollback" {
		t.Fatalf("rollback envelope = %+v, want project_cairnline_migration_rollback", rollback)
	}
	if !rollback.Data.Restored || rollback.Data.RestoredFrom == "" {
		t.Fatalf("rollback = %+v, want restored from a backup path", rollback.Data)
	}

	status := getJSON[e2eBackendStatusResponse](t, baseURL+"/hecate/v1/projects/backend-status")
	if status.Data.MigrationCutover == nil || status.Data.MigrationCutover.Status != "not_migrated" {
		t.Fatalf("post-rollback migration cutover = %+v, want not_migrated", status.Data.MigrationCutover)
	}
}

func assertMigratedVerifiedStatus(t *testing.T, baseURL string) {
	t.Helper()
	status := getJSON[e2eBackendStatusResponse](t, baseURL+"/hecate/v1/projects/backend-status")
	cutover := status.Data.MigrationCutover
	if cutover == nil || cutover.Status != "migrated_verified" || !cutover.Migrated || !cutover.Verified || !cutover.ParityMatch {
		t.Fatalf("migration cutover = %+v, want migrated_verified", cutover)
	}
	if containsE2EString(status.Data.WriteAdapterGaps, "migration-cutover") {
		t.Fatalf("write adapter gaps = %+v, want migration-cutover cleared after verified migration", status.Data.WriteAdapterGaps)
	}
	if containsE2EString(status.Data.MigrationBlockers, "migration-cutover") {
		t.Fatalf("migration blockers = %+v, want migration-cutover cleared after verified migration", status.Data.MigrationBlockers)
	}
	point := findE2EWriteSwitchpoint(status.Data.WriteSwitchpoints, "migration-cutover")
	if point == nil || point.CairnlineState != "migrated_verified" {
		t.Fatalf("migration switchpoint = %+v, want migrated_verified cairnline state", point)
	}
}

func deleteJSONExpectStatus(t *testing.T, url string, status int) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("new DELETE request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		t.Fatalf("DELETE %s: HTTP %d, want %d -- body: %s", url, resp.StatusCode, status, readBody(t, resp))
	}
}

func containsE2EString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func findE2EWriteSwitchpoint(points []e2eWriteSwitchpoint, name string) *e2eWriteSwitchpoint {
	for i := range points {
		if points[i].Name == name {
			return &points[i]
		}
	}
	return nil
}

type e2eMigrationProjectResponse struct {
	Object string `json:"object"`
	Data   struct {
		ID string `json:"id"`
	} `json:"data"`
}

type e2eProjectMemoryResponse struct {
	Object string `json:"object"`
	Data   struct {
		ID string `json:"id"`
	} `json:"data"`
}

type e2eMigrationResponse struct {
	Object string `json:"object"`
	Data   struct {
		Object          string   `json:"object"`
		Verified        bool     `json:"verified"`
		ParityMatch     bool     `json:"parity_match"`
		Target          string   `json:"target"`
		SourceAuthority []string `json:"source_authority"`
		ProjectCount    int      `json:"project_count"`
		Rollback        []string `json:"rollback"`
	} `json:"data"`
}

type e2eMigrationRollbackResponse struct {
	Object string `json:"object"`
	Data   struct {
		Restored     bool   `json:"restored"`
		Reason       string `json:"reason"`
		RestoredFrom string `json:"restored_from"`
		Target       string `json:"target"`
	} `json:"data"`
}

type e2eWriteSwitchpoint struct {
	Name           string `json:"name"`
	CairnlineState string `json:"cairnline_state"`
	LiveMirror     bool   `json:"live_mirror"`
}

type e2eBackendStatusResponse struct {
	Object string `json:"object"`
	Data   struct {
		WriteAdapterGaps  []string              `json:"write_adapter_gaps"`
		MigrationBlockers []string              `json:"migration_blockers"`
		WriteSwitchpoints []e2eWriteSwitchpoint `json:"write_switchpoints"`
		MigrationCutover  *struct {
			Status      string `json:"status"`
			Migrated    bool   `json:"migrated"`
			Verified    bool   `json:"verified"`
			ParityMatch bool   `json:"parity_match"`
		} `json:"migration_cutover"`
	} `json:"data"`
}
