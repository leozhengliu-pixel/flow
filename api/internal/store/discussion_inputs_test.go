package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW61SQLInput(actorID string) domain.Bootstrap {
	stale := func(id string) domain.User { return domain.User{ID: id, Name: "SQL reference", JobTitle: "SQL body"} }
	assignee, delegate := stale("duplicate"), stale("delegate")
	return domain.Bootstrap{
		Workspace: domain.Workspace{ID: "caller-workspace"}, ViewerRole: "member", NextIssueNumber: 73,
		Viewer: domain.User{ID: actorID, Name: "Live actor", Via: &domain.ActorBot{ID: "caller-app", Type: "oauthApplication", Name: "Live attribution"}},
		Issues: []domain.Issue{{
			ID: "selected-issue", Title: "Complete SQL issue", Description: "Retain SQL description",
			Team: domain.Team{ID: "selected-team"}, State: domain.WorkflowState{ID: "selected-state"},
			Creator: stale("creator"), Assignee: &assignee, Delegate: &delegate,
			Labels: []domain.IssueLabel{{ID: "selected-label"}}, Project: &domain.ProjectSummary{ID: "selected-project"},
			SubscriberIDs: []string{"subscriber"},
		}},
		Comments: map[string][]domain.Comment{
			"selected-issue": {{ID: "comment", Body: "Complete SQL comment", User: stale("commenter"), Reactions: map[string][]string{"like": {actorID}}}},
			"comment-only":   {{ID: "other-comment", Body: "Other scoped container", User: stale("duplicate")}},
		},
		Activities:             map[string][]domain.ActivityEvent{"selected-issue": {{ID: "activity", Actor: stale("activity-actor"), Metadata: map[string]string{"commentId": "comment"}}}},
		Notifications:          []domain.Notification{{ID: "notification", RecipientID: "notification-recipient", Actor: stale("notification-actor"), Text: "Complete SQL notification"}},
		NotificationDeliveries: []domain.NotificationDelivery{{ID: "delivery", RecipientID: "delivery-only", Attempts: 7, Status: "pending"}},
		CommentActions:         []domain.CommentAction{{ID: "caller-action", Status: "pending"}},
		UserSettings:           map[string]domain.UserSettings{"caller-only": {AgentInstructions: "caller-owned setting"}},
	}
}

func scaleP21SW61Preference(value domain.NotificationPreferences) domain.NotificationPreferences {
	return domain.NotificationPreferences{
		UserID: value.UserID, Inbox: value.Inbox, Email: value.Email, Desktop: value.Desktop, Chat: value.Chat,
		EmailFormat: value.EmailFormat, ImmediateUrgent: value.ImmediateUrgent, DelayLowPriority: value.DelayLowPriority,
	}
}

