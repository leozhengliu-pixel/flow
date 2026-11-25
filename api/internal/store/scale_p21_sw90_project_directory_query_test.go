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
)

func scaleP21SW90Projects(repetitions int) []domain.Project {
	body := strings.Repeat("Ignored project and milestone body. ", repetitions)
	at := time.Date(2026, 11, 14, 1, 2, 3, 4, time.UTC)
	past, future := "2000-01-01", "9999-12-31"
	user := domain.User{ID: "lead", Name: "Complete lead", App: true, AppScopes: []string{"read"}, AppTeamIDs: []string{"private", "public"}, OutOfOfficeUntil: &at}
	return []domain.Project{
		{ID: "first", Name: "Alpha", SlugID: "alpha-000000000001", PreviousSlugIDs: []string{"stale-000000000001", "old-alpha"}, Summary: "Summary-only needle", Description: body, TeamIDs: []string{"private"}, LabelIDs: []string{"label", "label"}, MemberIDs: []string{"member", "member"}, Lead: &user, Creator: &user, BotActor: &domain.ActorBot{ID: "bot", Type: "agent"}, Priority: 1, Health: "onTrack", Status: domain.ProjectStatus{ID: "started", Name: "In progress", Type: "started", Description: "Retained status description"}, TargetDate: &past, IssueCount: 731, Progress: 0.25, CreatedAt: at, UpdatedAt: at,
			Resources:            []domain.ProjectResource{{ID: "resource", Title: body, URL: body}},
			Comments:             []domain.Comment{{ID: "comment", Body: body, BodyData: map[string]any{"ignored": body}}},
			CommentAttachments:   []domain.Attachment{{ID: "attachment", URL: body}},
			DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: body, CreatedAt: at}},
			Milestones:           []domain.ProjectMilestone{{ID: "milestone", ProjectID: "first", Name: "Retained milestone", Description: body, TargetDate: &future, DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: body}}, CreatedAt: at, UpdatedAt: at}},
			UpdateSchedule:       &domain.ProjectUpdateSchedule{},
			IssueCountHistory:    []domain.ProjectProgressHistoryPoint{{Value: 99}}, ScopeHistory: []domain.ProjectProgressHistoryPoint{{Value: 100}},
			CompletedScopeHistory: []domain.ProjectProgressHistoryPoint{{Value: 101}}, InProgressScopeHistory: []domain.ProjectProgressHistoryPoint{{Value: 102}}, ProgressHistory: []domain.ProjectProgressHistoryPoint{{Value: 103}}},
		{ID: "duplicate", Name: "Beta", SlugID: "beta-000000000002", Description: body, TeamIDs: []string{}, MemberIDs: []string{}, LabelIDs: nil, Milestones: []domain.ProjectMilestone{}, Priority: 0, Health: "at_risk", Status: domain.ProjectStatus{ID: "backlog", Name: "Backlog", Type: "backlog"}, TargetDate: &future, CreatedAt: at, UpdatedAt: at},
		{ID: "duplicate", Name: "Gamma duplicate", SlugID: "gamma-000000000003", Summary: "Duplicate occurrence", Description: body, TeamIDs: []string{"public"}, Milestones: nil, Lead: &domain.User{ID: "other"}, LabelIDs: []string{"other-label"}, Priority: 4, Health: "noUpdate", Status: domain.ProjectStatus{ID: "done", Name: "Done", Type: "completed"}, ArchivedAt: &at, CreatedAt: at, UpdatedAt: at},
		{ID: "", Name: "Empty ID first", Description: body, TeamIDs: nil, Milestones: nil, CreatedAt: at, UpdatedAt: at},
		{ID: "", Name: "Empty ID later", Description: body, TeamIDs: []string{"private", "public"}, Milestones: []domain.ProjectMilestone{}, CreatedAt: at, UpdatedAt: at},
		{ID: "nul\x00project", Name: "Native\x00name", Description: body, TeamIDs: []string{"public"}, PreviousSlugIDs: []string{"nul\x00old"}, Summary: "Mixed CASE", CreatedAt: at, UpdatedAt: at},
	}
}

