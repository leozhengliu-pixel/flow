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

func scaleP21SW162Put(t *testing.T, f scaleP21SW70Fixture, tx *sqlTx, workspace string, physical any, order int64, issue domain.Issue, raw []byte) {
	t.Helper()
	values, err := issueRecordValues(workspace, issue)
	if err != nil {
		t.Fatal(err)
	}
	if raw == nil {
		raw, err = json.Marshal(issue)
		if err != nil {
			t.Fatal(err)
		}
	}
	values[1], values[2], values[len(values)-1] = physical, idgen.New("sw162-index-"), raw
	if _, err := tx.ExecContext(t.Context(), issueRecordInsert+issueRecordValuesSQL, values...); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE issue_records SET collection_order=? WHERE workspace_key=? AND id=?`, order, workspace, physical); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW162Context(t *testing.T, f scaleP21SW70Fixture, tx *sqlTx, workspace string, metadata domain.Bootstrap) context.Context {
	t.Helper()
	return context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{
		database: f.db, workspace: workspace, reader: tx, metadata: metadata,
	})
}

func scaleP21SW162Compare(t *testing.T, f scaleP21SW70Fixture, ctx context.Context, q IssueRecordQuery, count int) int {
	t.Helper()
	var want, got []domain.Issue
	wantErr := f.WalkIssueRecords(ctx, q, func(issue domain.Issue) error {
		want = append(want, issue)
		return nil
	})
	bodyQueries, payloadRows := 0, 0
	observed := WithSQLStatementObserver(ctx, func(query string) {
		if !strings.Contains(query, " AS row_bytes FROM issue_records") {
			return
		}
		bodyQueries++
		rows := strings.Count(query, " AS ordinal,")
		if rows < 1 || rows > bootstrapIssuePayloadBatchRows {
			t.Fatal("payload query exceeded the bounded row count", rows)
		}
		payloadRows += rows
	})
	gotErr := f.view.walkBoundedBootstrapIssueRecords(observed, q, func(issue domain.Issue) error {
		if f.view.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("issue reached visitor without row admission")
		}
		held := ctx.Value(workspaceReadKey{}).(workspaceRead)
		scaleP21SW70Usable(t, held.reader)
		got = append(got, issue)
		return nil
	})
	if reflect.TypeOf(wantErr) != reflect.TypeOf(gotErr) || wantErr != nil && wantErr.Error() != gotErr.Error() ||
		!reflect.DeepEqual(want, got) {
		t.Fatalf("literal/bounded walk differs: want %d/%v got %d/%v", len(want), wantErr, len(got), gotErr)
	}
	if wantErr == nil && len(got) != count {
		t.Fatalf("fixture matched %d rows, want %d", len(got), count)
	}
	if wantErr == nil && payloadRows != count {
		t.Fatal("payload batches did not cover the exact selected rows", payloadRows, count)
	}
	if wantErr == nil && count == 73 && bodyQueries != 3 {
		t.Fatal("small payloads regressed to per-row SQL queries", bodyQueries)
	}
	t.Logf("selected_rows=%d payload_queries=%d payload_rows=%d", len(got), bodyQueries, payloadRows)
	if f.view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("walk retained cursor, references or row admission")
	}
	return bodyQueries
}

func TestScaleP21SW162BootstrapIssueWalkSQLOrderAndReferences(t *testing.T) {
	f := scaleP21SW70Setup(t)
	tx := f.begin(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw162-")
	if f.dialect == "sqlite" {
		if _, err := tx.Tx.ExecContext(t.Context(), `UPDATE record_journal_context SET tx_id='' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
	}
	metadata := cloneBootstrap(f.data)
	team := domain.Team{ID: prefix + "team", Key: "NEW", Name: "Current team", Private: true}
	state := domain.WorkflowState{ID: prefix + "state", Name: "Current state"}
	user := domain.User{ID: prefix + "user", Name: "Current actor"}
	label := domain.IssueLabel{ID: prefix + "label", Name: "Current label"}
	project := domain.Project{ID: prefix + "project", Name: "Current project"}
	metadata.Teams, metadata.States, metadata.Users = []domain.Team{{ID: team.ID, Name: "Earlier duplicate"}, team}, []domain.WorkflowState{state}, []domain.User{user}
	metadata.Labels, metadata.Projects = []domain.IssueLabel{label}, []domain.Project{project}
	for i := range 73 {
		physical := any(fmt.Sprintf("%s%03d", prefix, i))
		if f.dialect == "sqlite" && i%4 == 0 {
			physical = []byte(fmt.Sprintf("%s%03d", prefix, i))
		}
		issue := domain.Issue{
			ID: fmt.Sprintf("logical-%d", i), Identifier: "NEW-1", PreviousIdentifiers: []string{"OLD-1"},
			Title: fmt.Sprintf("Case %03d", i), Description: "Full body", Creator: domain.User{ID: user.ID, Name: "Stale actor"},
			Team: domain.Team{ID: team.ID, Name: "Stale team"}, State: domain.WorkflowState{ID: state.ID, Name: "Stale state"},
			Assignee: &domain.User{ID: user.ID}, Delegate: &domain.User{ID: user.ID},
			Project: &domain.ProjectSummary{ID: project.ID}, Labels: []domain.IssueLabel{{ID: label.ID, Name: "Stale label"}},
		}
		scaleP21SW162Put(t, f, tx, workspace, physical, int64(i%3), issue, nil)
	}
	for _, reload := range []bool{false, true} {
		t.Run(fmt.Sprint("reloaded=", reload), func(t *testing.T) {
			input := metadata
			if reload {
				raw, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &input); err != nil {
					t.Fatal(err)
				}
			}
			ctx := scaleP21SW162Context(t, f, tx, workspace, input)
			scaleP21SW162Compare(t, f, ctx, IssueRecordQuery{Workspace: workspace, Archived: "all"}, 73)
			scaleP21SW162Compare(t, f, ctx, IssueRecordQuery{Workspace: workspace, Text: "Case 00", Archived: "all"}, 10)
			scaleP21SW162Compare(t, f, ctx, IssueRecordQuery{Workspace: workspace, IssueIDs: []string{}, RestrictToIssueIDs: true}, 0)
		})
	}
}