func TestScaleP21SW61DiscussionInputsLiteralReferences(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, actorID := f.data.Workspace.URLKey, f.data.Viewer.ID
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	at := time.Now()
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw61_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Users = []domain.User{
			{ID: actorID, Name: "Stored actor", JobTitle: "Full stored actor"},
			{ID: "duplicate", Name: "First duplicate", JobTitle: "First full body"},
			{ID: "creator", Name: "Creator", AvatarURL: "creator-avatar"},
			{ID: "delegate", Name: "Delegate", JobTitle: "Delegate full body"},
			{ID: "commenter", Name: "Comment author", JobTitle: "Comment author full body"},
			{ID: "activity-actor", Name: "Activity actor"},
			{ID: "notification-actor", Name: "Notification actor"},
			{ID: "mention-only", Name: "Mention Name", Email: "mention@example.test", DisplayName: "Mention Display", JobTitle: "Unneeded mention body"},
			{ID: "", Name: "", Email: "", JobTitle: "Unneeded empty-ID body"},
			{ID: "duplicate", Name: "Last duplicate", JobTitle: "Last full body"},
		}
		data.Teams = []domain.Team{{ID: "selected-team", Name: "First team"}, {ID: "selected-team", Name: "Last team"}, {ID: "foreign-team"}}
		data.States = []domain.WorkflowState{{ID: "selected-state", Name: "First state"}, {ID: "selected-state", Name: "Last state"}, {ID: "foreign-state"}}
		data.Labels = []domain.IssueLabel{{ID: "selected-label", Name: "First label"}, {ID: "selected-label", Name: "Last label"}, {ID: "foreign-label"}}
		data.Projects = []domain.Project{
			{ID: "selected-project", Name: "First project", Icon: "first-icon", Color: "#123456", Description: "Unneeded project body"},
			{ID: "selected-project", Name: "Last project", Icon: "last-icon", Color: "#654321", Description: "Unneeded later project body"},
			{ID: "foreign-project", Description: "Unneeded foreign project body"},
		}
		data.TeamSettings = map[string]domain.TeamSettings{
			"selected-team": {TeamID: "mismatched-payload-team", Timezone: "Raw/Unresolved", ResolvedSummaries: true, AgentGuidance: "Unneeded team guidance"},
			"foreign-team":  {Timezone: "UTC"},
		}
		data.NotificationPreferences = map[string]domain.NotificationPreferences{
			actorID: {UserID: "mismatched-payload-user", Inbox: domain.NotificationChannelPreferences{Enabled: true, Categories: nil}, Chat: nil, SoundEnabled: true, DesktopPermission: "unneeded"},
			"duplicate": {
				UserID: "duplicate", Email: domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{}},
				Chat:        &domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{"": true, "mentions": false, "CaseSensitive": true}},
				EmailFormat: "digest", ImmediateUrgent: true, DelayLowPriority: true,
			},
			"":            {UserID: "empty-map-key", Chat: &domain.NotificationChannelPreferences{Categories: nil}},
			"subscriber":  {EmailFormat: "immediate"},
			"thread-only": {Desktop: domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{"comments": true}}},
			"delivery-only": {Chat: &domain.NotificationChannelPreferences{
				Enabled: false, Categories: map[string]bool{},
			}},
			"nonrecipient": {UserID: "not-a-directory-or-scoped-recipient"},
		}
		data.PushSubscriptions = []domain.PushSubscription{
			{ID: "push-first", UserID: "duplicate", Enabled: true, Endpoint: "Unneeded secret endpoint", Auth: "unneeded"},
			{ID: "push-second", UserID: "duplicate", Enabled: false},
			{ID: "push-thread", UserID: "thread-only", Enabled: true},
			{ID: "foreign-push", UserID: "nonrecipient", Enabled: true},
		}
		data.ThreadSubscriptions = []domain.ThreadSubscription{
			{ID: "thread-first", IssueID: "selected-issue", CommentID: "comment", UserID: "thread-only", State: "muted", CreatedAt: at},
			{ID: "thread-second", IssueID: "comment-only", CommentID: "other-comment", UserID: "duplicate", State: "subscribed", CreatedAt: at},
			{ID: "foreign-thread", IssueID: "foreign-issue", UserID: "nonrecipient"},
		}
		data.IssueSLAs = []domain.IssueSLA{{ID: "selected-sla", IssueID: "selected-issue", Status: "breached", DueAt: at}, {ID: "comment-sla", IssueID: "comment-only", Status: "running"}, {ID: "foreign-sla", IssueID: "foreign-issue"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, reload := range []bool{false, true} {
		if reload {
			f.mu.Lock()
			f.forgetCopyRevisionLocked(workspace)
			f.mu.Unlock()
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		stored, found := f.storedWorkspace(workspace)
		if !found {
			t.Fatal("missing literal source")
		}
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
			t.Fatal(err)
		}
		for _, narrow := range []bool{true, false} {
			t.Run(fmt.Sprintf("reload=%t/narrow=%t", reload, narrow), func(t *testing.T) {
				t.Setenv("FLOW_NARROW_MUTATION_INPUTS", map[bool]string{true: "on", false: "off"}[narrow])
				sqlInput := scaleP21SW61SQLInput(actorID)
				actual := cloneBootstrap(sqlInput)
				actual.Viewer = mutationInputActor(sqlInput.Viewer)
				actual.NextIssueNumber = sqlInput.NextIssueNumber
				hooks := 0
				actual.FormerMembers = func() map[string]bool { hooks++; return map[string]bool{"former": true} }
				actual.CollaborationUpdates = func(id string) ([][]byte, error) { hooks++; return [][]byte{[]byte(id)}, nil }
				view.writeMu.Lock()
				defer view.writeMu.Unlock()
				read, err := view.loadIssueDiscussionInputs(t.Context(), workspace, &actual)
				if err != nil {
					t.Fatal(err)
				}
				defer read.close()
				if !reflect.DeepEqual(actual.Workspace, sqlInput.Workspace) || !reflect.DeepEqual(actual.Viewer, sqlInput.Viewer) || actual.ViewerRole != sqlInput.ViewerRole ||
					actual.NextIssueNumber != sqlInput.NextIssueNumber || !reflect.DeepEqual(actual.Issues, sqlInput.Issues) ||
					!reflect.DeepEqual(actual.Comments, sqlInput.Comments) || !reflect.DeepEqual(actual.Activities, sqlInput.Activities) ||
					!reflect.DeepEqual(actual.Notifications, sqlInput.Notifications) || !reflect.DeepEqual(actual.NotificationDeliveries, sqlInput.NotificationDeliveries) ||
					!reflect.DeepEqual(actual.CommentActions, sqlInput.CommentActions) || !reflect.DeepEqual(actual.UserSettings, sqlInput.UserSettings) || hooks != 0 {
					t.Fatal("metadata loader changed caller SQL containers, live actor or hooks")
				}
				if !actual.FormerMembers()["former"] {
					t.Fatal("former-member hook was replaced")
				}
				if updates, err := actual.CollaborationUpdates("owned"); err != nil || string(updates[0]) != "owned" || hooks != 2 {
					t.Fatal("collaboration hook was replaced", err)
				}
				literal, fields := issueScopeCallbackInput(stored, true)
				literal.Viewer = mutationInputActor(sqlInput.Viewer)
				literal.Issues, literal.Comments = cloneBootstrap(sqlInput).Issues, cloneBootstrap(sqlInput).Comments
				literal.Activities, literal.Notifications = cloneBootstrap(sqlInput).Activities, cloneBootstrap(sqlInput).Notifications
				if len(fields) > 0 {
					copyMutationProjects(&literal, stored)
				}
				refreshDisplayReferences(&literal)
				refreshIssueReferences(&literal)
				refreshDisplayReferences(&actual)
				refreshIssueReferences(&actual)
				if !reflect.DeepEqual(actual.Issues, literal.Issues) || !reflect.DeepEqual(actual.Comments, literal.Comments) ||
					!reflect.DeepEqual(actual.Activities, literal.Activities) || !reflect.DeepEqual(actual.Notifications, literal.Notifications) {
					t.Fatal("literal display/reference refresh changed", changedMetadataFields(literal, actual))
				}
				full := map[string]bool{actorID: true, "duplicate": true, "creator": true, "delegate": true, "commenter": true, "activity-actor": true, "notification-actor": true}
				wantUsers := make([]domain.User, len(stored.Users))
				for index, user := range stored.Users {
					wantUsers[index] = user
					if !full[user.ID] {
						wantUsers[index] = domain.User{ID: user.ID, Email: user.Email, Name: user.Name, DisplayName: user.DisplayName}
					}
				}
				if !reflect.DeepEqual(actual.Users, wantUsers) {
					t.Fatal("ordered existence directory or selected full-reference users changed")
				}
				first := slices.IndexFunc(actual.Users, func(user domain.User) bool { return user.ID == "duplicate" })
				last := newIssueReferences(actual).users["duplicate"]
				if first < 0 || actual.Users[first].Name != "First duplicate" || last.Name != "Last duplicate" {
					t.Fatal("first existence/last reference duplicate precedence changed")
				}
				var wantProjects []domain.Project
				for _, project := range stored.Projects {
					if project.ID == "selected-project" {
						wantProjects = append(wantProjects, domain.Project{ID: project.ID, Name: project.Name, Icon: project.Icon, Color: project.Color})
					}
				}
				wantPreferences := map[string]domain.NotificationPreferences{}
				recipients := []string{"subscriber", "thread-only", "delivery-only", "notification-recipient"}
				for _, user := range stored.Users {
					recipients = append(recipients, user.ID)
				}
				for _, key := range recipients {
					if preference, found := stored.NotificationPreferences[key]; found {
						wantPreferences[key] = scaleP21SW61Preference(preference)
					}
				}
				settings := stored.TeamSettings["selected-team"]
				wantSettings := map[string]domain.TeamSettings{"selected-team": {Timezone: settings.Timezone, ResolvedSummaries: settings.ResolvedSummaries}}
				if !reflect.DeepEqual(actual.Projects, wantProjects) || !reflect.DeepEqual(actual.TeamSettings, wantSettings) ||
					!reflect.DeepEqual(actual.NotificationPreferences, wantPreferences) ||
					!reflect.DeepEqual(actual.PushSubscriptions, []domain.PushSubscription{{UserID: "duplicate", Enabled: true}, {UserID: "duplicate"}, {UserID: "thread-only", Enabled: true}}) ||
					!reflect.DeepEqual(actual.ThreadSubscriptions, stored.ThreadSubscriptions[:2]) {
					t.Fatal("project summary, raw settings, preferences, push or thread closure changed")
				}
				var wantSLAs []domain.IssueSLA
				if !narrow {
					wantSLAs = stored.IssueSLAs[:2]
				}
				if !reflect.DeepEqual(actual.IssueSLAs, wantSLAs) {
					t.Fatal("narrow SLA omission or full-input relevant SLA changed")
				}
				read.close()
				read.close()
				if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("discussion input close leaked work or decoded full metadata")
				}
			})
		}
	}
}

