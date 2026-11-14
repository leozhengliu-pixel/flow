package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21HotNotificationsPagingAndAuthority(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, principal := f.data.Workspace.URLKey, f.data.Viewer.ID
	now := time.Now().UTC().Truncate(time.Millisecond)
	var ids []string
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.hot_notifications", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: "not-selected", Private: true})
		data.TeamSettings["not-selected"] = domain.TeamSettings{TeamID: "not-selected", Access: "private"}
		data.Projects = append(data.Projects,
			domain.Project{ID: "hot-notification-public", TeamIDs: []string{data.Teams[0].ID}},
			domain.Project{ID: "hot-notification-hidden", TeamIDs: []string{"not-selected"}},
			domain.Project{ID: "hot-notification-huge", Description: strings.Repeat("unrelated ", 100000)},
		)
		data.Notifications = nil
		for index := range 137 {
			item := domain.Notification{ID: fmt.Sprintf("hot-notification-%03d", index), RecipientID: principal, Actor: data.Viewer, CreatedAt: now, UpdatedAt: now}
			if index%3 == 1 {
				item.ProjectID = "hot-notification-public"
			} else if index%3 == 2 {
				item.ProjectID = "hot-notification-hidden"
			}
			data.Notifications = append(data.Notifications, item)
			if index%3 != 2 {
				ids = append(ids, item.ID)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	key := &domain.APIKey{ID: "hot-notification-key", TeamRestriction: "selected", TeamIDs: []string{f.data.Teams[0].ID}, Scopes: []string{"read"}}
	access, err := view.ResolveAccess(t.Context(), workspace, principal, key)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(ids)
	slices.Reverse(want)
	var got []string
	query := NotificationQuery{Workspace: workspace, UserID: principal, Limit: 17}
	for {
		read, err := view.BeginNotificationVisibilityRead(t.Context(), access)
		if err != nil {
			t.Fatal(err)
		}
		page, err := read.Query(query)
		if err != nil || page.UnreadCount != len(want) || len(page.Notifications) > 17 {
			read.Close()
			t.Fatalf("page=%+v error=%v", page, err)
		}
		if view.metadataResidency.work.used == 0 {
			t.Fatal("returned bodies lost processing admission")
		}
		for _, item := range page.Notifications {
			got = append(got, item.ID)
		}
		read.Close()
		if view.metadataResidency.work.used != 0 {
			t.Fatal("notification read leaked admission")
		}
		if !page.HasMore {
			break
		}
		query.Cursor = page.NextCursor
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keyset pages=%v want=%v", got, want)
	}
	read, err := view.BeginNotificationVisibilityRead(t.Context(), access)
	if err != nil {
		t.Fatal(err)
	}
	source, err := f.NotificationState(t.Context(), workspace, principal)
	if err != nil {
		t.Fatal(err)
	}
	source.ChangedIDs = []string{ids[6], "hot-notification-002", ids[0], ids[6]}
	state, err := read.State(&source)
	read.Close()
	if err != nil || state.UnreadCount != len(want) || !reflect.DeepEqual(state.ChangedIDs, []string{ids[6], ids[0], ids[6]}) {
		t.Fatalf("ordered changed IDs=%+v error=%v", state, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := view.BeginNotificationVisibilityRead(cancelled, access); !errors.Is(err, context.Canceled) || read != nil {
		t.Fatalf("cancelled read=%v error=%v", read, err)
	}
	forged := access
	forged.PrincipalID = "forged"
	if read, err := view.BeginNotificationVisibilityRead(t.Context(), forged); !errors.Is(err, ErrAuthForbidden) || read != nil {
		t.Fatalf("forged read=%v error=%v", read, err)
	}
	read, err = view.BeginNotificationVisibilityRead(t.Context(), access)
	if err != nil {
		t.Fatal(err)
	}
	_, err = read.Query(NotificationQuery{Workspace: workspace, UserID: "forged"})
	read.Close()
	if !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("query widened its recipient binding", err)
	}
	// Changing native membership while a read snapshot is queued must revoke
	// that result after its old-snapshot work finishes.
	parent, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Rollback()
	if _, err := readMetadataStamp(t.Context(), parent, workspace); err != nil {
		t.Fatal(err)
	}
	inherited := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: parent})
	read, err = view.BeginNotificationVisibilityRead(inherited, access)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SetTeamMembership(t.Context(), f.data.Workspace.ID, "not-selected", principal, "owner", true); err != nil {
		read.Close()
		t.Fatal(err)
	}
	_, err = read.State(nil)
	read.Close()
	if !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("queued membership change released state: %v", err)
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("notification reads decoded workspace metadata or leaked admission")
	}
}

