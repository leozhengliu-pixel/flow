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

func TestScaleP21HotIssueInputsNativeClosure(t *testing.T) {
	at := time.Date(2026, 11, 14, 9, 13, 0, 0, time.UTC)
	actor := domain.User{ID: "actor", Name: "Actor"}
	assignee := domain.User{ID: "assignee", Name: "First", OutOfOfficeUntil: &at}
	mention := domain.User{ID: "mention", Name: "Renée", Email: "renee@example.test"}
	structured := domain.User{ID: "structured", Name: "Structured"}
	duplicate := assignee
	duplicate.Name = "Second"
	target := domain.Issue{ID: "target", Team: domain.Team{ID: "team"}, Creator: actor, SubscriberIDs: []string{"assignee"}}
	description := "Hello @RENÉE"
	labels := []string{"label"}
	input := domain.IssueUpdateInput{
		AssigneeID: &assignee.ID, LabelIDs: &labels, Description: &description,
		DescriptionData: map[string]any{"type": "userMention", "attrs": map[string]any{"userId": structured.ID}},
	}
	baseline := 0
	for _, count := range []int{10, 100} {
		users := []domain.User{actor, assignee, mention, structured, duplicate}
		for i := 0; i < count; i++ {
			users = append(users, domain.User{ID: fmt.Sprint("unrelated-", i), Name: "Unrelated", DisplayName: "Unrelated", Email: "unrelated@example.test", AvatarURL: strings.Repeat("unrelated body ", count*100)})
		}
		stored := domain.Bootstrap{
			Users: users,
			UserSettings: map[string]domain.UserSettings{
				actor.ID: {UserID: "mismatched", AssignStarted: true, BranchFormat: strings.Repeat("unused preference ", count*100)},
			},
			NotificationPreferences: map[string]domain.NotificationPreferences{
				assignee.ID: {UserID: "mismatched", Inbox: domain.NotificationChannelPreferences{Enabled: false}},
				mention.ID:  {UserID: mention.ID, EmailFormat: "digest"},
				strings.Repeat("unrelated key ", count*100): {UserID: "unrelated"},
			},
			PushSubscriptions: []domain.PushSubscription{
				{ID: "related", UserID: assignee.ID, Enabled: true, Endpoint: strings.Repeat("unused endpoint ", count*100)},
				{ID: "unrelated", UserID: "unrelated", Enabled: true, Endpoint: strings.Repeat("unrelated endpoint ", count*100)},
			},
			CycleSettings: map[string]domain.CycleSettings{"team": {Enabled: true, AutoAddStarted: true}, "unrelated": {Enabled: true}},
			Cycles: []domain.Cycle{
				{ID: "future", TeamID: "team", Status: "upcoming", Description: strings.Repeat("unused future cycle ", count*100)},
				{ID: "current", TeamID: "team", Status: "current", StartsAt: at, Description: strings.Repeat("unused current cycle ", count*100)},
				{ID: "other", TeamID: "unrelated", Status: "current", Description: strings.Repeat("unrelated cycle ", count*100)},
			},
			Labels: []domain.IssueLabel{
				{ID: "label", GroupID: "group", Name: "Selected", Scope: "team"},
				{ID: "unrelated", Description: strings.Repeat("unrelated label ", count*100)},
			},
			LabelGroups: []domain.LabelGroup{
				{ID: "group", Name: strings.Repeat("unused group name ", count*100), ArchivedAt: &at},
				{ID: "unrelated", Name: strings.Repeat("unrelated group ", count*100)},
			},
			Settings: map[string]any{"sla": map[string]any{"enabled": false, "workWeek": "sunThu"}, "unrelated": strings.Repeat("unrelated setting ", count*100)},
			IssueSLAs: []domain.IssueSLA{
				{ID: "target-sla", IssueID: target.ID, RuleID: "disabled-rule", Status: "breached"},
				{ID: "unrelated", IssueID: "unrelated", RuleID: "other-rule"},
			},
			SLARules: []domain.SLARule{
				{ID: "wrong-team", TeamIDs: []string{"unrelated"}, Enabled: true, Name: strings.Repeat("unrelated rule ", count*100)},
				{ID: "disabled-rule", Enabled: false},
				{ID: "global", Enabled: true, TargetMinutes: 7},
				{ID: "team-rule", TeamIDs: []string{"team"}, Enabled: true},
			},
			Members: []domain.WorkspaceMember{{User: assignee, Status: "suspended"}, {User: domain.User{ID: "unrelated", AvatarURL: strings.Repeat("unrelated member ", count*100)}}},
		}
		view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": stored})
		data := domain.Bootstrap{Viewer: actor, Issues: []domain.Issue{target}}
		read, err := view.loadIssueHotInputs(t.Context(), "physical", &data, target, input)
		if err != nil {
			t.Fatal(err)
		}
		got := read.data
		if !reflect.DeepEqual(got.Users, users[:5]) {
			t.Fatal("native user order, duplicates, mentions or timestamps changed", got.Users)
		}
		if got.UserSettings[actor.ID].AssignStarted != true || got.UserSettings[actor.ID].BranchFormat != "" ||
			!reflect.DeepEqual(got.NotificationPreferences, map[string]domain.NotificationPreferences{assignee.ID: stored.NotificationPreferences[assignee.ID], mention.ID: stored.NotificationPreferences[mention.ID]}) {
			t.Fatal("viewer preferences or recipient map-key selection changed")
		}
		if !reflect.DeepEqual(got.PushSubscriptions, []domain.PushSubscription{{UserID: assignee.ID, Enabled: true}}) ||
			!reflect.DeepEqual(got.Cycles, []domain.Cycle{{ID: "current", TeamID: "team", Status: "current", StartsAt: at}}) ||
			!reflect.DeepEqual(got.Labels, stored.Labels[:1]) ||
			!reflect.DeepEqual(got.LabelGroups, []domain.LabelGroup{{ID: "group", ArchivedAt: &at}}) {
			t.Fatal("cycle, push or label closure changed")
		}
		if !reflect.DeepEqual(got.SLARules, stored.SLARules[1:]) || !reflect.DeepEqual(got.IssueSLAs, stored.IssueSLAs[:1]) || len(got.Members) != 1 || got.Members[0].Status != "suspended" {
			t.Fatal("SLA order or former-member fallback inputs changed")
		}
		held := view.metadataResidency.work.used
		if baseline == 0 {
			baseline = held
		}
		if held != baseline || held > 2<<20 {
			t.Fatal("10x unrelated bodies/count changed held work", baseline, held)
		}
		var oracle domain.Bootstrap
		read.install(&data, &oracle)
		data.Users[0].Name = "callback"
		if oracle.Users[0].Name != actor.Name {
			t.Fatal("P04 baseline aliases callback state")
		}
		read.close()
		read.close()
		if view.metadataResidency.work.used != 0 {
			t.Fatal("input selection leaked work")
		}
		t.Logf("unrelated_users=%d held_work=%d", count, held)
	}
}