func scaleP21SW90Queries() []ProjectRecordQuery {
	queries := []ProjectRecordQuery{
		{Admin: true}, {Admin: true, Archived: "all"}, {Admin: true, Archived: "true"},
		{AllowedTeamIDs: nil}, {AllowedTeamIDs: []string{}}, {AllowedTeamIDs: []string{"public"}},
		{Admin: true, TeamIDs: []string{"private"}}, {Admin: true, Search: " needle "},
		{Admin: true, Archived: "all", Search: "MIXED case"},
		{Admin: true, Search: "not present"}, {Admin: true, Limit: -1}, {Admin: true, Limit: 101},
	}
	for _, filter := range []ProjectDirectoryFilter{
		{Field: "status", Values: []string{"started"}}, {Field: "status", Values: []string{"In progress"}},
		{Field: "priority", Values: []string{"urgent"}}, {Field: "lead", Values: []string{"lead"}},
		{Field: "lead", Values: []string{""}}, {Field: "members", Values: []string{"member"}},
		{Field: "health", Values: []string{"on-track"}}, {Field: "health", Values: []string{"at-risk"}},
		{Field: "dates", Values: []string{"overdue"}}, {Field: "dates", Values: []string{"has-target"}},
		{Field: "dates", Values: []string{"no-target"}}, {Field: "milestones", Values: []string{"milestone"}},
		{Field: "milestones", Values: []string{"Retained milestone"}}, {Field: "labels", Values: []string{"label"}},
		{Field: "labels", Values: []string{""}}, {Field: "teams", Values: []string{"public"}},
		{Field: "project", Values: []string{"old-alpha"}}, {Field: "project", Values: []string{"stale-000000000001"}},
		{Field: "project", Values: []string{"000000000001"}}, {Field: "project", Values: []string{""}},
		{Field: "customers", Values: []string{"customer:customer"}},
		{Field: "customers", Values: []string{"customer-owner:owner"}},
		{Field: "customers", Values: []string{"customer-status:active"}},
		{Field: "customers", Values: []string{"customer-tier:gold"}},
	} {
		for _, operator := range []string{"is", "isNot"} {
			filter.Operator = operator
			queries = append(queries, ProjectRecordQuery{Admin: true, Archived: "all", Filters: []ProjectDirectoryFilter{filter}})
		}
	}
	for _, operator := range []string{"gte", "lte", "eq", "neq"} {
		queries = append(queries, ProjectRecordQuery{Admin: true, Archived: "all", Filters: []ProjectDirectoryFilter{{Field: "customers", Operator: operator, Values: []string{"customer-revenue:100"}}}})
	}
	for index := range queries {
		queries[index].CustomerSummaries = map[string]*ProjectCustomerSummary{
			"first":     {Customers: []ProjectCustomer{{ID: "customer", OwnerID: "owner", Status: "active", Tier: "gold", Revenue: 100, Size: 10}}},
			"duplicate": {UnknownCustomer: true},
		}
	}
	return queries
}

func scaleP21SW90Assert(t *testing.T, source *SQLiteStore, query ProjectRecordQuery, want ProjectRecordPage) int {
	t.Helper()
	queries := 0
	read, err := source.BeginProjectDirectoryQuery(WithSQLStatementObserver(t.Context(), func(string) { queries++ }), query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(read.Close)
	scaleP10Equal(t, want, read.Page)
	held := source.MetadataWorkUsedForTesting()
	if held == 0 {
		t.Fatal("directory output has no work owner")
	}
	read.Close()
	read.Close()
	if !reflect.DeepEqual(read.Page, ProjectRecordPage{}) || source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("directory close retained page/work or decoded workspace metadata")
	}
	return queries
}

