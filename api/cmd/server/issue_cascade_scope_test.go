package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestIssueCascadeScopesMatchFullPath runs the writes that cascade into a
// bounded set of issues — project and milestone deletion, cycle start,
// completion, settings, maintenance and deletion, SLA rules, label archive
// and move-to-team, workflow status create/edit/reorder, recurring issues,
// trash restore, asks, email intake, code review and Slack webhooks, and
// project templates — once through their issue scopes and once forced
// through the full workspace path, and requires the same realtime events,
// webhook events (with previous values) and persisted workspace.
func TestIssueCascadeScopesMatchFullPath(t *testing.T) {
	fast := runIssueCascadeScript(t, false)
	full := runIssueCascadeScript(t, true)
	if len(fast.events) < 40 {
		t.Fatalf("captured %d realtime events; the sinks are not wired", len(fast.events))
	}
	compareMutationLists(t, "realtime", fast.events, full.events)
	compareMutationLists(t, "webhook", fast.webhooks, full.webhooks)
	for key, value := range full.state {
		if fast.state[key] != value {
			t.Errorf("%q differs:\nfast %.3000s\nfull %.3000s", key, fast.state[key], value)
		}
	}
	for _, check := range fast.checks {
		t.Error(check)
	}
}

type cascadeScriptResult struct {
	events, webhooks []string
	state            map[string]string
	checks           []string
}

