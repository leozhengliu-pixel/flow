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

func scaleP21SW91Data(workspace domain.Workspace, repetitions int) domain.Bootstrap {
	at := time.Date(2026, 11, 15, 1, 2, 3, 456, time.UTC)
	body := strings.Repeat("Unrelated legal discussion input body. ", repetitions)
	actor := domain.User{ID: "sw91-actor", Name: "Directory actor before substitution", Email: "actor@example.test", DisplayName: "Directory actor"}
	recipient := domain.User{ID: "sw91-recipient", Name: "Recipient", Email: "recipient@example.test", JobTitle: "Complete referenced user body"}
	foreign := domain.User{ID: "sw91-delivery-only", Name: "Recipient absent from native Users"}
	stale := actor
	stale.Name = "Stale embedded actor"
	return domain.Bootstrap{
		Workspace: workspace, Viewer: actor, ViewerRole: "literal-native-role",
		Users: []domain.User{actor, recipient,
			{ID: "sw91-mention-only", Name: "Mention only", DisplayName: "Mention Display", Email: "mention@example.test", JobTitle: body},
			{ID: recipient.ID, Name: "Last duplicate directory recipient", JobTitle: "Complete last duplicate body"},
			{ID: "sw91-unrelated-user", Name: "Unrelated user", JobTitle: body}},
		Teams:    []domain.Team{{ID: "sw91-team", Name: "Target team"}, {ID: "sw91-other-team", Name: "Other urgency team"}, {ID: "sw91-unrelated-team", Name: body}},
		States:   []domain.WorkflowState{{ID: "sw91-state", Name: "Target state"}, {ID: "sw91-other-state", Name: "Other state"}, {ID: "sw91-unrelated-state", Description: body}},
		Labels:   []domain.IssueLabel{{ID: "sw91-label", Name: "First label"}, {ID: "sw91-label", Name: "Last label", IssueCount: 17}, {ID: "sw91-unrelated-label", Description: body}},
		Projects: []domain.Project{{ID: "sw91-project", Name: "First project", Description: body}, {ID: "sw91-project", Name: "Last project", Icon: "project-icon", Color: "#123456", Description: body}},
		Issues: []domain.Issue{
			{ID: "sw91-other-issue", Title: "Other issue consulted by a misgrouped notification", Creator: stale, Assignee: &foreign, Priority: 1, Team: domain.Team{ID: "sw91-other-team"}, State: domain.WorkflowState{ID: "sw91-other-state"}, CreatedAt: at},
			{ID: "sw91-issue", Title: "First decoded target occurrence", Description: "Complete selected issue", Creator: stale, Assignee: &recipient, Team: domain.Team{ID: "sw91-team"}, State: domain.WorkflowState{ID: "sw91-state"},
				Labels: []domain.IssueLabel{{ID: "sw91-label"}}, Project: &domain.ProjectSummary{ID: "sw91-project"}, SubscriberIDs: []string{recipient.ID}, CreatedAt: at},
			{ID: "sw91-issue", Title: "Later duplicate must not replace first target", Description: body},
			{ID: "sw91-unrelated-issue", Title: "Unrelated issue", Description: body},
		},
		Comments: map[string][]domain.Comment{
			"sw91-issue": {
				{ID: "sw91-root", Version: 7, Body: "First duplicate comment", User: stale, CreatedAt: at, BodyData: map[string]any{"native": int64(9007199254740993)}, Reactions: map[string][]string{"like": {actor.ID}}},
				{ID: "sw91-root", Version: 9, Body: "Later duplicate comment", User: recipient, CreatedAt: at},
				{ID: "sw91-reply", Body: "Complete reply", User: recipient, ParentID: new("sw91-root"), CreatedAt: at},
			},
			"SW91-issue": {{ID: "sw91-wrong-case", Body: body}}, "nil": nil, "empty": {},
		},
		Activities: map[string][]domain.ActivityEvent{
			"sw91-issue": {{ID: "sw91-old-activity", Type: "comment.created", Actor: stale, CreatedAt: at, Metadata: domain.ActivityMetadata{"literal": "preserve"}}},
			"other":      {{ID: "sw91-unrelated-activity", Metadata: domain.ActivityMetadata{"body": body}}},
		},
		Notifications: []domain.Notification{
			{ID: "sw91-read", GroupKey: recipient.ID + ":sw91-issue:comments", ReadAt: &at, UpdatedAt: at, Actor: recipient},
			{ID: "sw91-notification", GroupKey: recipient.ID + ":sw91-issue:comments", IssueID: "sw91-other-issue", RecipientID: foreign.ID, UpdatedAt: at.Add(time.Minute), Actor: stale, OccurrenceCount: 3, Text: "First globally ordered active candidate"},
			{ID: "sw91-unrelated", GroupKey: recipient.ID + ":other-issue:comments", UpdatedAt: at, Text: body},
			{ID: "sw91-old", GroupKey: recipient.ID + ":sw91-issue:comments", IssueID: "sw91-issue", RecipientID: recipient.ID, UpdatedAt: at.Add(-9 * time.Hour), Actor: recipient},
			{ID: "sw91-notification", GroupKey: recipient.ID + ":sw91-issue:comments", IssueID: "sw91-issue", RecipientID: recipient.ID, UpdatedAt: at, Actor: recipient, OccurrenceCount: 4, Text: "Last native active candidate"},
			{ID: "sw91-mention", GroupKey: "sw91-mention-only:sw91-issue:mentions", IssueID: "sw91-issue", RecipientID: "sw91-mention-only", UpdatedAt: at, Actor: recipient},
			{ID: "sw91-archived", GroupKey: recipient.ID + ":sw91-issue:comments", ArchivedAt: &at, UpdatedAt: at, Text: body},
			{ID: "sw91-unknown-recipient", GroupKey: "not-in-directory:sw91-issue:comments", UpdatedAt: at, Text: body},
			{ID: "sw91-other-category", GroupKey: recipient.ID + ":sw91-issue:subscriptions", UpdatedAt: at, Text: body},
		},
		NotificationDeliveries: []domain.NotificationDelivery{
			{ID: "delivery_sw91-notification_email", NotificationID: "mismatched-parent", RecipientID: "mismatched-recipient", Channel: "desktop", Status: "failed", Error: body, CreatedAt: at},
			{ID: "delivery_sw91-notification_email", NotificationID: "duplicate-native-id", Channel: "email", Status: "digest", CreatedAt: at},
			{ID: "delivery_notification_future_email", NotificationID: "collision-with-future-callback-ID", Status: "failed", Error: body},
			{ID: "", Error: body},
		},
		ThreadSubscriptions: []domain.ThreadSubscription{
			{ID: "sw91-duplicate-thread", IssueID: "sw91-issue", CommentID: "sw91-root", UserID: recipient.ID, State: "subscribed", CreatedAt: at},
			{ID: "sw91-unrelated-thread", IssueID: "other-issue", UserID: "unrelated"},
			{ID: "sw91-duplicate-thread", IssueID: "sw91-issue", CommentID: "sw91-root", UserID: "sw91-thread-only", State: "muted", CreatedAt: at},
		},
		TeamSettings: map[string]domain.TeamSettings{
			"sw91-team":           {ResolvedSummaries: true, Timezone: "Europe/Paris", AgentGuidance: body},
			"sw91-other-team":     {ResolvedSummaries: false, Timezone: "America/Los_Angeles", AgentGuidance: body},
			"sw91-unrelated-team": {Timezone: "UTC", AgentGuidance: body},
		},
		NotificationPreferences: map[string]domain.NotificationPreferences{
			actor.ID:               {UserID: actor.ID, Inbox: domain.NotificationChannelPreferences{Enabled: true}},
			recipient.ID:           {UserID: recipient.ID, Inbox: domain.NotificationChannelPreferences{Enabled: true}, Email: domain.NotificationChannelPreferences{Enabled: true}, EmailFormat: "digest", ImmediateUrgent: true, DelayLowPriority: true, SoundEnabled: true},
			"sw91-mention-only":    {UserID: "sw91-mention-only", Desktop: domain.NotificationChannelPreferences{Enabled: true}},
			"sw91-thread-only":     {UserID: "sw91-thread-only", Chat: &domain.NotificationChannelPreferences{Enabled: true}},
			foreign.ID:             {UserID: foreign.ID, Inbox: domain.NotificationChannelPreferences{Enabled: true}},
			"unrelated-preference": {UserID: "unrelated-preference", SoundEnabled: true},
		},
		PushSubscriptions: []domain.PushSubscription{{ID: "sw91-push", UserID: foreign.ID, Enabled: true, Endpoint: body}, {ID: "sw91-other-push", UserID: recipient.ID, Enabled: false}},
		IssueSLAs:         []domain.IssueSLA{{ID: "sw91-target-sla", IssueID: "sw91-issue", Status: "breached", DueAt: at}, {ID: "sw91-other-sla", IssueID: "sw91-other-issue", Status: "breached", DueAt: at}, {ID: "sw91-unrelated-sla", IssueID: "other", Status: "running"}},
		Documents:         []domain.Document{{ID: "sw91-unrelated-document", Content: body}},
		Settings:          map[string]any{"untouched": body},
	}
}