func TestScaleP21SW90ProjectDirectoryLiteral(t *testing.T) {
	t.Setenv("FLOW_INDEXED_READS", "off")
	projects := scaleP21SW90Projects(2)
	values := map[string]domain.Bootstrap{"physical\x00": {Workspace: domain.Workspace{ID: "organization", URLKey: "logical"}, Projects: projects}}
	legacy := &SQLiteStore{workspaces: values, lastWorkspaceKey: "physical\x00"}
	view := scaleP21HotLogoStore(t, values)
	view.lastWorkspaceKey = "physical\x00"
	for index, query := range scaleP21SW90Queries() {
		query.Workspace = "physical\x00"
		if query.Limit == 0 {
			query.Limit = 2
		}
		for _, total := range []bool{false, true} {
			query.IncludeTotal = total
			for _, cursor := range []string{"", "invalid", encodeProjectCursor(1), encodeProjectCursor(1000)} {
				query.Cursor = cursor
				t.Run(fmt.Sprintf("%d/total-%t/cursor-%s", index, total, cursor), func(t *testing.T) {
					want, err := legacy.queryProjectDirectoryLegacy(t.Context(), query)
					if err != nil {
						t.Fatal(err)
					}
					if sql := scaleP21SW90Assert(t, view, query, want); sql != 0 {
						t.Fatal("native directory used SQL", sql)
					}
				})
			}
		}
	}
	for _, mode := range []string{"off", "on", "shadow", "invalid"} {
		t.Run("installed-mode-"+mode, func(t *testing.T) {
			t.Setenv("FLOW_INDEXED_READS", mode)
			query := ProjectRecordQuery{Workspace: "physical\x00", Admin: true, Archived: "all", IncludeTotal: true, Limit: 2}
			scaleP21SW90Assert(t, view, query, filterProjectDirectory(projects, query))
		})
	}
	read, err := view.BeginProjectDirectoryQuery(t.Context(), ProjectRecordQuery{Workspace: "physical\x00", Admin: true, Archived: "all"})
	if err != nil {
		t.Fatal(err)
	}
	before := slices.Clone(projects[0].Milestones)
	read.Page.Items[0].Milestones[0].Name = "caller-owned"
	read.Page.Items[0].TeamIDs[0] = "caller-owned"
	if projects[0].TeamIDs[0] != "private" || !reflect.DeepEqual(projects[0].Milestones, before) {
		t.Fatal("directory output aliases installed fields")
	}
	read.Close()
	many := make([]domain.Project, 145)
	for index := range many {
		many[index] = domain.Project{ID: fmt.Sprint(index), Name: fmt.Sprint(index), Description: "stripped"}
	}
	manyView := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"many": {Projects: many}, "empty": {Projects: []domain.Project{}}})
	for _, limit := range []int{-1, 0, 101, 1000} {
		query := ProjectRecordQuery{Workspace: "many", Admin: true, Archived: "all", Limit: limit, IncludeTotal: true}
		scaleP21SW90Assert(t, manyView, query, filterProjectDirectory(many, query))
	}
	emptyQuery := ProjectRecordQuery{Workspace: "empty", Admin: true, Archived: "all", IncludeTotal: true}
	scaleP21SW90Assert(t, manyView, emptyQuery, filterProjectDirectory([]domain.Project{}, emptyQuery))
	for _, query := range []ProjectRecordQuery{{}, {Workspace: " \t "}} {
		if read, err := view.BeginProjectDirectoryQuery(t.Context(), query); read != nil || !errors.Is(err, ErrIssueQuery) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("blank workspace directory changed", err)
		}
	}
}

