package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21HotNotificationsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	config := scaleP03DatabaseConfig(t)
	config.MaxOpenConns = 1
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := apiKeyScopeFixture(t, repo)
	workspace := f.data.Workspace.URLKey
	users := []domain.User{f.data.Viewer}
	for _, spec := range []struct{ name, role string }{
		{"Notification admin outsider", "admin"},
		{"Notification member", "member"},
		{"Notification guest", "guest"},
	} {
		_, user := scaleP03Join(t, f, spec.name, idgen.New("hot-notification-user-")+"@example.test", spec.role, "scope-public")
		users = append(users, user)
	}
	_, inherited := scaleP03Join(t, f, "Notification inherited", idgen.New("hot-notification-user-")+"@example.test", "member", "scope-private")
	users = append(users, inherited)
	_, installedKey := apiKeyScopeCredential(t, f, "app", "selected", []string{"scope-public"}, []string{"read", "initiative:read"})
	users = append(users, domain.User{ID: installedKey.SubjectID()})
	now := time.Now().UTC().Truncate(time.Millisecond)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	var changed []string
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.notification_seed", "", nil, func(data *domain.Bootstrap) error {
		child := domain.Team{ID: "hot-notification-child", Key: "HNC", Name: "Inherited"}
		data.Teams = append(data.Teams, child)
		data.TeamSettings[child.ID] = domain.TeamSettings{TeamID: child.ID, ParentTeamID: "scope-private", Access: "public"}
		for index := range data.Issues {
			if data.Issues[index].ID == "scope-hidden-issue" {
				data.Issues[index].Permissions = append(data.Issues[index].Permissions, domain.IssuePermission{SubjectType: "user", SubjectID: users[3].ID, Role: "viewer"})
			}
		}
		data.Issues = append(data.Issues, domain.Issue{ID: "hot-notification-child-issue", Identifier: "HNC-1", Number: 1, Team: child, State: data.States[0], Creator: data.Viewer, CreatedAt: now, UpdatedAt: now})
		for index := range data.Users {
			if data.Users[index].ID == f.data.Viewer.ID {
				data.Users[index].AppTeamIDs = []string{"scope-public", "scope-private"}
			}
		}
		data.Reviews = append(data.Reviews,
			domain.CodeReview{ID: "hot-review", SlugID: "hot-review-slug", IssueIDs: []string{"scope-issue", "scope-hidden-issue"}},
			domain.CodeReview{ID: "hot-hidden-review", IssueIDs: []string{"scope-hidden-issue"}},
			domain.CodeReview{ID: "hot-unlinked-review"},
		)
		data.Documents = append(data.Documents,
			domain.Document{ID: "hot-granted-document", TeamIDs: []string{"scope-public"}, Permissions: []domain.DocumentPermission{{SubjectType: "workspace", Role: "viewer"}}},
			domain.Document{ID: "hot-private-document", TeamIDs: []string{"scope-private"}, InheritedTeamIDs: []string{"scope-private"}},
			domain.Document{ID: "hot-private-shared-document", TeamIDs: []string{"scope-private"}, Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: users[3].ID, Role: "viewer"}}},
		)
		for index := range data.Initiatives {
			if data.Initiatives[index].ID == "scope-initiative" {
				data.Initiatives[index].LeadTeamID = "scope-private"
			}
		}
		data.Projects = append(data.Projects, domain.Project{ID: "hot-unrelated", Description: strings.Repeat("unrelated body ", 100000)})
		for _, user := range users {
			items := []domain.Notification{
				{ID: "unreferenced"},
				{ID: "issue-wins", IssueID: "scope-issue", ProjectID: "scope-hidden-project", ReviewID: "missing"},
				{ID: "hidden-issue-wins", IssueID: "scope-hidden-issue", ProjectID: "scope-project"},
				{ID: "inherited", IssueID: "hot-notification-child-issue"},
				{ID: "project-wins", ProjectID: "scope-project", ReviewID: "missing"},
				{ID: "hidden-project-wins", ProjectID: "scope-hidden-project", ReviewID: "hot-review"},
				{ID: "review-slug", ReviewID: "hot-review-slug"},
				{ID: "hidden-review", ReviewID: "hot-hidden-review"},
				{ID: "unlinked-review", ReviewID: "hot-unlinked-review"},
				{ID: "project", SourceType: "project", SourceID: "scope-project"},
				{ID: "initiative", SourceType: "initiative", SourceID: "scope-initiative"},
				{ID: "document", SourceType: "document", SourceID: "hot-granted-document"},
				{ID: "private-document", SourceType: "document", SourceID: "hot-private-document"},
				{ID: "private-shared-document", SourceType: "document", SourceID: "hot-private-shared-document"},
				{ID: "cycle", SourceType: "cycle", SourceID: "scope-cycle"},
				{ID: "team", SourceType: "team", SourceID: "scope-public"},
				{ID: "unknown-source", SourceType: "unsupported", SourceID: "missing"},
				{ID: "source-issue-is-unreferenced", SourceType: "issue", SourceID: "scope-hidden-issue"},
				{ID: "read", IssueID: "scope-issue", ReadAt: &past},
				{ID: "archived", IssueID: "scope-issue", ArchivedAt: &past},
				{ID: "deleted", IssueID: "scope-issue", DeletedAt: &past},
				{ID: "snoozed", IssueID: "scope-issue", SnoozedUntil: &future},
				{ID: "expired", IssueID: "scope-issue", SnoozedUntil: &past},
			}
			for index, item := range items {
				item.ID = user.ID + "-" + item.ID
				item.RecipientID, item.CreatedAt, item.UpdatedAt = user.ID, now.Add(time.Duration(index)*time.Second), now
				item.Actor = domain.User{ID: f.data.Viewer.ID, AppTeamIDs: []string{"scope-public", "scope-private"}}
				data.Notifications = append(data.Notifications, item)
				if user.ID == users[0].ID {
					changed = append(changed, item.ID)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	readFalse, readTrue := false, true
	for _, user := range users {
		for _, keySpec := range []struct {
			name, actor string
			teams       []string
			scopes      []string
			canonical   bool
		}{
			{"selected", "user", []string{"scope-public"}, []string{"read", "initiative:read"}, false},
			{"empty", "user", []string{}, []string{"read"}, false},
			{"private", "user", []string{"scope-private"}, []string{"read", "initiative:read"}, false},
			{"inherited", "user", []string{"hot-notification-child"}, []string{"read"}, false},
			{"app-no-initiative", "app", []string{"scope-public"}, []string{"read"}, false},
			{"canonical-public", "user", []string{"scope-public"}, []string{"read", "initiative:read"}, true},
			{"canonical-private", "user", []string{"scope-private"}, []string{"read", "initiative:read"}, true},
		} {
			t.Run(user.ID+"/"+keySpec.name, func(t *testing.T) {
				outputContext, closeOutput := applicationOutputContext(t.Context())
				defer closeOutput()
				key := domain.APIKey{ID: idgen.New("hot-notification-key-"), CreatorID: user.ID, Actor: keySpec.actor, TeamRestriction: "selected", TeamIDs: keySpec.teams, Scopes: keySpec.scopes}
				access, err := repo.ResolveAccess(t.Context(), workspace, user.ID, &key)
				if err != nil {
					t.Fatal(err)
				}
				source, err := repo.NotificationState(t.Context(), workspace, user.ID)
				if err != nil {
					t.Fatal(err)
				}
				source.ChangedIDs = slices.Clone(changed)
				for index := range source.ChangedIDs {
					source.ChangedIDs[index] = strings.Replace(source.ChangedIDs[index], users[0].ID, user.ID, 1)
				}
				slices.Reverse(source.ChangedIDs)
				wantState, err := f.service.scopedNotificationState(t.Context(), access, &source)
				if err != nil {
					t.Fatal(err)
				}
				view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				if keySpec.canonical {
					// A supported metadata write after pinning the installed
					// source forces this reader's canonical reference path.
					if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "settings"), workspace, "scale_p21.notification_canonical", "", nil, func(data *domain.Bootstrap) error {
						data.Settings["hot.notifications.canonical"] = idgen.New("canonical-")
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					access, err = repo.ResolveAccess(t.Context(), workspace, user.ID, &key)
					if err != nil {
						t.Fatal(err)
					}
				}
				boundedAccess, err := view.ResolveAccess(outputContext, workspace, user.ID, &key)
				if err != nil {
					t.Fatal(err)
				}
				service := &server{store: view}
				gotState, err := service.scopedNotificationState(outputContext, boundedAccess, &source)
				if err != nil || !reflect.DeepEqual(gotState, wantState) {
					t.Fatalf("state=%+v want=%+v error=%v", gotState, wantState, err)
				}
				for _, query := range []store.NotificationQuery{
					{Limit: 3},
					{Limit: 5, Read: &readFalse},
					{Limit: 5, Read: &readTrue},
					{Limit: 100, IncludeArchived: true, IncludeDeleted: true, IncludeSnoozed: true},
				} {
					query.Workspace, query.UserID = workspace, user.ID
					for pageIndex := 0; pageIndex < 20; pageIndex++ {
						want := scaleP21OldNotificationPage(t, f.service, user.ID, key, query)
						read, err := view.BeginNotificationVisibilityRead(outputContext, boundedAccess)
						if err != nil {
							t.Fatal(err)
						}
						got, err := read.Query(query)
						if err != nil {
							read.Close()
							t.Fatal(err)
						}
						left, _ := json.Marshal(want)
						right, _ := json.Marshal(got)
						read.Close()
						if string(left) != string(right) {
							t.Fatalf("query=%+v page=%d\nold=%s\nnew=%s", query, pageIndex, left, right)
						}
						if !got.HasMore {
							break
						}
						query.Cursor = got.NextCursor
						if pageIndex == 19 {
							t.Fatal("notification cursor did not finish")
						}
					}
				}
				for _, altered := range []store.NotificationState{
					{Revision: source.Revision, ChangedIDs: []string{"removed"}},
					{Revision: "-1", ChangedIDs: source.ChangedIDs},
					{Revision: source.Revision, Invalidated: true, ChangedIDs: source.ChangedIDs},
				} {
					want, err := f.service.scopedNotificationState(t.Context(), access, &altered)
					if err != nil {
						t.Fatal(err)
					}
					got, err := service.scopedNotificationState(outputContext, boundedAccess, &altered)
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("invalidated state=%+v want=%+v error=%v", got, want, err)
					}
				}
				if view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("bounded notifications decoded workspace metadata")
				}
			})
		}
	}
}

func scaleP21OldNotificationPage(t *testing.T, service *server, principal string, key domain.APIKey, query store.NotificationQuery) store.NotificationPage {
	t.Helper()
	ctx, release, err := service.store.BeginWorkspaceRead(t.Context(), query.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx = context.WithValue(ctx, authUserContextKey{}, domain.User{ID: principal})
	ctx = context.WithValue(ctx, credentialReadPrincipalKey{}, principal)
	ctx = context.WithValue(ctx, apiKeyContextKey{}, key)
	request := (&http.Request{}).WithContext(ctx)
	data, err := service.store.PagedWorkspaceMetadata(ctx, query.Workspace, principal)
	if err != nil {
		t.Fatal(err)
	}
	filterBootstrapForAPIKey(&data, request)
	if err := service.filterPreferenceIssueTeams(request, &data); err != nil {
		t.Fatal(err)
	}
	page, err := service.queryAPIKeyNotifications(ctx, key, data, query)
	if err != nil {
		t.Fatal(err)
	}
	return page
}
