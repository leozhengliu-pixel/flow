package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21ApplicationTaskContextReadNative(t *testing.T) {
	at := time.Date(2025, 1, 2, 3, 4, 5, 6, time.UTC)
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "workspace", URLKey: "native\x00workspace", Name: "Native workspace"},
		Viewer:    domain.User{ID: "viewer"}, Users: []domain.User{{ID: "actor", Name: "First"}, {ID: "actor", Name: "Last\x00actor"}},
		Teams: []domain.Team{{ID: "team", Name: "Native team", Key: "NATIVE"}, {ID: "parent", Name: "Parent"}},
		TeamSettings: map[string]domain.TeamSettings{
			"team": {ParentTeamID: "parent", AgentGuidance: "Team instructions"}, "parent": {AgentGuidance: "Parent instructions"},
		},
		UserSettings: map[string]domain.UserSettings{"viewer": {AgentInstructions: "Personal instructions"}},
		Projects: []domain.Project{
			{ID: "project", Name: "Native\x00project", IssueCount: 73, TeamIDs: []string{"team"}, Creator: &domain.User{ID: "actor", Name: "Stale"}},
			{ID: "project", Name: "Duplicate project", IssueCount: 83, TeamIDs: []string{"team"}},
		},
		Documents: []domain.Document{
			{ID: "hidden-exact", SlugID: "alias", Title: "First document"},
			{ID: "document", PreviousSlugIDs: []string{"alias"}, Title: "Alias document", ContentData: map[string]any{"number": int64(7), "time": at}},
		},
		Issues: []domain.Issue{
			{ID: "issue", Team: domain.Team{ID: "team"}, Project: &domain.ProjectSummary{ID: "project"}},
			{ID: "archived", Team: domain.Team{ID: "team"}, Project: &domain.ProjectSummary{ID: "project"}, ArchivedAt: &at},
		},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical\x00": data})
	view.lastWorkspaceKey = "physical\x00"
	for _, metadata := range []bool{false, true} {
		for _, kind := range []string{"project", "document", "codingEnvironment"} {
			request := ApplicationTaskContextRequest{ResourceType: kind, ResourceID: "project", TeamID: "team", MetadataOnly: metadata}
			if kind == "document" {
				request.ResourceID = "alias"
			}
			queries := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
			read, err := view.BeginApplicationTaskContextRead(ctx, "", "", nil, request)
			if err != nil {
				t.Fatal(kind, metadata, err)
			}
			if read.Data.ViewerRole != "admin" || read.Data.Workspace != data.Workspace || read.Data.TeamSettings["parent"].AgentGuidance != "Parent instructions" ||
				read.Data.UserSettings["viewer"].AgentInstructions != "Personal instructions" || queries != 0 || view.MetadataWorkUsedForTesting() == 0 {
				t.Fatal("native task context changed or lacks ownership", kind, metadata, queries)
			}
			if kind == "project" {
				wantName := "Last\x00actor"
				if metadata {
					wantName = "Stale"
				}
				if read.Data.Projects[0].Creator.Name != wantName || read.ProjectIssueCount != map[bool]int{false: 1, true: 0}[metadata] {
					t.Fatal("native actor refresh or inline issue count changed", metadata, read.Data.Projects[0], read.ProjectIssueCount)
				}
			}
			if kind == "document" {
				before, _ := json.Marshal(data.Documents)
				after, _ := json.Marshal(read.Data.Documents)
				if !bytes.Equal(before, after) {
					t.Fatal("task document matching lost exact/alias collection order")
				}
			}
			if len(read.Data.Issues) != 0 {
				t.Fatal("task context exposed count-only issue inputs")
			}
			if err := read.TransferOutput(t.Context()); !errors.Is(err, ErrBoundedMetadataCoverage) || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("task context escaped without a containing owner", err)
			}
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	request := ApplicationTaskContextRequest{ResourceType: "project", ResourceID: "project", TeamID: "team"}
	if read, err := view.BeginApplicationTaskContextRead(cancelled, "", "", nil, request); !errors.Is(err, context.Canceled) || read != nil || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cancelled task context retained work", err)
	}
	limit := view.metadataResidency.work.limit
	for _, size := range []int{1, 128 << 10, 2<<20 + 128<<10} {
		view.metadataResidency.work.limit = size
		if read, err := view.BeginApplicationTaskContextRead(t.Context(), "", "", nil, request); !errors.Is(err, ErrMetadataWorkBudget) || read != nil || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("partial task admission leaked", size, err)
		}
	}
	view.metadataResidency.work.limit = limit
}

func TestScaleP21ApplicationTaskContextReadPinned(t *testing.T) {
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "workspace", URLKey: "physical"},
		Projects:  []domain.Project{{ID: "project", Name: "Before"}},
		Issues:    []domain.Issue{},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	parent, _, err := view.BeginInstalledMetadataRead(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data = cloneBootstrap(data)
	data.Projects[0].Name = "After"
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("physical", source)
	request := ApplicationTaskContextRequest{ResourceType: "project", ResourceID: "project"}
	read, err := view.BeginApplicationTaskContextRead(ctx, "physical", "", nil, request)
	if err != nil || read.Data.Projects[0].Name != "Before" {
		t.Fatal("task context changed pinned source", err)
	}
	parent.Close()
	if read.Data.Projects[0].Name != "Before" {
		t.Fatal("task context borrowed parent lifetime")
	}
	read.Close()
	var workers sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 3 {
				read, err := view.BeginApplicationTaskContextRead(t.Context(), "physical", "", nil, request)
				if err == nil && read.Data.Projects[0].Name != "After" {
					err = ErrBoundedMetadataCoverage
				}
				if read != nil {
					read.Close()
				}
				if err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("concurrent task context retained work")
	}
}