func TestScaleP21SW162BootstrapIssueWalkAccessMatrix(t *testing.T) {
	f := scaleP21SW70Setup(t)
	tx := f.begin(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw162-access-")
	public, private := prefix+"public", prefix+"private"
	guest, inherited := prefix+"guest", prefix+"inherited"
	ids := []string{prefix + "public-issue", prefix + "private-issue", prefix + "parent", prefix + "child", prefix + "archived"}
	for i, id := range ids {
		issue := domain.Issue{ID: id, Identifier: id, Title: id, Team: domain.Team{ID: private}}
		if i == 0 || i == 4 {
			issue.Team.ID = public
		}
		if i == 3 {
			issue.ParentID = &ids[2]
		}
		if i == 4 {
			stamp := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
			issue.ArchivedAt = &stamp
		}
		scaleP21SW162Put(t, f, tx, workspace, id, int64(i), issue, nil)
	}
	scaleP21SW162Put(t, f, tx, prefix+"other-workspace", prefix+"cross-workspace", 0, domain.Issue{ID: prefix + "cross-workspace", Team: domain.Team{ID: public}}, nil)
	for _, grant := range [][2]string{{ids[1], guest}, {ids[2], inherited}} {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO issue_permission_records(workspace_key,issue_id,subject_type,subject_id,role) VALUES(?,?,'user',?,'viewer')`, workspace, grant[0], grant[1]); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name    string
		access  *IssueRecordAccess
		allowed []string
		count   int
	}{
		{"admin", &IssueRecordAccess{Admin: true}, nil, 4},
		{"admin-outside-private-team", &IssueRecordAccess{UserID: prefix + "admin", WorkspaceAdmin: true, VisibleTeamIDs: []string{public}}, nil, 1},
		{"member", &IssueRecordAccess{UserID: prefix + "member", VisibleTeamIDs: []string{public}}, nil, 1},
		{"guest-explicit-grant", &IssueRecordAccess{UserID: guest}, nil, 1},
		{"private-team-outsider", &IssueRecordAccess{UserID: prefix + "outsider"}, nil, 0},
		{"sub-team-visible", &IssueRecordAccess{UserID: prefix + "subteam", VisibleTeamIDs: []string{private}}, nil, 3},
		{"grant-descendant-inheritance", &IssueRecordAccess{UserID: inherited}, nil, 2},
		{"api-unrestricted", &IssueRecordAccess{Admin: true}, nil, 4},
		{"api-selected-empty", &IssueRecordAccess{Admin: true}, []string{}, 0},
		{"api-selected-set", &IssueRecordAccess{Admin: true}, []string{public}, 1},
		{"archived-team-hidden", &IssueRecordAccess{Admin: true, ArchivedTeamIDs: []string{private}}, nil, 1},
	}
	ctx := scaleP21SW162Context(t, f, tx, workspace, f.data)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scaleP21SW162Compare(t, f, ctx, IssueRecordQuery{Workspace: workspace, Access: test.access, AllowedTeamIDs: test.allowed}, test.count)
		})
	}
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM issue_permission_records WHERE workspace_key=? AND subject_id=?`, workspace, guest); err != nil {
		t.Fatal(err)
	}
	scaleP21SW162Compare(t, f, ctx, IssueRecordQuery{Workspace: workspace, Access: &IssueRecordAccess{UserID: guest}}, 0)
}

func TestScaleP21SW162BootstrapIssueOutlinesLiteral(t *testing.T) {
	f := scaleP21SW70Setup(t)
	tx := f.begin(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw162-outline-")
	issue := domain.Issue{ID: prefix, Title: "Full title", Description: "Body", Reactions: map[string][]string{"like": {"one", "two"}}}
	summary := issueListProjection(issue)
	summary.Title = "Stored summary title"
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		id := fmt.Sprint(prefix, i)
		raw := []byte(nil)
		if i == 2 {
			raw = []byte("{invalid full body")
		}
		scaleP21SW162Put(t, f, tx, workspace, id, int64(i), issue, raw)
		if i != 0 {
			if _, err := tx.ExecContext(t.Context(), `UPDATE issue_records SET list_data=? WHERE workspace_key=? AND id=?`, summaryJSON, workspace, id); err != nil {
				t.Fatal(err)
			}
		} else if _, err := tx.ExecContext(t.Context(), `UPDATE issue_records SET list_data=NULL WHERE workspace_key=? AND id=?`, workspace, id); err != nil {
			t.Fatal(err)
		}
	}
	// The old outline selects metadata.Workspace.URLKey after resolving its
	// source; the caller's snapshot key may still be an old route slug.
	alias := workspace + "-old-route"
	ctx := scaleP21SW162Context(t, f, tx, alias, f.data)
	want := []domain.Issue{}
	rows, err := tx.QueryContext(ctx, `SELECT COALESCE(list_data,data) FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var raw []byte
		var value domain.Issue
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		normalizeIssueRecord(&value)
		want = append(want, issueListProjection(value))
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	var got []domain.Issue
	if err := f.view.walkBoundedBootstrapIssueOutlines(ctx, alias, func(value domain.Issue) error {
		scaleP21SW70Usable(t, tx)
		got = append(got, value)
		return nil
	}); err != nil || !reflect.DeepEqual(want, got) || len(got) != 3 {
		t.Fatal("literal per-row outline differs", err)
	}
	if f.view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("outline walk leaked admission")
	}
}

func TestScaleP21SW162BootstrapIssueWalkLargeDenseAndRelease(t *testing.T) {
	f := scaleP21SW70Setup(t)
	tx := f.begin(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw162-large-")
	large := domain.Issue{ID: prefix + "large", Title: "Large legal record", Description: strings.Repeat("x", 3<<20)}
	dense := domain.Issue{ID: prefix + "dense", Title: "Dense legal record", Labels: make([]domain.IssueLabel, 6000)}
	scaleP21SW162Put(t, f, tx, workspace, large.ID, 0, large, nil)
	scaleP21SW162Put(t, f, tx, workspace, dense.ID, 1, dense, nil)
	for i := range 5 {
		issue := domain.Issue{ID: fmt.Sprint(prefix, "medium-", i), Description: strings.Repeat("m", 64<<10)}
		scaleP21SW162Put(t, f, tx, workspace, issue.ID, int64(i+2), issue, nil)
	}
	ctx := scaleP21SW162Context(t, f, tx, workspace, f.data)
	q := IssueRecordQuery{Workspace: workspace}
	if batches := scaleP21SW162Compare(t, f, ctx, q, 7); batches != 4 {
		t.Fatal("giant rows or the 256 KiB payload boundary were not isolated", batches)
	}
	sentinel := errors.New("visitor stopped")
	count := 0
	if err := f.view.walkBoundedBootstrapIssueRecords(ctx, q, func(domain.Issue) error { count++; return sentinel }); !errors.Is(err, sentinel) || count != 1 {
		t.Fatal("visitor stop was not preserved", err, count)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	count = 0
	err := f.view.walkBoundedBootstrapIssueRecords(cancelCtx, q, func(domain.Issue) error {
		count++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || count != 1 {
		t.Fatal("canceled visitor continued", err, count)
	}
	limit := f.view.metadataResidency.work.limit
	f.view.metadataResidency.work.limit = 128 << 10
	called := false
	err = f.view.walkBoundedBootstrapIssueRecords(ctx, q, func(domain.Issue) error { called = true; return nil })
	f.view.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) || called || f.view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("quota failed to reject before visitor or release work", err, called)
	}
	if err := f.view.walkBoundedBootstrapIssueRecords(t.Context(), q, func(domain.Issue) error { return nil }); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("unowned SQL/reference source was admitted", err)
	}
	scaleP21SW70Usable(t, tx)
}
