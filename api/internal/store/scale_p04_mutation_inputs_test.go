package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP04Store(t *testing.T) *SQLiteStore {
	t.Helper()
	var repo *SQLiteStore
	var err error
	if driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL"); driver != "" && url != "" {
		repo, err = OpenDatabase(DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4})
	} else {
		repo, err = OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	return repo
}

func scaleP04Hash(t *testing.T, data domain.Bootstrap) [32]byte {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(raw)
}

func TestScaleP04MetadataFieldsDifferential(t *testing.T) {
	repo := scaleP04Store(t)
	data := repo.Bootstrap()
	data.NextIssueNumber = 567
	data.Settings = map[string]any{"nested": map[string]any{"list": []any{"value"}}}
	for _, fields := range [][]string{nil, {"settings"}, issueCallbackFields, discussionCallbackFields} {
		t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
		legacy := metadataFieldSnapshot(data, fields)
		t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
		narrow := metadataFieldSnapshot(data, fields)
		if !reflect.DeepEqual(legacy, narrow) {
			t.Fatalf("field snapshot differs for %v", fields)
		}
		assertNoSharedReferences(t, "metadata", reflect.ValueOf(legacy), reflect.ValueOf(narrow))
		for _, field := range persistedMetadataFields() {
			if slices.Contains(fields, field.name) || field.name == "workspace" || field.name == "viewer" {
				assertNoSharedReferences(t, field.name, reflect.ValueOf(data).Field(field.index), reflect.ValueOf(narrow).Field(field.index))
			}
		}
	}
}

func TestScaleP04ActorAttributionIsOwned(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
	actor := domain.User{ID: "actor", AppScopes: []string{"write"}, Via: &domain.ActorBot{}}
	owned := mutationInputActor(actor)
	if !reflect.DeepEqual(owned, actor) || owned.Via == actor.Via {
		t.Fatal("request attribution was dropped or shared")
	}
	owned.AppScopes[0] = "changed"
	if actor.AppScopes[0] != "write" {
		t.Fatal("actor scopes were shared")
	}
}

func TestScaleP04TeamsRetainLegacyInputs(t *testing.T) {
	data := bulkBootstrap(3)
	for _, mode := range []string{"off", "on"} {
		t.Setenv("FLOW_NARROW_MUTATION_INPUTS", mode)
		for _, event := range []string{"team.updated", "team.created", "team.settings_updated"} {
			input, fields := teamCallbackInput(data, event, nil)
			if len(fields) != 0 || &input.Teams[0] != &data.Teams[0] || reflect.ValueOf(input.TeamSettings).Pointer() != reflect.ValueOf(data.TeamSettings).Pointer() {
				t.Fatalf("%s %s did not retain legacy inputs", mode, event)
			}
		}
	}
}

func TestScaleP04CallbacksOwnSnapshots(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
	repo := scaleP04Store(t)
	fixture := repo.Bootstrap()
	key, issue := fixture.Workspace.URLKey, fixture.Issues[0]
	abort := errors.New("callback abort")
	for _, kind := range []string{"issue", "issueFallback", "comment", "scoped"} {
		t.Run(kind, func(t *testing.T) {
			stored, _ := repo.storedWorkspace(key)
			hash := scaleP04Hash(t, stored)
			check := func(data *domain.Bootstrap) error {
				source, input := reflect.ValueOf(stored), reflect.ValueOf(*data)
				for _, field := range persistedMetadataFields() {
					if field.name != "projects" && !input.Field(field.index).IsZero() {
						assertNoSharedReferences(t, field.name, source.Field(field.index), input.Field(field.index))
					}
				}
				for _, project := range data.Projects {
					index := slices.IndexFunc(stored.Projects, func(original domain.Project) bool { return original.ID == project.ID })
					if index < 0 {
						t.Fatal("unknown callback project")
					}
					assertNoSharedReferences(t, "project", reflect.ValueOf(stored.Projects[index]), reflect.ValueOf(project))
				}
				// A concurrent reader retains the committed copy while the callback
				// mutates its own nested containers. The race run checks both sides.
				read := make(chan [32]byte, 1)
				go func() { read <- scaleP04Hash(t, stored) }()
				data.Teams[0].Name = "uncommitted"
				data.TeamSettings[data.Teams[0].ID] = domain.TeamSettings{TeamID: "uncommitted"}
				data.Users[0].Name = "uncommitted"
				if len(data.Projects) > 0 {
					data.Projects[0].Name = "uncommitted"
				}
				if got := <-read; got != hash || scaleP04Hash(t, stored) != hash {
					t.Fatal("callback changed the committed snapshot")
				}
				return abort
			}
			var err error
			switch kind {
			case "issue", "issueFallback":
				var payload any = domain.IssueUpdateInput{}
				if kind == "issueFallback" {
					payload = map[string]string{"automation": "test"}
				}
				_, err = repo.UpdateIssueRecord(t.Context(), key, issue.ID, nil, IssueMutationScope{Payload: payload}, func(data *domain.Bootstrap, _ *domain.Issue) error { return check(data) })
			case "comment":
				err = repo.mutateIssueScope(WithIssueDiscussionMutation(t.Context(), issue.ID, ""), key, "comment.created", nil, func(data *domain.Bootstrap) (string, error) { return issue.ID, check(data) })
			case "scoped":
				err = repo.mutateScoped(WithIssueDiscussionMutation(t.Context(), issue.ID, ""), key, "comment.created", nil, MutationScope{IssueIDs: []string{issue.ID}}, func(data *domain.Bootstrap) (string, error) { return issue.ID, check(data) })
			}
			if !errors.Is(err, abort) {
				t.Fatalf("error = %v, want callback abort", err)
			}
			committed, _ := repo.storedWorkspace(key)
			if scaleP04Hash(t, committed) != hash {
				t.Fatal("failed callback changed metadata")
			}
		})
	}
}