func scaleP21SW91Prepare(t *testing.T, view *SQLiteStore, data domain.Bootstrap) *metadataLogicalSource {
	t.Helper()
	source := scaleP21SW78Capture(t, data)
	refreshed, err := view.refreshLegacyDisplayImage(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(refreshed.Release)
	source.Release()
	actor := scaleP21SW69OwnedActor(data.Viewer)
	actor.Name = "Actor substituted after reference refresh"
	actor.Via = &domain.ActorBot{ID: "sw91-app", Type: domain.ActorBotTypeOAuthApplication, Name: "Native callback attribution", OnBehalfOfID: "person"}
	image, err := view.prepareLegacyActorImage(t.Context(), refreshed, &actor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(image.Close)
	result := image.Source()
	if err := result.Retain(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(result.Release)
	image.Close()
	refreshed.Release()
	return result
}

func scaleP21SW91Want(full domain.Bootstrap, issueID string) (domain.Bootstrap, []int, []int, []int, []int) {
	want := domain.Bootstrap{Workspace: full.Workspace, Viewer: full.Viewer, ViewerRole: full.ViewerRole}
	source, target := reflect.ValueOf(full), reflect.ValueOf(&want).Elem()
	for _, name := range append([]string{"issues", "comments", "activities", "notifications", "notificationDeliveries"}, issueDiscussionInputFields...) {
		index, _, _ := metadataLogicalField(name)
		field := source.Field(index)
		if !field.IsNil() {
			if field.Kind() == reflect.Map {
				target.Field(index).Set(reflect.MakeMap(field.Type()))
			} else {
				target.Field(index).Set(reflect.MakeSlice(field.Type(), 0, 0))
			}
		}
	}
	if values, found := full.Comments[issueID]; found {
		want.Comments[issueID] = values
	}
	if values, found := full.Activities[issueID]; found {
		want.Activities[issueID] = values
	}
	users, recipients, teams, states, labels, projects := map[string]bool{full.Viewer.ID: true}, map[string]bool{full.Viewer.ID: true}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	groups, issueIDs := map[string]bool{}, map[string]bool{issueID: true}
	for _, user := range full.Users {
		recipients[user.ID] = true
		for _, category := range []string{"comments", "mentions"} {
			groups[user.ID+":"+issueID+":"+category] = true
		}
	}
	issueOrdinals, notificationOrdinals, deliveryOrdinals, threadOrdinals := []int{}, []int{}, []int{}, []int{}
	for index, item := range full.Notifications {
		if groups[item.GroupKey] && item.ReadAt == nil && item.DeletedAt == nil && item.ArchivedAt == nil {
			want.Notifications = append(want.Notifications, item)
			notificationOrdinals = append(notificationOrdinals, index)
			issueIDs[item.IssueID], users[item.Actor.ID], recipients[item.RecipientID] = true, true, true
		}
	}
	seen := map[string]bool{}
	for index, issue := range full.Issues {
		if !issueIDs[issue.ID] || seen[issue.ID] {
			continue
		}
		seen[issue.ID] = true
		want.Issues = append(want.Issues, issue)
		issueOrdinals = append(issueOrdinals, index)
		users[issue.Creator.ID], teams[issue.Team.ID], states[issue.State.ID] = true, true, true
		for _, user := range []*domain.User{issue.Assignee, issue.Delegate} {
			if user != nil {
				users[user.ID] = true
			}
		}
		for _, id := range issue.SubscriberIDs {
			recipients[id] = true
		}
		for _, label := range issue.Labels {
			labels[label.ID] = true
		}
		if issue.Project != nil {
			projects[issue.Project.ID] = true
		}
	}
	for _, comment := range full.Comments[issueID] {
		users[comment.User.ID], recipients[comment.User.ID] = true, true
	}
	for _, activity := range full.Activities[issueID] {
		users[activity.Actor.ID] = true
	}
	for index, item := range full.NotificationDeliveries {
		want.NotificationDeliveries = append(want.NotificationDeliveries, domain.NotificationDelivery{ID: item.ID})
		deliveryOrdinals = append(deliveryOrdinals, index)
	}
	for index, item := range full.ThreadSubscriptions {
		if item.IssueID == issueID {
			want.ThreadSubscriptions = append(want.ThreadSubscriptions, item)
			threadOrdinals = append(threadOrdinals, index)
			recipients[item.UserID] = true
		}
	}
	for _, item := range full.Users {
		if users[item.ID] {
			want.Users = append(want.Users, item)
		} else {
			want.Users = append(want.Users, domain.User{ID: item.ID, Email: item.Email, Name: item.Name, DisplayName: item.DisplayName})
		}
	}
	for _, item := range full.Teams {
		if teams[item.ID] {
			want.Teams = append(want.Teams, item)
		}
	}
	for _, item := range full.States {
		if states[item.ID] {
			want.States = append(want.States, item)
		}
	}
	for _, item := range full.Labels {
		if labels[item.ID] {
			want.Labels = append(want.Labels, item)
		}
	}
	for _, item := range full.Projects {
		if projects[item.ID] {
			want.Projects = append(want.Projects, domain.Project{ID: item.ID, Name: item.Name, Icon: item.Icon, Color: item.Color})
		}
	}
	for key, item := range full.TeamSettings {
		if teams[key] {
			want.TeamSettings[key] = domain.TeamSettings{ResolvedSummaries: item.ResolvedSummaries, Timezone: item.Timezone}
		}
	}
	for key, item := range full.NotificationPreferences {
		if recipients[key] {
			want.NotificationPreferences[key] = scaleP21SW61Preference(item)
		}
	}
	for _, item := range full.PushSubscriptions {
		if recipients[item.UserID] {
			want.PushSubscriptions = append(want.PushSubscriptions, domain.PushSubscription{UserID: item.UserID, Enabled: item.Enabled})
		}
	}
	for _, item := range full.IssueSLAs {
		if issueIDs[item.IssueID] {
			want.IssueSLAs = append(want.IssueSLAs, item)
		}
	}
	return want, issueOrdinals, notificationOrdinals, deliveryOrdinals, threadOrdinals
}

func scaleP21SW91Check(t *testing.T, view *SQLiteStore, source *metadataLogicalSource, issueID string) (*legacyDiscussionInputs, int) {
	t.Helper()
	scope := scaleP21SW72FullScope(t, source)
	full, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	want, issues, notifications, deliveries, threads := scaleP21SW91Want(full, issueID)
	queries := 0
	ctx := WithSQLStatementObserver(ContextWithActor(t.Context(), domain.User{ID: "wrong-context-actor", Name: "Must not substitute another actor"}), func(string) { queries++ })
	read, err := view.loadLegacyDiscussionInputs(ctx, source, issueID)
	if err != nil || read == nil || read.Data == nil {
		t.Fatal("legacy callback inputs failed", err)
	}
	t.Cleanup(read.Close)
	if !reflect.DeepEqual(*read.Data, want) {
		left, right := reflect.ValueOf(*read.Data), reflect.ValueOf(want)
		for index := range left.NumField() {
			if !reflect.DeepEqual(left.Field(index).Interface(), right.Field(index).Interface()) {
				t.Errorf("legacy callback field %s differs", left.Type().Field(index).Name)
			}
		}
		t.FailNow()
	}
	if !slices.Equal(read.IssueOrdinals, issues) || !slices.Equal(read.NotificationOrdinals, notifications) ||
		!slices.Equal(read.DeliveryOrdinals, deliveries) || !slices.Equal(read.ThreadSubscriptionOrdinals, threads) {
		t.Fatal("legacy occurrence descriptors changed", read.IssueOrdinals, read.NotificationOrdinals, read.DeliveryOrdinals, read.ThreadSubscriptionOrdinals)
	}
	expectedTarget := -1
	for index, issue := range full.Issues {
		if issue.ID == issueID {
			expectedTarget = index
			break
		}
	}
	_, commentsPresent := full.Comments[issueID]
	_, activitiesPresent := full.Activities[issueID]
	if read.TargetIssueOrdinal != expectedTarget || read.ResourceKey != issueID || read.CommentsPresent != commentsPresent || read.ActivitiesPresent != activitiesPresent {
		t.Fatal("legacy exact group or first occurrence identity changed")
	}
	if queries != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("logical callback input selection queried SQL or decoded workspace metadata", queries)
	}
	scaleP21SW72Compare(t, source, scope, full)
	return read, view.MetadataWorkUsedForTesting()
}

func TestScaleP21SW91LegacyDiscussionInputsLiteral(t *testing.T) {
	for _, variant := range []string{"prepared-native", "missing-issue", "wrong-case-key", "nil-comment-value", "empty-comment-value", "large-selected-comment", "native-missing-role", "narrow-flag-on", "narrow-flag-off"} {
		t.Run(variant, func(t *testing.T) {
			view := &SQLiteStore{metadataResidency: newMetadataResidency()}
			data := scaleP21SW91Data(domain.Workspace{ID: "sw91-native", URLKey: "sw91-native"}, 1000)
			issueID := "sw91-issue"
			switch variant {
			case "missing-issue":
				issueID = "missing-issue"
			case "wrong-case-key":
				issueID = "SW91-issue"
			case "nil-comment-value":
				data.Comments[issueID] = nil
			case "empty-comment-value":
				data.Comments[issueID] = []domain.Comment{}
			case "large-selected-comment":
				data.Comments[issueID][0].Body = strings.Repeat("Complete legal selected comment body. ", 20000)
			case "native-missing-role":
				data.ViewerRole = ""
			case "narrow-flag-on":
				t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
			case "narrow-flag-off":
				t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
			}
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			source := scaleP21SW91Prepare(t, view, data)
			read, held := scaleP21SW91Check(t, view, source, issueID)
			if held == 0 {
				t.Fatal("callback inputs have no owned work allowance")
			}
			if issueID == "sw91-issue" {
				if read.TargetIssueOrdinal != 1 || len(read.IssueOrdinals) != 2 || read.IssueOrdinals[0] != 0 ||
					len(read.NotificationOrdinals) != 4 || !slices.Equal(read.NotificationOrdinals, []int{1, 3, 4, 5}) ||
					len(read.DeliveryOrdinals) != len(data.NotificationDeliveries) {
					t.Fatal("fixture did not prove first native issue, global notification order and all global delivery IDs")
				}
				if read.Data.Issues[1].Creator.Name == read.Data.Viewer.Name || read.Data.Viewer.Via == nil {
					t.Fatal("selector refreshed references after actor substitution or lost native Via")
				}
				if len(read.Data.IssueSLAs) != 2 {
					t.Fatal("legacy SLA inputs followed the native narrow flag")
				}
			}
			source.Release()
			if len(read.Data.NotificationDeliveries) != len(data.NotificationDeliveries) || view.MetadataWorkUsedForTesting() != held {
				t.Fatal("released source discarded callback-owned values or admission")
			}
			read.Close()
			read.Close()
			if read.Data != nil || view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("legacy callback inputs leaked ownership")
			}
		})
	}
}

type scaleP21SW91CallbackReads struct {
	Issue, NotificationIssue                                             *domain.Issue
	Comment                                                              *domain.Comment
	Viewer                                                               domain.User
	ParentFound, ExpectedVersion, AddedReaction, Summaries, Urgent, Push bool
	DeletedComments                                                      []string
	DeletedThreads                                                       []domain.ThreadSubscription
	Notification                                                         *domain.Notification
	FirstDelivery, FutureDeliveryFirst                                   int
	Timezone                                                             string
	MentionDirectory                                                     []domain.User
}

// These are the native lookups the six real comment callbacks and their
// notification helpers consume. HTTP side effects are proved by the writer's
// integration harness; this table proves its selected input read closure.
func scaleP21SW91ReadCallback(t *testing.T, data *domain.Bootstrap, event string) scaleP21SW91CallbackReads {
	t.Helper()
	result := scaleP21SW91CallbackReads{Viewer: data.Viewer, FirstDelivery: -1,
		FutureDeliveryFirst: slices.IndexFunc(data.NotificationDeliveries, func(delivery domain.NotificationDelivery) bool {
			return delivery.ID == "delivery_notification_future_email"
		})}
	issue := func(id string) *domain.Issue {
		for index := range data.Issues {
			if data.Issues[index].ID == id {
				return &data.Issues[index]
			}
		}
		return nil
	}
	result.Issue = issue("sw91-issue")
	for index := range data.Comments["sw91-issue"] {
		if data.Comments["sw91-issue"][index].ID == "sw91-root" {
			result.Comment = &data.Comments["sw91-issue"][index]
			break
		}
	}
	switch event {
	case "comment.created":
		var input domain.CommentCreateInput
		if err := json.Unmarshal([]byte(`{"body":"@recipient@example.test complete reply","parentId":"sw91-root","bodyData":{"type":"userMention","attrs":{"userId":"sw91-recipient"}}}`), &input); err != nil {
			t.Fatal(err)
		}
		result.ParentFound = input.ParentID != nil && slices.ContainsFunc(data.Comments["sw91-issue"], func(comment domain.Comment) bool { return comment.ID == *input.ParentID })
	case "comment.updated", "comment.resolved", "comment.unresolved":
		var input domain.CommentUpdateInput
		if err := json.Unmarshal([]byte(`{"body":"Updated supported producer body","expectedVersion":7,"resolved":true}`), &input); err != nil {
			t.Fatal(err)
		}
		result.ExpectedVersion = input.ExpectedVersion != nil && result.Comment != nil && result.Comment.Version == *input.ExpectedVersion
		if result.Issue != nil {
			result.Summaries = data.TeamSettings[result.Issue.Team.ID].ResolvedSummaries
		}
	case "comment.deleted":
		for _, comment := range data.Comments["sw91-issue"] {
			if comment.ID == "sw91-root" || comment.ParentID != nil && *comment.ParentID == "sw91-root" {
				result.DeletedComments = append(result.DeletedComments, comment.ID)
			}
		}
		for _, subscription := range data.ThreadSubscriptions {
			if subscription.IssueID == "sw91-issue" && subscription.CommentID == "sw91-root" {
				result.DeletedThreads = append(result.DeletedThreads, subscription)
			}
		}
	case "comment.reaction_toggled":
		var input domain.ReactionInput
		if err := json.Unmarshal([]byte(`{"emoji":"like"}`), &input); err != nil {
			t.Fatal(err)
		}
		result.AddedReaction = result.Comment != nil && !slices.Contains(result.Comment.Reactions[input.Emoji], data.Viewer.ID)
	default:
		t.Fatal("unsupported callback fixture", event)
	}
	for _, user := range data.Users {
		result.MentionDirectory = append(result.MentionDirectory, domain.User{ID: user.ID, Email: user.Email, Name: user.Name, DisplayName: user.DisplayName})
	}
	at := time.Date(2026, 11, 15, 1, 2, 3, 456, time.UTC)
	for index := len(data.Notifications) - 1; index >= 0; index-- {
		item := &data.Notifications[index]
		if item.GroupKey == "sw91-recipient:sw91-issue:comments" && item.ReadAt == nil && item.DeletedAt == nil && item.ArchivedAt == nil && at.Sub(item.UpdatedAt) <= 6*time.Hour {
			result.Notification = item
			break
		}
	}
	if result.Notification != nil {
		result.NotificationIssue = issue(result.Notification.IssueID)
		if item := result.NotificationIssue; item != nil {
			result.Timezone = data.TeamSettings[item.Team.ID].Timezone
			if item.Assignee != nil && item.Assignee.ID == result.Notification.RecipientID {
				result.Urgent = item.Priority == 1 || slices.ContainsFunc(data.IssueSLAs, func(sla domain.IssueSLA) bool { return sla.IssueID == item.ID && sla.Status == "breached" })
			}
		}
		result.Push = slices.ContainsFunc(data.PushSubscriptions, func(subscription domain.PushSubscription) bool {
			return subscription.UserID == result.Notification.RecipientID && subscription.Enabled
		})
		result.FirstDelivery = slices.IndexFunc(data.NotificationDeliveries, func(delivery domain.NotificationDelivery) bool {
			return delivery.ID == "delivery_"+result.Notification.ID+"_email"
		})
	}
	return result
}

func TestScaleP21SW91LegacyDiscussionSupportedCallbackReads(t *testing.T) {
	view := &SQLiteStore{metadataResidency: newMetadataResidency()}
	data := scaleP21SW91Data(domain.Workspace{ID: "sw91-callback", URLKey: "sw91-callback"}, 1000)
	// The last native same-group row is too old, so the winner names another
	// issue/recipient despite its logical GroupKey. Both are legal SQL values.
	data.Notifications[4].UpdatedAt = data.Notifications[4].UpdatedAt.Add(-10 * time.Hour)
	source := scaleP21SW91Prepare(t, view, data)
	full, err := source.Select(t.Context(), scaleP21SW72FullScope(t, source), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	read, _ := scaleP21SW91Check(t, view, source, "sw91-issue")
	for _, event := range []string{"comment.created", "comment.updated", "comment.resolved", "comment.unresolved", "comment.deleted", "comment.reaction_toggled"} {
		t.Run(event, func(t *testing.T) {
			want, got := scaleP21SW91ReadCallback(t, &full, event), scaleP21SW91ReadCallback(t, read.Data, event)
			if !reflect.DeepEqual(got, want) {
				t.Fatal("literal callback native reads differ", event)
			}
			if got.NotificationIssue == nil || got.NotificationIssue.ID != "sw91-other-issue" || !got.Urgent || !got.Push || got.FirstDelivery != 0 || got.FutureDeliveryFirst != 2 || got.Timezone != "America/Los_Angeles" {
				t.Fatal("misgrouped notification or first global delivery collision fixture did not exercise callback closure")
			}
		})
	}
	read.Close()
	source.Release()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("supported callback reads retained input work")
	}
}

func TestScaleP21SW91LegacyDiscussionInputsOwnershipAndFailures(t *testing.T) {
	view := &SQLiteStore{metadataResidency: newMetadataResidency()}
	data := scaleP21SW91Data(domain.Workspace{ID: "sw91-own", URLKey: "sw91-own"}, 1000)
	source := scaleP21SW91Prepare(t, view, data)
	scope := scaleP21SW72FullScope(t, source)
	full, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	read, err := view.loadLegacyDiscussionInputs(ctx, source, "sw91-issue")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(read.Close)
	read.Data.Comments["sw91-issue"][0].Body = "Owned comment edit"
	read.Data.Users[0].Name = "Owned directory edit"
	preferences := read.Data.NotificationPreferences["sw91-recipient"]
	preferences.Email.Categories = map[string]bool{"comments": false}
	read.Data.NotificationPreferences["sw91-recipient"] = preferences
	read.Data.NotificationDeliveries[0].Status = "owned status edit"
	scaleP21SW72Compare(t, source, scope, full)
	source.Release()
	cancel()
	if read.Data.Comments["sw91-issue"][0].Body != "Owned comment edit" || read.Data.Users[0].Name != "Owned directory edit" {
		t.Fatal("cancellation invalidated owned callback output")
	}
	read.Close()
	for _, failure := range []string{"cancelled", "quota"} {
		t.Run(failure, func(t *testing.T) {
			input := scaleP21SW91Prepare(t, view, data)
			work, limit := view.metadataResidency.work, view.metadataResidency.work.limit
			readCtx, stop := context.WithCancel(t.Context())
			defer stop()
			want := error(context.Canceled)
			if failure == "cancelled" {
				stop()
			} else {
				work.limit, want = 1, ErrMetadataWorkBudget
			}
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			failed, err := view.loadLegacyDiscussionInputs(readCtx, input, "sw91-issue")
			work.limit = limit
			if failed != nil {
				failed.Close()
				t.Fatal("failed callback input returned partial ownership")
			}
			if !errors.Is(err, want) || view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("callback input failure changed error or leaked", err)
			}
			input.Release()
		})
	}
}

