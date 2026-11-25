package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestInlineMetadataFieldsReport(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	repo, _, data := inlineRebaseStore(t)
	workspace := data.Workspace.URLKey
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := repo.MutateWorkspace(t.Context(), workspace, "inline_report.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Labels = append(data.Labels, domain.IssueLabel{ID: "report-dup", Name: "A", CreatedAt: at}, domain.IssueLabel{ID: "report-dup", Name: "B", CreatedAt: at})
		data.IssueSuggestions = append(data.IssueSuggestions, domain.IssueSuggestion{IssueID: "i", Type: "team", State: "active", CreatedAt: at, UpdatedAt: at, StateChangedAt: at},
			domain.IssueSuggestion{ID: strings.Repeat("s", 192), IssueID: "i", Type: "team", State: "active", CreatedAt: at, UpdatedAt: at, StateChangedAt: at})
		if data.UserSettings == nil {
			data.UserSettings = map[string]domain.UserSettings{}
		}
		data.UserSettings[strings.Repeat("u", 200)] = domain.UserSettings{Language: "en"}
		if data.ProjectUpdates == nil {
			data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
		}
		data.ProjectUpdates["report-project"] = []domain.ProjectUpdate{{ID: "u", Body: "a", CreatedAt: at}, {ID: "u", Body: "b", CreatedAt: at}, {ID: "v", Body: "c", CreatedAt: at}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var root []byte
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&root); err != nil {
		t.Fatal(err)
	}
	perParent := func(field string) (map[string]json.RawMessage, error) {
		rows, err := repo.db.QueryContext(t.Context(), `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field=?`, workspace, field)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := map[string]json.RawMessage{}
		for rows.Next() {
			var key string
			var raw []byte
			if err := rows.Scan(&key, &raw); err != nil {
				return nil, err
			}
			result[key] = raw
		}
		return result, rows.Err()
	}
	report, err := InlineMetadataFields(root, perParent)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]InlineMetadataField{}
	for _, field := range report {
		got[field.Field] = field
	}
	labels := got["labels"]
	labels.Entries = 0 // the workspace's default labels
	want := map[string]InlineMetadataField{
		"labels":           {Field: "labels", Kind: "list", Stored: "inline", DuplicateIDs: 1},
		"issueSuggestions": {Field: "issueSuggestions", Kind: "list", Stored: "inline", Entries: 2, EmptyIDs: 1, OverlongIDs: 1},
		"userSettings":     {Field: "userSettings", Kind: "map", Stored: "inline", OverlongIDs: 1},
		"projectUpdates":   {Field: "projectUpdates", Kind: "updates", Stored: "perParent", Entries: 3, DuplicateIDs: 1},
	}
	userSettings := got["userSettings"]
	userSettings.Entries = 0 // also every member's defaults
	got["labels"], got["userSettings"] = labels, userSettings
	if len(got) != len(want) {
		t.Fatalf("reported fields %v", report)
	}
	for name, field := range want {
		if !reflect.DeepEqual(got[name], field) {
			t.Fatalf("%s: %+v, want %+v", name, got[name], field)
		}
	}
	// A root without inline collections reports nothing.
	if clean, err := InlineMetadataFields([]byte(`{"labels":[],"teams":[{"id":"t"}],"_flowCollections":{"labels":"array","teams":"array"}}`), nil); err != nil || len(clean) != 0 {
		t.Fatalf("clean root reported %v %v", clean, err)
	}
}
