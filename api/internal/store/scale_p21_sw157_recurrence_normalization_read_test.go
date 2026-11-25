package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP21SW157Fixture struct {
	scaleP09Fixture
	issues []domain.Issue
}

func scaleP21SW157Seed(t *testing.T) scaleP21SW157Fixture {
	t.Helper()
	f := scaleP21SW157Fixture{scaleP09Fixture: scaleP09Database(t)}
	at := time.Date(2026, 11, 15, 1, 2, 3, 456, time.UTC)
	prefix := idgen.New("sw157-")
	if err := f.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw157_seed", "", nil, func(data *domain.Bootstrap) error {
		teamID := prefix + "private"
		data.Teams = append(data.Teams, domain.Team{ID: teamID, Key: "PRIVATE", Private: true, ArchivedAt: &at})
		data.TeamSettings[teamID] = domain.TeamSettings{TeamID: teamID, Timezone: "America/New_York", Access: "private", ParentTeamID: data.Teams[0].ID}
		data.TeamSettings[data.Teams[0].ID] = domain.TeamSettings{TeamID: data.Teams[0].ID, Timezone: "Asia/Tokyo"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	installed, found := f.WorkspaceMetadata(f.data.Workspace.URLKey)
	if !found {
		t.Fatal("seed workspace missing")
	}
	for index := range 8 {
		due := fmt.Sprintf("2026-11-%02d", index+1)
		next := at.Add(time.Duration(8-index) * time.Hour)
		issue := domain.Issue{
			ID: prefix + fmt.Sprintf("issue-%d", index), Identifier: prefix + fmt.Sprintf("REC-%d", index),
			Team: installed.Teams[index%len(installed.Teams)], Creator: installed.Viewer,
			State: installed.States[0], Version: 1, SortOrder: float64(index),
			Title: "ignored title", Description: "ignored description",
			Recurrence: []string{"daily", "weekly", "FREQ=WEEKLY;BYDAY=MO", "", "invalid-rule"}[index%5],
			DueDate:    &due, NextOccurrenceAt: &next, CreatedAt: at, UpdatedAt: at,
		}
		if index == 1 {
			issue.DueDate = nil
		}
		if index == 2 {
			empty := ""
			issue.DueDate = &empty
		}
		if index == 3 {
			issue.ArchivedAt = &at
		}
		if index >= 6 {
			issue.Recurrence, issue.NextOccurrenceAt = "", nil
		}
		f.issues = append(f.issues, issue)
	}
	f.issues[0].ParentID = &f.issues[6].ID
	if err := f.ImportIssues(t.Context(), f.data.Workspace.URLKey, f.issues); err != nil {
		t.Fatal(err)
	}
	return f
}

func scaleP21SW157Headers(issues []domain.Issue) []domain.Issue {
	headers := scaleP21IssueAuthorizationHeaders(issues)
	for index, issue := range issues {
		headers[index].Recurrence, headers[index].DueDate, headers[index].NextOccurrenceAt = issue.Recurrence, issue.DueDate, issue.NextOccurrenceAt
	}
	return headers
}

func scaleP21SW157Query(workspace string, ids []string) IssueRecordQuery {
	return IssueRecordQuery{Workspace: workspace, IssueIDs: ids, RestrictToIssueIDs: true, Archived: "all", Summary: true, Limit: 500}
}

func scaleP21SW157Install(t *testing.T, view *SQLiteStore, workspace string, data domain.Bootstrap) {
	t.Helper()
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource(workspace, source)
}

func TestScaleP21SW157RecurringNormalizationWarmReload(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21SW157Seed(t)
	workspace := f.data.Workspace.URLKey
	for _, state := range []string{"warm", "reloaded", "unknown-revision"} {
		t.Run(state, func(t *testing.T) {
			if state == "reloaded" {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if state == "unknown-revision" {
				if err := f.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
					t.Fatal(err)
				}
			}
			view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
			wantIDs, err := f.RecurringIssueIDs(t.Context(), workspace, time.Time{}, 0)
			if err != nil {
				t.Fatal(err)
			}
			statements := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
			read, err := view.BeginRecurringNormalizationRead(ctx, workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			if !reflect.DeepEqual(read.IDs, wantIDs) || statements != 2 {
				t.Fatal("sparse IDs/order or preflight SQL differ", read.IDs, wantIDs, statements)
			}
			idWork := view.MetadataWorkUsedForTesting()
			pages := 0
			for _, ids := range [][]string{
				read.IDs, {f.issues[3].ID, f.issues[0].ID, f.issues[0].ID, "missing"},
				{strings.ToUpper(f.issues[0].ID), f.issues[0].Identifier, f.issues[0].ID + "\x00"},
				nil, {},
			} {
				want, err := f.QueryIssueRecords(t.Context(), scaleP21SW157Query(workspace, ids))
				if err != nil {
					t.Fatal(err)
				}
				page, err := read.Page(ctx, ids)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(page.Data.Issues, scaleP21SW157Headers(want.Items)) {
					t.Fatal("trusted first-page headers differ", ids, page.Data.Issues, scaleP21SW157Headers(want.Items))
				}
				for _, issue := range page.Data.Issues {
					settings, _ := f.TeamSettingsFor(workspace, issue.Team.ID)
					got, err := read.TeamTimezone(issue.Team.ID)
					if err != nil || got != settings.Timezone {
						t.Fatal("first timezone lookup differs", issue.Team.ID, got, settings.Timezone, err)
					}
					before := view.MetadataWorkUsedForTesting()
					if again, err := read.TeamTimezone(issue.Team.ID); err != nil || again != got || view.MetadataWorkUsedForTesting() != before {
						t.Fatal("cached timezone changed allowance", err)
					}
					if issue.Title != "" || issue.Description != "" || issue.DocumentContent != nil {
						t.Fatal("timing profile retained unrelated issue bodies")
					}
				}
				page.Close()
				page.Close()
				pages++
			}
			if view.MetadataWorkUsedForTesting() < idWork {
				t.Fatal("page close released normalizer-owned work")
			}
			read.Close()
			if read.IDs != nil || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("normalizer close leaked IDs/timezones or hydrated metadata")
			}
			t.Logf("state=%s recurring_ids=%d page_pairs=%d total_read_sql=%d", state, len(wantIDs), pages, statements)
		})
	}
}

func TestScaleP21SW157RecurringNormalizationFirstPage(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21SW157Seed(t)
	workspace := f.data.Workspace.URLKey
	ids := []string{f.issues[0].ID, f.issues[1].ID, f.issues[2].ID}
	for _, index := range []int{0, 1} {
		issue := f.issues[index]
		issue.Title = strings.Repeat("x", (2<<20)+4096)
		body, err := json.Marshal(issueListProjection(issue))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), "UPDATE issue_records SET list_data=? WHERE workspace_key=? AND id=?", body, workspace, issue.ID); err != nil {
			t.Fatal(err)
		}
	}
	want, err := f.QueryIssueRecords(t.Context(), scaleP21SW157Query(workspace, ids))
	if err != nil || len(want.Items) != 1 || !want.HasMore {
		t.Fatal("literal fixture did not hit summary byte boundary", len(want.Items), want.HasMore, err)
	}
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	read, err := view.BeginRecurringNormalizationRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	page, err := read.Page(t.Context(), ids)
	if err != nil || !reflect.DeepEqual(page.Data.Issues, scaleP21SW157Headers(want.Items)) {
		t.Fatal("normalizer fetched a second page or an ancestor", err)
	}
	read.Close()
	if len(page.Data.Issues) != 1 || page.Data.Issues[0].ID != f.issues[0].ID || view.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("page did not retain independent lifetime")
	}
	page.Close()
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("summary boundary test leaked or hydrated")
	}
	t.Logf("literal_first_page=%d selected_ids=%d summary_limit=%d", len(want.Items), len(ids), 4<<20)
}

