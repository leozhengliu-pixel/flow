package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestUnchangedEditComparesValues(t *testing.T) {
	now := time.Now().UTC()
	local := now.In(time.FixedZone("x", 3600))
	before := domain.Comment{ID: "c", Body: "Hi", Version: 3, CreatedAt: now, BodyData: map[string]any{"type": "doc", "content": []any{}}}
	after := before
	after.Version, after.CreatedAt, after.BodyData = 4, local, map[string]any{"content": []any{}, "type": "doc"}
	if !unchangedEdit(before, after) {
		t.Fatal("a version bump, the same instant in another zone or an equal map is a change")
	}
	after.Reactions = map[string][]string{}
	if !unchangedEdit(before, after) {
		t.Fatal("an empty map differs from a nil one")
	}
	after.Body = "Hello"
	if unchangedEdit(before, after) {
		t.Fatal("a new body is not a change")
	}
	view := domain.SavedView{Filters: []byte(`[ {"a": 1} ]`)}
	stored := domain.SavedView{Filters: []byte(`[{"a":1}]`)}
	if !unchangedEdit(stored, view) {
		t.Fatal("the same JSON with other whitespace is a change")
	}
	settings := map[string]any{"a": []any{"x"}}
	snapshot := jsonSnapshot(settings)
	settings["a"] = []any{"x"}
	if !unchangedJSON(snapshot, settings) {
		t.Fatal("the same settings again are a change")
	}
	settings["a"] = []any{"y"}
	if unchangedJSON(snapshot, settings) {
		t.Fatal("a settings map edited in place is not a change")
	}
}

// repeatCase is one PATCH or PUT the no-op test sends twice.
type repeatCase struct {
	name, method, path string
	change             any
	// versioned: the endpoint takes expectedVersion.
	versioned bool
	// status is the success status (200 when zero).
	status int
	// several: the change may write more than one event, or an event the
	// realtime channel does not carry; the repeat still writes none.
	several bool
}