func TestScaleP21HotNotificationsSourceChoiceAndBudgets(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, principal := f.data.Workspace.URLKey, f.data.Viewer.ID
	team := f.data.Teams[0].ID
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.hot_notifications_source", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = []domain.Project{{ID: "hot-source-project", TeamIDs: []string{team}}}
		now := time.Now().UTC()
		data.Notifications = []domain.Notification{
			{ID: "hot-source", RecipientID: principal, ProjectID: "hot-source-project", CreatedAt: now, UpdatedAt: now},
			{ID: "hot-large", RecipientID: principal, Title: strings.Repeat("large body ", 40000), PulseSummary: &domain.PulseSummary{Text: strings.Repeat("generated summary ", 200000)}, CreatedAt: now.Add(-time.Second), UpdatedAt: now},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	installed := f.workspaces[workspace]
	installed.Projects[0].TeamIDs = []string{"not-selected"}
	f.workspaces[workspace] = installed
	f.mu.Unlock()
	key := &domain.APIKey{ID: "hot-source-key", TeamRestriction: "selected", TeamIDs: []string{team}, Scopes: []string{"read"}}
	for _, exact := range []bool{true, false} {
		if !exact {
			f.mu.Lock()
			f.forgetCopyRevisionLocked(workspace)
			f.mu.Unlock()
		}
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		access, err := view.ResolveAccess(t.Context(), workspace, principal, key)
		if err != nil {
			t.Fatal(err)
		}
		read, err := view.BeginNotificationVisibilityRead(t.Context(), access)
		if err != nil {
			t.Fatal(err)
		}
		state, err := read.State(nil)
		read.Close()
		want := 1
		if !exact {
			want = 2
		}
		if err != nil || state.UnreadCount != want {
			t.Fatalf("exact=%v state=%+v want=%d error=%v", exact, state, want, err)
		}
		// QueryNotifications accepts this legal large body. Size-based work
		// admission retains it, while generated Pulse summaries stay in SQL.
		read, err = view.BeginNotificationVisibilityRead(t.Context(), access)
		if err != nil {
			t.Fatal(err)
		}
		page, err := read.Query(NotificationQuery{Workspace: workspace, UserID: principal})
		if err != nil || len(page.Notifications) != want || len(page.Notifications[len(page.Notifications)-1].Title) != 440000 || page.Notifications[len(page.Notifications)-1].PulseSummary != nil {
			read.Close()
			t.Fatalf("large selected body=%+v error=%v", page, err)
		}
		if view.metadataResidency.work.used < 10<<20 {
			t.Fatal("large returned body lost size-based work admission")
		}
		read.Close()
		read, err = view.BeginNotificationVisibilityRead(t.Context(), access)
		if err != nil {
			t.Fatal(err)
		}
		_, release, err := view.tryAdmitAdditionalMetadataWork(t.Context(), metadataWorkBytes-view.metadataResidency.work.used-(1<<20))
		if err != nil {
			t.Fatal(err)
		}
		_, err = read.Query(NotificationQuery{Workspace: workspace, UserID: principal})
		release()
		read.Close()
		if !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatalf("saturated work admitted a large body: %v", err)
		}
		if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
			t.Fatal("source selection decoded full metadata or leaked admission")
		}
		view.releaseMetadataLogicalSources()
	}
}

func TestScaleP21HotNotificationsUnrelatedCountBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, principal := f.data.Workspace.URLKey, f.data.Viewer.ID
	key := &domain.APIKey{ID: "hot-notification-count-key", TeamRestriction: "selected", TeamIDs: []string{f.data.Teams[0].ID}, Scopes: []string{"read"}}
	held := map[bool]int{}
	for _, count := range []int{100, 1000} {
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.hot_notification_count", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = []domain.Project{{ID: "hot-count-target", TeamIDs: []string{f.data.Teams[0].ID}}}
			for index := range count {
				data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("hot-count-unrelated-%04d", index), TeamIDs: []string{f.data.Teams[0].ID}, Description: "unrelated"})
			}
			now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
			data.Notifications = []domain.Notification{{ID: "hot-count", RecipientID: principal, ProjectID: "hot-count-target", Actor: data.Viewer, CreatedAt: now, UpdatedAt: now}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		for _, canonical := range []bool{false, true} {
			if canonical {
				f.mu.Lock()
				f.forgetCopyRevisionLocked(workspace)
				f.mu.Unlock()
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			access, err := view.ResolveAccess(t.Context(), workspace, principal, key)
			if err != nil {
				t.Fatal(err)
			}
			read, err := view.BeginNotificationVisibilityRead(t.Context(), access)
			if err != nil {
				t.Fatal(err)
			}
			page, err := read.Query(NotificationQuery{Workspace: workspace, UserID: principal})
			if err != nil || len(page.Notifications) != 1 || page.UnreadCount != 1 {
				read.Close()
				t.Fatalf("count=%d canonical=%v page=%+v error=%v", count, canonical, page, err)
			}
			used := view.metadataResidency.work.used
			if count == 100 {
				held[canonical] = used
			} else if used != held[canonical] {
				t.Fatalf("100->1000 unrelated records grew retained request work: canonical=%v %d->%d", canonical, held[canonical], used)
			}
			t.Logf("unrelated=%d canonical=%v retained_request_bytes=%d", count, canonical, used)
			read.Close()
			if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
				t.Fatal("unrelated record count caused a full decode or admission leak")
			}
			view.releaseMetadataLogicalSources()
		}
	}
}