func TestScaleP21SW157RecurringNormalizationTimezoneSources(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21SW157Seed(t)
	workspace := f.data.Workspace.URLKey
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	parent := scaleP21SW152Parent(t, view, workspace)
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginRecurringNormalizationRead(pinned, workspace)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := f.WorkspaceMetadata(workspace)
	data.TeamSettings = map[string]domain.TeamSettings{
		"physical":                             {TeamID: "decoded", Timezone: "America/New_York", Description: strings.Repeat("ignored ", 10000)},
		"decoded":                              {TeamID: "physical", Timezone: "Asia/Tokyo"},
		"":                                     {TeamID: "nonempty", Timezone: "UTC"},
		" raw\x00":                             {Timezone: "invalid/location"},
		strings.Repeat("unrelated-key", 10000): {Timezone: strings.Repeat("unrelated-timezone", 10000)},
	}
	scaleP21SW157Install(t, view, workspace, data)
	for _, key := range []string{"physical", "", " raw\x00", "missing", "PHYSICAL", "physical "} {
		want := data.TeamSettings[key].Timezone
		if got, err := read.TeamTimezone(key); err != nil || got != want {
			t.Fatal("timezone used a pinned source, alias, JSON TeamID or projection", key, got, want, err)
		}
	}
	held := view.MetadataWorkUsedForTesting()
	data.TeamSettings["physical"] = domain.TeamSettings{Timezone: "Pacific/Auckland"}
	data.TeamSettings["decoded"] = domain.TeamSettings{Timezone: "Europe/London"}
	data.TeamSettings["missing"] = domain.TeamSettings{Timezone: "UTC"}
	scaleP21SW157Install(t, view, workspace, data)
	if got, err := read.TeamTimezone("physical"); err != nil || got != "America/New_York" || view.MetadataWorkUsedForTesting() != held {
		t.Fatal("cached first timezone changed after registry replacement", got, err)
	}
	if got, err := read.TeamTimezone("missing"); err != nil || got != "" || view.MetadataWorkUsedForTesting() != held {
		t.Fatal("cached missing physical key changed", got, err)
	}
	if got, err := read.TeamTimezone("decoded"); err != nil || got != "Europe/London" {
		t.Fatal("new timezone lookup did not select fresh source", got, err)
	}
	read.Close()
	parent.Close()
	if _, err := read.TeamTimezone("physical"); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed normalizer served timezone", err)
	}
	if page, err := read.Page(t.Context(), nil); page != nil || !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed normalizer served page", err)
	}
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("timezone cache retained owners/work or hydrated metadata")
	}
}

