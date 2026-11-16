package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW185TriageAccumulatedCandidateOrder(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TriageWriterSetup(t)
	var seed domain.Bootstrap
	if err := json.Unmarshal(f.baseline, &seed); err != nil {
		t.Fatal(err)
	}
	scoped := f.triage
	scoped.Title, scoped.Description = "Marketplace image preview", "Image preview of marketplace listings."
	scoped.State.Type, scoped.TriagedAt = "backlog", nil
	ghost := scoped
	ghost.ID, ghost.Identifier, ghost.Number = "sw185-vanished", "VANISHED-900", 900
	seed.Issues = []domain.Issue{scoped}
	seed.WorkspaceSettings.FeatureSettings.TriageIntelligence.TeamAction = "hide"
	seed.WorkspaceSettings.FeatureSettings.TriageIntelligence.DuplicateAction = "auto"
	seed.WorkspaceSettings.FeatureSettings.TriageIntelligence.RelatedAction = "auto"
	seed.WorkspaceSettings.FeatureSettings.TriageIntelligence.LabelAction = "auto"
	ids := []string{scoped.ID, ghost.ID}
	sets := map[string][]domain.Issue{scoped.ID: {ghost, ghost}, ghost.ID: {scoped}}
	ctx, closeReads := beginMetadataOutputReads(context.Background())
	defer closeReads()
	source, err := f.probe.BeginIssueSuggestionCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for _, id := range ids {
		if err := source.Start(ctx, id); err != nil {
			t.Fatal(err)
		}
		for _, issue := range sets[id] {
			if err := source.Append(ctx, issue); err != nil {
				t.Fatal(err)
			}
		}
		if err := source.Finish(); err != nil {
			t.Fatal(err)
		}
	}
	var normalized []string
	for _, bounded := range []bool{false, true} {
		raw, err := json.Marshal(seed)
		if err != nil {
			t.Fatal(err)
		}
		var data domain.Bootstrap
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		var updated domain.Issue
		var results []domain.Issue
		generated := false
		mutate := generateTriageMutation(ids, sets, &updated, &results, &generated)
		if bounded {
			mutate = generateBoundedTriageMutation(ctx, ids, source, &updated, &results, &generated)
		}
		started := time.Now().UTC()
		if err := mutate(&data); err != nil {
			t.Fatal(err)
		}
		if !generated || len(results) != 2 || results[1].ID != ghost.ID {
			t.Fatalf("bounded=%t lost an issue supplied by an earlier candidate snapshot", bounded)
		}
		normalized = append(normalized, scaleP21TriageNormalize(t, []any{data, updated, results}, started))
	}
	if normalized[0] != normalized[1] {
		t.Fatal("streamed candidate encounter order changed legacy results", scaleP21M2Diff(normalized[0], normalized[1]))
	}
}