func TestScaleP21SW91LegacyDiscussionInputsWarmReloadBodyBound(t *testing.T) {
	f := scaleP21SW70Setup(t)
	workspace := f.data.Workspace.URLKey
	held := map[bool]int{}
	mergeHeld := map[bool]int{}
	for _, size := range []int{1000, 10000} {
		f.db.SetMaxOpenConns(4)
		seed := scaleP21SW91Data(f.data.Workspace, size)
		restore := ForceFullMutationsForTesting(true)
		err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw91_seed", "", nil, func(data *domain.Bootstrap) error {
			*data = cloneBootstrap(collectionMetadata(seed))
			return nil
		})
		restore()
		if err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			t.Run(fmt.Sprintf("body-%d/reload-%t", size, reload), func(t *testing.T) {
				f.db.SetMaxOpenConns(4)
				if reload {
					f.mu.Lock()
					f.forgetCopyRevisionLocked(workspace)
					f.mu.Unlock()
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
					t.Fatal(err)
				}
				installed, err := f.view.retainMetadataLogicalSource(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				tx := f.begin(t)
				for index, issue := range seed.Issues {
					raw, err := json.Marshal(issue)
					if err != nil {
						t.Fatal(err)
					}
					f.put(t, tx, workspace, fmt.Sprintf("sw91-physical-issue-%d", index), int64(index), raw)
				}
				issues, err := f.view.prepareLegacyIssueImage(t.Context(), tx, workspace, installed)
				installed.Release()
				if err != nil {
					t.Fatal(err)
				}
				// The caller may already supply normalized external collections.
				// Retain them exactly while SQL supplies physical/JSON issue IDs.
				external, err := issues.Transform(t.Context(), func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
					index, _, _ := metadataLogicalField(name)
					if slices.Contains([]string{"comments", "activities", "notifications", "notificationDeliveries"}, name) {
						return metadataAggregateNativeNode{value: reflect.ValueOf(seed).Field(index)}, true, nil
					}
					return metadataAggregateStoredNode{value: node}, false, nil
				})
				issues.Release()
				if err != nil {
					t.Fatal(err)
				}
				literal, err := external.Select(t.Context(), scaleP21SW72FullScope(t, external), 64<<20)
				external.Release()
				if err != nil {
					t.Fatal(err)
				}
				prepared := scaleP21SW91Prepare(t, f.view, literal)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				read, cost := scaleP21SW91Check(t, f.view, prepared, "sw91-issue")
				if held[reload] == 0 {
					held[reload] = cost
				} else if held[reload] != cost {
					t.Fatal("10x unrelated bodies increased held legacy callback inputs", held[reload], cost)
				}
				t.Logf("body_size=%d reload=%t callback_held=%d", size, reload, cost)
				scope := scaleP21SW72FullScope(t, prepared)
				want, err := prepared.Select(t.Context(), scope, 64<<20)
				if err != nil {
					t.Fatal(err)
				}
				scaleP21SW91MergeCallback(t, &want, "comment.reaction_toggled")
				scaleP21SW91MergeCallback(t, read.Data, "comment.reaction_toggled")
				merged, err := read.MergeCallback(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(merged.Release)
				mergedCost := f.view.MetadataWorkUsedForTesting()
				if mergeHeld[reload] == 0 {
					mergeHeld[reload] = mergedCost
				} else if mergeHeld[reload] != mergedCost {
					t.Fatal("10x unrelated bodies increased retained merged callback work", mergeHeld[reload], mergedCost)
				}
				scaleP21SW72Compare(t, merged, scope, want)
				prepared.Release()
				read.Close()
				scaleP21SW72Compare(t, merged, scope, want)
				merged.Release()
				if f.view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("warm/reloaded callback retained work after Close")
				}
			})
		}
	}
}

