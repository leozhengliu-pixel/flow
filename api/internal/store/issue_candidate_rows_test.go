package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestCandidateRowsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := journalScaleDatabase(t)
	prefix, workspace := idgen.New("sw192-"), f.data.Workspace.URLKey
	projectID, labelID, stateID := prefix+"project", prefix+"label", prefix+"state"
	project := domain.Project{ID: projectID, Name: "Installed project", Description: strings.Repeat("unloaded project body ", 1<<16)}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw192.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, project)
		data.States = append(data.States, domain.WorkflowState{ID: stateID, Name: "Canonical state", Type: "backlog"})
		data.Labels = append(data.Labels, domain.IssueLabel{ID: labelID, Name: "Canonical label", Description: "Full canonical label", Scope: "workspace"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var issues []domain.Issue
	at := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	for i := range 12 {
		text := "alpha beta"
		switch i {
		case 9:
			text = "alpha only"
		case 10:
			text = "beta only"
		case 11:
			text = "gamma only"
		}
		issue := domain.Issue{
			ID: fmt.Sprintf("%s%02d", prefix, i), Identifier: fmt.Sprintf("SW192-%d", i), Number: 20000 + i, Version: 1,
			Title: fmt.Sprintf("Needle candidate %02d %s", i, text), Priority: i % 3, Team: f.data.Teams[0],
			State:   domain.WorkflowState{ID: stateID, Name: "Embedded state", Type: "backlog"},
			Creator: f.data.Viewer, Labels: []domain.IssueLabel{{ID: labelID, Name: "Embedded label"}},
			Project: &domain.ProjectSummary{ID: projectID, Name: "Embedded project"}, CreatedAt: at, UpdatedAt: at.Add(time.Duration(i) * time.Second),
		}
		if i == 0 {
			issue.Description = strings.Repeat("valid list avoids full issue body ", 1<<14)
		}
		issues = append(issues, issue)
	}
	if err := f.ImportIssues(t.Context(), workspace, issues); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	// Canonical rows, rather than the installed copy, own query references.
	project.Name = "Canonical SQL project"
	projectRaw, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND record_key=?`, projectRaw, workspace, projectID); err != nil {
		t.Fatal(err)
	}
	originals := map[string][2][]byte{}
	for _, issue := range issues {
		var raw, list []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data,list_data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, issue.ID).Scan(&raw, &list); err != nil {
			t.Fatal(err)
		}
		originals[issue.ID] = [2][]byte{raw, list}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for id, values := range originals {
			if _, err := f.db.ExecContext(ctx, `UPDATE issue_records SET data=?,list_data=? WHERE workspace_key=? AND id=?`, values[0], values[1], workspace, id); err != nil {
				t.Error("restore owned candidate row", err)
			}
		}
	})
	for index, raw := range map[int][]byte{
		1: nil, 2: []byte(`[]`), 3: []byte(`{"invalid":`), 4: []byte(`{"id":"duplicate","title":"earlier duplicate"}`),
		5: []byte(`{"id":"duplicate","title":"later duplicate"}`), 7: []byte(`{`), 8: []byte(`null`),
	} {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET list_data=? WHERE workspace_key=? AND id=?`, raw, workspace, issues[index].ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, index := range []int{6, 7} {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, []byte(`{"invalid":`), workspace, issues[index].ID); err != nil {
			t.Fatal(err)
		}
	}
	source, err := view.BeginIssueSuggestionCandidates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for _, test := range []struct {
		name   string
		query  IssueRecordQuery
		terms  []string
		labels []string
		limit  int
	}{
		{"union", IssueRecordQuery{}, []string{"alpha beta", "alpha", "beta"}, nil, 100},
		{"mixed-term-union", IssueRecordQuery{Access: &IssueRecordAccess{UserID: f.data.Viewer.ID, WorkspaceID: f.data.Workspace.ID, VisibleTeamIDs: []string{f.data.Teams[0].ID}}}, []string{`"absent"`, "beta", "11"}, nil, 100},
		{"title", IssueRecordQuery{Sort: "title", Direction: "asc"}, []string{"Needle"}, nil, 100},
		{"label-union", IssueRecordQuery{Filter: IssueFilter{Field: "priority", Values: []string{"1"}}}, []string{"absent"}, []string{labelID}, 100},
		{"selected-empty", IssueRecordQuery{AllowedTeamIDs: []string{}}, []string{"Needle"}, nil, 100},
		{"selected-set", IssueRecordQuery{AllowedTeamIDs: []string{f.data.Teams[0].ID}}, []string{"Needle"}, nil, 3},
		{"member", IssueRecordQuery{Access: &IssueRecordAccess{UserID: f.data.Viewer.ID, WorkspaceID: f.data.Workspace.ID, VisibleTeamIDs: []string{f.data.Teams[0].ID}}}, []string{"Needle"}, nil, 100},
		{"blank", IssueRecordQuery{}, nil, nil, 100},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.query.Workspace, test.query.Archived = workspace, "all"
			var want []json.RawMessage
			for _, bounded := range []bool{false, true} {
				repository, ctx := f.SQLiteStore, t.Context()
				if bounded {
					repository, ctx = view, source.Context(ctx)
				}
				var got []json.RawMessage
				seen := map[string]bool{}
				queries, matches, peak := 0, 0, 0
				var bodyQuery string
				ctx = WithSQLStatementObserver(ctx, func(query string) {
					queries++
					matches += strings.Count(query, "issue_search_fts MATCH")
					if strings.Contains(query, "output.list_data ELSE NULL END") {
						bodyQuery = query
					}
					peak = max(peak, view.MetadataWorkUsedForTesting())
				})
				err := repository.SearchIssueCandidateTerms(ctx, test.query, test.terms, test.labels, test.limit, func(issue domain.Issue) error {
					if issue.Project != nil && issue.Project.ID == projectID && issue.Project.Name != project.Name {
						t.Fatalf("query used stale installed project: %+v", issue.Project)
					}
					raw, err := json.Marshal(issue)
					got = append(got, raw)
					seen[issue.ID] = true
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				if repository.dialect == "sqlite" && len(test.terms) > 0 && matches != 1 {
					t.Fatalf("bounded=%v used %d FTS matches for one candidate selection", bounded, matches)
				}
				if repository.dialect == "sqlite" {
					checkHits := func(wantAlpha, wantGamma bool) {
						t.Helper()
						if seen[issues[9].ID] != wantAlpha || !seen[issues[10].ID] || seen[issues[11].ID] != wantGamma {
							t.Fatalf("unexpected alpha, beta, or gamma hits: %t, %t, %t", seen[issues[9].ID], seen[issues[10].ID], seen[issues[11].ID])
						}
					}
					switch test.name {
					case "union":
						checkHits(true, false)
					case "mixed-term-union":
						checkHits(false, true)
					}
				}
				if bounded && repository.dialect == "sqlite" && test.name == "union" {
					if bodyQuery == "" {
						t.Fatal("candidate query did not fetch selected bodies")
					}
					plan, err := repository.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+bodyQuery, 4096, "[1,2]", workspace, test.terms[0])
					if err != nil {
						t.Fatal(err)
					}
					defer plan.Close()
					var details []string
					pointLookup := false
					for plan.Next() {
						var id, parent, unused int
						var detail string
						if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
							t.Fatal(err)
						}
						details = append(details, detail)
						pointLookup = pointLookup || strings.Contains(detail, "SEARCH output USING INTEGER PRIMARY KEY (rowid=?)")
					}
					if err := plan.Err(); err != nil || !pointLookup {
						t.Fatalf("candidate body query did not use rowid lookups: %v (%v)", details, err)
					}
				}
				if bounded && !reflect.DeepEqual(want, got) {
					t.Fatalf("candidate encounter order, fallback, dedup or canonical references changed:\nlegacy=%s\nbounded=%s", want, got)
				}
				if !bounded {
					want = got
				}
				t.Logf("bounded=%v rows=%d SQL=%d peak_work=%d", bounded, len(got), queries, peak)
			}
		})
	}
	baseline := view.MetadataWorkUsedForTesting()
	stop := errors.New("sw192 visitor stop")
	err = view.SearchIssueCandidateTerms(source.Context(t.Context()), IssueRecordQuery{Workspace: workspace, Archived: "all"}, []string{"Needle"}, nil, 100, func(domain.Issue) error { return stop })
	if !errors.Is(err, stop) || view.MetadataWorkUsedForTesting() != baseline {
		t.Fatalf("visitor failure leaked work: err=%v work=%d baseline=%d", err, view.MetadataWorkUsedForTesting(), baseline)
	}
	ctx, cancel := context.WithCancel(source.Context(t.Context()))
	cancel()
	if err := view.SearchIssueCandidateTerms(ctx, IssueRecordQuery{Workspace: workspace}, []string{"Needle"}, nil, 100, func(domain.Issue) error { t.Fatal("cancelled visitor"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(source.Context(t.Context()))
	ctx = WithSQLStatementObserver(ctx, func(query string) {
		if strings.Contains(query, "output.list_data ELSE NULL END") {
			cancel()
		}
	})
	err = view.SearchIssueCandidateTerms(ctx, IssueRecordQuery{Workspace: workspace, Archived: "all"}, []string{"Needle"}, nil, 100, func(domain.Issue) error { t.Fatal("cancelled SQL visitor"); return nil })
	cancel()
	if !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != baseline {
		t.Fatalf("cancelled SQL retained query scratch: err=%v work=%d", err, view.MetadataWorkUsedForTesting())
	}
	view.metadataResidency.work.limit = baseline + 1
	err = view.SearchIssueCandidateTerms(source.Context(t.Context()), IssueRecordQuery{Workspace: workspace}, []string{"Needle"}, nil, 100, func(domain.Issue) error { t.Fatal("exhausted visitor"); return nil })
	view.metadataResidency.work.limit = metadataWorkBytes()
	if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != baseline {
		t.Fatalf("exhausted query retained work: err=%v work=%d", err, view.MetadataWorkUsedForTesting())
	}
	source.Close()
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatalf("released source work=%d full decodes=%d", view.MetadataWorkUsedForTesting(), view.WorkspaceMetadataDecodes())
	}
}