func TestScaleP21SW185TriageScoringProjection(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TriageWriterSetup(t)
	var data domain.Bootstrap
	if err := json.Unmarshal(f.baseline, &data); err != nil {
		t.Fatal(err)
	}
	settings := &data.WorkspaceSettings.FeatureSettings.TriageIntelligence
	settings.AssigneeAction, settings.ProjectAction, settings.LabelAction = "suggest", "suggest", "suggest"
	settings.DuplicateAction, settings.RelatedAction, settings.TeamAction = "suggest", "suggest", "suggest"
	target := f.triage
	target.Project, target.Assignee, target.Labels = nil, nil, nil
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	full := []triageSuggestionCandidate{
		{issue: domain.Issue{
			ID: "sw185-projection-a", Identifier: "", Title: "Title used when the identifier is empty", UpdatedAt: clock,
			Description: strings.Repeat("Unneeded after numeric scoring. ", 10000), Team: domain.Team{ID: f.private, Name: "Unneeded team name"},
			Project:  &domain.ProjectSummary{ID: "triage-p-public", Name: "Unneeded project name"},
			Assignee: &domain.User{ID: f.data.Viewer.ID, Name: "Unneeded actor name"},
			Labels:   []domain.IssueLabel{{ID: "triage-l-public", Description: "Unneeded label body"}},
		}, score: 0.8, titleScore: 0.7, descriptionScore: 0.6},
		{issue: domain.Issue{ID: "sw185-projection-b", Identifier: "PUB-999", Title: "Hidden by the identifier", UpdatedAt: clock, Team: domain.Team{ID: f.public}}, score: 0.5},
	}
	owner, err := f.probe.BeginIssueSuggestionCandidates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	scores, err := owner.BeginScores(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer scores.Close()
	projected := slices.Clone(full)
	for index := range projected {
		projected[index].issue, err = scores.Project(t.Context(), projected[index].issue)
		if err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now().UTC()
	before := generateIssueSuggestionsWithCandidates(&data, &target, clock, full)
	after := generateIssueSuggestionsWithCandidates(&data, &target, clock, projected)
	a, b := scaleP21TriageNormalize(t, before, started), scaleP21TriageNormalize(t, after, started)
	if a != b {
		t.Fatal("scoring projection changed downstream suggestions", scaleP21M2Diff(a, b))
	}
}

func TestScaleP21SW185TriageLargeGenerationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TriageWriterSetup(t)
	workspace := f.data.Workspace.URLKey
	for _, count := range []int{350, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ids := make([]string, count)
			for index := range ids {
				ids[index] = fmt.Sprintf("sw185-issue-%04d", index)
			}
			f.prepare = func(t *testing.T) {
				if err := f.repository.MutateWorkspace(t.Context(), workspace, "scale_p21.sw185_seed", "", nil, func(data *domain.Bootstrap) error {
					settings := &data.WorkspaceSettings.FeatureSettings.TriageIntelligence
					settings.AssigneeAction, settings.ProjectAction, settings.LabelAction = "suggest", "suggest", "suggest"
					settings.DuplicateAction, settings.RelatedAction, settings.TeamAction = "hide", "hide", "hide"
					data.Labels = append(data.Labels, domain.IssueLabel{ID: "sw185-label", Name: "Marketplace", Scope: "workspace"})
					for index, id := range ids {
						issue := f.triage
						issue.ID, issue.Identifier = id, fmt.Sprintf("SW185-%d", index+1)
						issue.Title, issue.Description = fmt.Sprintf("Marketplace %d", index), ""
						issue.SuggestionsGeneratedAt, issue.TriagedAt = nil, nil
						issue.State = domain.WorkflowState{ID: "triage-backlog", Name: "Triage", Type: "backlog", TeamID: f.public}
						data.Issues = append(data.Issues, issue)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			defer func() { f.prepare = nil }()
			outcomes := f.differential(t, "large generation", func(service *server) *httptest.ResponseRecorder {
				r := f.literalRequest(f.data.Viewer, nil, nil)
				queries, peakWork := 0, 0
				r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(string) {
					queries++
					peakWork = max(peakWork, service.store.MetadataWorkUsedForTesting())
				}))
				r, closeOutput := beginRequestResourceReads(r)
				defer closeOutput()
				started := time.Now()
				results, err := service.generateTriageIntelligenceForIssues(r.Context(), workspace, ids)
				t.Logf("bounded=%t issues=%d SQL=%d SQL_sampled_peak_admitted_bytes=%d elapsed=%s", service.store.BoundedMetadataEnabled(), count, queries, peakWork, time.Since(started))
				if err != nil {
					t.Fatalf("generation bounded=%t count=%d: %v", service.store.BoundedMetadataEnabled(), count, err)
				}
				if len(results) != count {
					t.Fatalf("generated %d issues, want %d", len(results), count)
				}
				var suggestions []domain.IssueSuggestion
				if service.store.BoundedMetadataEnabled() {
					read, found, err := service.store.BeginInstalledMetadataRead(t.Context(), workspace)
					if err != nil || !found {
						t.Fatal("read generated suggestions", err)
					}
					var raw strings.Builder
					err = read.StreamFieldJSON(t.Context(), "issueSuggestions", &raw)
					read.Close()
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal([]byte(raw.String()), &suggestions); err != nil {
						t.Fatal(err)
					}
				} else {
					data, found := service.store.WorkspaceMetadataForRead(t.Context(), workspace)
					if !found {
						t.Fatal("generated suggestions workspace missing")
					}
					suggestions = data.IssueSuggestions
				}
				if len(suggestions) < count {
					t.Fatalf("generated %d suggestions, want at least %d", len(suggestions), count)
				}
				rootRows := f.rows(t, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace)
				if len(rootRows) != 1 {
					t.Fatal("generated suggestions root missing")
				}
				var persisted struct {
					Suggestions []domain.IssueSuggestion `json:"issueSuggestions"`
					Shapes      map[string]string        `json:"_flowCollections"`
				}
				if err := json.Unmarshal([]byte(rootRows[0][0].(string)), &persisted); err != nil {
					t.Fatal(err)
				}
				if persisted.Shapes["issueSuggestions"] != "array" {
					expected, _ := json.Marshal(suggestions)
					actual, _ := json.Marshal(persisted.Suggestions)
					// The existing PostgreSQL writer removes NUL from stored
					// JSON while installed native values keep it until reload.
					nul := strings.NewReplacer(`\u0000`, "")
					if nul.Replace(string(expected)) != nul.Replace(string(actual)) {
						t.Fatal("installed generated suggestions differ from persisted inline JSON")
					}
				}
				w := httptest.NewRecorder()
				writeJSON(w, http.StatusOK, map[string]any{"issues": results, "suggestions": suggestions})
				return w
			})
			if len(outcomes[0].State.Events) != 1 {
				t.Fatalf("generated %d events, want 1", len(outcomes[0].State.Events))
			}
		})
	}
}
