package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// inlineMetadataImage is the JSON of fields from an installed copy.
func inlineMetadataImage(t *testing.T, data domain.Bootstrap, fields ...string) string {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	selected := map[string]json.RawMessage{}
	for _, field := range fields {
		selected[field] = all[field]
	}
	encoded, err := json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// inlineMetadataReloadEqual fails unless the installed copy of fields equals
// a store opened on the same database (a load from SQL).
func inlineMetadataReloadEqual(t *testing.T, repo *SQLiteStore, config DatabaseConfig, workspace string, fields ...string) domain.Bootstrap {
	t.Helper()
	installed, ok := repo.BootstrapFor(workspace)
	if !ok {
		t.Fatal("workspace missing")
	}
	reloaded, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	fresh, ok := reloaded.BootstrapFor(workspace)
	if !ok {
		t.Fatal("reloaded workspace missing")
	}
	// Loading fills defaults (label resource types, settings versions); the
	// copy gets the same before comparing.
	normalize(&installed)
	normalize(&fresh)
	if a, b := inlineMetadataImage(t, installed, fields...), inlineMetadataImage(t, fresh, fields...); a != b {
		t.Fatalf("installed copy differs from the SQL load:\ninstalled %s\nreloaded  %s", a, b)
	}
	return fresh
}

// Record writers (team catalog, project records, personal settings) used to
// write rows for a field the root keeps inline, or skip an edit whose row
// was missing. They now persist inline fields whole, like the legacy delta
// writer, or fall back to it.
func TestInlineMetadataRecordWritersPersist(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	// Project record writes need record hydration.
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, config, data := inlineRebaseStore(t)
	workspace := data.Workspace.URLKey
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	long := strings.Repeat("u", 200)
	if err := repo.MutateWorkspace(t.Context(), workspace, "inline_writers.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Labels = append(data.Labels,
			domain.IssueLabel{ID: "inline-label-dup", Name: "First", CreatedAt: at},
			domain.IssueLabel{ID: "inline-label-dup", Name: "Second", CreatedAt: at})
		data.Projects = append(data.Projects,
			domain.Project{ID: "inline-project-dup", Name: "First", TeamIDs: []string{}},
			domain.Project{ID: "inline-project-dup", Name: "Second", TeamIDs: []string{}},
			domain.Project{ID: "inline-project", Name: "Edit me", TeamIDs: []string{}})
		if data.UserSettings == nil {
			data.UserSettings = map[string]domain.UserSettings{}
		}
		data.UserSettings[long] = domain.UserSettings{UserID: long, Language: "en"}
		data.UserSettings["inline-user"] = domain.UserSettings{UserID: "inline-user", Language: "en"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"labels", "projects", "userSettings"} {
		if inline, err := metadataFieldStoredInline(t.Context(), repo.db, repo.dialect, workspace, field); err != nil || !inline {
			t.Fatalf("fixture %s is not stored inline: %v", field, err)
		}
	}

	t.Run("team-catalog", func(t *testing.T) {
		if err := repo.MutateWorkspaceWithAggregate(ContextWithActor(t.Context(), data.Viewer), workspace, "team.created", map[string]any{"name": "Inline"}, func(data *domain.Bootstrap) (string, error) {
			data.Teams = append(data.Teams, domain.Team{ID: "inline-team", Name: "Inline", Key: "INL"})
			data.Labels = append(data.Labels, domain.IssueLabel{ID: "inline-label-new", Name: "New", Scope: "inline-team", CreatedAt: at})
			return "inline-team", nil
		}); err != nil {
			t.Fatal(err)
		}
		fresh := inlineMetadataReloadEqual(t, repo, config, workspace, "teams", "labels")
		names := []string{}
		for _, label := range fresh.Labels {
			if strings.HasPrefix(label.ID, "inline-label") {
				names = append(names, label.Name)
			}
		}
		if strings.Join(names, ",") != "First,Second,New" {
			t.Fatalf("labels after the team write: %v", names)
		}
	})

	t.Run("project-record", func(t *testing.T) {
		ctx := WithProjectRecordSelection(ContextWithActor(t.Context(), data.Viewer), func(domain.Bootstrap) (MetadataRecordScope, bool) {
			return MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: "inline-project"}}}, true
		})
		if err := repo.MutateWorkspace(ctx, workspace, "project.updated", "inline-project", map[string]any{"name": "Edited"}, func(data *domain.Bootstrap) error {
			for index := range data.Projects {
				if data.Projects[index].ID == "inline-project" {
					data.Projects[index].Name = "Edited"
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		fresh := inlineMetadataReloadEqual(t, repo, config, workspace, "projects")
		names := []string{}
		for _, project := range fresh.Projects {
			if strings.HasPrefix(project.ID, "inline-project") {
				names = append(names, project.Name)
			}
		}
		if strings.Join(names, ",") != "First,Second,Edited" {
			t.Fatalf("projects after the record write: %v", names)
		}
	})

	t.Run("user-settings", func(t *testing.T) {
		if err := repo.MutateWorkspace(ContextWithActor(t.Context(), data.Viewer), workspace, "user_settings.updated", "inline-user", map[string]any{"language": "fr"}, func(data *domain.Bootstrap) error {
			settings := data.UserSettings["inline-user"]
			settings.Language = "fr"
			data.UserSettings["inline-user"] = settings
			data.UserSettings["inline-user-new"] = domain.UserSettings{UserID: "inline-user-new", Language: "de"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		fresh := inlineMetadataReloadEqual(t, repo, config, workspace, "userSettings")
		if fresh.UserSettings["inline-user"].Language != "fr" || fresh.UserSettings["inline-user-new"].Language != "de" || fresh.UserSettings[long].Language != "en" {
			t.Fatalf("user settings after the record write: %v", fresh.UserSettings)
		}
	})

	// Writers without the complete field are refused before any row write.
	t.Run("row-writer-refuses", func(t *testing.T) {
		tx, err := repo.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for _, base := range [][]byte{nil, []byte(`{"id":"inline-label-dup","name":"First"}`)} {
			_, write, err := rebaseMetadataRecordWrite(t.Context(), tx, workspace, newIssueReferences(domain.Bootstrap{}), "labels", "inline-label-dup", base, []byte(`{"id":"inline-label-dup","name":"Third"}`))
			if !errors.Is(err, ErrMetadataFieldInline) || write {
				t.Fatalf("row write to an inline field: write=%t err=%v", write, err)
			}
		}
		if err := ensureOAuthMetadataShape(t.Context(), tx, workspace, "labels", "array", ""); !errors.Is(err, ErrMetadataFieldInline) {
			t.Fatalf("shape change over inline entries: %v", err)
		}
	})
}

// Project updates whose IDs repeat within a project are stored per project
// (one row holding the project's update list). Writes used to fail decoding
// that row as a single update; they now persist and reload unchanged.
func TestProjectUpdatesWithDuplicateIDsPersist(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, config, data := inlineRebaseStore(t)
	workspace := data.Workspace.URLKey
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := repo.MutateWorkspace(t.Context(), workspace, "project_updates.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "updates-dup", Name: "Duplicates", TeamIDs: []string{}},
			domain.Project{ID: "updates-unique", Name: "Unique", TeamIDs: []string{}})
		if data.ProjectUpdates == nil {
			data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
		}
		data.ProjectUpdates["updates-dup"] = []domain.ProjectUpdate{
			{ID: "update-a", ProjectID: "updates-dup", Body: "first", Health: "onTrack", CreatedAt: at},
			{ID: "update-twice", ProjectID: "updates-dup", Body: "twice", Health: "onTrack", CreatedAt: at},
			{ID: "update-twice", ProjectID: "updates-dup", Body: "twice again", Health: "onTrack", CreatedAt: at},
		}
		data.ProjectUpdates["updates-unique"] = []domain.ProjectUpdate{{ID: "update-u", ProjectID: "updates-unique", Body: "only", Health: "atRisk", CreatedAt: at}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if inline, err := metadataFieldStoredInline(t.Context(), repo.db, repo.dialect, workspace, "projectUpdates"); err != nil || !inline {
		t.Fatalf("fixture projectUpdates is stored per update: %v", err)
	}
	bodies := func(data domain.Bootstrap) string {
		var parts []string
		for _, id := range []string{"updates-dup", "updates-unique"} {
			for _, update := range data.ProjectUpdates[id] {
				parts = append(parts, update.ID+":"+update.Body)
			}
		}
		return strings.Join(parts, ",")
	}
	t.Run("full-write", func(t *testing.T) {
		if err := repo.MutateWorkspace(t.Context(), workspace, "project_updates.edit", "", nil, func(data *domain.Bootstrap) error {
			updates := append([]domain.ProjectUpdate(nil), data.ProjectUpdates["updates-dup"]...)
			updates[0].Body = "edited"
			updates = append(updates, domain.ProjectUpdate{ID: "update-new", ProjectID: "updates-dup", Body: "new", Health: "onTrack", CreatedAt: at})
			data.ProjectUpdates["updates-dup"] = updates
			unique := append([]domain.ProjectUpdate(nil), data.ProjectUpdates["updates-unique"]...)
			unique[0].Body = "only edited"
			data.ProjectUpdates["updates-unique"] = unique
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		fresh := inlineMetadataReloadEqual(t, repo, config, workspace, "projectUpdates")
		if got := bodies(fresh); got != "update-a:edited,update-twice:twice,update-twice:twice again,update-new:new,update-u:only edited" {
			t.Fatalf("project updates after the full write: %s", got)
		}
	})
	t.Run("project-record", func(t *testing.T) {
		ctx := WithProjectRecordSelection(ContextWithActor(t.Context(), data.Viewer), func(domain.Bootstrap) (MetadataRecordScope, bool) {
			return MetadataRecordScope{Fields: []string{"projectUpdates"}, Write: []RecordKey{{Kind: "projects", ID: "updates-dup"}}}, true
		})
		if err := repo.MutateWorkspace(ctx, workspace, "project.updated", "updates-dup", map[string]any{"name": "Renamed"}, func(data *domain.Bootstrap) error {
			for index := range data.Projects {
				if data.Projects[index].ID == "updates-dup" {
					data.Projects[index].Name = "Renamed"
				}
			}
			updates := append([]domain.ProjectUpdate(nil), data.ProjectUpdates["updates-dup"]...)
			updates[1].Body = "twice edited"
			data.ProjectUpdates["updates-dup"] = updates
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		fresh := inlineMetadataReloadEqual(t, repo, config, workspace, "projects", "projectUpdates")
		if got := bodies(fresh); got != "update-a:edited,update-twice:twice edited,update-twice:twice again,update-new:new,update-u:only edited" {
			t.Fatalf("project updates after the record write: %s", got)
		}
	})
}