// A PATCH or PUT that repeats the current values writes nothing: same version
// and updatedAt, no domain event, no realtime push. A real change still
// writes, and a stale expectedVersion is a 409 even when the values match.
func TestRepeatedPatchWritesNothing(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, authDisabled: true, uploadPath: t.TempDir()}
	handler := newHandler(s)
	// The delivery scheduler is a background writer; pause it so this test
	// only counts the events its own requests produce.
	defer s.stopDeliveryScheduler(context.Background())
	var mu sync.Mutex
	pushed := 0
	repository.SetRealtimeSink(func(workspace string, event domain.RealtimeEvent) {
		mu.Lock()
		pushed++
		mu.Unlock()
		s.publishRealtime(workspace, event)
	})
	data := repository.Bootstrap()
	team := data.Teams[0]
	workspace := data.Workspace.URLKey
	counts := func() (int, int) {
		events, err := repository.Events(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		return len(events), pushed
	}
	create := func(path string, body map[string]any) map[string]any {
		return requestJSON[map[string]any](t, handler, http.MethodPost, path, body, http.StatusCreated)
	}
	id := func(value map[string]any) string { return value["id"].(string) }
	other := ""
	for _, user := range data.Users {
		if user.ID != data.Viewer.ID && !user.App {
			other = user.ID
			break
		}
	}
	if other == "" {
		t.Fatal("the fixture has no second member")
	}

	document := create("/api/documents", map[string]any{"title": "Unchanged writes"})
	issue := create("/api/issue-records", map[string]any{"title": "Unchanged writes", "teamId": team.ID})
	legacyIssue := create("/api/issue-records", map[string]any{"title": "Unchanged legacy writes", "teamId": team.ID})
	project := create("/api/projects", map[string]any{"name": "Unchanged writes", "teamIds": []string{team.ID}})
	otherProject := create("/api/projects", map[string]any{"name": "Unchanged related", "teamIds": []string{team.ID}})
	initiative := create("/api/initiatives", map[string]any{"name": "Unchanged writes"})
	otherInitiative := create("/api/initiatives", map[string]any{"name": "Unchanged related"})
	label := create("/api/labels", map[string]any{"name": "Unchanged writes"})
	view := create("/api/views", map[string]any{"name": "Unchanged writes"})
	issueComment := create("/api/issues/"+id(issue)+"/comments", map[string]any{"body": "First"})
	documentComment := create("/api/documents/"+id(document)+"/comments", map[string]any{"body": "First"})
	projectComment := create("/api/projects/"+id(project)+"/comments", map[string]any{"body": "First"})
	initiativeComment := create("/api/initiatives/"+id(initiative)+"/comments", map[string]any{"body": "First"})
	milestone := create("/api/projects/"+id(project)+"/milestones", map[string]any{"name": "Unchanged writes"})
	customer := create("/api/customers", map[string]any{"name": "Unchanged writes"})
	upserted := create("/api/customers", map[string]any{"name": "Unchanged upsert"})
	teamLabel := create("/api/teams/"+team.ID+"/labels", map[string]any{"name": "Unchanged writes"})
	labelGroup := create("/api/label-groups", map[string]any{"name": "Unchanged writes"})
	release := create("/api/releases", map[string]any{"name": "Unchanged writes"})
	pipeline := create("/api/release-pipelines", map[string]any{"name": "Unchanged writes", "teamIds": []string{team.ID}, "type": "scheduled"})
	emoji := create("/api/custom-emojis", map[string]any{"name": ":unchanged:", "imageUrl": "data:image/png;base64,iVBORw0KGgo="})
	ask := create("/api/asks", map[string]any{"title": "Unchanged writes", "teamId": team.ID})
	projectTemplate := create("/api/project-templates", map[string]any{"name": "Unchanged writes"})
	issueTemplate := create("/api/issue-templates", map[string]any{"name": "Unchanged writes"})
	teamTemplate := create("/api/teams/"+team.ID+"/templates", map[string]any{"name": "Unchanged writes"})
	documentTemplate := create("/api/document-templates", map[string]any{"name": "Unchanged writes", "title": "Notes", "content": "Agenda"})
	webhook := create("/api/webhooks", map[string]any{"name": "Unchanged writes", "url": "https://example.com/flow", "resourceTypes": []string{"issues"}, "teamIds": []string{}, "enabled": false})
	slaRule := create("/api/sla-rules", map[string]any{"name": "Unchanged writes", "teamIds": []string{team.ID}, "targetMinutes": 60})
	draft := create("/api/drafts", map[string]any{"type": "issue", "title": "Unchanged writes", "body": "Body"})
	folder := create("/api/favorite-folders", map[string]any{"name": "Unchanged writes"})
	dashboard := create("/api/dashboards", map[string]any{"name": "Unchanged writes"})
	post := create("/api/posts", map[string]any{"title": "Unchanged writes", "body": "Ready", "teamIds": []string{team.ID}})
	meeting := create("/api/meetings", map[string]any{"title": "Unchanged writes", "startsAt": time.Now().UTC().Add(time.Hour), "durationMinutes": 30, "teamIds": []string{team.ID}})
	customerRequest := create("/api/customer-requests", map[string]any{"customerId": id(customer), "body": "Request"})
	customerStatus := create("/api/customer-statuses", map[string]any{"name": "Unchanged writes"})
	activity := create("/api/agent/activities", map[string]any{"sessionId": "session", "type": "tool"})
	conversation := create("/api/ai/conversations", map[string]any{"title": "Unchanged writes"})
	workflowInput := map[string]any{"name": "Unchanged writes", "trigger": "schedule", "schedule": "0 9 * * *", "actions": []map[string]any{{"type": "notify", "config": map[string]string{}}}, "maxAttempts": 3}
	workflow := create("/api/workflows", workflowInput)
	skill := create("/api/agent/skills", map[string]any{"name": "Unchanged writes", "instructions": "Summarize."})
	releaseNote := create("/api/releases/"+id(release)+"/notes", map[string]any{"title": "Unchanged writes"})
	section := create("/api/teams/"+team.ID+"/resource-sections", map[string]any{"name": "Unchanged writes"})
	pinned := create("/api/teams/"+team.ID+"/resources", map[string]any{"resourceType": "document", "resourceId": id(document)})
	projectResource := create("/api/projects/"+id(project)+"/resources", map[string]any{"type": "link", "title": "Runbook", "url": "https://example.com/runbook"})
	initiativeResource := create("/api/initiatives/"+id(initiative)+"/resources", map[string]any{"type": "link", "title": "Runbook", "url": "https://example.com/runbook"})
	projectUpdate := create("/api/projects/"+id(project)+"/updates", map[string]any{"body": "Unchanged writes", "health": "onTrack"})
	initiativeUpdate := create("/api/initiatives/"+id(initiative)+"/updates", map[string]any{"body": "Unchanged writes"})
	projectRelation := create("/api/projects/"+id(project)+"/relations", map[string]any{"type": "related", "relatedProjectId": id(otherProject)})
	initiativeRelation := create("/api/initiatives/"+id(initiative)+"/relations", map[string]any{"type": "related", "relatedInitiativeId": id(otherInitiative)})
	oauthApp := create("/api/oauth-applications", map[string]any{"name": "Unchanged writes"})
	attachment := create("/api/issues/"+id(issue)+"/link-attachments", map[string]any{"url": "https://example.com/spec", "title": "Spec"})
	automation := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/git-automations", map[string]any{"teamId": team.ID, "repository": "acme/api", "event": "pull_request.merged", "workflowStateId": data.States[0].ID, "enabled": true}, http.StatusOK)
	branch := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/target-branches", map[string]any{"teamId": team.ID, "repository": "acme/api", "branch": "main"}, http.StatusOK)
	state := data.States[0]
	if len(data.Cycles) == 0 {
		t.Fatal("the fixture has no cycle")
	}
	cycle := data.Cycles[0]

	cases := []repeatCase{
		{name: "document", path: "/api/documents/" + id(document), change: map[string]any{"title": "Renamed", "content": "Body", "archived": false}, versioned: true},
		{name: "issue", path: "/api/issue-records/" + id(issue), change: map[string]any{"title": "Renamed", "priority": 2, "estimate": 3, "labelIds": []string{}}, versioned: true},
		{name: "legacy issue route", path: "/api/issues/" + id(legacyIssue), change: map[string]any{"title": "Renamed", "priority": 3}, versioned: true},
		{name: "issue comment", path: "/api/issues/" + id(issue) + "/comments/" + id(issueComment), change: map[string]any{"body": "Edited"}, versioned: true},
		{name: "issue comment resolve", path: "/api/issues/" + id(issue) + "/comments/" + id(issueComment), change: map[string]any{"resolved": true}, versioned: true},
		{name: "document comment", path: "/api/documents/" + id(document) + "/comments/" + id(documentComment), change: map[string]any{"body": "Edited"}, versioned: true},
		{name: "project comment", path: "/api/projects/" + id(project) + "/comments/" + id(projectComment), change: map[string]any{"body": "Edited"}, versioned: true},
		{name: "initiative comment", path: "/api/initiatives/" + id(initiative) + "/comments/" + id(initiativeComment), change: map[string]any{"body": "Edited"}, versioned: true},
		{name: "project", path: "/api/projects/" + id(project), change: map[string]any{"name": "Renamed", "description": "Why", "priority": 2, "archived": false, "memberIds": []string{}}},
		{name: "initiative", path: "/api/initiatives/" + id(initiative), change: map[string]any{"name": "Renamed", "description": "Why", "status": "planned"}},
		{name: "label", path: "/api/labels/" + id(label), change: map[string]any{"name": "Renamed", "color": "#123456"}},
		{name: "view", path: "/api/views/" + id(view), change: map[string]any{"name": "Renamed", "filters": []any{}}},
		{name: "team", path: "/api/workspaces/" + workspace + "/teams/" + team.ID, change: map[string]any{"name": "Renamed team", "color": "#123456"}},
		{name: "milestone", path: "/api/projects/" + id(project) + "/milestones/" + id(milestone), change: map[string]any{"name": "Renamed", "description": "Why"}},
		{name: "customer", path: "/api/customers/" + id(customer), change: map[string]any{"name": "Renamed"}},
		{name: "team label", path: "/api/teams/" + team.ID + "/labels/" + id(teamLabel), change: map[string]any{"name": "Renamed", "color": "#123456"}},
		{name: "label group", path: "/api/label-groups/" + id(labelGroup), change: map[string]any{"name": "Renamed", "description": "Why"}},
		{name: "workflow state", path: "/api/teams/" + team.ID + "/states/" + state.ID, change: map[string]any{"name": "Renamed state", "type": state.Type}},
		{name: "cycle", path: "/api/cycles/" + cycle.ID, change: map[string]any{"name": "Renamed", "description": "Why"}},
		// Covered since: the remaining stored PATCH and PUT endpoints.
		{name: "release", path: "/api/releases/" + id(release), change: map[string]any{"name": "Renamed", "description": "Why", "archived": false}},
		{name: "release pipeline", path: "/api/release-pipelines/" + id(pipeline), change: map[string]any{"name": "Renamed pipeline"}},
		{name: "release note", path: "/api/releases/" + id(release) + "/notes/" + id(releaseNote), change: map[string]any{"title": "Renamed", "published": true}},
		{name: "issue releases", method: http.MethodPut, path: "/api/issues/" + id(issue) + "/releases", change: map[string]any{"releaseIds": []string{id(release)}}, several: true},
		{name: "custom emoji", path: "/api/custom-emojis/" + id(emoji), change: map[string]any{"name": "renamed-emoji"}},
		{name: "ask", path: "/api/asks/" + id(ask), change: map[string]any{"title": "Renamed"}},
		{name: "project template", path: "/api/project-templates/" + id(projectTemplate), change: map[string]any{"name": "Renamed", "description": "Why"}},
		{name: "workspace issue template", path: "/api/issue-templates/" + id(issueTemplate), change: map[string]any{"name": "Renamed"}},
		{name: "team issue template", path: "/api/teams/" + team.ID + "/templates/" + id(teamTemplate), change: map[string]any{"name": "Renamed"}},
		{name: "document template", path: "/api/document-templates/" + id(documentTemplate), change: map[string]any{"name": "Renamed"}},
		{name: "project status", path: "/api/project-statuses/" + data.ProjectStatuses[0].ID, change: map[string]any{"name": "Renamed status", "description": "Why"}, several: true},
		{name: "webhook", path: "/api/webhooks/" + id(webhook), change: map[string]any{"name": "Renamed", "enabled": false}},
		{name: "sla rule", path: "/api/sla-rules/" + id(slaRule), change: map[string]any{"name": "Renamed"}},
		{name: "sla settings", method: http.MethodPut, path: "/api/sla-settings", change: map[string]any{"enabled": true, "workWeek": "monFri"}},
		{name: "project update settings", method: http.MethodPut, path: "/api/project-update-settings", change: map[string]any{"reminders": true}},
		{name: "draft", path: "/api/drafts/" + id(draft), change: map[string]any{"title": "Renamed"}},
		{name: "favorite", method: http.MethodPut, path: "/api/favorites/project/" + id(project)},
		{name: "favorite position", path: "/api/favorites/project/" + id(project), change: map[string]any{"position": 7}},
		{name: "favorite folder", path: "/api/favorite-folders/" + id(folder), change: map[string]any{"name": "Renamed"}},
		{name: "subscription", method: http.MethodPut, path: "/api/subscriptions/initiative/" + id(otherInitiative)},
		{name: "dashboard", path: "/api/dashboards/" + id(dashboard), change: map[string]any{"name": "Renamed"}},
		{name: "dashboard subscription", method: http.MethodPut, path: "/api/dashboards/" + id(dashboard) + "/subscription"},
		{name: "post", path: "/api/posts/" + id(post), change: map[string]any{"title": "Renamed", "archived": false}},
		{name: "meeting", path: "/api/meetings/" + id(meeting), change: map[string]any{"title": "Renamed"}},
		{name: "customer request", path: "/api/customer-requests/" + id(customerRequest), change: map[string]any{"body": "Edited", "priority": 1}},
		{name: "customer upsert", method: http.MethodPut, path: "/api/customers", change: map[string]any{"id": id(upserted), "name": "Renamed upsert"}},
		{name: "customer status", path: "/api/customer-statuses/" + id(customerStatus), change: map[string]any{"name": "Renamed", "description": "Why"}},
		{name: "agent activity", path: "/api/agent/activities/" + id(activity), change: map[string]any{"status": "done", "body": "Done"}},
		{name: "ai conversation", path: "/api/ai/conversations/" + id(conversation), change: map[string]any{"title": "Renamed"}},
		{name: "workflow", path: "/api/workflows/" + id(workflow), change: map[string]any{"name": "Renamed", "trigger": "schedule", "schedule": "0 9 * * *", "actions": workflowInput["actions"], "maxAttempts": 3}},
		{name: "agent skill", path: "/api/agent/skills/" + id(skill), change: map[string]any{"name": "Renamed", "instructions": "Summarize."}},
		{name: "team resource section", path: "/api/teams/" + team.ID + "/resource-sections/" + id(section), change: map[string]any{"name": "Renamed", "position": 2}},
		{name: "team resource", path: "/api/teams/" + team.ID + "/resources/" + id(pinned), change: map[string]any{"title": "Renamed", "position": 2}},
		{name: "team settings", path: "/api/teams/" + team.ID + "/settings", change: map[string]any{"description": "Why", "detailedHistory": true}, several: true},
		{name: "cycle settings", path: "/api/teams/" + team.ID + "/cycle-settings", change: map[string]any{"upcomingCount": 3, "cooldownWeeks": 1}, several: true},
		{name: "cycle capacity", method: http.MethodPut, path: "/api/cycles/" + cycle.ID + "/capacity", change: map[string]any{"capacity": 12}},
		{name: "project resource", path: "/api/projects/" + id(project) + "/resources/" + id(projectResource), change: map[string]any{"title": "Renamed"}},
		{name: "initiative resource", path: "/api/initiatives/" + id(initiative) + "/resources/" + id(initiativeResource), change: map[string]any{"title": "Renamed"}},
		{name: "project update", path: "/api/projects/" + id(project) + "/updates/" + id(projectUpdate), change: map[string]any{"body": "Edited", "health": "atRisk"}},
		{name: "initiative update", path: "/api/initiatives/" + id(initiative) + "/updates/" + id(initiativeUpdate), change: map[string]any{"body": "Edited", "health": "atRisk"}},
		{name: "project relation", path: "/api/projects/" + id(project) + "/relations/" + id(projectRelation), change: map[string]any{"type": "blocks"}},
		{name: "initiative relation", path: "/api/initiatives/" + id(initiative) + "/relations/" + id(initiativeRelation), change: map[string]any{"type": "blocks", "sortOrder": 2}},
		{name: "issue thread subscription", method: http.MethodPut, path: "/api/issues/" + id(issue) + "/comments/" + id(issueComment) + "/subscription", change: map[string]any{"state": "muted"}},
		{name: "document thread subscription", method: http.MethodPut, path: "/api/documents/" + id(document) + "/comments/" + id(documentComment) + "/subscription", change: map[string]any{"state": "muted"}},
		{name: "project thread subscription", method: http.MethodPut, path: "/api/projects/" + id(project) + "/comments/" + id(projectComment) + "/subscription", change: map[string]any{"state": "muted"}},
		{name: "initiative thread subscription", method: http.MethodPut, path: "/api/initiatives/" + id(initiative) + "/comments/" + id(initiativeComment) + "/subscription", change: map[string]any{"state": "muted"}},
		{name: "document permissions", method: http.MethodPut, path: "/api/documents/" + id(document) + "/permissions", change: map[string]any{"permissions": []map[string]any{{"subjectType": "user", "subjectId": other, "role": "editor"}}}, several: true},
		{name: "link attachment", path: "/api/issues/" + id(issue) + "/attachments/" + id(attachment), change: map[string]any{"title": "Renamed spec"}, several: true},
		{name: "oauth application", path: "/api/oauth-applications/" + id(oauthApp), change: map[string]any{"name": "Renamed", "description": "Why"}},
		{name: "application policy", method: http.MethodPut, path: "/api/application-policies", change: map[string]any{"id": "policy_unchanged", "name": "Unchanged writes", "kind": "oauth", "status": "approved"}},
		{name: "git automation", method: http.MethodPut, path: "/api/git-automations/" + id(automation), change: map[string]any{"id": id(automation), "teamId": team.ID, "repository": "acme/api", "event": "pull_request.merged", "workflowStateId": data.States[0].ID, "enabled": false}},
		{name: "target branch", method: http.MethodPut, path: "/api/target-branches/" + id(branch), change: map[string]any{"id": id(branch), "teamId": team.ID, "repository": "acme/api", "branch": "release", "default": true}},
		{name: "notification preferences", path: "/api/notification-preferences", change: map[string]any{"emailFormat": "digest"}},
		{name: "account settings", path: "/api/account/settings", change: map[string]any{"pulseSchedule": "weekly"}},
		{name: "account profile", path: "/api/account/profile", change: map[string]any{"displayName": data.Viewer.DisplayName, "username": "renamed.viewer", "jobTitle": "Engineer"}},
		{name: "workspace preferences", path: "/api/workspace/preferences", change: map[string]any{"welcomeMessage": "Hello", "welcomeMessageEnabled": true}},
		{name: "workspace settings", method: http.MethodPut, path: "/api/workspace/settings", change: map[string]any{"theme": "calm"}},
		{name: "workspace agent guidance", path: "/api/workspace/agent-guidance", change: map[string]any{"instructions": "Be brief."}},
		{name: "project display default", method: http.MethodPut, path: "/api/workspace/project-display-default", change: map[string]any{"display": map[string]any{"layout": "board"}}},
		{name: "workspace", path: "/api/workspaces/" + workspace, change: map[string]any{"name": "Renamed workspace"}, several: true},
		{name: "member", path: "/api/workspaces/" + workspace + "/members/" + other, change: map[string]any{"role": "admin"}},
		{name: "team member", method: http.MethodPut, path: "/api/workspaces/" + workspace + "/teams/" + team.ID + "/members/" + other, change: map[string]any{"member": true, "role": "owner"}, status: http.StatusNoContent},
	}
	if index := slices.IndexFunc(data.Notifications, func(item domain.Notification) bool { return item.RecipientID == data.Viewer.ID }); index >= 0 {
		read := data.Notifications[index].ReadAt == nil
		cases = append(cases, repeatCase{name: "notification", path: "/api/notifications/" + data.Notifications[index].ID, change: map[string]any{"read": read}})
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			method, status := item.method, item.status
			if method == "" {
				method = http.MethodPatch
			}
			if status == 0 {
				status = http.StatusOK
			}
			events, pushes := counts()
			changed := requestJSON[any](t, handler, method, item.path, item.change, status)
			afterEvents, afterPushes := counts()
			if item.several && afterEvents == events || !item.several && (afterEvents != events+1 || afterPushes != pushes+1) {
				all, _ := repository.Events(context.Background(), "")
				for _, event := range all[events:] {
					t.Logf("event %s %s", event.Type, event.Payload)
				}
				t.Fatalf("a change wrote %d events and %d pushes, want 1 each", afterEvents-events, afterPushes-pushes)
			}
			repeated := requestJSON[any](t, handler, method, item.path, item.change, status)
			if finalEvents, finalPushes := counts(); finalEvents != afterEvents || finalPushes != afterPushes {
				all, _ := repository.Events(context.Background(), "")
				for _, event := range all[afterEvents:] {
					t.Logf("event %s %s", event.Type, event.Payload)
				}
				t.Fatalf("a repeated %s wrote %d events and %d pushes", method, finalEvents-afterEvents, finalPushes-afterPushes)
			}
			changedRecord, record := changed.(map[string]any)
			repeatedRecord, _ := repeated.(map[string]any)
			if record {
				for _, field := range []string{"version", "updatedAt", "editedAt"} {
					if repeatedRecord[field] != changedRecord[field] {
						t.Fatalf("a repeated %s moved %s %v -> %v", method, field, changedRecord[field], repeatedRecord[field])
					}
				}
			} else if left, right := jsonSnapshot(changed), jsonSnapshot(repeated); string(left) != string(right) {
				t.Fatalf("a repeated %s answered %s, the change %s", method, right, left)
			}
			if !item.versioned {
				return
			}
			version := changedRecord["version"].(float64)
			body := func(expected float64) map[string]any {
				value := map[string]any{"expectedVersion": expected}
				for key, field := range item.change.(map[string]any) {
					value[key] = field
				}
				return value
			}
			requestJSON[map[string]any](t, handler, method, item.path, body(version-1), http.StatusConflict)
			if same := requestJSON[map[string]any](t, handler, method, item.path, body(version), http.StatusOK); same["version"] != changedRecord["version"] {
				t.Fatalf("a matching expectedVersion moved the version to %v", same["version"])
			}
		})
	}
}

