package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func ageIssueForActivityTest(t *testing.T, repo *store.SQLiteStore, workspace string, issue domain.Issue) domain.Issue {
	t.Helper()
	updated, err := repo.UpdateIssueRecord(t.Context(), workspace, issue.ID, nil, store.IssueMutationScope{}, func(_ *domain.Bootstrap, current *domain.Issue) error {
		current.CreatedAt = time.Now().UTC().Add(-4 * time.Minute)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func TestIssueActivityPATCHBurstAndGrace(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "activity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	var hooks []domain.DomainEvent
	repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { hooks = append(hooks, event) })
	data := repo.Bootstrap()
	team := data.Teams[0]
	findState := func(kind string) domain.WorkflowState {
		t.Helper()
		for _, state := range data.States {
			if state.Type == kind && (state.TeamID == "" || state.TeamID == team.ID) {
				return state
			}
		}
		t.Fatalf("missing state %s", kind)
		return domain.WorkflowState{}
	}
	started, done, todo := findState("started"), findState("completed"), findState("unstarted")
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Activity contract", "teamId": team.ID, "stateId": started.ID}, http.StatusCreated)
	history := func() []domain.ActivityEvent {
		t.Helper()
		page := requestJSON[store.IssueHistory](t, handler, http.MethodGet, "/api/issue-records/"+issue.ID+"/history", nil, http.StatusOK)
		return slices.DeleteFunc(page.Activities, func(event domain.ActivityEvent) bool { return event.Type == "issue.created" })
	}
	patch := func(input map[string]any) {
		t.Helper()
		requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, input, http.StatusOK)
	}
	patch(map[string]any{"priority": 4})
	if got := history(); len(got) != 0 {
		t.Fatalf("creator grace produced history: %+v", got)
	}
	err = repo.MutateWorkspace(store.WithIssueRecordMutations(t.Context(), issue.ID), data.Workspace.URLKey, "issue.updated", issue.ID, nil, func(current *domain.Bootstrap) error {
		target, err := issueByID(current, issue.ID)
		if err != nil {
			return err
		}
		target.CreatedAt = time.Now().UTC().Add(-time.Hour)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	patch(map[string]any{"stateId": done.ID})
	first := history()[0]
	hooksBefore := len(hooks)
	patch(map[string]any{"stateId": todo.ID})
	if len(hooks) != hooksBefore+1 || hooks[len(hooks)-1].Type != "issue.updated" {
		t.Fatalf("merged history changed the issue webhook stream: %+v", hooks[hooksBefore:])
	}
	got := history()
	if len(got) != 1 || got[0].Metadata["stateBefore"] != started.Name || got[0].Metadata["state"] != todo.Name || got[0].ID != first.ID || !got[0].CreatedAt.Equal(first.CreatedAt) || got[0].UpdatedAt == nil {
		t.Fatalf("PATCH did not merge status endpoints: %+v", got)
	}
	patch(map[string]any{"stateId": started.ID})
	if got := history(); len(got) != 0 {
		t.Fatalf("round trip survived: %+v", got)
	}
	patch(map[string]any{"description": "Temporary description", "estimate": 2})
	patch(map[string]any{"description": "", "estimate": -1})
	if got := history(); len(got) != 0 {
		t.Fatalf("description/estimate round trip survived: %+v", got)
	}
	patch(map[string]any{"stateId": done.ID})
	requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments", map[string]any{"body": "Interrupt the burst"}, http.StatusCreated)
	patch(map[string]any{"stateId": todo.ID, "title": "Activity final", "priority": 2, "dueDate": "2026-11-17"})
	got = history()
	fields := slices.DeleteFunc(slices.Clone(got), func(event domain.ActivityEvent) bool { return event.Type != "issue.updated" })
	if len(fields) != 2 {
		t.Fatalf("comment did not split burst: %+v", got)
	}
	last := fields[1].Metadata
	for key, want := range map[string]string{"titleBefore": "Activity contract", "title": "Activity final", "priorityBefore": "Low", "priority": "High", "dueDateBefore": "", "dueDate": "2026-11-17"} {
		if value, exists := last[key]; !exists || value != want {
			t.Fatalf("%s = %q (exists %t), want %q: %+v", key, value, exists, want, last)
		}
	}
	label := data.Labels[0]
	patch(map[string]any{"labelIds": []string{label.ID}})
	page := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/issue-records/"+issue.ID+"/history", nil, http.StatusOK)
	activities := page["activities"].([]any)
	metadata := activities[len(activities)-1].(map[string]any)["metadata"].(map[string]any)
	if ids, ok := metadata["addedLabelIds"].([]any); !ok || len(ids) != 1 || ids[0] != label.ID {
		t.Fatalf("label delta was not a JSON array: %+v", metadata)
	}
	patch(map[string]any{"labelIds": []string{}})
	got = history()
	last = got[len(got)-1].Metadata
	if _, remains := last["labels"]; remains {
		t.Fatalf("label round trip remained in mixed-field burst: %+v", last)
	}
}

func TestIssueActivityRelationsAndLinks(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "relations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	data := repo.Bootstrap()
	create := func(title string) domain.Issue {
		return requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": title, "teamId": data.Teams[0].ID}, http.StatusCreated)
	}
	issue, target := create("Activity source"), create("Activity target")
	history := func(id string) []domain.ActivityEvent {
		return requestJSON[store.IssueHistory](t, handler, http.MethodGet, "/api/issue-records/"+id+"/history", nil, http.StatusOK).Activities
	}
	for _, kind := range []string{"blocks", "blocked_by", "related", "duplicate", "parent_of", "sub_issue_of"} {
		t.Run(kind, func(t *testing.T) {
			relation := requestJSON[domain.IssueRelation](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/relations", map[string]any{"type": kind, "relatedIssueId": target.ID}, http.StatusCreated)
			check := func(eventType string) {
				for _, side := range []struct{ id, related, kind, direction string }{{issue.ID, target.ID, kind, "outgoing"}, {target.ID, issue.ID, inverseRelation(kind), "inverse"}} {
					items := history(side.id)
					event := items[len(items)-1]
					if event.Type != eventType || event.Metadata["relationType"] != side.kind || event.Metadata["direction"] != side.direction || event.Metadata["relatedIssueId"] != side.related || event.Metadata["relationId"] != relation.ID {
						t.Fatalf("missing relation side: %+v", event)
					}
				}
			}
			check("issue.relation_added")
			requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+issue.ID+"/relations/"+relation.ID, nil, http.StatusNoContent)
			check("issue.relation_removed")
		})
	}
	before := len(history(issue.ID))
	for _, link := range []struct{ url, title, source string }{{"https://github.com/example/repo", "Repository", "github"}, {"https://gitlab.com/example/repo", "Review", "gitlab"}, {"https://example.test/page", "Document", "url"}} {
		attachment := requestJSON[domain.Attachment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/links", map[string]any{"title": link.title, "url": link.url}, http.StatusCreated)
		items := history(issue.ID)
		event := items[len(items)-1]
		if event.Type != "issue.link_created" || event.Metadata["linkTitle"] != link.title || event.Metadata["linkUrl"] != link.url || event.Metadata["linkSource"] != link.source {
			t.Fatalf("missing link metadata: %+v", event)
		}
		requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+issue.ID+"/attachments/"+attachment.ID, nil, http.StatusNoContent)
		items = history(issue.ID)
		if items[len(items)-1].Metadata["linkUrl"] != link.url {
			t.Fatalf("deleted link lost payload: %+v", items)
		}
	}
	if got := len(history(issue.ID)); got != before+6 {
		t.Fatalf("links merged: got %d events, want %d", got, before+6)
	}
}

func TestIssueActivityBeforeValuesAndDuplicateState(t *testing.T) {
	data := domain.Bootstrap{
		Viewer:     domain.User{ID: "creator"},
		Activities: map[string][]domain.ActivityEvent{},
		States:     []domain.WorkflowState{{ID: "duplicate", Name: "Duplicate", Type: "canceled", Reserved: true}},
	}
	issue := domain.Issue{ID: "issue", Title: "Before", State: domain.WorkflowState{ID: "todo", Name: "Todo", Type: "unstarted"}, Creator: data.Viewer, CreatedAt: time.Now().Add(-time.Hour)}
	data.Issues = []domain.Issue{issue}
	changes, err := applyUpdate(&data, &data.Issues[0], domain.IssueUpdateInput{StateID: new("duplicate"), Title: new("After"), Priority: new(2), DueDate: new("2026-11-11")})
	if err != nil {
		t.Fatal(err)
	}
	appendIssueUpdateActivity(&data, issue.ID, changes)
	event := data.Activities[issue.ID][0]
	if _, exists := event.Metadata["state"]; exists || event.Metadata["titleBefore"] != "Before" {
		t.Fatalf("duplicate state was not suppressed independently: %+v", event)
	}
	before := domain.Issue{Title: "Old", Assignee: &domain.User{ID: "old-user"}, Delegate: &domain.User{ID: "old-agent"}, Project: &domain.ProjectSummary{ID: "old-project"}, ParentID: new("old-parent"), ProjectMilestoneID: new("old-milestone"), CycleID: new("old-cycle"), Team: domain.Team{ID: "old-team"}}
	after := before
	changes = map[string]string{"assignee": "new-user", "delegate": "new-agent", "project": "new-project", "parent": "new-parent", "projectMilestone": "new-milestone", "cycle": "new-cycle", "team": "new-team"}
	addIssueActivityBeforeValues(before, after, changes)
	for field, want := range map[string]string{"assignee": "old-user", "delegate": "old-agent", "project": "old-project", "parent": "old-parent", "projectMilestone": "old-milestone", "cycle": "old-cycle", "team": "old-team"} {
		if changes[field+"Before"] != want {
			t.Fatalf("%sBefore = %q, want %q", field, changes[field+"Before"], want)
		}
	}
}