func scaleP21SW91MergeCallback(t *testing.T, data *domain.Bootstrap, event string) {
	t.Helper()
	issueID, commentID := "sw91-issue", "sw91-root"
	now := time.Date(2026, 11, 15, 4, 2, 3, 456, time.UTC)
	commentIndex := slices.IndexFunc(data.Comments[issueID], func(comment domain.Comment) bool { return comment.ID == commentID })
	var comment *domain.Comment
	switch event {
	case "comment.created":
		var input domain.CommentCreateInput
		if err := json.Unmarshal([]byte(`{"body":"Complete new comment","parentId":"sw91-root","bodyData":{"type":"paragraph","content":[]}}`), &input); err != nil {
			t.Fatal(err)
		}
		if commentIndex < 0 {
			input.ParentID = nil
		}
		created := domain.Comment{ID: "comment_sw91-new", Version: 1, Body: input.Body, BodyData: input.BodyData, ParentID: input.ParentID,
			User: data.Viewer, BotActor: data.Viewer.Via, Reactions: map[string][]string{}, CreatedAt: now}
		data.Comments[issueID] = append(data.Comments[issueID], created)
		comment = &data.Comments[issueID][len(data.Comments[issueID])-1]
		commentID = created.ID
	case "comment.updated":
		if commentIndex < 0 {
			t.Fatal("update fixture has no target comment")
		}
		var input domain.CommentUpdateInput
		if err := json.Unmarshal([]byte(`{"body":"Complete supported edited comment","bodyData":{"type":"paragraph","content":[]},"expectedVersion":7}`), &input); err != nil {
			t.Fatal(err)
		}
		comment = &data.Comments[issueID][commentIndex]
		comment.Body, comment.BodyData, comment.EditedAt = input.Body, input.BodyData, &now
		comment.Version++
	case "comment.resolved", "comment.unresolved":
		if commentIndex < 0 {
			t.Fatal("resolve fixture has no target comment")
		}
		comment = &data.Comments[issueID][commentIndex]
		comment.Resolved = event == "comment.resolved"
		comment.Version++
		if comment.Resolved {
			comment.ThreadSummary = &domain.CommentThreadSummary{Content: "Complete resolved thread summary"}
		} else {
			comment.ThreadSummary = nil
		}
	case "comment.deleted":
		data.Comments[issueID] = slices.DeleteFunc(data.Comments[issueID], func(comment domain.Comment) bool {
			return comment.ID == commentID || comment.ParentID != nil && *comment.ParentID == commentID
		})
		data.ThreadSubscriptions = slices.DeleteFunc(data.ThreadSubscriptions, func(subscription domain.ThreadSubscription) bool {
			return subscription.IssueID == issueID && subscription.CommentID == commentID
		})
	case "comment.reaction_toggled":
		if commentIndex < 0 {
			t.Fatal("reaction fixture has no target comment")
		}
		comment = &data.Comments[issueID][commentIndex]
		if comment.Reactions == nil {
			comment.Reactions = map[string][]string{}
		}
		if users := comment.Reactions["like"]; slices.Contains(users, data.Viewer.ID) {
			users = slices.DeleteFunc(users, func(id string) bool { return id == data.Viewer.ID })
			if len(users) == 0 {
				delete(comment.Reactions, "like")
			} else {
				comment.Reactions["like"] = users
			}
		} else {
			comment.Reactions["like"] = append(users, data.Viewer.ID)
		}
		return
	case "drop-comment-group":
		delete(data.Comments, issueID)
		return
	default:
		t.Fatal("unsupported merge fixture callback", event)
	}
	activity := RecordIssueActivity(data, issueID, domain.ActivityEvent{ID: "activity_sw91-fixed", Type: event, Actor: data.Viewer, BotActor: data.Viewer.Via, CreatedAt: now, Metadata: domain.ActivityMetadata{"commentId": commentID}})
	if comment == nil || event == "comment.updated" {
		return
	}
	group := "sw91-recipient:" + issueID + ":comments"
	index := -1
	for current := len(data.Notifications) - 1; current >= 0; current-- {
		item := data.Notifications[current]
		if item.GroupKey == group && item.ReadAt == nil && item.DeletedAt == nil && item.ArchivedAt == nil && now.Sub(item.UpdatedAt) <= 6*time.Hour {
			index = current
			break
		}
	}
	if index < 0 {
		data.Notifications = append(data.Notifications, domain.Notification{
			ID: "notification_" + activity.ID + "_sw91-recipient_comments", RecipientID: "sw91-recipient", IssueID: issueID, Type: "comment", Category: "comments", GroupKey: group,
			Actor: data.Viewer, LatestActorIDs: []string{data.Viewer.ID}, OccurrenceCount: 1, CreatedAt: now, UpdatedAt: now,
		})
		index = len(data.Notifications) - 1
	} else {
		item := &data.Notifications[index]
		item.OccurrenceCount++
		item.Actor = activity.Actor
		if !slices.Contains(item.LatestActorIDs, activity.Actor.ID) {
			item.LatestActorIDs = append(item.LatestActorIDs, activity.Actor.ID)
		}
		if len(item.LatestActorIDs) > 3 {
			item.LatestActorIDs = item.LatestActorIDs[len(item.LatestActorIDs)-3:]
		}
	}
	notification := &data.Notifications[index]
	notification.SourceType, notification.SourceID, notification.CommentID, notification.ActivityID, notification.UpdatedAt = "comment", comment.ID, comment.ID, activity.ID, now
	// Exercise the literal enqueue helper's first global ID lookup and its
	// only assignments to an existing delivery. Other native fields stay.
	channels := []string{"email", "desktop"}
	if slices.ContainsFunc(data.PushSubscriptions, func(subscription domain.PushSubscription) bool {
		return subscription.UserID == notification.RecipientID && subscription.Enabled
	}) {
		channels = append(channels, "push")
	}
	for _, channel := range channels {
		status := "pending"
		if channel == "email" {
			status = "digest"
		}
		id := "delivery_" + notification.ID + "_" + channel
		existing := slices.IndexFunc(data.NotificationDeliveries, func(delivery domain.NotificationDelivery) bool { return delivery.ID == id })
		if existing < 0 {
			data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{ID: id, NotificationID: notification.ID, RecipientID: notification.RecipientID, Channel: channel, Status: status, CreatedAt: now, UpdatedAt: now})
			existing = len(data.NotificationDeliveries) - 1
		} else {
			data.NotificationDeliveries[existing].Status, data.NotificationDeliveries[existing].UpdatedAt = status, now
		}
		if status == "digest" {
			due := now.Add(time.Hour)
			zone := time.UTC
			for _, issue := range data.Issues {
				if issue.ID == notification.IssueID {
					if loaded, err := time.LoadLocation(data.TeamSettings[issue.Team.ID].Timezone); err == nil {
						zone = loaded
					}
					break
				}
			}
			local := due.In(zone)
			if local.Hour() >= 18 {
				local = time.Date(local.Year(), local.Month(), local.Day()+1, 9, 0, 0, 0, zone)
			}
			if local.Hour() < 9 {
				local = time.Date(local.Year(), local.Month(), local.Day(), 9, 0, 0, 0, zone)
			}
			for local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
				local = local.AddDate(0, 0, 1)
			}
			due = local.UTC()
			data.NotificationDeliveries[existing].NextAttemptAt = &due
		}
	}
}

