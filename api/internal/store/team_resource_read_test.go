package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW307ResourceOutput(data domain.Bootstrap, team string) domain.Bootstrap {
	result := domain.Bootstrap{TeamResourceSections: []domain.TeamResourceSection{}, TeamPinnedResources: []domain.TeamPinnedResource{}}
	for _, item := range data.TeamResourceSections {
		if item.TeamID == team {
			result.TeamResourceSections = append(result.TeamResourceSections, item)
		}
	}
	for _, item := range data.TeamPinnedResources {
		if item.TeamID == team {
			result.TeamPinnedResources = append(result.TeamPinnedResources, item)
		}
	}
	sort.SliceStable(result.TeamResourceSections, func(i, j int) bool {
		return result.TeamResourceSections[i].Position < result.TeamResourceSections[j].Position
	})
	sort.SliceStable(result.TeamPinnedResources, func(i, j int) bool {
		return result.TeamPinnedResources[i].Position < result.TeamPinnedResources[j].Position
	})
	return result
}

func TestScaleP21SW307OwnedNativeInlineResources(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, userID, team := f.data.Workspace.URLKey, f.data.Viewer.ID, f.data.Teams[0].ID
	prefix := idgen.New("sw307-store-")
	at := time.Date(2026, 11, 17, 1, 2, 3, 456, time.FixedZone("native-sw307", 7*3600+123))
	release := ForceFullMutationsForTesting(true)
	err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw307_rows", "", nil, func(data *domain.Bootstrap) error {
		data.TeamResourceSections = []domain.TeamResourceSection{
			{ID: prefix, TeamID: team, Name: "later", Position: 2, CreatedAt: at},
			{ID: prefix, TeamID: team, Name: "first tie", Position: 1, CreatedAt: at},
			{ID: "", TeamID: team, Name: "second tie", Position: 1, CreatedAt: at},
			{ID: prefix + "hidden", TeamID: prefix + "other", Name: strings.Repeat("unrelated ", 100000)},
		}
		data.TeamPinnedResources = []domain.TeamPinnedResource{
			{ID: prefix, TeamID: team, ResourceType: "link", ResourceID: "retained-reference", Title: strings.Repeat("selected <&> ", 40000), Position: 1, CreatedAt: at},
			{ID: prefix, TeamID: team, ResourceType: "link", Title: strings.Repeat("second selected <&> ", 40000), Position: 1, CreatedAt: at},
			{ID: "", TeamID: team, ResourceType: "link", Title: strings.Repeat("first selected <&> ", 40000), Position: -1, CreatedAt: at},
		}
		return nil
	})
	release()
	if err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"warm", "reloaded", "unknown-revision", "caller-snapshot"} {
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
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			data, found := f.WorkspaceMetadata(workspace)
			if !found {
				t.Fatal("literal source missing")
			}
			ctx := t.Context()
			if state == "caller-snapshot" {
				tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				data.TeamResourceSections[1].Name = "caller snapshot"
				data.TeamPinnedResources[0].Title = "caller-only native source"
				ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx, metadata: data})
			}
			literal, err := f.PagedWorkspaceMetadata(ctx, workspace, userID)
			if err != nil {
				t.Fatal(err)
			}
			want := scaleP21SW307ResourceOutput(literal, team)
			queries, bodyQueries := 0, 0
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				queries++
				if strings.Contains(query, "field='teamResourceSections'") || strings.Contains(query, "field='teamPinnedResources'") ||
					strings.Contains(query, "FROM issue_records") || strings.Contains(query, "FROM workspace_content_records") {
					bodyQueries++
				}
			})
			read, err := view.BeginTeamResourceAuthorizationRead(ctx, workspace, userID, team, true, nil)
			if err != nil || !read.Allowed || len(read.Data.Teams) != 1 || read.Data.Teams[0].ID != team {
				t.Fatal("owned team resource authorization failed", err)
			}
			got := scaleP21SW307ResourceOutput(read.Data, team)
			// FixedZone's private Location graph is intentionally unavailable
			// to native readers. This output-only route requires exact JSON,
			// including timestamp offsets and stable ties, not zone identity.
			before, err := json.Marshal(want)
			if err != nil {
				t.Fatal("literal team resource JSON", err)
			}
			after, err := json.Marshal(got)
			if err != nil {
				t.Fatal("bounded team resource JSON", err)
			}
			if !bytes.Equal(before, after) || bodyQueries != 0 || view.WorkspaceMetadataDecodes() != 0 ||
				view.MetadataWorkUsedForTesting() == 0 || !read.SourceRetainedForTesting() {
				t.Fatal("owned output lost exact JSON, source or admission, or loaded canonical bodies", queries, bodyQueries)
			}
			retainedStrings := 0
			for _, row := range got.TeamPinnedResources {
				retainedStrings += len(row.Title)
			}
			if view.MetadataWorkUsedForTesting() < retainedStrings*8 {
				t.Fatal("complete multi-row output was charged only as a largest-row cursor")
			}
			read.Close()
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 || read.SourceRetainedForTesting() || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) {
				t.Fatal("close retained complete output/source/admission")
			}
			t.Logf("state=%s SQL=%d body_SQL=%d", state, queries, bodyQueries)
		})
	}
	t.Run("cancellation-and-partial-admission", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if read, err := view.BeginTeamResourceAuthorizationRead(ctx, workspace, userID, team, true, nil); !errors.Is(err, context.Canceled) ||
			read != nil || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("cancelled projection retained a partial owner", err)
		}
		ctx, cancel = context.WithCancel(t.Context())
		defer cancel()
		seen := false
		ctx = WithSQLStatementObserver(ctx, func(query string) {
			if strings.Contains(query, "workspace_memberships") {
				seen = true
				cancel()
			}
		})
		read, err := view.BeginTeamResourceAuthorizationRead(ctx, workspace, userID, team, true, nil)
		if !seen || err == nil || read != nil || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("cancellation after source/policy admission retained output", seen, err)
		}
		limit := view.metadataResidency.work.limit
		defer func() { view.metadataResidency.work.limit = limit }()
		for _, amount := range []int{1, 2 << 20, 3 << 20, 4<<20 + 128<<10} {
			view.metadataResidency.work.limit = amount
			read, err := view.BeginTeamResourceAuthorizationRead(t.Context(), workspace, userID, team, true, nil)
			if !errors.Is(err, ErrMetadataWorkBudget) || read != nil || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("partial total-output admission failure leaked", amount, err)
			}
		}
	})
	t.Run("concurrent-independent-owners", func(t *testing.T) {
		var workers sync.WaitGroup
		failures := make(chan error, 4)
		for range 4 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				read, err := view.BeginTeamResourceAuthorizationRead(t.Context(), workspace, userID, team, true, nil)
				if err != nil {
					failures <- err
					return
				}
				if !read.Allowed || len(read.Data.TeamResourceSections) != 3 || read.Data.TeamResourceSections[0].Name != "first tie" {
					failures <- fmt.Errorf("independent output changed")
				}
				read.Close()
			}()
		}
		workers.Wait()
		close(failures)
		for err := range failures {
			t.Error(err)
		}
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("concurrent readers retained output work")
		}
	})
	t.Run("fresh-read-observes-revocation", func(t *testing.T) {
		first, err := view.BeginTeamResourceAuthorizationRead(t.Context(), workspace, userID, team, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer first.Close()
		held := view.MetadataWorkUsedForTesting()
		before, err := json.Marshal(first.Data.TeamPinnedResources)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, userID); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := f.db.ExecContext(context.Background(), `UPDATE workspace_memberships SET status='active' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, userID); err != nil {
				t.Error(err)
			}
		}()
		next, err := view.BeginTeamResourceAuthorizationRead(t.Context(), workspace, userID, team, true, nil)
		if !errors.Is(err, ErrAuthForbidden) || next != nil || view.MetadataWorkUsedForTesting() != held {
			t.Fatal("fresh projection reused revoked authority or leaked its failed owner", err)
		}
		after, err := json.Marshal(first.Data.TeamPinnedResources)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("later refusal mutated the already-owned output", err)
		}
		first.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("revocation path retained source/output admission")
		}
	})
}
