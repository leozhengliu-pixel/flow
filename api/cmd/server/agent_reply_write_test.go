package main

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// notifyAgentReply on its closed bounded writers (the issue-record point
// writer for issues, the resource-scope image writer for documents and
// projects), compared with the legacy scoped writers: new and refreshed
// notifications (issue key then session key, read/archived/deleted reset,
// occurrence count), generated delivery replacement, email digest timing and
// urgency, muted threads, thread subscribers, app and former-member
// recipients, document access, a missing anchor and an inactive agent.
func TestScaleP21AgentReplyNotificationsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, narrow := range []string{"off", "on"} {
		t.Run("narrow-"+narrow, func(t *testing.T) {
			t.Setenv("FLOW_NARROW_MUTATION_INPUTS", narrow)
			scaleP21AgentReplyDifferential(t)
		})
	}
}

func scaleP21AgentReplyDifferential(t *testing.T) {
	f := scaleP21IssueUpdateSetup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner, admin, member, guest, private := f.principals[0].user, f.principals[1].user, f.principals[2].user, f.principals[3].user, f.principals[4].user
	install := func(name string) domain.ApplicationInstallation {
		app, err := repo.InstallApplication(store.ContextWithActor(t.Context(), f.data.Viewer), workspace, domain.ApplicationInstallation{
			ClientID: idgen.New("ar-app-"), Name: name, Active: true,
			InstalledBy: f.data.Viewer.ID, TeamIDs: []string{f.public}, Scopes: []string{"read", "write"},
		}, "")
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	app, other := install("Reply agent"), install("Other agent")
	full, ok := repo.BootstrapFor(workspace)
	if !ok {
		t.Fatal("fixture workspace missing")
	}
	var err error
	if f.baseline, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
	issueID, documentID, projectID := f.triage.ID, "ar-document", "ar-project"
	tasks := map[string]domain.AgentTask{}
	for _, anchor := range []struct{ kind, resource string }{
		{"issue", issueID}, {"document", documentID}, {"project", projectID}, {"missing", "ar-missing"},
	} {
		task := domain.AgentTask{ID: idgen.New("mention_ar_"), WorkspaceKey: workspace, AppUserID: app.UserID, CreatorID: owner.ID, CommentID: "ar-reply"}
		switch anchor.kind {
		case "issue":
			task.ResourceType, task.IssueID = "issue", anchor.resource
		case "missing":
			task.ResourceType, task.ResourceID = "document", anchor.resource
		default:
			task.ResourceType, task.ResourceID = anchor.kind, anchor.resource
		}
		if err := repo.CreateMentionTask(t.Context(), task); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.AppendAgentActivity(t.Context(), workspace, task.ID, 1, domain.AgentActivity{ID: idgen.New("ar-prompt-"), ActorID: private.ID, Type: "prompt", Body: "Look again"}); err != nil {
			t.Fatal(err)
		}
		saved, err := repo.AgentTask(t.Context(), workspace, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		tasks[anchor.kind] = saved
	}
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	seed := func(kind, shape string) func(*testing.T) {
		return func(t *testing.T) {
			if shape == "urgent" {
				if err := repo.MutateWorkspace(store.WithIssueRecordMutations(t.Context(), issueID), workspace, "scale_p21.agent_reply_issue", issueID, nil, func(data *domain.Bootstrap) error {
					issue, err := issueByID(data, issueID)
					if err != nil {
						return err
					}
					issue.Assignee, issue.Priority = &owner, 1
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if shape == "inactive-agent" {
				f.exec(t, `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, app.UserID)
				t.Cleanup(func() {
					f.exec(t, `UPDATE workspace_memberships SET status='active' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, app.UserID)
				})
			}
			f.seedWriter(t, func(data *domain.Bootstrap) {
				root := "ar-root"
				comments := []domain.Comment{
					{ID: root, Version: 1, Body: "Root", User: owner, CreatedAt: at},
					{ID: "ar-reply", Version: 1, Body: "Reply", ParentID: &root, User: member, CreatedAt: at.Add(time.Minute)},
					{ID: "ar-former", Version: 1, Body: "Former", ParentID: &root, User: f.former, CreatedAt: at.Add(2 * time.Minute)},
					{ID: "ar-other-root", Version: 1, Body: "Elsewhere", User: admin, CreatedAt: at.Add(3 * time.Minute)},
				}
				subscriptions := []domain.ThreadSubscription{
					{ID: "ar-sub-guest", UserID: guest.ID, CommentID: root, State: "subscribed", CreatedAt: at, UpdatedAt: at},
					{ID: "ar-sub-app", UserID: other.UserID, CommentID: root, State: "subscribed", CreatedAt: at, UpdatedAt: at},
					{ID: "ar-sub-other-thread", UserID: admin.ID, CommentID: "ar-other-root", State: "subscribed", CreatedAt: at, UpdatedAt: at},
				}
				if shape == "muted" {
					subscriptions = append(subscriptions, domain.ThreadSubscription{ID: "ar-sub-muted", UserID: member.ID, CommentID: root, State: "muted", CreatedAt: at, UpdatedAt: at})
				}
				resource := map[string]string{"issue": issueID, "document": documentID, "project": projectID}[kind]
				for index := range subscriptions {
					if kind == "project" {
						subscriptions[index].ProjectID = resource
					} else {
						subscriptions[index].IssueID = resource
					}
				}
				data.ThreadSubscriptions = append(data.ThreadSubscriptions, subscriptions...)
				switch kind {
				case "issue":
					data.Comments[issueID] = append(data.Comments[issueID], comments...)
				case "document":
					document := domain.Document{ID: documentID, SlugID: "ar-document-slug", PreviousSlugIDs: []string{"ar-old"}, Title: "Reply document", Content: "Body", Creator: owner, TeamIDs: []string{f.public}, CreatedAt: at, UpdatedAt: at}
					if shape == "restricted" {
						document.Permissions = []domain.DocumentPermission{{ID: "ar-doc-owner", SubjectType: "user", SubjectID: owner.ID, Role: "owner"}, {ID: "ar-doc-guest", SubjectType: "user", SubjectID: guest.ID, Role: "viewer"}}
					}
					data.Documents = append(data.Documents, document)
					data.Comments[documentID] = append(data.Comments[documentID], comments...)
				case "project":
					for index := range data.Projects {
						if data.Projects[index].ID == f.projects[0] {
							project := data.Projects[index]
							project.ID, project.Name, project.SlugID, project.Comments = projectID, "Reply project", "ar-project-slug", comments
							data.Projects = append(data.Projects, project)
							break
						}
					}
				}
				if data.NotificationPreferences == nil {
					data.NotificationPreferences = map[string]domain.NotificationPreferences{}
				}
				on := map[string]bool{"comments": true}
				data.NotificationPreferences[owner.ID] = domain.NotificationPreferences{UserID: owner.ID,
					Inbox: domain.NotificationChannelPreferences{Enabled: true, Categories: on}, Email: domain.NotificationChannelPreferences{Enabled: true, Categories: on},
					EmailFormat: "digest", DelayLowPriority: true, ImmediateUrgent: true}
				data.NotificationPreferences[member.ID] = domain.NotificationPreferences{UserID: member.ID,
					Inbox: domain.NotificationChannelPreferences{Enabled: true}, Desktop: domain.NotificationChannelPreferences{Enabled: true}, EmailFormat: "immediate"}
				data.NotificationPreferences[guest.ID] = domain.NotificationPreferences{UserID: guest.ID,
					Inbox: domain.NotificationChannelPreferences{Enabled: true}, Chat: &domain.NotificationChannelPreferences{Enabled: true}}
				data.NotificationPreferences[admin.ID] = domain.NotificationPreferences{UserID: admin.ID,
					Inbox: domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{"comments": false}}}
				data.PushSubscriptions = append(data.PushSubscriptions, domain.PushSubscription{ID: "ar-push", UserID: member.ID, Endpoint: "https://push.example.test/ar", Enabled: true, CreatedAt: at})
				if kind == "issue" {
					for index := range data.TeamSettings {
						settings := data.TeamSettings[index]
						settings.Timezone = "America/Los_Angeles"
						data.TeamSettings[index] = settings
					}
					data.IssueSLAs = append(data.IssueSLAs, domain.IssueSLA{ID: "ar-sla", IssueID: issueID, Status: "breached", StartedAt: at, DueAt: at})
				}
				task := tasks[kind]
				groupKey := owner.ID + ":agent-session:" + task.ID
				if kind == "issue" && shape != "legacy-key" {
					groupKey = owner.ID + ":agent-issue:" + issueID
				}
				if shape == "existing" || shape == "legacy-key" {
					read := at.Add(time.Hour)
					existing := domain.Notification{ID: "ar-existing", RecipientID: owner.ID, Type: agentReplyNotificationType, Category: "comments",
						GroupKey: groupKey, SourceType: "document", SourceID: resource, OccurrenceCount: 2, Title: "Old", Text: "Old text",
						ReadAt: &read, ArchivedAt: &read, CreatedAt: at, UpdatedAt: at}
					if kind == "issue" {
						existing.IssueID, existing.SourceType, existing.SourceID = issueID, "agentSession", task.ID
					}
					if kind == "project" {
						existing.ProjectID = projectID
					}
					earlier := existing
					earlier.ID, earlier.GroupKey, earlier.RecipientID = "ar-unrelated", "nobody:agent-issue:"+issueID, admin.ID
					data.Notifications = append(data.Notifications, earlier, existing)
					data.NotificationDeliveries = append(data.NotificationDeliveries,
						domain.NotificationDelivery{ID: "delivery_ar-existing_email", NotificationID: "ar-existing", RecipientID: owner.ID, Channel: "email", Status: "sent", CreatedAt: at, UpdatedAt: at},
						domain.NotificationDelivery{ID: "delivery_ar-existing_push", NotificationID: "ar-existing", RecipientID: owner.ID, Channel: "push", Status: "sent", CreatedAt: at, UpdatedAt: at})
				}
			})
		}
	}
	cases := []struct{ kind, shape string }{}
	for _, kind := range []string{"issue", "document", "project"} {
		for _, shape := range []string{"fresh", "existing", "muted"} {
			cases = append(cases, struct{ kind, shape string }{kind, shape})
		}
	}
	cases = append(cases,
		struct{ kind, shape string }{"issue", "legacy-key"}, struct{ kind, shape string }{"issue", "urgent"},
		struct{ kind, shape string }{"issue", "inactive-agent"}, struct{ kind, shape string }{"document", "restricted"},
		struct{ kind, shape string }{"missing", "fresh"})
	for _, test := range cases {
		t.Run(test.kind+"/"+test.shape, func(t *testing.T) {
			seedKind := test.kind
			if seedKind == "missing" {
				seedKind = "document"
			}
			f.scaleP21TriageWriterFixture.prepare = seed(seedKind, test.shape)
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			for _, activity := range []domain.AgentActivity{
				{ID: "ar-activity", ActorID: app.UserID, Type: "response", Body: "**Done**: [fixed](https://example.test) it.\n\n```suggestions\nMore\n```"},
				{ID: "ar-question", ActorID: app.UserID, Type: "elicitation", Body: "Which one?"},
			} {
				outcomes := f.writerDifferential(t, test.kind+"/"+test.shape+"/"+activity.Type, func(service *server) (*httptest.ResponseRecorder, any) {
					service.notifyAgentReply(t.Context(), tasks[test.kind], activity)
					return nil, nil
				})
				legacy := outcomes[0]
				notifications := scaleP21WriterContent(legacy, "notification")
				agent := slices.DeleteFunc(notifications, func(item map[string]any) bool { return item["type"] != agentReplyNotificationType })
				switch {
				case test.kind == "missing" || test.shape == "inactive-agent":
					if len(legacy.State.Events) != 0 {
						t.Fatalf("legacy wrote for %s: %v", test.shape, legacy.State.Events)
					}
				case len(legacy.State.Events) != 1 || len(agent) == 0:
					t.Fatalf("legacy did not notify: events=%d notifications=%v", len(legacy.State.Events), agent)
				}
				if test.shape == "restricted" {
					// The agent is on the document's team; a recipient is not
					// on it because the agent is. The prompter is on another
					// team only and has no grant; the member is on the team.
					recipients := map[string]bool{}
					for _, item := range agent {
						recipients[item["recipientId"].(string)] = true
					}
					if recipients[private.ID] || !recipients[member.ID] || !recipients[guest.ID] {
						t.Fatalf("document access judged by the agent's teams: %v", recipients)
					}
				}
				if test.shape == "existing" {
					found := false
					for _, item := range agent {
						if item["id"] == "ar-existing" {
							found = item["readAt"] == nil && item["occurrenceCount"] == float64(3)
						}
					}
					if !found {
						t.Fatalf("legacy did not refresh the existing notification: %v", agent)
					}
				}
			}
		})
	}
}
