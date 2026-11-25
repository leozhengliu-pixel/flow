package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// A successful bounded replacement must preserve both existing installed
// dispositions; the persisted rows alone do not identify the old rewrite.
func TestScaleP21SW26ResourceSlugWarmColdBeforeImage(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	projectID, initiativeID := idgen.New("project-sw26-slug-"), idgen.New("initiative-sw26-slug-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects", "initiatives"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = []domain.Project{{ID: projectID, Name: "Project\x00Name", SlugID: "project", Resources: []domain.ProjectResource{{ID: "resource", PinnedTeamIDs: nil}}}}
		data.Initiatives = []domain.Initiative{{ID: initiativeID, Name: "Initiative", SlugID: "initiative"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read := func(field, id string) []byte {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	projectRaw, initiativeRaw := read("projects", projectID), read("initiatives", initiativeID)
	if projects, initiatives, err := f.migrateWorkspaceResourceSlugs(t.Context(), workspace); err != nil || projects != 1 || initiatives != 1 {
		t.Fatalf("warm migration: projects=%d initiatives=%d error=%v", projects, initiatives, err)
	}
	warmProject, warmInitiative := read("projects", projectID), read("initiatives", initiativeID)
	for _, row := range []struct {
		field, id string
		raw       []byte
	}{{"projects", projectID, projectRaw}, {"initiatives", initiativeID, initiativeRaw}} {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`, row.raw, workspace, row.field, row.id); err != nil {
			t.Fatal(err)
		}
	}
	f.dropMetadataCache(t.Context(), workspace)
	f.mu.Lock()
	f.forgetCopyRevisionLocked(workspace)
	f.mu.Unlock()
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(projectRaw, read("projects", projectID)) || !reflect.DeepEqual(initiativeRaw, read("initiatives", initiativeID)) {
		t.Fatal("reload changed the canonical baseline before the migration")
	}
	if projects, initiatives, err := f.migrateWorkspaceResourceSlugs(t.Context(), workspace); err != nil || projects != 1 || initiatives != 1 {
		t.Fatalf("cold migration: projects=%d initiatives=%d error=%v", projects, initiatives, err)
	}
	coldProject, coldInitiative := read("projects", projectID), read("initiatives", initiativeID)
	var warmP, coldP domain.Project
	if err := json.Unmarshal(warmProject, &warmP); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(coldProject, &coldP); err != nil {
		t.Fatal(err)
	}
	suffix := domain.DerivedSlugSuffix(projectID)
	if warmP.SlugID != "project-name-"+suffix || coldP.SlugID != "projectname-"+suffix {
		t.Fatalf("fixture failed to expose legacy installed-name slug derivation: warm=%q cold=%q", warmP.SlugID, coldP.SlugID)
	}
	var warm, cold domain.Initiative
	if err := json.Unmarshal(warmInitiative, &warm); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(coldInitiative, &cold); err != nil {
		t.Fatal(err)
	}
	t.Logf("same canonical input, legacy warm/cold output differs: warmSlug=%q coldSlug=%q; creator/default fields are rebased by the existing writer", warmP.SlugID, coldP.SlugID)
	var one int
	if err := f.db.QueryRowContext(context.Background(), "SELECT 1").Scan(&one); err != nil {
		t.Fatal(err)
	}
}