func TestScaleP21SW61DiscussionInputsDirectoryBound(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
	held := 0
	for _, bodySize := range []int{32, 320} {
		body := strings.Repeat("unused ", bodySize)
		stored := domain.Bootstrap{
			Users:        []domain.User{{ID: "actor", Name: "Selected actor", JobTitle: "Selected full body"}},
			UserSettings: map[string]domain.UserSettings{"actor": {AgentInstructions: body}},
			Settings:     map[string]any{"unrelated": body}, Projects: []domain.Project{{ID: "unrelated", Description: body}},
		}
		for index := 1; index < 2000; index++ {
			id := fmt.Sprintf("directory-%04d", index)
			stored.Users = append(stored.Users, domain.User{ID: id, Name: id, Email: id + "@example.test", DisplayName: id, JobTitle: body})
		}
		view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": stored})
		data := domain.Bootstrap{Viewer: domain.User{ID: "actor"}}
		view.writeMu.Lock()
		read, err := view.loadIssueDiscussionInputs(t.Context(), "physical", &data)
		if err != nil {
			t.Fatal(err)
		}
		defer read.close()
		if len(data.Users) != 2000 || !reflect.DeepEqual(data.Users[0], stored.Users[0]) || data.UserSettings != nil || data.Settings != nil || len(data.Projects) != 0 {
			t.Fatal("2k directory dropped headers, selected full user or retained unrelated metadata")
		}
		for index := 1; index < len(data.Users); index++ {
			user := stored.Users[index]
			if !reflect.DeepEqual(data.Users[index], domain.User{ID: user.ID, Name: user.Name, Email: user.Email, DisplayName: user.DisplayName}) {
				t.Fatal("unrelated directory user retained full body or changed order", index)
			}
		}
		work := view.MetadataWorkUsedForTesting()
		if held == 0 {
			held = work
		}
		if work != held || work == 0 {
			t.Fatal("unrelated JobTitle/AgentInstructions body growth changed held work", held, work)
		}
		read.close()
		view.writeMu.Unlock()
		if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("2k directory leaked work or decoded full metadata")
		}
		t.Logf("users=2000 unrelated_body_bytes=%d held_work=%d", len(body), held)
	}
}