func TestScaleP21SW157RecurringNormalizationCancellationAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21SW157Seed(t)
	workspace := f.data.Workspace.URLKey
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := view.BeginRecurringNormalizationRead(canceled, workspace); read != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("initial cancellation retained IDs/work", err)
	}
	for _, transfer := range []bool{false, true} {
		base, cancel := context.WithCancel(t.Context())
		ctx := WithSQLStatementObserver(base, func(query string) {
			if strings.Contains(query, "recurring_normalization_ids") != transfer {
				cancel()
			}
		})
		read, err := view.BeginRecurringNormalizationRead(ctx, workspace)
		cancel()
		if read != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("preflight/transfer cancellation retained IDs/work", transfer, err)
		}
	}
	limit := view.metadataResidency.work.limit
	for _, available := range []int{1, (192 << 10) - 1} {
		view.metadataResidency.work.limit = available
		transfers := 0
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			if strings.HasPrefix(query, "SELECT a.issue_id") {
				transfers++
			}
		})
		read, err := view.BeginRecurringNormalizationRead(ctx, workspace)
		view.metadataResidency.work.limit = limit
		if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || transfers != 0 || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("ID transfer preceded admission", available, transfers, err)
		}
	}
	read, err := view.BeginRecurringNormalizationRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	baseline := view.MetadataWorkUsedForTesting()
	if page, err := read.Page(canceled, read.IDs); page != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != baseline {
		t.Fatal("canceled page retained partial work", err)
	}
	base, cancel := context.WithCancel(t.Context())
	ctx := WithSQLStatementObserver(base, func(query string) {
		if strings.Contains(query, "AS ordinal") {
			cancel()
		}
	})
	page, err := read.Page(ctx, read.IDs)
	cancel()
	if page != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != baseline {
		t.Fatal("mid-page cancellation retained partial work", err)
	}
	for _, available := range []int{1, 160 << 10} {
		view.metadataResidency.work.limit = baseline + available
		page, err := read.Page(t.Context(), read.IDs)
		view.metadataResidency.work.limit = limit
		if page != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != baseline {
			t.Fatal("refused page retained partial work", available, err)
		}
	}
	ctx = WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.HasPrefix(query, "SELECT field,record_key,") {
			view.metadataResidency.work.limit = view.MetadataWorkUsedForTesting() + 1
		}
	})
	page, err = read.Page(ctx, read.IDs)
	view.metadataResidency.work.limit = limit
	if page != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != baseline {
		t.Fatal("reference admission refusal retained partially projected issues", err)
	}
	if page, err := read.Page(t.Context(), make([]string, 201)); page != nil || !errors.Is(err, ErrIssueQuery) {
		t.Fatal("oversized page accepted", err)
	}
	for _, available := range []int{1, (192 << 10) + len(workspace)*8 + len(f.issues[0].Team.ID)*8 + 1} {
		view.metadataResidency.work.limit = baseline + available
		_, err = read.TeamTimezone(f.issues[0].Team.ID)
		view.metadataResidency.work.limit = limit
		if !errors.Is(err, ErrMetadataWorkBudget) || len(read.timezones) != 0 || view.MetadataWorkUsedForTesting() != baseline {
			t.Fatal("refused timezone was cached or retained", available, err)
		}
	}
	if _, err := read.TeamTimezone(f.issues[0].Team.ID); err != nil {
		t.Fatal("timezone did not recover after quota refusal", err)
	}
	page, err = read.Page(t.Context(), read.IDs)
	if err != nil {
		t.Fatal("page did not recover after cancellation/quota refusal", err)
	}
	page.Close()
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("recovered normalizer retained work")
	}
}