func TestScaleP04MixedReplicaWrites(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			key, template := peerWorkspace(t, pair[0])
			issue := createPeerIssue(t, pair[0], key, idgen.New("scale_p04_"), template)
			for index, mode := range []string{"off", "on", "off", "on"} {
				t.Setenv("FLOW_NARROW_MUTATION_INPUTS", mode)
				before, err := pair[index%2].IssueRecord(t.Context(), key, issue.ID)
				if err != nil {
					t.Fatal(err)
				}
				title := mode + string(rune('a'+index))
				updated, err := pair[index%2].UpdateIssueRecord(context.Background(), key, issue.ID, &before.Version, IssueMutationScope{Payload: domain.IssueUpdateInput{Title: &title}}, func(_ *domain.Bootstrap, target *domain.Issue) error {
					target.Title = title
					return nil
				})
				if err != nil || updated.Title != title || updated.Version != before.Version+1 {
					t.Fatalf("%s update = %+v, err=%v", mode, updated, err)
				}
			}
		})
	}
}

func TestScaleP04FlagDefaultsOnWithLegacyRollback(t *testing.T) {
	data := domain.Bootstrap{Projects: []domain.Project{{ID: "project"}}, Trash: []domain.TrashEntry{{ID: "trash"}}}
	for _, mode := range []string{"off", "false", "0", "shadow", "invalid"} {
		t.Setenv("FLOW_NARROW_MUTATION_INPUTS", mode)
		if narrowMutationInputs() {
			t.Fatalf("mode %q enabled narrow inputs", mode)
		}
		input := issueUpdateMetadata(data)
		if &input.Projects[0] != &data.Projects[0] || &input.Trash[0] != &data.Trash[0] {
			t.Fatalf("mode %q changed legacy issue inputs", mode)
		}
	}
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "")
	if !narrowMutationInputs() {
		t.Fatal("narrow inputs are not on by default")
	}
	if !verifyNarrowMutationInputs() {
		t.Fatal("test callbacks must always be verified")
	}
	input := issueUpdateMetadata(data)
	assertNoSharedReferences(t, "enabled", reflect.ValueOf(data), reflect.ValueOf(input))
	restore := ForceFullMutationsForTesting(true)
	defer restore()
	if narrowMutationInputs() {
		t.Fatal("full mutation oracle did not disable narrow inputs")
	}
}

func TestScaleP04CommittedWritesKeepSnapshots(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
	repo := scaleP04Store(t)
	fixture := repo.Bootstrap()
	key, issueID := fixture.Workspace.URLKey, fixture.Issues[0].ID
	for _, kind := range []string{"issue", "comment", "scoped"} {
		t.Run(kind, func(t *testing.T) {
			stored, _ := repo.storedWorkspace(key)
			hash := scaleP04Hash(t, stored)
			var err error
			comment := func(data *domain.Bootstrap) (string, error) {
				data.Comments[issueID] = append(data.Comments[issueID], domain.Comment{ID: "scale-p04-" + kind, Body: kind, User: data.Viewer})
				data.ThreadSubscriptions = append(data.ThreadSubscriptions, domain.ThreadSubscription{ID: "scale-p04-thread-" + kind, IssueID: issueID, CommentID: "scale-p04-" + kind, UserID: data.Viewer.ID, State: "subscribed"})
				return issueID, nil
			}
			switch kind {
			case "issue":
				title := "P04 committed title"
				_, err = repo.UpdateIssueRecord(t.Context(), key, issueID, nil, IssueMutationScope{Payload: domain.IssueUpdateInput{Title: &title}}, func(_ *domain.Bootstrap, issue *domain.Issue) error { issue.Title = title; return nil })
			case "comment":
				err = repo.mutateIssueScope(WithIssueDiscussionMutation(t.Context(), issueID, ""), key, "comment.created", nil, comment)
			case "scoped":
				err = repo.mutateScoped(WithIssueDiscussionMutation(t.Context(), issueID, ""), key, "comment.created", nil, MutationScope{IssueIDs: []string{issueID}}, comment)
			}
			if err != nil {
				t.Fatal(err)
			}
			if scaleP04Hash(t, stored) != hash {
				t.Fatal("committed write changed the prior snapshot")
			}
		})
	}
}