func TestScaleP21HotIssueInputsBudgetAndBlockers(t *testing.T) {
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"workspace": {Users: []domain.User{{ID: "user", AvatarURL: strings.Repeat("large selected user ", 100000)}}}})
	target := domain.Issue{ID: "target"}
	data := domain.Bootstrap{Issues: []domain.Issue{target}}
	input := domain.IssueUpdateInput{AssigneeID: new("user")}
	read, err := view.loadIssueHotInputs(t.Context(), "workspace", &data, target, input)
	if err != nil || len(read.data.Users) != 1 {
		t.Fatal("legal large relevant user rejected", err)
	}
	read.close()
	view.metadataResidency.work.limit = 128 << 10
	if _, err := view.loadIssueHotInputs(t.Context(), "workspace", &data, target, input); !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != 0 {
		t.Fatal("partial quota failure accepted or leaked", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := view.loadIssueHotInputs(ctx, "workspace", &data, target, input); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation changed", err)
	}
	for _, blocked := range []domain.IssueUpdateInput{
		{TeamID: new("destination")}, {ProjectID: new("project")}, {ProjectMilestoneID: new("milestone")},
		{ContentState: new("opaque collaboration state")}, {DescriptionState: new("opaque legacy editor state")},
	} {
		if _, err := view.loadIssueHotInputs(t.Context(), "workspace", &data, target, blocked); !errors.Is(err, ErrBoundedMetadataCoverage) {
			t.Fatal("unported callback boundary was silently accepted", blocked, err)
		}
	}
	data.Issues[0].Project = &domain.ProjectSummary{ID: "project"}
	if _, err := view.loadIssueHotInputs(t.Context(), "workspace", &data, target, input); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("project progress closure silently accepted", err)
	}
}

func TestScaleP21HotIssueInputsManyRelevantPeople(t *testing.T) {
	var people []domain.User
	var subscribers []string
	for i := 0; i < 60; i++ {
		id := fmt.Sprint("person-", i)
		people = append(people, domain.User{ID: id, Name: id})
		subscribers = append(subscribers, id)
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"workspace": {Users: people}})
	target := domain.Issue{ID: "target"}
	data := domain.Bootstrap{Issues: []domain.Issue{target}}
	read, err := view.loadIssueHotInputs(t.Context(), "workspace", &data, target, domain.IssueUpdateInput{SubscriberIDs: &subscribers})
	if err != nil || !reflect.DeepEqual(read.data.Users, people) {
		t.Fatal("more than 50 legitimate relevant people were rejected", err)
	}
	read.close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("many-recipient selection leaked work")
	}
}

