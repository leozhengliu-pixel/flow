package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// inlineRebaseStore opens the configured test database (a temporary SQLite
// file by default) with an owned account and workspace, and returns its
// configuration so a second store can load the workspace from SQL.
func inlineRebaseStore(t *testing.T) (*SQLiteStore, DatabaseConfig, domain.Bootstrap) {
	t.Helper()
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	config := DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4}
	if driver == "" {
		config.Driver, config.Path = "sqlite", filepath.Join(t.TempDir(), "inline.db")
	} else if driver == "sqlite" {
		config.Path = databaseURL
	}
	repo, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	user, token, err := repo.Register(t.Context(), "Inline owner", idgen.New("inline-rebase-")+"@example.test", "inline-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	workspace := ""
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if workspace != "" {
			if err := repo.DeleteWorkspace(ctx, workspace); err != nil {
				t.Errorf("clean up owned workspace: %v", err)
			}
		}
		if err := repo.EraseAccount(ctx, user.ID); err != nil {
			t.Errorf("clean up owned account: %v", err)
		}
	})
	if err := repo.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	data, err := repo.CreateWorkspace(ContextWithActor(t.Context(), user), "Inline rebase", idgen.New("inline-rebase-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	workspace = data.Workspace.URLKey
	return repo, config, data
}

// A collection whose entries lack unique IDs is kept inline in the root
// document, without record rows. Editing one of its entries must persist the
// edit and keep the entry: the rebase onto record rows used to read the
// missing row as a deletion by another write, dropping the entry from the
// installed copy and the edit from the database.
func TestInlineMetadataFieldEditsSurviveReload(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	repo, config, data := inlineRebaseStore(t)
	{
		{
			workspace := data.Workspace.URLKey
			if err := repo.MutateWorkspace(t.Context(), workspace, "inline_rebase.seed", "", nil, func(data *domain.Bootstrap) error {
				for _, id := range []string{"inline-s-a", "inline-s-duplicate", "inline-s-duplicate", "inline-s-b"} {
					data.IssueSuggestions = append(data.IssueSuggestions, domain.IssueSuggestion{ID: id, IssueID: "inline-issue", Type: "team",
						State: "active", SuggestedTeamID: "inline-team", StateChangedAt: at, CreatedAt: at, UpdatedAt: at})
				}
				if data.CycleSettings == nil {
					data.CycleSettings = map[string]domain.CycleSettings{}
				}
				// A key longer than a row key keeps the whole map inline.
				data.CycleSettings[strings.Repeat("k", 200)] = domain.CycleSettings{DurationWeeks: 1}
				data.CycleSettings["inline-team"] = domain.CycleSettings{DurationWeeks: 2}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var shapes map[string]string
			var raw []byte
			if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var root map[string]json.RawMessage
			if err := json.Unmarshal(raw, &root); err != nil {
				t.Fatal(err)
			}
			_ = json.Unmarshal(root[metadataCollectionsKey], &shapes)
			if shapes["issueSuggestions"] == "array" || shapes["cycleSettings"] == "map" {
				t.Fatalf("fixture collections are record-backed: %v", shapes)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "inline_rebase.edit", "", nil, func(data *domain.Bootstrap) error {
				for index := range data.IssueSuggestions {
					if data.IssueSuggestions[index].ID == "inline-s-a" {
						data.IssueSuggestions[index].State, data.IssueSuggestions[index].UpdatedAt = "accepted", at.Add(time.Hour)
					}
				}
				data.CycleSettings["inline-team"] = domain.CycleSettings{DurationWeeks: 3}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
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
			for _, view := range []struct {
				name string
				data domain.Bootstrap
			}{{"installed", installed}, {"reloaded", fresh}} {
				suggestions := []string{}
				for _, item := range view.data.IssueSuggestions {
					if item.IssueID == "inline-issue" {
						suggestions = append(suggestions, item.ID+":"+item.State)
					}
				}
				if want := []string{"inline-s-a:accepted", "inline-s-duplicate:active", "inline-s-duplicate:active", "inline-s-b:active"}; !reflect.DeepEqual(suggestions, want) {
					t.Fatalf("%s suggestions %v, want %v", view.name, suggestions, want)
				}
				if settings, ok := view.data.CycleSettings["inline-team"]; !ok || settings.DurationWeeks != 3 || view.data.CycleSettings[strings.Repeat("k", 200)].DurationWeeks != 1 {
					t.Fatalf("%s cycle settings %v (present %t)", view.name, settings, ok)
				}
			}
		}
	}
}