func TestScaleP21SW91LegacyDiscussionMergeCallback(t *testing.T) {
	for _, event := range []string{"comment.created", "comment.updated", "comment.resolved", "comment.unresolved", "comment.deleted", "comment.reaction_toggled", "drop-comment-group"} {
		for _, shape := range []string{"existing-candidates", "append-notification", "misgrouped-winner", "nil-group"} {
			if shape == "nil-group" && event != "comment.created" {
				continue
			}
			t.Run(event+"/"+shape, func(t *testing.T) {
				view := &SQLiteStore{metadataResidency: newMetadataResidency()}
				data := scaleP21SW91Data(domain.Workspace{ID: "sw91-merge", URLKey: "sw91-merge"}, 1000)
				preferences := data.NotificationPreferences["sw91-recipient"]
				preferences.Desktop.Enabled, preferences.ImmediateUrgent = true, false
				data.NotificationPreferences["sw91-recipient"] = preferences
				if event == "comment.unresolved" {
					data.Comments["sw91-issue"][0].Resolved = true
					data.Comments["sw91-issue"][0].ThreadSummary = &domain.CommentThreadSummary{Content: "Previously resolved summary"}
				}
				if shape == "append-notification" {
					for index := range data.Notifications {
						if data.Notifications[index].GroupKey == "sw91-recipient:sw91-issue:comments" {
							data.Notifications[index].ReadAt = new(data.Notifications[index].UpdatedAt)
						}
					}
					data.NotificationDeliveries[2].ID = "delivery_notification_activity_sw91-fixed_sw91-recipient_comments_email"
				}
				if shape == "misgrouped-winner" {
					data.Notifications[4].UpdatedAt = data.Notifications[4].UpdatedAt.Add(-10 * time.Hour)
				}
				if shape == "nil-group" {
					data.Comments["sw91-issue"], data.Activities["sw91-issue"] = nil, nil
				}
				disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
				source := scaleP21SW91Prepare(t, view, data)
				scope := scaleP21SW72FullScope(t, source)
				before, err := source.Select(t.Context(), scope, 64<<20)
				if err != nil {
					t.Fatal(err)
				}
				want, err := source.Select(t.Context(), scope, 64<<20)
				if err != nil {
					t.Fatal(err)
				}
				read, _ := scaleP21SW91Check(t, view, source, "sw91-issue")
				scaleP21SW91MergeCallback(t, &want, event)
				scaleP21SW91MergeCallback(t, read.Data, event)
				queries := 0
				ctx, cancel := context.WithCancel(WithSQLStatementObserver(t.Context(), func(string) { queries++ }))
				result, err := read.MergeCallback(ctx)
				if err != nil || result == nil {
					t.Fatal("legacy callback merge failed", err)
				}
				t.Cleanup(result.Release)
				scaleP21SW72Compare(t, result, scope, want)
				scaleP21SW72Compare(t, source, scope, before)
				if result.Stamp() != source.Stamp() || queries != 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("callback merge changed generation or read SQL", queries)
				}
				for index, node := range source.fields {
					name := reflect.TypeFor[domain.Bootstrap]().Field(index).Tag.Get("json")
					switch name {
					case "-", "comments", "activities", "notifications", "notificationDeliveries", "threadSubscriptions":
						continue
					}
					if result.fields[index] != node {
						t.Fatal("callback merge rewrote an unrelated native field", name)
					}
				}
				source.Release()
				cancel()
				read.Close()
				scaleP21SW72Compare(t, result, scope, want)
				result.Release()
				result.Release()
				if _, err := result.Select(t.Context(), scope, 64<<20); !errors.Is(err, errMetadataLogicalClosed) ||
					view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
					t.Fatal("callback merge leaked result, input work or storage", err)
				}
			})
		}
	}
}