func TestScaleP21SW61DiscussionInputsOwnership(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {Users: []domain.User{{ID: "actor", Name: "First generation"}}}})
	view.writeMu.Lock()
	defer view.writeMu.Unlock()
	data := domain.Bootstrap{Viewer: domain.User{ID: "actor"}, Issues: []domain.Issue{{ID: "caller-owned"}}}
	read, err := view.loadIssueDiscussionInputs(t.Context(), "physical", &data)
	if err != nil {
		t.Fatal(err)
	}
	defer read.close()
	held := view.MetadataWorkUsedForTesting()
	pin, found, err := view.BeginInstalledMetadataRead(t.Context(), "physical")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer pin.Close()
	bound, err := pin.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{Users: []domain.User{{ID: "actor", Name: "Replacement generation"}}}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("physical", replacement)
	current := domain.Bootstrap{Viewer: domain.User{ID: "actor"}}
	fresh, err := view.loadIssueDiscussionInputs(bound, "physical", &current)
	if fresh != nil {
		defer fresh.close()
	}
	if err != nil || fresh == nil || len(current.Users) != 1 || current.Users[0].Name != "Replacement generation" || fresh.source != replacement {
		t.Fatal("discussion lookup borrowed an unrelated earlier request pin", err)
	}
	fresh.close()
	pin.Close()
	retained, err := read.source.Select(t.Context(), MetadataRecordScope{Fields: []string{"users"}}, 2<<20)
	if err != nil || data.Users[0].Name != "First generation" || retained.Users[0].Name != "First generation" {
		t.Fatal("owned discussion input switched installed source generation", err)
	}
	read.close()
	read.close()
	if read.source != nil || !reflect.DeepEqual(read.data, domain.Bootstrap{}) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("close retained source, projection or work")
	}
	for _, mode := range []string{"quota", "canceled", "missing"} {
		input := domain.Bootstrap{Viewer: domain.User{ID: "actor"}, Users: []domain.User{{ID: "caller-sentinel"}}, Issues: []domain.Issue{{ID: "caller-owned"}}}
		before := cloneBootstrap(input)
		ctx, workspace, want := t.Context(), "physical", error(ErrMetadataWorkBudget)
		view.metadataResidency.work.limit = held - 1
		if mode != "quota" {
			view.metadataResidency.work.limit = metadataWorkBytes
		}
		if mode == "canceled" {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
			want = context.Canceled
		}
		if mode == "missing" {
			workspace, want = "missing", sql.ErrNoRows
		}
		failed, err := view.loadIssueDiscussionInputs(ctx, workspace, &input)
		if failed != nil || !errors.Is(err, want) || !reflect.DeepEqual(input, before) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("failed discussion admission changed caller input or leaked ownership", mode, err)
		}
	}
}
