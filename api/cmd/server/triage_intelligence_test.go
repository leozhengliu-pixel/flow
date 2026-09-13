package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestTriageSimilarityRecognizesRelatedTitles(t *testing.T) {
	left := triageTokens("Vehicle marketplace image preview component")
	right := triageTokens("Vehicle marketplace needs a reusable image preview component")
	if score := triageSimilarity(left, right); score < 0.6 {
		t.Fatalf("unexpected similarity: %.3f", score)
	}
}

func TestTriageSimilarityRecognizesCJKContext(t *testing.T) {
	left := triageTokens("车辆市场图片预览组件")
	right := triageTokens("车辆市场需要可复用的图片预览组件")
	if score := triageSimilarity(left, right); score < 0.55 {
		t.Fatalf("unexpected CJK similarity: %.3f", score)
	}
}

func TestTriageIntelligenceSuggestionsAndActions(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	bootstrap := repository.Bootstrap()
	team := bootstrap.Teams[0]
	project := bootstrap.Projects[0]
	label := bootstrap.Labels[0]
	assignee := bootstrap.Users[1]
	backlogState := slices.IndexFunc(bootstrap.States, func(state domain.WorkflowState) bool { return state.Type == "backlog" })
	if backlogState < 0 {
		t.Fatal("test workspace has no backlog state")
	}
	backlogStateID := bootstrap.States[backlogState].ID
	if err := repository.MutateWorkspace(t.Context(), bootstrap.Workspace.URLKey, "test.triage_intelligence_setup", team.ID, nil, func(data *domain.Bootstrap) error {
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
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	existing := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title":       "Vehicle marketplace image preview component",
		"description": "Build a reusable image preview component for vehicle marketplace listings.",
		"teamId":      team.ID,
		"stateId":     backlogStateID,
		"projectId":   project.ID,
		"labelIds":    []string{label.ID},
		"assigneeId":  assignee.ID,
	}, http.StatusCreated)
	persistedBefore := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	probe := domain.Issue{
		ID:          "probe",
		Title:       "Vehicle marketplace needs a reusable image preview component",
		Description: "Vehicle marketplace listings need image preview behavior reused from the existing component.",
		Team:        team,
		State:       bootstrap.States[backlogState],
	}
	if candidates := similarTriageIssues(&persistedBefore, &probe); len(candidates) == 0 || candidates[0].score < 0.5 {
		t.Fatalf("similarity engine did not find the existing issue: %#v", candidates)
	}
	preview := requestJSON[struct {
		Suggestions []triageSuggestionPreview `json:"suggestions"`
	}](t, handler, http.MethodGet, "/api/issue-suggestions?text=Vehicle+marketplace+image+preview&teamId="+team.ID, nil, http.StatusOK)
	if len(preview.Suggestions) == 0 || !slices.ContainsFunc(preview.Suggestions, func(item triageSuggestionPreview) bool {
		return item.Type == "project" && item.ID == project.ID
	}) {
		t.Fatalf("quick suggestions did not infer the related project: %#v", preview.Suggestions)
	}

	created := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title":       "Vehicle marketplace needs a reusable image preview component",
		"description": "Vehicle marketplace listings need image preview behavior reused from the existing component.",
		"teamId":      team.ID,
		"stateId":     backlogStateID,
	}, http.StatusCreated)
	persisted := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	listed := requestJSON[struct {
		IssueID     string                   `json:"issueId"`
		Suggestions []domain.IssueSuggestion `json:"suggestions"`
	}](t, handler, http.MethodGet, "/api/issues/"+created.ID+"/suggestions", nil, http.StatusOK)
	if listed.IssueID != created.ID || len(listed.Suggestions) < 3 {
		t.Fatalf("issue suggestion query returned incomplete data: %#v", listed)
	}
	actualCreated, err := issueByID(&persisted, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	actualCandidates := similarTriageIssues(&persisted, actualCreated)
	if len(actualCandidates) == 0 {
		for _, item := range persisted.Issues {
			t.Logf("issue=%s state=%s title=%q archived=%v", item.Identifier, item.State.Type, item.Title, item.ArchivedAt)
		}
		t.Fatalf("actual created issue did not match the existing issue: %#v", actualCreated)
	}
	createdSuggestions := slices.DeleteFunc(slices.Clone(persisted.IssueSuggestions), func(item domain.IssueSuggestion) bool {
		return item.IssueID != created.ID || item.State != "active"
	})
	if created.SuggestionsGeneratedAt == nil || len(createdSuggestions) < 3 {
		t.Fatalf("triage intelligence did not generate suggestions: issue=%#v state=%s triaged=%v triageEnabled=%v suggestions=%#v", created.SuggestionsGeneratedAt, created.State.Type, created.TriagedAt, persisted.TeamSettings[team.ID].TriageEnabled, createdSuggestions)
	}
	var related domain.IssueSuggestion
	if !slices.ContainsFunc(createdSuggestions, func(item domain.IssueSuggestion) bool {
		if item.Type == "similarIssue" || item.Type == "relatedIssue" {
			related = item
			return true
		}
		return false
	}) {
		t.Fatalf("expected a related or duplicate suggestion: %#v", createdSuggestions)
	}
	accepted := requestJSON[domain.IssueSuggestion](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/suggestions/"+related.ID+"/accept", nil, http.StatusOK)
	if accepted.State != "accepted" {
		t.Fatalf("suggestion was not accepted: %#v", accepted)
	}
	afterAccept := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	updated, err := issueByID(&afterAccept, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(updated.Relations, func(relation domain.IssueRelation) bool {
		return relation.RelatedIssueID == existing.ID && (relation.Type == "duplicate" || relation.Type == "related")
	}) {
		t.Fatalf("accepted suggestion did not update the issue: %#v", updated.Relations)
	}
	target, err := issueByID(&afterAccept, existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	expectedRelationType := "related"
	if related.Type == "similarIssue" {
		expectedRelationType = "duplicate"
	}
	if !slices.ContainsFunc(target.Relations, func(relation domain.IssueRelation) bool {
		return relation.RelatedIssueID == created.ID && relation.Type == expectedRelationType
	}) {
		t.Fatalf("accepted suggestion did not create the inverse relation: %#v", target.Relations)
	}
	var dismissible domain.IssueSuggestion
	if !slices.ContainsFunc(afterAccept.IssueSuggestions, func(item domain.IssueSuggestion) bool {
		if item.IssueID == created.ID && item.State == "active" {
			dismissible = item
			return true
		}
		return false
	}) {
		t.Fatal("expected another active suggestion to dismiss")
	}
	dismissed := requestJSON[domain.IssueSuggestion](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/suggestions/"+dismissible.ID+"/dismiss", nil, http.StatusOK)
	if dismissed.State != "dismissed" {
		t.Fatalf("suggestion was not dismissed: %#v", dismissed)
	}
}

func TestTriageIntelligenceAutoAppliesSettings(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	bootstrap := repository.Bootstrap()
	team := bootstrap.Teams[0]
	project := bootstrap.Projects[0]
	backlogState := slices.IndexFunc(bootstrap.States, func(state domain.WorkflowState) bool { return state.Type == "backlog" })
	if backlogState < 0 {
		t.Fatal("test workspace has no backlog state")
	}
	backlogStateID := bootstrap.States[backlogState].ID
	if err := repository.MutateWorkspace(t.Context(), bootstrap.Workspace.URLKey, "test.triage_auto_setup", team.ID, nil, func(data *domain.Bootstrap) error {
		settings := teamSettings(data, team.ID)
		settings.TriageEnabled = true
		data.TeamSettings[team.ID] = settings
		data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = true
		data.WorkspaceSettings.FeatureSettings.TriageIntelligence.ProjectAction = "auto"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Vehicle marketplace image preview", "teamId": team.ID, "stateId": backlogStateID, "projectId": project.ID,
	}, http.StatusCreated)
	created := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Vehicle marketplace image preview behavior", "teamId": team.ID, "stateId": backlogStateID,
	}, http.StatusCreated)
	if created.Project == nil || created.Project.ID != project.ID {
		t.Fatalf("auto project suggestion was not applied: %#v", created.Project)
	}
}

func TestTriageIntelligenceRegeneratesWhenIssueReturnsToTriage(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	bootstrap := repository.Bootstrap()
	team := bootstrap.Teams[0]
	backlogState := slices.IndexFunc(bootstrap.States, func(state domain.WorkflowState) bool { return state.Type == "backlog" })
	todoState := slices.IndexFunc(bootstrap.States, func(state domain.WorkflowState) bool { return state.Type == "unstarted" })
	if backlogState < 0 || todoState < 0 {
		t.Fatal("test workspace needs backlog and unstarted states")
	}
	backlogStateID := bootstrap.States[backlogState].ID
	if err := repository.MutateWorkspace(t.Context(), bootstrap.Workspace.URLKey, "test.triage_return_setup", team.ID, nil, func(data *domain.Bootstrap) error {
		settings := teamSettings(data, team.ID)
		settings.TriageEnabled = true
		data.TeamSettings[team.ID] = settings
		data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	handler := newHandler(service)
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title":       "Returns to triage vehicle marketplace preview",
		"description": "Reusable vehicle marketplace image preview component.",
		"teamId":      team.ID,
		"stateId":     backlogStateID,
	}, http.StatusCreated)
	created := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title":       "Returns to triage vehicle marketplace image preview",
		"description": "Reusable vehicle marketplace image preview component.",
		"teamId":      team.ID,
		"stateId":     bootstrap.States[todoState].ID,
	}, http.StatusCreated)
	if created.TriagedAt == nil {
		t.Fatal("issue created outside triage should be marked as already triaged")
	}
	updated := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{
		"stateId":         backlogStateID,
		"expectedVersion": created.Version,
	}, http.StatusOK)
	if updated.TriagedAt != nil || updated.SuggestionsGeneratedAt == nil {
		t.Fatalf("returning to triage did not reset and regenerate suggestions: %#v", updated)
	}
	listed := requestJSON[struct {
		Suggestions []domain.IssueSuggestion `json:"suggestions"`
	}](t, handler, http.MethodGet, "/api/issues/"+created.ID+"/suggestions", nil, http.StatusOK)
	if len(listed.Suggestions) == 0 {
		t.Fatal("returning to triage produced no suggestions")
	}
}

func BenchmarkTriageIntelligenceGenerate2000Candidates(b *testing.B) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(b.TempDir(), "flow.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	team := data.Teams[0]
	state := data.States[0]
	for index := 0; index < 2000; index++ {
		data.Issues = append(data.Issues, domain.Issue{
			ID:          fmt.Sprintf("benchmark_issue_%d", index),
			Identifier:  fmt.Sprintf("BEN-%d", index),
			Title:       fmt.Sprintf("Vehicle marketplace image preview component %d", index%100),
			Description: "Reusable image preview behavior for vehicle marketplace listings.",
			Team:        team,
			State:       state,
			Labels:      []domain.IssueLabel{},
			Relations:   []domain.IssueRelation{},
			Attachments: []domain.Attachment{},
		})
	}
	probe := domain.Issue{
		ID:          "benchmark_probe",
		Title:       "Vehicle marketplace needs a reusable image preview component",
		Description: "Vehicle marketplace listings need image preview behavior reused from the existing component.",
		Team:        team,
		State:       state,
		Labels:      []domain.IssueLabel{},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = generateIssueSuggestions(&data, &probe, time.Now().UTC())
	}
}