func TestScaleP21SW91LegacyDiscussionMergeRejectsUnsupportedEdits(t *testing.T) {
	for _, edit := range []string{"issue", "other-comment-key", "comment-author", "notification-recipient", "delivery-parent", "activity-prefix", "thread-edit", "viewer"} {
		t.Run(edit, func(t *testing.T) {
			view := &SQLiteStore{metadataResidency: newMetadataResidency()}
			data := scaleP21SW91Data(domain.Workspace{ID: "sw91-reject", URLKey: "sw91-reject"}, 1000)
			source := scaleP21SW91Prepare(t, view, data)
			scope := scaleP21SW72FullScope(t, source)
			before, err := source.Select(t.Context(), scope, 64<<20)
			if err != nil {
				t.Fatal(err)
			}
			read, _ := scaleP21SW91Check(t, view, source, "sw91-issue")
			switch edit {
			case "issue":
				read.Data.Issues[0].Title = "Undeclared issue edit"
			case "other-comment-key":
				read.Data.Comments["other-key"] = []domain.Comment{{ID: "not-selected"}}
			case "comment-author":
				read.Data.Comments["sw91-issue"][0].User.Name = "Undeclared author edit"
			case "notification-recipient":
				read.Data.Notifications[0].RecipientID = "Undeclared recipient"
			case "delivery-parent":
				read.Data.NotificationDeliveries[0].NotificationID = "Undeclared parent"
			case "activity-prefix":
				read.Data.Activities["sw91-issue"][0].Type = "Undeclared existing activity edit"
			case "thread-edit":
				read.Data.ThreadSubscriptions[0].State = "Undeclared state change"
			case "viewer":
				read.Data.Viewer.Name = "Undeclared viewer edit"
			}
			result, err := read.MergeCallback(t.Context())
			if result != nil {
				result.Release()
				t.Fatal("unsupported callback returned partial full image")
			}
			if !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("unsupported callback edit was not rejected", err)
			}
			scaleP21SW72Compare(t, source, scope, before)
			read.Close()
			source.Release()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("rejected callback retained its dynamic work")
			}
		})
	}
}

func TestScaleP21SW91LegacyDiscussionMergeCancellation(t *testing.T) {
	view := &SQLiteStore{metadataResidency: newMetadataResidency()}
	data := scaleP21SW91Data(domain.Workspace{ID: "sw91-cancel-merge", URLKey: "sw91-cancel-merge"}, 1000)
	source := scaleP21SW91Prepare(t, view, data)
	read, _ := scaleP21SW91Check(t, view, source, "sw91-issue")
	scaleP21SW91MergeCallback(t, read.Data, "comment.created")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	held := view.MetadataWorkUsedForTesting()
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	result, err := read.MergeCallback(ctx)
	if result != nil {
		result.Release()
		t.Fatal("canceled callback merge returned an image")
	}
	if !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != held ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("canceled callback merge changed its error or leaked", err)
	}
	work, limit := view.metadataResidency.work, view.metadataResidency.work.limit
	work.limit = held
	result, err = read.MergeCallback(t.Context())
	work.limit = limit
	if result != nil {
		result.Release()
		t.Fatal("unadmitted callback merge returned an image")
	}
	if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != held ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("unadmitted callback merge changed its error or leaked", err)
	}
	read.Close()
	source.Release()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("canceled callback input retained work")
	}
}