func runIssueCascadeScript(t *testing.T, forceFull bool) cascadeScriptResult {
	t.Helper()
	restore := store.ForceFullMutationsForTesting(forceFull)
	defer restore()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	result := cascadeScriptResult{state: map[string]string{}}
	api := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	handler := newHandler(api)
	repository.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		result.events = append(result.events, event.Type+" "+mutationNanoID.ReplaceAllString(event.AggregateID, "<n>")+" "+normalizeMutationJSON(event.Payload, true))
	})
	repository.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		// Label and project issue counts are derived on read; only the full
		// path also re-persists them, so previous values may carry stale ones.
		result.webhooks = append(result.webhooks, event.Type+" "+mutationNanoID.ReplaceAllString(event.AggregateID, "<n>")+" "+normalizeMutationJSON(event.Payload, false)+" "+normalizeMutationJSON(event.PreviousValues, true))
	})
	call := func(method, path string, input any, want int) map[string]any {
		t.Helper()
		value := requestJSON[any](t, handler, method, path, input, want)
		object, _ := value.(map[string]any)
		return object
	}
	id := func(value map[string]any) string { text, _ := value["id"].(string); return text }
	check := func(ok bool, format string, args ...any) {
		if !ok {
			result.checks = append(result.checks, fmt.Sprintf(format, args...))
		}
	}
	fixture := repository.Bootstrap()
	workspace := fixture.Workspace.URLKey
	team, project := fixture.Teams[0], fixture.Projects[0]
	issueByID := func(issueID string) domain.Issue {
		return requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+issueID, nil, http.StatusOK)
	}
	// An event-triggered loop makes every mutation compute webhook previous values.
	call(http.MethodPost, "/api/loops", map[string]any{"name": "Watcher", "status": "published", "triggerType": "issue", "triggerConfig": map[string]any{"event": "created"}, "instructions": "Watch"}, http.StatusCreated)
	newIssue := func(title string, extra map[string]any) string {
		input := map[string]any{"title": title, "teamId": team.ID}
		for key, value := range extra {
			input[key] = value
		}
		return id(call(http.MethodPost, "/api/issues", input, http.StatusCreated))
	}

	// Workflow statuses: the first create materializes team-owned statuses
	// (re-pointing the team's issues), later writes touch no issue record.
	created := call(http.MethodPost, "/api/teams/"+team.ID+"/states", map[string]any{"name": "In review", "type": "started", "color": "#123456"}, http.StatusCreated)
	states := requestJSON[[]domain.WorkflowState](t, handler, http.MethodGet, "/api/teams/"+team.ID+"/states", nil, http.StatusOK)
	check(slices.ContainsFunc(states, func(state domain.WorkflowState) bool { return state.TeamID == team.ID }), "status create did not materialize team statuses")
	order := []string{}
	for _, state := range states {
		order = append(order, state.ID)
	}
	call(http.MethodPost, "/api/teams/"+team.ID+"/states/reorder", map[string]any{"stateIds": order}, http.StatusOK)
	call(http.MethodPatch, "/api/teams/"+team.ID+"/states/"+id(created), map[string]any{"type": "unstarted"}, http.StatusOK)
	call(http.MethodPost, "/api/teams/"+team.ID+"/states", map[string]any{"name": "QA", "type": "started"}, http.StatusCreated)
	doneState, startedState := "", ""
	for _, state := range requestJSON[[]domain.WorkflowState](t, handler, http.MethodGet, "/api/teams/"+team.ID+"/states", nil, http.StatusOK) {
		if state.Type == "completed" && doneState == "" {
			doneState = state.ID
		}
		if state.Type == "started" && startedState == "" {
			startedState = state.ID
		}
	}

	// Project and milestone deletion detach the project's issues.
	doomed := call(http.MethodPost, "/api/projects", map[string]any{"name": "Doomed", "teamIds": []string{team.ID}}, http.StatusCreated)
	inDoomed := newIssue("In doomed project", map[string]any{"projectId": id(doomed)})
	call(http.MethodDelete, "/api/projects/"+id(doomed), nil, http.StatusNoContent)
	check(issueByID(inDoomed).Project == nil, "project deletion left the issue in the project")
	milestone := call(http.MethodPost, "/api/projects/"+project.ID+"/milestones", map[string]any{"name": "Beta"}, http.StatusCreated)
	withMilestone := newIssue("On milestone", map[string]any{"projectId": project.ID, "projectMilestoneId": id(milestone)})
	call(http.MethodDelete, "/api/projects/"+project.ID+"/milestones/"+id(milestone), nil, http.StatusNoContent)
	check(issueByID(withMilestone).ProjectMilestoneID == nil, "milestone deletion left the issue on the milestone")

	// Cycles: enabling creates a current cycle; completing it migrates open
	// issues; maintenance and inherited settings reconcile; deletion detaches.
	call(http.MethodPatch, "/api/teams/"+team.ID+"/cycle-settings", map[string]any{"enabled": true, "autoCreate": true, "autoMigrate": true, "upcomingCount": 2}, http.StatusOK)
	cycles := func() (current domain.Cycle, upcoming []domain.Cycle) {
		data, _ := repository.WorkspaceMetadata(workspace)
		for _, cycle := range data.Cycles {
			if cycle.TeamID != team.ID {
				continue
			}
			if cycle.Status == "current" {
				current = cycle
			} else if cycle.Status == "upcoming" {
				upcoming = append(upcoming, cycle)
			}
		}
		slices.SortFunc(upcoming, func(a, b domain.Cycle) int { return a.StartsAt.Compare(b.StartsAt) })
		return current, upcoming
	}
	current, _ := cycles()
	check(current.ID != "", "enabling cycles created no current cycle")
	inCycle := newIssue("In cycle", map[string]any{"cycleId": current.ID, "stateId": startedState})
	call(http.MethodPost, "/api/cycles/"+current.ID+"/complete", nil, http.StatusOK)
	next, upcoming := cycles()
	check(issueByID(inCycle).CycleID != nil && *issueByID(inCycle).CycleID == next.ID, "completing the cycle did not migrate the open issue")
	if len(upcoming) > 0 {
		call(http.MethodPost, "/api/cycles/"+upcoming[0].ID+"/start", nil, http.StatusOK)
	}
	api.maintainCycleSchedule(context.Background(), workspace)
	call(http.MethodPatch, "/api/teams/"+team.ID+"/cycle-settings", map[string]any{"capacity": 12}, http.StatusOK)
	call(http.MethodPatch, "/api/teams/"+team.ID+"/cycle-settings", map[string]any{"enabled": false}, http.StatusOK)
	call(http.MethodDelete, "/api/teams/"+team.ID+"/cycles", nil, http.StatusOK)
	check(issueByID(inCycle).CycleID == nil, "deleting the team's cycles left the issue in a cycle")

	// SLA rules: a team rule applies to the team's issues; a priority-only
	// rule can match any issue (full path).
	rule := call(http.MethodPost, "/api/sla-rules", map[string]any{"name": "Team SLA", "teamIds": []string{team.ID}, "targetMinutes": 60}, http.StatusCreated)
	call(http.MethodPatch, "/api/sla-rules/"+id(rule), map[string]any{"targetMinutes": 120, "pauseStatuses": []string{"started"}}, http.StatusOK)
	call(http.MethodPost, "/api/sla-rules", map[string]any{"name": "Urgent", "filters": map[string]any{"priority": 1}}, http.StatusCreated)
	call(http.MethodPost, "/api/sla-rules", map[string]any{"name": "Project SLA", "filters": map[string]any{"project": project.ID}}, http.StatusCreated)

	// Labels: archive (display data only) and move to teams (re-points issues).
	label := call(http.MethodPost, "/api/labels", map[string]any{"name": "cascade-label", "color": "#ff0000"}, http.StatusCreated)
	labeled := newIssue("Labeled", map[string]any{"labelIds": []string{id(label)}})
	call(http.MethodPatch, "/api/labels/"+id(label), map[string]any{"archivedAt": "2026-01-01T00:00:00Z"}, http.StatusOK)
	call(http.MethodPatch, "/api/labels/"+id(label), map[string]any{"archivedAt": ""}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodPost, "/api/labels/"+id(label)+"/move-to-teams", nil, http.StatusOK)
	check(!slices.ContainsFunc(issueByID(labeled).Labels, func(item domain.IssueLabel) bool { return item.ID == id(label) }), "moving the label to teams left the workspace label on the issue")
	group := call(http.MethodPost, "/api/label-groups", map[string]any{"name": "Cascade group"}, http.StatusCreated)
	call(http.MethodPatch, "/api/label-groups/"+id(group), map[string]any{"archivedAt": "2026-01-01T00:00:00Z"}, http.StatusOK)

	// Recurring issues: once the due date passed the next instance is created
	// from the sequence.
	recurring := newIssue("Weekly sync", nil)
	past := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	call(http.MethodPatch, "/api/issues/"+recurring, map[string]any{"recurrence": "daily", "dueDate": past}, http.StatusOK)
	occurrences, err := api.generateWorkspaceRecurringIssues(context.Background(), workspace, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	check(len(occurrences) == 1, "recurring generation created %d issues, want 1", len(occurrences))

	// Trash: a deleted issue and document come back.
	trashed := newIssue("Trashed", nil)
	call(http.MethodDelete, "/api/issues/"+trashed, nil, http.StatusNoContent)
	document := call(http.MethodPost, "/api/documents", map[string]any{"title": "Trashed doc"}, http.StatusCreated)
	call(http.MethodDelete, "/api/documents/"+id(document), nil, http.StatusNoContent)
	data, _ := repository.WorkspaceMetadata(workspace)
	for _, entry := range data.Trash {
		if entry.ResourceID == trashed || entry.ResourceID == id(document) {
			call(http.MethodPost, "/api/trash/"+entry.ID+"/restore", nil, http.StatusOK)
		}
	}
	check(issueByID(trashed).ID == trashed, "restoring the issue did not bring it back")

	// Asks and email intake create issues.
	ask := call(http.MethodPost, "/api/asks", map[string]any{"title": "Need access", "teamId": team.ID}, http.StatusCreated)
	call(http.MethodPost, "/api/asks/"+id(ask)+"/decision", map[string]any{"decision": "approved"}, http.StatusOK)
	type intakeResponse struct {
		Address      domain.EmailIntakeAddress `json:"address"`
		InboundToken string                    `json:"inboundToken"`
		DNSRecord    map[string]string         `json:"dnsRecord"`
	}
	intake := requestJSON[intakeResponse](t, handler, http.MethodPost, "/api/teams/"+team.ID+"/email-intake-addresses", map[string]string{"localPart": "cascade", "domain": "example.test"}, http.StatusCreated)
	requestJSON[domain.EmailIntakeAddress](t, handler, http.MethodPost, "/api/teams/"+team.ID+"/email-intake-addresses/"+intake.Address.ID+"/verify", map[string]string{"txtValue": intake.DNSRecord["value"]}, http.StatusOK)
	mail := map[string]any{"messageId": "cascade-mail-1", "from": "customer@example.test", "subject": "Printer on fire", "text": "Help"}
	emailed := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/email-intake/"+intake.InboundToken+"/receive", mail, http.StatusCreated)
	again := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/email-intake/"+intake.InboundToken+"/receive", mail, http.StatusOK)
	check(emailed.ID != "" && again.ID == emailed.ID, "redelivered email created another issue")

	// Code review webhooks link issues named in the request and run PR
	// automations; Slack deliveries record a notification once.
	call(http.MethodPatch, "/api/teams/"+team.ID+"/settings", map[string]any{"prAutomations": map[string]string{"merged": doneState}}, http.StatusOK)
	requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/github?workspace=test-workspace", map[string]any{"name": "acme", "config": map[string]string{"organization": "acme", "webhookSecret": "secret"}}, http.StatusOK)
	linked := issueByID(newIssue("Linked by PR", nil))
	github := func(delivery, action string, merged bool) {
		t.Helper()
		state := "open"
		if merged {
			state = "closed"
		}
		payload := []byte(fmt.Sprintf(`{"action":%q,"number":15,"pull_request":{"id":9915,"title":"Fix %s","body":"Please review","html_url":"https://github.com/acme/store/pull/15","state":%q,"merged":%v,"user":{"login":"dependabot"},"base":{"ref":"main"},"head":{"ref":"fix-%s","sha":"abc"}},"repository":{"full_name":"acme/store"}}`, action, linked.Identifier, state, merged, linked.Identifier))
		mac := hmac.New(sha256.New, []byte("secret"))
		_, _ = mac.Write(payload)
		request := httptest.NewRequest(http.MethodPost, "/api/integrations/github/webhook?workspace=test-workspace", bytes.NewReader(payload))
		request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		request.Header.Set("X-GitHub-Delivery", delivery)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("github webhook status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	github("delivery-1", "opened", false)
	github("delivery-1", "opened", false)
	github("delivery-2", "closed", true)
	check(issueByID(linked.ID).State.ID == doneState, "merging the PR did not run the status automation")
	if reviews, _ := repository.WorkspaceMetadataFields(workspace, "reviews"); len(reviews.Reviews) > 0 {
		call(http.MethodPut, "/api/reviews/"+reviews.Reviews[0].ID+"/previews", map[string]any{"environment": "Preview", "url": "https://preview.example.test", "state": "ready"}, http.StatusOK)
	}
	requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/slack?workspace=test-workspace", map[string]any{"name": "Slack", "config": map[string]string{"signingSecret": "signing-secret"}}, http.StatusOK)
	slack := func() {
		t.Helper()
		payload := []byte(`{"type":"event_callback","event_id":"Ev-cascade","team_id":"T1","event":{"type":"message","user":"U1","text":"hello"}}`)
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte("signing-secret"))
		_, _ = mac.Write([]byte("v0:" + timestamp + ":" + string(payload)))
		request := httptest.NewRequest(http.MethodPost, "/api/integrations/slack/webhook?workspace=test-workspace", bytes.NewReader(payload))
		request.Header.Set("X-Slack-Request-Timestamp", timestamp)
		request.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("slack webhook status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	slack()
	slack()

	// Project templates validate their issues; a project created from one
	// takes them over.
	templated := newIssue("Template issue", nil)
	template := call(http.MethodPost, "/api/project-templates", map[string]any{"name": "Launch", "issueIds": []string{templated}}, http.StatusCreated)
	call(http.MethodPatch, "/api/project-templates/"+id(template), map[string]any{"name": "Launch v2", "issueIds": []string{templated, inDoomed}}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodPatch, "/api/project-templates/"+id(template), map[string]any{"issueIds": []string{"issue_missing"}}, http.StatusBadRequest)
	fromTemplate := call(http.MethodPost, "/api/projects", map[string]any{"templateId": id(template), "teamIds": []string{team.ID}}, http.StatusCreated)
	check(issueByID(templated).Project != nil && issueByID(templated).Project.ID == id(fromTemplate), "the templated project did not take over the template's issue")

	stored, ok := repository.BootstrapFor(workspace)
	if !ok {
		t.Fatal("workspace missing")
	}
	for key, value := range map[string]any{"issues": stored.Issues, "projects": stored.Projects, "cycles": stored.Cycles, "cycleSettings": stored.CycleSettings, "states": stored.States, "labels": stored.Labels, "labelGroups": stored.LabelGroups, "slaRules": stored.SLARules, "issueSlas": stored.IssueSLAs, "slaEvents": stored.SLAEvents, "trash": stored.Trash, "documents": stored.Documents, "asks": stored.Asks, "emailIntakeMessages": stored.EmailIntakeMessages, "reviews": stored.Reviews, "integrationConnections": stored.IntegrationConnections, "projectTemplates": stored.ProjectTemplates, "teamSettings": stored.TeamSettings, "issueTemplates": stored.IssueTemplates, "notifications": stored.Notifications, "deliveries": stored.NotificationDeliveries, "activities": stored.Activities, "comments": stored.Comments, "auditLog": stored.AuditLog} {
		raw, _ := json.Marshal(value)
		result.state[key] = normalizeMutationJSON(raw, false)
	}
	// Intake secrets are random per run.
	secrets := intakeSecrets{}
	for index := range result.events {
		result.events[index] = secrets.Replace(opaqueMutationID.ReplaceAllString(result.events[index], "<id>"))
	}
	for index := range result.webhooks {
		result.webhooks[index] = secrets.Replace(opaqueMutationID.ReplaceAllString(result.webhooks[index], "<id>"))
	}
	for key, value := range result.state {
		result.state[key] = secrets.Replace(opaqueMutationID.ReplaceAllString(value, "<id>"))
	}
	return result
}

// intakeSecrets masks the random email intake token hash and verification
// token (after nanosecond normalization, so match them by field).
type intakeSecrets struct{}

var intakeSecretPattern = regexp.MustCompile(`((?:inboundTokenHash|verificationToken)":")(?:[^"\\]|\\.)*|flow-verification=(?:[^"\\]|\\.)*`)

func (intakeSecrets) Replace(value string) string {
	return intakeSecretPattern.ReplaceAllString(value, "${1}<secret>")
}