func TestScaleP21SW90ProjectDirectoryCurrentAndProjected(t *testing.T) {
	t.Setenv("FLOW_INDEXED_READS", "off")
	before := domain.Bootstrap{Projects: scaleP21SW90Projects(2)}
	after := before
	after.Projects = slices.Clone(before.Projects)
	after.Projects[0].Name, after.Projects[0].Summary = "Current installed name", "Current installed summary"
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": before, "foreign": {Projects: []domain.Project{{ID: "foreign", Name: "Foreign"}}}})
	view.lastWorkspaceKey = "physical"
	parent, _, err := view.BeginInstalledMetadataRead(t.Context(), "foreign")
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("physical", next)
	view.mu.Lock()
	view.workspaces["physical"] = after
	view.mu.Unlock()
	query := ProjectRecordQuery{Workspace: "physical", Admin: true, Archived: "all", IncludeTotal: true}
	read, err := view.BeginProjectDirectoryQuery(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	parent.Close()
	scaleP10Equal(t, filterProjectDirectory(after.Projects, query), read.Page)
	read.Close()
	// A pre-projected handoff deliberately owns a different generation.
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw90_projected_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = []domain.Project{{ID: "selected", Name: "Installed", Summary: "Installed summary", TeamIDs: nil, Milestones: []domain.ProjectMilestone{{ID: "milestone", Name: "Retained", Description: "Ignored"}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bounded, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.releaseMetadataLogicalSources()
	handed, _ := f.WorkspaceMetadata(workspace)
	handed.Projects = slices.Clone(handed.Projects)
	handed.Projects[0].Name, handed.Projects[0].Summary = "Earlier handed name", "Earlier handed summary"
	directory, err := bounded.BeginResourceDirectoryRead(t.Context(), workspace, "", nil, "projects", &handed)
	if err != nil {
		t.Fatal(err)
	}
	query = ProjectRecordQuery{Workspace: workspace, Admin: true, Archived: "all", Search: "handed", IncludeTotal: true}
	read, err = directory.QueryProjectDirectory(t.Context(), query)
	if err != nil {
		directory.Close()
		t.Fatal(err)
	}
	directory.Close()
	scaleP10Equal(t, QueryProjectedProjectDirectory(handed.Projects, query), read.Page)
	read.Close()
	if bounded.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("projected page borrowed its directory lifetime")
	}
}

func TestScaleP21SW90ProjectDirectoryCanonicalAndCatalog(t *testing.T) {
	s, workspace := scaleP05Database(t)
	projects := scaleP21SW90Projects(2)
	projects[5].ID, projects[5].Name = "ordinary", "Ordinary"
	projects[5].PreviousSlugIDs = []string{"ordinary-old"}
	for index, project := range projects {
		scaleP05Put(t, s, workspace, "projects", fmt.Sprintf("physical-%02d", index), index/2, project)
	}
	scaleP05Finish(t, s, workspace, "projects")
	view := &SQLiteStore{db: s.db, dialect: s.dialect, metadataResidency: newMetadataResidency(), workspaces: map[string]domain.Bootstrap{}}
	defer view.releaseMetadataLogicalSources()
	for _, mode := range []string{"off", "on", "shadow"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_INDEXED_READS", mode)
			for index, query := range scaleP21SW90Queries() {
				query.Workspace, query.Limit, query.IncludeTotal = workspace, 2, true
				want, err := s.queryProjectDirectoryAdapter(t.Context(), query)
				if err != nil {
					t.Fatal(err)
				}
				t.Run(fmt.Sprint(index), func(t *testing.T) {
					scaleP21SW90Assert(t, view, query, want)
				})
			}
		})
	}
	t.Setenv("FLOW_INDEXED_READS", "off")
	tx, err := s.db.BeginTx(t.Context(), metadataReadOptions(s.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace + "-other", reader: tx})
	query := ProjectRecordQuery{Workspace: workspace, Admin: true, Archived: "all", IncludeTotal: true}
	want, err := s.queryProjectDirectoryLegacy(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginProjectDirectoryQuery(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	scaleP10Equal(t, want, read.Page)
	read.Close()
	var one int
	if err := tx.QueryRowContext(t.Context(), "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatal("directory reader consumed borrowed cross-namespace transaction", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	nilSourceData := domain.Bootstrap{Workspace: domain.Workspace{ID: "canonical", URLKey: workspace}, Projects: nil}
	view.mu.Lock()
	view.workspaces[workspace] = nilSourceData
	view.mu.Unlock()
	// Policy-only recovery deliberately has no logical generation. Its
	// nil Projects matches the literal SQL-only projectSnapshot fallback.
	scaleP21SW90Assert(t, view, query, want)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if source, usable, err := view.projectDirectoryInstalledSource(cancelled, workspace); source != nil || usable || !errors.Is(err, context.Canceled) {
		t.Fatal("policy-only fallback swallowed cancellation", usable, err)
	}
	populated := nilSourceData
	populated.Projects = []domain.Project{}
	view.mu.Lock()
	view.workspaces[workspace] = populated
	view.mu.Unlock()
	failed, failure := view.BeginProjectDirectoryQuery(t.Context(), query)
	if failed != nil {
		failed.Close()
	}
	if failed != nil || !errors.Is(failure, ErrBoundedMetadataCoverage) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("missing logical source for an authoritative empty snapshot silently fell back", failure)
	}
	view.mu.Lock()
	view.workspaces[workspace] = nilSourceData
	view.mu.Unlock()
	nilSource, err := captureMetadataLogicalSource(t.Context(), nilSourceData, copyStamp{workspaceID: "canonical", revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	view.mu.Lock()
	view.workspaces[workspace] = nilSourceData
	view.mu.Unlock()
	view.installMetadataLogicalSource(workspace, nilSource)
	scaleP21SW90Assert(t, view, query, want)
	if s.dialect == "sqlite" {
		// Identity-only storage-class changes bypass neither body validation
		// nor the canonical SQL ordering selected by the literal reader.
		if _, err := s.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET record_key=CAST(record_key AS BLOB) WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, "physical-01"); err != nil {
			t.Fatal(err)
		}
		want, err = s.queryProjectDirectoryLegacy(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		scaleP21SW90Assert(t, view, query, want)
	}
}

func TestScaleP21SW90ProjectDirectoryWarmReloadAndRelease(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_INDEXED_READS", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	held := map[bool]int{}
	for _, size := range []int{2000, 20000} {
		restore := ForceFullMutationsForTesting(true)
		err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw90_body_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = []domain.Project{scaleP21SW90Projects(size)[0]}
			return nil
		})
		restore()
		if err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			t.Run(fmt.Sprintf("size-%d/reload-%t", size, reload), func(t *testing.T) {
				if reload {
					f.mu.Lock()
					f.forgetCopyRevisionLocked(workspace)
					f.mu.Unlock()
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
					t.Fatal(err)
				}
				query := ProjectRecordQuery{Workspace: workspace, Admin: true, Archived: "all", IncludeTotal: true}
				want, err := f.queryProjectDirectoryLegacy(t.Context(), query)
				if err != nil {
					t.Fatal(err)
				}
				read, err := view.BeginProjectDirectoryQuery(t.Context(), query)
				if err != nil {
					t.Fatal(err)
				}
				scaleP10Equal(t, want, read.Page)
				work := view.MetadataWorkUsedForTesting()
				if held[reload] == 0 {
					held[reload] = work
				}
				if work != held[reload] {
					t.Fatal("10x stripped bodies increased retained directory work", work, held[reload])
				}
				read.Close()
				t.Logf("body_size=%d reload=%t directory_held=%d", size, reload, work)
			})
		}
	}
	query := ProjectRecordQuery{Workspace: workspace, Admin: true, Archived: "all"}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := view.BeginProjectDirectoryQuery(cancelled, query); read != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cancelled directory retained output/work", err)
	}
	limit := view.metadataResidency.work.limit
	t.Cleanup(func() { view.metadataResidency.work.limit = limit })
	for _, budget := range []int{1, 128 << 10, 256 << 10} {
		view.metadataResidency.work.limit = budget
		read, err := view.BeginProjectDirectoryQuery(t.Context(), query)
		if read != nil {
			read.Close()
		}
		if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("directory partial admission leaked work", budget, err)
		}
	}
	view.metadataResidency.work.limit = 512 << 10
	read, err := view.BeginProjectDirectoryQuery(t.Context(), query)
	if err != nil || read == nil {
		if read != nil {
			read.Close()
		}
		t.Fatal("directory fixture no longer fits its admitted budget", err)
	}
	if view.MetadataWorkUsedForTesting() == 0 {
		read.Close()
		t.Fatal("successful directory read had no retained work")
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("successful directory read retained work after close")
	}
	view.metadataResidency.work.limit = limit
	source, err := view.BeginProjectDirectoryQuery(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(source.Page)
	if err != nil {
		t.Fatal(err)
	}
	view.releaseMetadataLogicalSources()
	got, err := json.Marshal(source.Page)
	if err != nil || string(got) != string(want) {
		t.Fatal("owned directory page borrowed installed source lifetime", err)
	}
	source.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("directory output retained work after source release")
	}
}

