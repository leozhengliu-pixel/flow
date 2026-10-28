package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Triage suggestions are indexed as one issue_attribute_records row per
// target ("suggestedLabel:label_1761600000000000000"), which is longer than
// the column's original 32 characters. Generating and storing them works on
// SQLite and on the MySQL or Postgres database named by
// FLOW_TEST_DATABASE_DRIVER/URL, and each target stays filterable, including
// one whose id is longer than the column (stored as a prefix plus digest).
func TestTriageSuggestionsWithLongTargetIdsAreStored(t *testing.T) {
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testTriageSuggestionsWithLongTargetIds(t, repo) })
	}
}

func testTriageSuggestionsWithLongTargetIds(t *testing.T, repo *store.SQLiteStore) {
	bootstrap := repo.Bootstrap()
	workspace := bootstrap.Workspace.URLKey
	team := bootstrap.Teams[0]
	backlog := slices.IndexFunc(bootstrap.States, func(state domain.WorkflowState) bool {
		return state.Type == "backlog" && state.TeamID == team.ID
	})
	if backlog < 0 {
		backlog = slices.IndexFunc(bootstrap.States, func(state domain.WorkflowState) bool { return state.Type == "backlog" })
	}
	if backlog < 0 {
		t.Fatal("no backlog state")
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	// An imported project keeps its source id; an over-long one must still be
	// indexed without failing the write.
	importedProject := "project_import_" + suffix + "_" + strings.Repeat("0123456789abcdef", 12)
	if err := repo.MutateWorkspace(t.Context(), workspace, "test.triage_long_ids_setup", team.ID, nil, func(data *domain.Bootstrap) error {
		settings := teamSettings(data, team.ID)
		settings.TriageEnabled = true
		data.TeamSettings[team.ID] = settings
		if data.WorkspaceSettings.FeatureFlags == nil {
			data.WorkspaceSettings.FeatureFlags = map[string]bool{}
		}
		data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = true
		data.WorkspaceSettings.FeatureSettings.TriageIntelligence = domain.TriageIntelligenceSettings{
			AssigneeAction: "suggest", ProjectAction: "suggest", LabelAction: "suggest",
			TeamAction: "suggest", DuplicateAction: "suggest", RelatedAction: "suggest",
		}
		now := time.Now().UTC()
		data.Projects = append(data.Projects, domain.Project{ID: importedProject, SlugID: "imported-" + suffix, Name: "Imported " + suffix, TeamIDs: []string{team.ID}, CreatedAt: now, UpdatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	label := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Checkout " + suffix}, http.StatusCreated)
	if len("suggestedLabel:"+label.ID) <= 32 {
		t.Fatalf("label id %q does not exercise the old 32-character field", label.ID)
	}
	other := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Checkout payment fails " + suffix, "teamId": team.ID, "labelIds": []string{label.ID}}, http.StatusCreated)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Checkout payment fails again " + suffix, "teamId": team.ID, "stateId": bootstrap.States[backlog].ID}, http.StatusCreated)

	// The generator's own path: refresh produces and stores suggestions.
	requestJSON[[]domain.IssueSuggestion](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/suggestions/refresh", nil, http.StatusOK)

	now := time.Now().UTC()
	if err := repo.MutateWorkspace(store.WithIssueRecordMutations(t.Context(), issue.ID), workspace, "test.triage_long_ids", issue.ID, nil, func(data *domain.Bootstrap) error {
		target, err := issueByID(data, issue.ID)
		if err != nil {
			return err
		}
		storeTriageSuggestions(data, target, []domain.IssueSuggestion{
			{ID: "s-label-" + suffix, IssueID: issue.ID, Type: "label", State: "active", SuggestedLabelID: label.ID, StateChangedAt: now, CreatedAt: now, UpdatedAt: now},
			{ID: "s-project-" + suffix, IssueID: issue.ID, Type: "project", State: "active", SuggestedProjectID: importedProject, StateChangedAt: now, CreatedAt: now, UpdatedAt: now},
			{ID: "s-duplicate-" + suffix, IssueID: issue.ID, Type: "similarIssue", State: "active", SuggestedIssueID: other.ID, StateChangedAt: now, CreatedAt: now, UpdatedAt: now},
		}, now, triageSourceHeuristic, "")
		return nil
	}); err != nil {
		t.Fatalf("store suggestions with long target ids: %v", err)
	}
	record := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+issue.ID, nil, http.StatusOK)
	if !slices.Contains(record.SuggestedLabelIDs, label.ID) || !slices.Contains(record.SuggestedProjectIDs, importedProject) {
		t.Fatalf("suggestion targets not stored: labels=%v projects=%v", record.SuggestedLabelIDs, record.SuggestedProjectIDs)
	}
	matches := func(field string) bool {
		raw, _ := json.Marshal(map[string]any{"field": field, "operator": "isNotEmpty"})
		paged := requestJSON[store.IssueRecordPage](t, handler, http.MethodGet, "/api/issue-records?limit=200&filter="+url.QueryEscape(string(raw)), nil, http.StatusOK)
		return slices.ContainsFunc(paged.Items, func(item domain.Issue) bool { return item.ID == issue.ID })
	}
	for _, field := range []string{"suggestedLabel:" + label.ID, "suggestedProject:" + importedProject, "suggestedDuplicate:" + other.ID, "suggested:label", "suggested:project"} {
		if !matches(field) {
			t.Fatalf("%s did not match the triage issue", field)
		}
	}
	if matches("suggestedProject:" + importedProject + "x") {
		t.Fatal("a different long project id matched")
	}
}