// Every PATCH and PUT route either writes nothing when it repeats the current
// values or is excluded here with the reason; a new route must join one list.
func TestEveryPatchAndPutRouteIsClassified(t *testing.T) {
	excluded := map[string]string{
		"PATCH /api/agent/sessions/{id}/messages/{messageId}":        "editing a message runs the agent reply again",
		"PATCH /api/agent/sessions/{id}/messages/{messageId}/stream": "appends streamed agent output",
		"PATCH /api/application-installations/{id}":                  "installing again issues a new webhook secret",
		"PATCH /api/loops/{id}":                                      "loop edits record loop versions and output contracts; left to the loop contract work",
		"PATCH /api/workspace/loop-settings":                         "left to the loop contract work",
		"PATCH /scim/v2/{workspace}/Groups/{id}":                     "SCIM: identity tables only, no domain event; already audits only changed attributes",
		"PATCH /scim/v2/{workspace}/Users/{id}":                      "SCIM: identity tables only, no domain event; already audits only changed attributes",
		"PUT /api/application-policies/{id}/headers":                 "write-only encrypted credential; each save renews its expiry",
		"PUT /api/files/uploads/{token}":                             "uploads the bytes behind a single-use upload token",
		"PUT /api/integrations/{provider}":                           "connecting runs the provider's credential check",
		"PUT /api/integrations/{provider}/chat":                      "saving runs the provider's tenant check and stores the credentials again",
		"PUT /api/mcp/uploads/{token}":                               "uploads the bytes behind a single-use upload token",
		"PUT /internal/agent-runtime/runs/{id}/state":                "internal worker protocol: run state",
		"PUT /internal/coding-runtime/harness-state":                 "internal worker protocol: runtime state",
		"PUT /internal/coding-runtime/snapshots/{id}/archive":        "internal worker protocol: snapshot archive upload",
		"PUT /scim/v2/{workspace}/Groups/{id}":                       "SCIM: identity tables only, no domain event; already audits only changed attributes",
		"PUT /scim/v2/{workspace}/Users/{id}":                        "SCIM: identity tables only, no domain event; already audits only changed attributes",
	}
	covered := []string{
		"PATCH /api/account/passkeys/{id}",
		"PATCH /api/account/profile",
		"PATCH /api/account/settings",
		"PATCH /api/agent-tasks/{id}",
		"PATCH /api/agent/activities/{id}",
		"PATCH /api/agent/sessions/{id}",
		"PATCH /api/agent/skills/{id}",
		"PATCH /api/ai/conversations/{id}",
		"PATCH /api/api-keys/{id}",
		"PATCH /api/asks/{id}",
		"PATCH /api/coding/environments/{id}",
		"PATCH /api/coding/settings",
		"PATCH /api/custom-emojis/{id}",
		"PATCH /api/customer-requests/{id}",
		"PATCH /api/customer-statuses/{id}",
		"PATCH /api/customer-tiers/{id}",
		"PATCH /api/customers/{id}",
		"PATCH /api/cycles/{id}",
		"PATCH /api/dashboards/{id}",
		"PATCH /api/document-templates/{id}",
		"PATCH /api/documents/{id}",
		"PATCH /api/documents/{id}/comments/{commentId}",
		"PATCH /api/documents/{id}/drafts/{draftId}",
		"PATCH /api/documents/{id}/permissions/{permissionId}",
		"PATCH /api/drafts/{id}",
		"PATCH /api/favorite-folders/{id}",
		"PATCH /api/favorites/{type}/{id}",
		"PATCH /api/identity-providers/{id}",
		"PATCH /api/initiatives/{id}",
		"PATCH /api/initiatives/{id}/comments/{commentId}",
		"PATCH /api/initiatives/{id}/relations/{relationId}",
		"PATCH /api/initiatives/{id}/resources/{resourceId}",
		"PATCH /api/initiatives/{id}/updates/{updateId}",
		"PATCH /api/integrations/{provider}/{id}",
		"PATCH /api/issue-records/{id}",
		"PATCH /api/issue-records/{id}/attachments/{attachmentId}",
		"PATCH /api/issue-records/{id}/comments/{commentId}",
		"PATCH /api/issue-records/{id}/permissions/{permissionId}",
		"PATCH /api/issue-templates/{id}",
		"PATCH /api/issues/{id}",
		"PATCH /api/issues/{id}/attachments/{attachmentId}",
		"PATCH /api/issues/{id}/comments/{commentId}",
		"PATCH /api/issues/{id}/permissions/{permissionId}",
		"PATCH /api/jira/links/{id}",
		"PATCH /api/label-groups/{id}",
		"PATCH /api/labels/{id}",
		"PATCH /api/meetings/{id}",
		"PATCH /api/migrations/{id}/mappings",
		"PATCH /api/notification-preferences",
		"PATCH /api/notifications/{id}",
		"PATCH /api/oauth-applications/{id}",
		"PATCH /api/posts/{id}",
		"PATCH /api/project-statuses/{id}",
		"PATCH /api/project-templates/{id}",
		"PATCH /api/projects/{id}",
		"PATCH /api/projects/{id}/comments/{commentId}",
		"PATCH /api/projects/{id}/milestones/{milestoneId}",
		"PATCH /api/projects/{id}/relations/{relationId}",
		"PATCH /api/projects/{id}/resources/{resourceId}",
		"PATCH /api/projects/{id}/updates/{updateId}",
		"PATCH /api/release-pipelines/{id}",
		"PATCH /api/releases/{id}",
		"PATCH /api/releases/{id}/notes/{noteId}",
		"PATCH /api/reviews/import",
		"PATCH /api/reviews/{id}",
		"PATCH /api/reviews/{id}/comments/{commentId}",
		"PATCH /api/sla-rules/{id}",
		"PATCH /api/teams/{id}/cycle-settings",
		"PATCH /api/teams/{id}/email-intake-addresses/{addressId}",
		"PATCH /api/teams/{id}/labels/{labelId}",
		"PATCH /api/teams/{id}/resource-sections/{sectionId}",
		"PATCH /api/teams/{id}/resources/{resourceId}",
		"PATCH /api/teams/{id}/settings",
		"PATCH /api/teams/{id}/states/{stateId}",
		"PATCH /api/teams/{id}/templates/{templateId}",
		"PATCH /api/teams/{id}/triage-responsibilities/{responsibilityId}",
		"PATCH /api/teams/{id}/triage-rules/{ruleId}",
		"PATCH /api/views/{id}",
		"PATCH /api/webhooks/{id}",
		"PATCH /api/workflows/{id}",
		"PATCH /api/workspace/agent-guidance",
		"PATCH /api/workspace/audit-log-stream",
		"PATCH /api/workspace/preferences",
		"PATCH /api/workspaces/{workspaceKey}",
		"PATCH /api/workspaces/{workspaceKey}/members/{userId}",
		"PATCH /api/workspaces/{workspaceKey}/teams/{teamId}",
		"PUT /api/account/last-workspace",
		"PUT /api/agent/sessions/{id}/messages/{messageId}/feedback",
		"PUT /api/application-policies",
		"PUT /api/customers",
		"PUT /api/cycles/{id}/capacity",
		"PUT /api/dashboards/{id}/subscription",
		"PUT /api/documents/{id}/comments/{commentId}/subscription",
		"PUT /api/documents/{id}/permissions",
		"PUT /api/favorites/{type}/{id}",
		"PUT /api/git-automations/{id}",
		"PUT /api/initiatives/{id}/comments/{commentId}/subscription",
		"PUT /api/integrations/gitlab/{id}/oauth-application",
		"PUT /api/integrations/gitlab/{id}/token/rotation",
		"PUT /api/issue-records/{id}/comments/{commentId}/subscription",
		"PUT /api/issue-records/{id}/permissions",
		"PUT /api/issue-records/{id}/releases",
		"PUT /api/issues/{id}/comments/{commentId}/subscription",
		"PUT /api/issues/{id}/permissions",
		"PUT /api/issues/{id}/releases",
		"PUT /api/oauth-applications/{id}/manifest",
		"PUT /api/oauth-applications/{id}/webhook",
		"PUT /api/project-update-settings",
		"PUT /api/projects/{id}/comments/{commentId}/subscription",
		"PUT /api/pulse/subscriptions/initiative/{id}/project-updates",
		"PUT /api/pulse/subscriptions/{type}/{id}",
		"PUT /api/reviews/{id}/previews",
		"PUT /api/sla-settings",
		"PUT /api/subscriptions/{type}/{id}",
		"PUT /api/target-branches/{id}",
		"PUT /api/teams/{id}/default-favorites",
		"PUT /api/teams/{id}/members/{userId}",
		"PUT /api/workspace/project-display-default",
		"PUT /api/workspace/settings",
		"PUT /api/workspaces/{workspaceKey}/teams/{teamId}/members/{userId}",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	route := regexp.MustCompile(`"((?:PATCH|PUT) /[^"]*)"`)
	registered := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range route.FindAllStringSubmatch(string(raw), -1) {
			registered[match[1]] = true
		}
	}
	for name := range registered {
		if _, skip := excluded[name]; !skip && !slices.Contains(covered, name) {
			t.Errorf("%s is neither covered by the no-op rule nor excluded with a reason", name)
		}
	}
	for _, name := range covered {
		if !registered[name] {
			t.Errorf("%s is listed but not registered", name)
		}
	}
	for name := range excluded {
		if !registered[name] {
			t.Errorf("%s is excluded but not registered", name)
		}
	}
}