func TestScaleP21HotIssueInputsEmptyAndLiteralMentionSemantics(t *testing.T) {
	text := "An @unknown mention"
	user := domain.User{ID: "empty-names"}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"workspace": {
		Users:         []domain.User{user},
		UserSettings:  map[string]domain.UserSettings{"": {AssignStarted: true}},
		CycleSettings: map[string]domain.CycleSettings{"": {Enabled: true}},
		Cycles:        []domain.Cycle{{ID: "", TeamID: "", Status: "current"}},
	}})
	target := domain.Issue{ID: "target"}
	data := domain.Bootstrap{Issues: []domain.Issue{target}}
	read, err := view.loadIssueHotInputs(t.Context(), "workspace", &data, target, domain.IssueUpdateInput{Description: &text})
	if err != nil {
		t.Fatal(err)
	}
	defer read.close()
	// The literal mention scanner tests "@" when a stored name/email is
	// empty. Preserve that behavior rather than normalizing the directory.
	if !reflect.DeepEqual(read.data.Users, []domain.User{user}) || !read.data.UserSettings[""].AssignStarted || !read.data.CycleSettings[""].Enabled || len(read.data.Cycles) != 1 {
		t.Fatal("empty keys or literal empty-name mention behavior changed")
	}
}

func TestScaleP21HotIssueInputsUpdateDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	actor := f.data.Viewer
	person := domain.User{ID: "p21-input-person", Name: "Person", Active: true}
	label := domain.IssueLabel{ID: "p21-input-label", Name: "Selected"}
	at := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	cycle := domain.Cycle{ID: "p21-input-cycle", TeamID: f.data.Teams[0].ID, Status: "current", StartsAt: at}
	var issues []domain.Issue
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.hot_issue_inputs_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Users = append(data.Users, person)
		data.Labels = append(data.Labels, label)
		data.Cycles = append(data.Cycles, cycle)
		data.CycleSettings[cycle.TeamID] = domain.CycleSettings{Enabled: true, AutoAddStarted: true}
		for index := range 2 {
			issue := domain.Issue{ID: fmt.Sprint("p21-input-issue-", index), Identifier: fmt.Sprintf("%s-%d", f.data.Teams[0].Key, index+1), Number: index + 1, Version: 1, Creator: actor, Team: f.data.Teams[0], State: f.data.States[0], CreatedAt: at, UpdatedAt: at}
			issues = append(issues, issue)
			data.Issues = append(data.Issues, issue)
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
	input := domain.IssueUpdateInput{Title: new("Updated"), AssigneeID: &person.ID, LabelIDs: &[]string{label.ID}}
	stores := []*SQLiteStore{f.SQLiteStore, view}
	var outputs []domain.Issue
	beforeComparisons, beforeDifferences := NarrowMutationVerificationCounts()
	for index, repo := range stores {
		output, err := repo.UpdateIssueRecord(t.Context(), workspace, issues[index].ID, &issues[index].Version, IssueMutationScope{Payload: input}, func(data *domain.Bootstrap, current *domain.Issue) error {
			current.Title = *input.Title
			user := slices.IndexFunc(data.Users, func(user domain.User) bool { return user.ID == *input.AssigneeID })
			labelIndex := slices.IndexFunc(data.Labels, func(item domain.IssueLabel) bool { return item.ID == label.ID })
			cycleIndex := slices.IndexFunc(data.Cycles, func(item domain.Cycle) bool { return item.TeamID == current.Team.ID && item.Status == "current" })
			if user < 0 || labelIndex < 0 || cycleIndex < 0 || !data.CycleSettings[current.Team.ID].Enabled {
				t.Fatal("callback reference closure absent", index)
			}
			current.Assignee = &data.Users[user]
			current.Labels = []domain.IssueLabel{data.Labels[labelIndex]}
			current.CycleID = &data.Cycles[cycleIndex].ID
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		output.ID, output.Identifier, output.Number, output.UpdatedAt = "", "", 0, time.Time{}
		outputs = append(outputs, output)
	}
	if !reflect.DeepEqual(outputs[0], outputs[1]) || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("literal/bounded update or ownership differs", outputs)
	}
	afterComparisons, afterDifferences := NarrowMutationVerificationCounts()
	if afterComparisons-beforeComparisons != 2 || afterDifferences != beforeDifferences {
		t.Fatal("P04 comparison did not retain exact selected baseline")
	}
}
