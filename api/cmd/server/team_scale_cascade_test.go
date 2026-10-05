package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
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
		timed("issue-record recurrence", http.MethodPatch, "/api/issue-records/"+source, map[string]any{"recurrence": "daily", "dueDate": time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")})
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
			data.ProjectUpdates[project.ID] = append(data.ProjectUpdates[project.ID], domain.ProjectUpdate{ID: "pulse-scale-update", ProjectID: project.ID, Body: "Scale update", User: domain.User{ID: "usr_scale_author", Name: "Scale author"}, CreatedAt: now.Add(-5 * time.Hour)})
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
	users, _ := repository.WorkspaceMetadataFields(workspace, "users")
	label := fmt.Sprintf("pulse tick (%d users)", len(users.Users))
	for i := 0; i < 4; i++ {
		// Every user is due again: clear the delivery cursors (a reused
		// fixture keeps them from the previous run).
		reset := store.WithMetadataFields(context.Background(), "settings")
		if err := repository.MutateWorkspace(reset, workspace, "workspace.agent_guidance_updated", "", nil, func(data *domain.Bootstrap) error {
			delete(data.Settings, "pulseDeliveryCursors")
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		begin := time.Now()
		if err := srv.preparePulseSummaries(context.Background(), workspace, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Errorf("pulse summaries: %v", err)
		}
		timedSample(label, time.Since(begin))
	}
	after, _ := repository.WorkspaceMetadataFields(workspace, "settings")
	if cursors := pulseCursors(&after); len(cursors) < len(users.Users) {
		t.Errorf("pulse tick advanced %d of %d users", len(cursors), len(users.Users))
	}
}

// seedPulseScaleFixture adds a long-lived tenant's Pulse history to the scale
// fixture: FLOW_SCALE_PROJECTS projects (default 300) sharing
// FLOW_SCALE_PROJECT_UPDATES updates (default 4800) and FLOW_SCALE_INITIATIVES
// initiatives (default 50) sharing FLOW_SCALE_INITIATIVE_UPDATES updates
// (default 1000), each update with a snapshot, two comments and reactions.
// A fixture that already holds them is left alone.
func seedPulseScaleFixture(t *testing.T, db scaleDB) {
	t.Helper()
	const workspace = "test-workspace"
	projectCount := scaleEnvInt("FLOW_SCALE_PROJECTS", 300)
	projectUpdates := scaleEnvInt("FLOW_SCALE_PROJECT_UPDATES", 4800)
	initiativeCount := scaleEnvInt("FLOW_SCALE_INITIATIVES", 50)
	initiativeUpdates := scaleEnvInt("FLOW_SCALE_INITIATIVE_UPDATES", 1000)
	var have int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key LIKE 'pulse_scale_project_%'`, workspace).Scan(&have); err != nil {
		t.Fatal(err)
	}
	if have >= projectCount {
		return
	}
	started := time.Now()
	var rootRaw []byte
	if err := db.QueryRow(`SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&rootRaw); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(rootRaw, &root); err != nil {
		t.Fatal(err)
	}
	shapes := map[string]string{}
	_ = json.Unmarshal(root["_flowCollections"], &shapes)
	for field, shape := range map[string]string{"projects": "array", "initiatives": "array", "projectUpdates": "updates", "initiativeUpdates": "updates"} {
		if shapes[field] == "" {
			shapes[field] = shape
		}
		if shapes[field] != shape {
			t.Fatalf("%s is stored as %q", field, shapes[field])
		}
	}
	orders := map[string]int64{}
	for _, field := range []string{"projects", "initiatives"} {
		var highest sql.NullInt64
		if err := db.QueryRow(`SELECT MAX(collection_order) FROM workspace_metadata_records WHERE workspace_key=? AND field=?`, workspace, field).Scan(&highest); err != nil {
			t.Fatal(err)
		}
		orders[field] = highest.Int64 + 1
	}
	var templateRaw []byte
	if err := db.QueryRow(`SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' ORDER BY collection_order LIMIT 1`, workspace).Scan(&templateRaw); err != nil {
		t.Fatal(err)
	}
	var template domain.Project
	if err := json.Unmarshal(templateRaw, &template); err != nil {
		t.Fatal(err)
	}
	records := &scaleInserter{t: t, db: db, prefix: `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data)`, columns: 5}
	put := func(field, key string, order int64, value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		records.add(workspace, field, key, order, raw)
	}
	type updateRecord struct {
		Parent string `json:"parent"`
		Item   any    `json:"item,omitempty"`
	}
	hashKey := func(parent, id string) string {
		key, _ := json.Marshal([]string{parent, id})
		return fmt.Sprintf("%x", sha256.Sum256(key))
	}
	parentKey := func(parent string) string {
		return fmt.Sprintf("%x", sha256.Sum256([]byte("parent:"+parent)))
	}
	now := time.Now().UTC()
	user := func(n int) domain.User {
		id := fmt.Sprintf("usr_scale_%d", n%200)
		return domain.User{ID: id, Name: fmt.Sprintf("Scale user %d", n%200), DisplayName: fmt.Sprintf("scale%d", n%200), Email: fmt.Sprintf("scale%d@example.test", n%200), Active: true}
	}
	comments := func(prefix string, n int, at time.Time) []domain.Comment {
		return []domain.Comment{
			{ID: prefix + "_c1", Body: "Looks good, thanks for the update.", Reactions: map[string][]string{"👍": {user(n + 1).ID}}, CreatedAt: at.Add(time.Hour), User: user(n + 1)},
			{ID: prefix + "_c2", Body: "Can we get the milestone dates confirmed before the review?", Reactions: map[string][]string{}, CreatedAt: at.Add(2 * time.Hour), User: user(n + 2)},
		}
	}
	snapshot := func(n int, at time.Time) *domain.PulseSnapshot {
		priority, progress := n%4, float64(n%100)/100
		return &domain.PulseSnapshot{CapturedAt: at, StatusID: "started", Status: "In Progress", Priority: &priority, PriorityLabel: "Medium", LeadID: user(n).ID, Lead: user(n).Name, TargetDate: "2026-12-31", Progress: &progress, Milestones: []domain.PulseMilestoneSnapshot{{ID: "ms_a", Name: "Alpha", Progress: progress, Total: 40, Completed: int64(n % 40)}, {ID: "ms_b", Name: "Beta", Progress: progress / 2, Total: 30, Completed: int64(n % 30)}}}
	}
	body := "We finished the API migration and started the rollout to the first customers. Next week we focus on performance, the dashboard polish and the remaining edge cases in the importer."
	projectIDs := make([]string, projectCount)
	perProject := max(projectUpdates/projectCount, 1)
	for i := range projectCount {
		project := template
		project.ID = fmt.Sprintf("pulse_scale_project_%d", i)
		projectIDs[i] = project.ID
		project.Name, project.SlugID = fmt.Sprintf("Pulse scale project %d", i), fmt.Sprintf("pulse-scale-%d", i)
		project.TeamIDs = []string{"team_test"}
		project.MemberIDs = []string{user(i).ID, user(i + 1).ID, user(i + 2).ID}
		project.Comments, project.Initiatives, project.Milestones, project.DescriptionRevisions = []domain.Comment{}, []string{}, []domain.ProjectMilestone{}, []domain.ProjectDescriptionRevision{}
		project.CreatedAt, project.UpdatedAt = now.Add(-time.Duration(i)*time.Hour), now
		put("projects", project.ID, orders["projects"], project)
		orders["projects"]++
		put("projectUpdates", parentKey(project.ID), 0, updateRecord{Parent: project.ID})
		for j := range perProject {
			at := now.Add(-time.Duration(j)*24*time.Hour - time.Duration(i)*time.Minute)
			id := fmt.Sprintf("pulse_scale_pu_%d_%d", i, j)
			update := domain.ProjectUpdate{ID: id, ProjectID: project.ID, Body: body, Health: "onTrack", CreatedAt: at, User: user(i + j), Comments: comments(id, i+j, at), Reactions: map[string][]string{"🎉": {user(i).ID, user(i + 3).ID}}, Attachments: []domain.Attachment{}, Snapshot: snapshot(i+j, at)}
			put("projectUpdates", hashKey(project.ID, id), int64(j), updateRecord{Parent: project.ID, Item: update})
		}
	}
	perInitiative := max(initiativeUpdates/initiativeCount, 1)
	admin := domain.User{ID: "usr_admin", Name: "Test admin", Email: "admin@example.test", Active: true}
	for i := range initiativeCount {
		id := fmt.Sprintf("pulse_scale_initiative_%d", i)
		linked := []string{}
		for k := range 6 {
			linked = append(linked, projectIDs[(i*6+k)%projectCount])
		}
		initiative := domain.Initiative{ID: id, Name: fmt.Sprintf("Pulse scale initiative %d", i), SlugID: fmt.Sprintf("pulse-initiative-%d", i), Color: "#5E6AD2", Status: "active", Health: "onTrack", Owner: &admin, Creator: admin, ContributingTeamIDs: []string{}, LabelIDs: []string{}, ParentInitiativeIDs: []string{}, ProjectIDs: linked, Resources: []domain.InitiativeResource{}, Comments: []domain.Comment{}, DescriptionHistory: []domain.InitiativeDescriptionRevision{}, CreatedAt: now.Add(-time.Duration(i) * time.Hour), UpdatedAt: now}
		put("initiatives", id, orders["initiatives"], initiative)
		orders["initiatives"]++
		put("initiativeUpdates", parentKey(id), 0, updateRecord{Parent: id})
		refs := []domain.PulseRef{}
		for _, projectID := range linked {
			refs = append(refs, domain.PulseRef{ID: projectID, Name: projectID})
		}
		for j := range perInitiative {
			at := now.Add(-time.Duration(j)*24*time.Hour - time.Duration(i)*time.Minute)
			updateID := fmt.Sprintf("pulse_scale_iu_%d_%d", i, j)
			update := domain.InitiativeUpdate{ID: updateID, InitiativeID: id, Body: body, Health: "onTrack", CreatedAt: at, User: admin, Comments: comments(updateID, i+j, at), Reactions: map[string][]string{"👀": {user(i).ID}}, Attachments: []domain.Attachment{}, Snapshot: &domain.PulseSnapshot{CapturedAt: at, Status: "active", StatusID: "active", LeadID: admin.ID, Lead: admin.Name, Projects: refs}}
			put("initiativeUpdates", hashKey(id, updateID), int64(j), updateRecord{Parent: id, Item: update})
		}
	}
	records.flush()
	root["_flowCollections"], _ = json.Marshal(shapes)
	encoded, _ := json.Marshal(root)
	if _, err := db.Exec(`UPDATE workspace_states SET data=? WHERE workspace_key=?`, encoded, workspace); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d projects with %d updates and %d initiatives with %d updates in %s", projectCount, perProject*projectCount, initiativeCount, perInitiative*initiativeCount, time.Since(started).Round(time.Millisecond))
}

// runPulseUpdateScaleRoutes times posting, commenting on and reacting to
// project and initiative updates in a workspace with thousands of stored
// updates (seedPulseScaleFixture). Each write must touch only its own
// project's or initiative's update records.
func runPulseUpdateScaleRoutes(t *testing.T, timed scaleTimer, repeat int) {
	t.Helper()
	id := func(value map[string]any) string { text, _ := value["id"].(string); return text }
	for i := range repeat {
		project := fmt.Sprintf("pulse_scale_project_%d", 10+i)
		initiative := fmt.Sprintf("pulse_scale_initiative_%d", 1+i%40)
		n := strconv.Itoa(i)
		update := timed("pulse: project update post", http.MethodPost, "/api/projects/"+project+"/updates", map[string]any{"body": "Scale pulse update " + n, "health": "atRisk"}, http.StatusCreated)
		timed("pulse: project update comment", http.MethodPost, "/api/projects/"+project+"/updates/"+id(update)+"/comments", map[string]any{"body": "Scale comment " + n}, http.StatusCreated)
		timed("pulse: project update reaction", http.MethodPost, "/api/projects/"+project+"/updates/pulse_scale_pu_"+strconv.Itoa(10+i)+"_3/reactions", map[string]any{"emoji": "🚀"})
		timed("pulse: project update comment (old)", http.MethodPost, "/api/projects/"+project+"/updates/pulse_scale_pu_"+strconv.Itoa(10+i)+"_5/comments", map[string]any{"body": "Late comment " + n}, http.StatusCreated)
		posted := timed("pulse: initiative update post", http.MethodPost, "/api/initiatives/"+initiative+"/updates", map[string]any{"body": "Scale initiative update " + n, "health": "onTrack"}, http.StatusCreated)
		timed("pulse: initiative update comment", http.MethodPost, "/api/initiatives/"+initiative+"/updates/"+id(posted)+"/comments", map[string]any{"body": "Initiative comment " + n}, http.StatusCreated)
		timed("pulse: initiative update reaction", http.MethodPost, "/api/initiatives/"+initiative+"/updates/"+id(posted)+"/reactions", map[string]any{"emoji": "👍"})
		timed("pulse: feed following", http.MethodGet, "/api/pulse/feed?view=following&limit=30", nil)
		timed("pulse: feed popular", http.MethodGet, "/api/pulse/feed?view=popular&limit=30", nil)
		timed("pulse: unread", http.MethodGet, "/api/pulse/unread", nil)
	}
}