func TestScaleP21SW157RecurringNormalizationBodyIndependent(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21SW157Seed(t)
	workspace := f.data.Workspace.URLKey
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	data, _ := f.WorkspaceMetadata(workspace)
	retained := 0
	for _, size := range []int{1024, 16384} {
		issue := f.issues[0]
		issue.Title, issue.Description = strings.Repeat("x", size), strings.Repeat("y", size)
		raw, err := json.Marshal(issueListProjection(issue))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), "UPDATE issue_records SET list_data=? WHERE workspace_key=? AND id=?", raw, workspace, issue.ID); err != nil {
			t.Fatal(err)
		}
		data.TeamSettings[issue.Team.ID] = domain.TeamSettings{Timezone: "Asia/Tokyo", Description: strings.Repeat("ignored", size)}
		data.TeamSettings[strings.Repeat("unselected-key", size)] = domain.TeamSettings{Timezone: strings.Repeat("unselected", size)}
		scaleP21SW157Install(t, view, workspace, data)
		read, err := view.BeginRecurringNormalizationRead(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		page, err := read.Page(t.Context(), []string{issue.ID})
		if err != nil {
			t.Fatal(err)
		}
		if timezone, err := read.TeamTimezone(issue.Team.ID); err != nil || timezone != "Asia/Tokyo" {
			t.Fatal("body-independent timezone failed", timezone, err)
		}
		work := view.MetadataWorkUsedForTesting()
		if retained != 0 && retained != work {
			t.Fatal("ignored issue/settings bodies changed retained work", retained, work)
		}
		retained = work
		if page.Data.Issues[0].Title != "" || page.Data.Issues[0].Description != "" {
			t.Fatal("timing headers retained ignored bodies")
		}
		page.Close()
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("body-independent normalizer leaked/hydrated")
		}
		t.Logf("ignored_body_bytes=%d retained_work=%d", size, work)
	}
}

func TestScaleP21SW157RecurringNormalizationIDSnapshot(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21SW157Seed(t)
	workspace := f.data.Workspace.URLKey
	want, err := f.RecurringIssueIDs(t.Context(), workspace, time.Time{}, 0)
	if err != nil || len(want) == 0 {
		t.Fatal("sparse index fixture missing", err)
	}
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	changed := false
	var mutationErr error
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.HasPrefix(query, "SELECT a.issue_id") && !changed {
			changed = true
			_, mutationErr = f.db.ExecContext(t.Context(), "DELETE FROM issue_attribute_records WHERE workspace_key=? AND issue_id=? AND field='nextOccurrenceAt'", workspace, want[0])
		}
	})
	read, err := view.BeginRecurringNormalizationRead(ctx, workspace)
	if mutationErr != nil || !changed || err != nil {
		t.Fatal("snapshot interleave did not complete", changed, mutationErr, err)
	}
	if !reflect.DeepEqual(read.IDs, want) {
		t.Fatal("ID transfer escaped its admitted SQL snapshot", read.IDs, want)
	}
	current, err := f.RecurringIssueIDs(t.Context(), workspace, time.Time{}, 0)
	if err != nil || len(current) != len(want)-1 || slices.Contains(current, want[0]) {
		t.Fatal("external sparse index change was not committed", current, err)
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("snapshot reader retained work")
	}
}