func TestScaleP21SW90ProjectDirectoryCanonicalBodyBound(t *testing.T) {
	t.Setenv("FLOW_INDEXED_READS", "off")
	s, workspace := scaleP05Database(t)
	view := &SQLiteStore{db: s.db, dialect: s.dialect, metadataResidency: newMetadataResidency(), workspaces: map[string]domain.Bootstrap{}}
	defer view.releaseMetadataLogicalSources()
	var expectedHeld, expectedPeak int
	for _, size := range []int{2000, 20000} {
		project := scaleP21SW90Projects(size)[0]
		scaleP05Put(t, s, workspace, "projects", "one-physical-row", 0, project)
		query := ProjectRecordQuery{Workspace: workspace, Admin: true, Archived: "all", IncludeTotal: true}
		want, err := s.queryProjectDirectoryLegacy(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		peak, queries := 0, 0
		ctx := WithSQLStatementObserver(t.Context(), func(string) {
			queries++
			peak = max(peak, view.MetadataWorkUsedForTesting())
		})
		disk := metadataLogicalDiskUsed.Load()
		read, err := view.BeginProjectDirectoryQuery(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		scaleP10Equal(t, want, read.Page)
		held := view.MetadataWorkUsedForTesting()
		if expectedHeld == 0 {
			expectedHeld, expectedPeak = held, peak
		}
		if held != expectedHeld || peak != expectedPeak || metadataLogicalDiskUsed.Load() != disk {
			read.Close()
			t.Fatal("10x stripped canonical bodies increased heap admission or retained a row spool", held, expectedHeld, peak, expectedPeak)
		}
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("canonical body projection retained work")
		}
		t.Logf("body_size=%d canonical_directory_sql=%d sql_peak=%d output_held=%d", size, queries, peak, held)
	}
}
