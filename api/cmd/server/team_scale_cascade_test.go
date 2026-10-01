package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleTimer func(label, method, path string, input any, want ...int) map[string]any

// runIssueCascadeScaleRoutes times the writes that cascade into a bounded set
// of issues (see TestIssueCascadeScopesMatchFullPath) against the scale
// fixture: project/milestone deletion, cycles, SLA rules, label archive and
// move, workflow statuses, recurring issues, trash restore, asks, email
// intake, code review and Slack webhooks, and project templates.
func runIssueCascadeScaleRoutes(t *testing.T, srv *server, repository *store.SQLiteStore, timed scaleTimer, repeat int, suffix string) {
	t.Helper()
	id := func(value map[string]any) string { text, _ := value["id"].(string); return text }
	const team = "team_scale_5"
	issue := func(n int) string { return "scale_" + strconv.Itoa(n) }
	for i := 0; i < repeat; i++ {
		n := strconv.Itoa(i)
		project := timed("project create", http.MethodPost, "/api/projects", map[string]any{"name": "Scale doomed " + suffix + n, "teamIds": []string{"team_test"}})
		for j := 0; j < 5; j++ {
			timed("issue-record project", http.MethodPatch, "/api/issue-records/"+issue(100+i*5+j), map[string]any{"projectId": id(project)})
		}
		timed("project delete (5 issues)", http.MethodDelete, "/api/projects/"+id(project), nil, http.StatusNoContent)
		milestone := timed("milestone create", http.MethodPost, "/api/projects/project_aut/milestones", map[string]any{"name": "Scale milestone " + n})
		timed("milestone delete", http.MethodDelete, "/api/projects/project_aut/milestones/"+id(milestone), nil, http.StatusNoContent)
	}

	// Cycles on a mid-sized team.
	timed("cycle settings", http.MethodPatch, "/api/teams/"+team+"/cycle-settings", map[string]any{"enabled": true, "autoCreate": true, "autoMigrate": true, "upcomingCount": 2})
	cycles := func() (current string, upcoming string) {
		data, _ := repository.WorkspaceMetadataFields("test-workspace", "cycles")
		var earliest time.Time
		seen := map[string]int{}
		for _, cycle := range data.Cycles {
			seen[cycle.ID]++
		}
		for _, cycle := range data.Cycles {
			// Cycles created in one tight loop can share a nanosecond id.
			if cycle.TeamID != team || seen[cycle.ID] > 1 {
				continue
			}
			if cycle.Status == "current" {
				current = cycle.ID
			} else if cycle.Status == "upcoming" && (upcoming == "" || cycle.StartsAt.Before(earliest)) {
				upcoming, earliest = cycle.ID, cycle.StartsAt
			}
		}
		return current, upcoming
	}
	for i := 0; i < repeat; i++ {
		timed("cycle settings", http.MethodPatch, "/api/teams/"+team+"/cycle-settings", map[string]any{"capacity": 5 + i})
		if current, _ := cycles(); current != "" {
			timed("cycle complete", http.MethodPost, "/api/cycles/"+current+"/complete", nil)
		}
		if _, upcoming := cycles(); upcoming != "" {
			timed("cycle start", http.MethodPost, "/api/cycles/"+upcoming+"/start", nil)
		}
	}

	// SLA rules scoped to a team.
	for i := 0; i < repeat; i++ {
		rule := timed("SLA rule create", http.MethodPost, "/api/sla-rules", map[string]any{"name": "Scale SLA " + strconv.Itoa(i), "teamIds": []string{team}, "targetMinutes": 60})
		timed("SLA rule update", http.MethodPatch, "/api/sla-rules/"+id(rule), map[string]any{"targetMinutes": 120})
	}

	// Labels: archive/unarchive and move to teams.
	for i := 0; i < repeat; i++ {
		n := strconv.Itoa(i)
		label := timed("label create", http.MethodPost, "/api/labels", map[string]any{"name": "scale-cascade-" + suffix + n, "color": "#00aa00"})
		timed("label archive", http.MethodPatch, "/api/labels/"+id(label), map[string]any{"archivedAt": "2026-01-01T00:00:00Z"})
		timed("label unarchive", http.MethodPatch, "/api/labels/"+id(label), map[string]any{"archivedAt": ""})
		for j := 0; j < 3; j++ {
			timed("issue-record labels", http.MethodPatch, "/api/issue-records/"+issue(200+i*3+j), map[string]any{"labelIds": []string{id(label)}})
		}
		timed("label move to teams", http.MethodPost, "/api/labels/"+id(label)+"/move-to-teams", nil)
		group := timed("label group create", http.MethodPost, "/api/label-groups", map[string]any{"name": "Scale cascade group " + suffix + n})
		timed("label group archive", http.MethodPatch, "/api/label-groups/"+id(group), map[string]any{"archivedAt": "2026-01-01T00:00:00Z"})
	}

	// Workflow statuses on a team with its own statuses.
	for i := 0; i < repeat; i++ {
		state := timed("workflow state create", http.MethodPost, "/api/teams/"+team+"/states", map[string]any{"name": "Scale review " + strconv.Itoa(i), "type": "started"})
		states := []string{}
		data, _ := repository.WorkspaceMetadataFields("test-workspace", "states")
		order := map[string]float64{}
		for _, item := range data.States {
			if item.TeamID == team {
				states = append(states, item.ID)
				order[item.ID] = workflowStateSortKey(item)
			}
		}
		sortByKey(states, order)
		timed("workflow state reorder", http.MethodPost, "/api/teams/"+team+"/states/reorder", map[string]any{"stateIds": states})
		timed("workflow state delete", http.MethodDelete, "/api/teams/"+team+"/states/"+id(state), nil, http.StatusNoContent)
	}

	// Recurring issues (the scheduler's per-series write).
	for i := 0; i < repeat; i++ {
		source := issue(300 + i)
		timed("issue-record recurrence", http.MethodPatch, "/api/issue-records/"+source, map[string]any{"recurrence": "daily", "nextOccurrenceAt": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)})
		begin := time.Now()
		if _, err := srv.createRecurringOccurrence(context.Background(), "test-workspace", source, time.Now().UTC()); err != nil {
			t.Errorf("recurring occurrence: %v", err)
		}
		timedSample("recurring occurrence", time.Since(begin))
	}

	// Trash restore.
	for i := 0; i < repeat; i++ {
		document := timed("document create", http.MethodPost, "/api/documents", map[string]any{"title": "Scale trashed " + strconv.Itoa(i)})
		timed("document delete", http.MethodDelete, "/api/documents/"+id(document), nil, http.StatusNoContent, http.StatusOK)
		created := timed("issue-record create", http.MethodPost, "/api/issue-records", map[string]any{"title": "Scale trashed issue", "teamId": team})
		timed("issue delete", http.MethodDelete, "/api/issue-records/"+id(created), nil, http.StatusNoContent, http.StatusOK)
		data, _ := repository.WorkspaceMetadataFields("test-workspace", "trash")
		for _, entry := range data.Trash {
			switch entry.ResourceID {
			case id(document):
				timed("trash restore (document)", http.MethodPost, "/api/trash/"+entry.ID+"/restore", nil)
			case id(created):
				timed("trash restore (issue)", http.MethodPost, "/api/trash/"+entry.ID+"/restore", nil)
			}
		}
	}

	// Asks and email intake.
	for i := 0; i < repeat; i++ {
		ask := timed("ask create", http.MethodPost, "/api/asks", map[string]any{"title": "Scale ask " + strconv.Itoa(i), "teamId": team})
		timed("ask approve", http.MethodPost, "/api/asks/"+id(ask)+"/decision", map[string]any{"decision": "approved"})
	}
	intake := timed("email intake create", http.MethodPost, "/api/teams/"+team+"/email-intake-addresses", map[string]any{"localPart": "scale" + suffix, "domain": "example.test"}, http.StatusCreated)
	address, _ := intake["address"].(map[string]any)
	record, _ := intake["dnsRecord"].(map[string]any)
	token, _ := intake["inboundToken"].(string)
	// Signed-in verification resolves DNS; mark the address verified directly.
	_ = record
	if err := repository.MutateWorkspace(context.Background(), "test-workspace", "test.intake_verified", id(address), nil, func(data *domain.Bootstrap) error {
		for index := range data.EmailIntakeAddresses {
			if data.EmailIntakeAddresses[index].ID == id(address) {
				data.EmailIntakeAddresses[index].VerificationState = "verified"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < repeat; i++ {
		timed("email intake receive", http.MethodPost, "/api/email-intake/"+token+"/receive", map[string]any{"messageId": "scale-" + suffix + strconv.Itoa(i), "from": "customer@example.test", "subject": "Scale email " + strconv.Itoa(i), "text": "Help"}, http.StatusCreated)
	}

	// Code review and Slack webhooks.
	timed("github connect", http.MethodPut, "/api/integrations/github?workspace=test-workspace", map[string]any{"name": "acme", "config": map[string]string{"organization": "acme", "webhookSecret": "secret"}})
	timed("slack connect", http.MethodPut, "/api/integrations/slack?workspace=test-workspace", map[string]any{"name": "Slack", "config": map[string]string{"signingSecret": "signing-secret"}})
	handler := newHandler(srv)
	send := func(label string, request *http.Request) {
		begin := time.Now()
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		timedSample(label, time.Since(begin))
		if recorder.Code != http.StatusAccepted {
			t.Errorf("%s: status %d: %.300s", label, recorder.Code, recorder.Body.String())
		}
	}
	for i := 0; i < repeat; i++ {
		payload := []byte(fmt.Sprintf(`{"action":"opened","number":%d,"pull_request":{"id":%d,"title":"Fix SCL-%d","body":"Scale","html_url":"https://github.com/acme/store/pull/%d","state":"open","user":{"login":"dependabot"},"base":{"ref":"main"},"head":{"ref":"fix-%d","sha":"abc"}},"repository":{"full_name":"acme/store"}}`, 7000+i, 97000+i, 400+i, 7000+i, i))
		mac := hmac.New(sha256.New, []byte("secret"))
		_, _ = mac.Write(payload)
		request := httptest.NewRequest(http.MethodPost, "/api/integrations/github/webhook?workspace=test-workspace", bytes.NewReader(payload))
		request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		request.Header.Set("X-GitHub-Delivery", "scale-delivery-"+suffix+strconv.Itoa(i))
		send("code review webhook", request)

		body := []byte(fmt.Sprintf(`{"type":"event_callback","event_id":"Ev-scale-%s-%d","team_id":"T1","event":{"type":"message","user":"U1","text":"hello"}}`, suffix, i))
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		slackMAC := hmac.New(sha256.New, []byte("signing-secret"))
		_, _ = slackMAC.Write([]byte("v0:" + timestamp + ":" + string(body)))
		slack := httptest.NewRequest(http.MethodPost, "/api/integrations/slack/webhook?workspace=test-workspace", bytes.NewReader(body))
		slack.Header.Set("X-Slack-Request-Timestamp", timestamp)
		slack.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(slackMAC.Sum(nil)))
		send("slack webhook", slack)
	}

	// Project templates.
	for i := 0; i < repeat; i++ {
		n := strconv.Itoa(i)
		template := timed("project template create", http.MethodPost, "/api/project-templates", map[string]any{"name": "Scale template " + suffix + n, "issueIds": []string{issue(500 + i)}})
		timed("project template update", http.MethodPatch, "/api/project-templates/"+id(template), map[string]any{"name": "Scale template v2 " + suffix + n})
		timed("project from template", http.MethodPost, "/api/projects", map[string]any{"templateId": id(template), "name": "Scale templated " + suffix + n, "teamIds": []string{"team_test"}})
	}
}

var scaleExtraSamples = map[string][]time.Duration{}

func timedSample(label string, elapsed time.Duration) {
	scaleExtraSamples[label] = append(scaleExtraSamples[label], elapsed)
}

func workflowStateSortKey(state domain.WorkflowState) float64 {
	return float64(workflowStateRank(state))*1e6 + state.Position
}

func sortByKey(values []string, keys map[string]float64) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && keys[values[j]] < keys[values[j-1]]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// runPulseScaleRoutes times the pulse summary scheduler (one metadata write
// per due user) in a workspace with a live event-triggered loop, which makes
// every write compute previous values and run the post-write loop check.
func runPulseScaleRoutes(t *testing.T, srv *server, repository *store.SQLiteStore, timed scaleTimer) {
	t.Helper()
	const workspace = "test-workspace"
	today := time.Now().UTC()
	now := time.Date(today.Year(), today.Month(), today.Day(), 10, 0, 0, 0, time.UTC)
	timed("loop create (issue trigger)", http.MethodPost, "/api/loops", map[string]any{"name": "Scale issue loop", "instructions": "Summarize", "triggerType": "issue"})
	// A metadata-only event with a field scope keeps this setup off the full
	// (every issue and content record) write path.
	setup := store.WithMetadataFields(context.Background(), "workspaceSettings", "loops", "projects", "projectUpdates")
	if err := repository.MutateWorkspace(setup, workspace, "workspace.agent_guidance_updated", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule = "daily"
		if data.WorkspaceSettings.FeatureFlags == nil {
			data.WorkspaceSettings.FeatureFlags = map[string]bool{}
		}
		data.WorkspaceSettings.FeatureFlags["pulse"] = true
		for index := range data.Loops {
			if data.Loops[index].TriggerType == "issue" {
				data.Loops[index].Enabled, data.Loops[index].Status = true, "published"
			}
		}
		if len(data.Projects) > 0 {
			if data.ProjectUpdates == nil {
				data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
			}
			project := data.Projects[0]
			data.ProjectUpdates[project.ID] = append(data.ProjectUpdates[project.ID], domain.ProjectUpdate{ID: "pulse-scale-update", ProjectID: project.ID, Body: "Scale update", User: data.Viewer, CreatedAt: now.Add(-2 * time.Hour)})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		begin := time.Now()
		_, _ = repository.WorkspaceMetadata(workspace)
		timedSample("metadata snapshot clone", time.Since(begin))
	}
	for i := 0; i < 4; i++ {
		begin := time.Now()
		if err := srv.preparePulseSummaries(context.Background(), workspace, now); err != nil {
			t.Errorf("pulse summaries: %v", err)
		}
		timedSample("pulse tick (100 users)", time.Since(begin))
	}
}