func TestScaleP21ApplicationTaskContextReadDatabase(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("p21-task-reader-")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	var baseline map[string]int
	for _, repetitions := range []int{1000, 10000} {
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.task_reader_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = []domain.Project{
				{ID: prefix + "project", Name: "Target project", TeamIDs: []string{data.Teams[0].ID}, Creator: &domain.User{ID: data.Viewer.ID, Name: "Old actor"}},
				{ID: prefix + "unrelated", Description: strings.Repeat("Unrelated project body ", repetitions)},
			}
			data.Documents = []domain.Document{
				{ID: prefix + "document", Title: "Target document", Content: "Content", Creator: data.Viewer},
				{ID: prefix + "unrelated-document", Content: strings.Repeat("Unrelated document body ", repetitions)},
			}
			at := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
			data.Issues = []domain.Issue{
				{ID: prefix + "issue", Identifier: "CTX-44", Title: "Count target", Description: strings.Repeat("Unused issue description ", repetitions), Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, Project: &domain.ProjectSummary{ID: prefix + "project"}, CreatedAt: at, UpdatedAt: at},
				{ID: prefix + "other-issue", Identifier: "CTX-45", Title: "Unrelated", Description: strings.Repeat("Unrelated issue description ", repetitions), Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, CreatedAt: at, UpdatedAt: at},
			}
			data.TeamSettings[data.Teams[0].ID] = domain.TeamSettings{AgentGuidance: "Required team guidance", Description: strings.Repeat("Unused team description ", repetitions)}
			value := data.UserSettings[data.Viewer.ID]
			value.AgentInstructions = "Required personal guidance"
			data.UserSettings[data.Viewer.ID] = value
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		costs := map[string]int{}
		for _, state := range []string{"warm", "reloaded", "unknown-revision"} {
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
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"project", "document", "codingEnvironment"} {
				ref := prefix + kind
				var peak, queries atomic.Int64
				ctx := WithSQLStatementObserver(t.Context(), func(string) {
					queries.Add(1)
					used := int64(view.MetadataWorkUsedForTesting())
					for previous := peak.Load(); used > previous && !peak.CompareAndSwap(previous, used); previous = peak.Load() {
					}
				})
				read, err := view.BeginApplicationTaskContextRead(ctx, workspace, f.data.Viewer.ID, nil, ApplicationTaskContextRequest{ResourceType: kind, ResourceID: ref, TeamID: f.data.Teams[0].ID})
				if err != nil {
					t.Fatal(kind, state, err)
				}
				legacyQueries := 0
				legacy, ok, err := f.BootstrapForUser(WithSQLStatementObserver(t.Context(), func(string) { legacyQueries++ }), workspace, f.data.Viewer.ID)
				if !ok || err != nil {
					t.Fatal("literal task context", err)
				}
				if !reflect.DeepEqual(legacy.Viewer, read.Data.Viewer) || legacy.ViewerRole != read.Data.ViewerRole || legacy.Workspace != read.Data.Workspace ||
					read.Data.TeamSettings[f.data.Teams[0].ID].AgentGuidance != legacy.TeamSettings[f.data.Teams[0].ID].AgentGuidance ||
					read.Data.UserSettings[f.data.Viewer.ID].AgentInstructions != legacy.UserSettings[f.data.Viewer.ID].AgentInstructions {
					t.Fatal("task identity/guidance differs", kind, state)
				}
				if kind == "project" {
					before, _ := json.Marshal(legacy.Projects[0])
					value := read.Data.Projects[0]
					value.IssueCount = read.ProjectIssueCount
					after, _ := json.Marshal(value)
					if !bytes.Equal(before, after) {
						t.Fatalf("task project differs %s:\n%s\n%s", state, before, after)
					}
				}
				costs[state+"/"+kind] = view.MetadataWorkUsedForTesting()
				costs[state+"/"+kind+"/peak"] = int(peak.Load())
				t.Logf("body_repetitions=%d state=%s kind=%s SQL_off=%d SQL_bounded=%d SQL_observed_admitted_peak=%d held_output=%d", repetitions, state, kind, legacyQueries, queries.Load(), peak.Load(), costs[state+"/"+kind])
				read.Close()
				if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("task context retained work or fully decoded metadata")
				}
			}
		}
		if baseline == nil {
			baseline = costs
		} else if !reflect.DeepEqual(baseline, costs) {
			t.Fatal("unrelated body growth changed task context admission", baseline, costs)
		}
		t.Logf("unrelated_body_repetitions=%d task_context_admission=%v", repetitions, costs)
	}
	access, err := view.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = f.db.ExecContext(context.Background(), `UPDATE workspace_memberships SET status='active' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID)
	}()
	if err := view.ValidateAccess(t.Context(), access); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("fixture failed to revoke authority", err)
	}
	if read, err := view.BeginApplicationTaskContextRead(t.Context(), workspace, f.data.Viewer.ID, nil, ApplicationTaskContextRequest{ResourceType: "project", ResourceID: prefix + "project"}); !errors.Is(err, ErrAuthForbidden) || read != nil || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("revoked task context escaped", err)
	}
}