func TestScaleP21SW157RecurringNormalizationStaleCallerSnapshot(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP21SW157Seed(t)
	workspace := f.data.Workspace.URLKey
	view := scaleP21IssueAuthorizationView(t, f.scaleP09Fixture, false)
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
	before, err := f.RecurringIssueIDs(ctx, workspace, time.Time{}, 0)
	if err != nil || len(before) == 0 {
		t.Fatal("caller snapshot did not establish sparse IDs", before, err)
	}
	if _, err := f.db.ExecContext(t.Context(), "DELETE FROM issue_attribute_records WHERE workspace_key=? AND issue_id=? AND field='nextOccurrenceAt'", workspace, before[0]); err != nil {
		t.Fatal(err)
	}
	current, err := f.RecurringIssueIDs(t.Context(), workspace, time.Time{}, 0)
	if err != nil || len(current) != len(before)-1 || slices.Contains(current, before[0]) {
		t.Fatal("fresh sparse query did not observe committed deletion", current, err)
	}
	want, err := f.RecurringIssueIDs(ctx, workspace, time.Time{}, 0)
	if err != nil || !reflect.DeepEqual(want, before) {
		t.Fatal("literal wrapper escaped caller snapshot", want, before, err)
	}
	read, err := view.BeginRecurringNormalizationRead(ctx, workspace)
	if err != nil || !reflect.DeepEqual(read.IDs, want) {
		t.Fatal("owned preflight/IDs diverged from literal caller snapshot", err)
	}
	read.Close()
	if again, err := f.RecurringIssueIDs(ctx, workspace, time.Time{}, 0); err != nil || !reflect.DeepEqual(again, before) {
		t.Fatal("owned reader closed or advanced borrowed caller snapshot", again, err)
	}
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("borrowed SQL snapshot reader retained work or hydrated")
	}
	t.Logf("caller_snapshot_ids=%d fresh_ids=%d owned_ids=%d", len(before), len(current), len(want))
}

func TestScaleP21SW157RecurringNormalizationIndexLimit(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite bulk fixture; indexed reader/oracle comparisons run on every configured database")
	}
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw157-limit-")
	if _, err := f.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<10003)
		INSERT INTO issue_records(workspace_key,id,identifier,team_id,state_id,state_type,priority,assignee_id,project_id,creator_id,cycle_id,parent_id,sort_order,title,archived,version,created_at,updated_at,collection_order,data)
		SELECT ?,?||x,?||x,'','','',0,'','','','','',0,'',CASE WHEN x=1 THEN 1 ELSE 0 END,0,'','',x,'{}' FROM n`, workspace, prefix, prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO issue_attribute_records(workspace_key,issue_id,field,value)
		SELECT workspace_key,id,'nextOccurrenceAt',CASE WHEN collection_order<3 THEN 'early' ELSE 'same' END FROM issue_records WHERE workspace_key=? AND id LIKE ?`, workspace, prefix+"%"); err != nil {
		t.Fatal(err)
	}
	want, err := f.RecurringIssueIDs(t.Context(), workspace, time.Time{}, 0)
	if err != nil || len(want) != 10000 || slices.Contains(want, prefix+"1") || want[0] != prefix+"2" {
		t.Fatal("literal index limit fixture differs", len(want), err)
	}
	view := scaleP21IssueAuthorizationView(t, f, false)
	read, err := view.BeginRecurringNormalizationRead(t.Context(), workspace)
	if err != nil || !reflect.DeepEqual(read.IDs, want) {
		t.Fatal("normalizer changed sparse index ordering/limit/archived exclusion", err)
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("index-limit reader leaked or hydrated")
	}
	t.Logf("indexed_rows=10003 recurring_ids=%d archived_excluded=true", len(want))
}
