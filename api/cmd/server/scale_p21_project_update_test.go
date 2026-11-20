package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// scaleP21ProjectUpdateServe runs PATCH /api/projects/{id} as the server
// does: the COLD route mux (the route's own temperature), the real session or
// API-key authentication and workspace authorization, then the handler.
func scaleP21ProjectUpdateServe(f scaleP21M2Fixture, source *store.SQLiteStore, token string, user domain.User, key *domain.APIKey, ref string, body any) *httptest.ResponseRecorder {
	service := &server{store: source, realtime: newRealtimeHub(), uploadPath: f.testingT.TempDir()}
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/projects/{id}", service.updateProject)
	w := httptest.NewRecorder()
	raw, _ := json.Marshal(body)
	if key == nil {
		r := httptest.NewRequest(http.MethodPatch, "/api/projects/"+ref, bytes.NewReader(raw))
		r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		r, closeOutput := beginRequestResourceReads(r)
		defer closeOutput()
		service.withColdMetadataRouteMux(mux, service.authenticate(mux)).ServeHTTP(w, r)
		return w
	}
	r := f.literalRequest(user, key, body)
	r.Method, r.URL.Path = http.MethodPatch, "/api/projects/"+ref
	r, closeOutput := beginRequestResourceReads(r)
	defer closeOutput()
	service.withColdMetadataRouteMux(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if service.authorizeWorkspaceRequest(w, r, user) {
			mux.ServeHTTP(w, r)
		}
	})).ServeHTTP(w, r)
	return w
}

// scaleP21ProjectUpdateNormalize replaces the values a write generates (IDs
// it creates and clocks inside [started, finished]) with stable names, in a
// sorted walk, so a literal and a bounded outcome compare exactly.
func scaleP21ProjectUpdateNormalize(t *testing.T, value any, existing map[string]bool, started, finished time.Time) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	generated := map[string]string{}
	var walk func(any) any
	walk = func(value any) any {
		switch value := value.(type) {
		case string:
			if replacement, found := generated[value]; found {
				return replacement
			}
			for _, prefix := range []string{"evt_", "audit_", "project_description_revision_", "project_relation_", "notification_", "delivery_"} {
				if strings.HasPrefix(value, prefix) && !existing[value] {
					generated[value] = fmt.Sprintf("<generated:%s%d>", prefix, len(generated))
					return generated[value]
				}
			}
			// A clock the fixture does not hold was set by this run (its
			// reset or its write): both answers name it alike.
			if clock, err := time.Parse(time.RFC3339Nano, value); err == nil && !existing[value] && !clock.Before(started) && !clock.After(finished.Add(time.Second)) {
				return "<clock>"
			}
			// Raw JSON carried as a string (event payloads in envelopes).
			if len(value) > 1 && (value[0] == '{' || value[0] == '[') {
				var nested any
				nestedDecoder := json.NewDecoder(strings.NewReader(value))
				nestedDecoder.UseNumber()
				if nestedDecoder.Decode(&nested) == nil {
					return walk(nested)
				}
			}
		case map[string]any:
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if key == "requestId" || key == "seq" || key == "hash" || key == "prevHash" {
					delete(value, key)
					continue
				}
				value[key] = walk(value[key])
			}
		case []any:
			for index := range value {
				value[index] = walk(value[index])
			}
		}
		return value
	}
	return walk(decoded)
}

// scaleP21ProjectUpdateEmptyAsNull: an installed field compares nil and
// empty lists as one value. A legacy copy keeps the nil lists the write left
// and a published image the empty lists the load normalization gives them;
// both decode to the same records.
func scaleP21ProjectUpdateEmptyAsNull(t *testing.T, fields map[string]json.RawMessage) map[string]any {
	t.Helper()
	var walk func(any) any
	walk = func(value any) any {
		switch value := value.(type) {
		case []any:
			if len(value) == 0 {
				return nil
			}
			for index := range value {
				value[index] = walk(value[index])
			}
		case map[string]any:
			for key, child := range value {
				value[key] = walk(child)
			}
		}
		return value
	}
	result := map[string]any{}
	for name, raw := range fields {
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		result[name] = walk(decoded)
	}
	return result
}

func scaleP21ProjectUpdateInstalled(t *testing.T, repo *store.SQLiteStore, workspace string) map[string]json.RawMessage {
	t.Helper()
	names := []string{"projects", "projectRelations", "projectUpdates"}
	result := map[string]json.RawMessage{}
	if repo.BoundedMetadataEnabled() {
		read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("installed project image missing", err)
		}
		defer read.Close()
		for _, name := range names {
			var output strings.Builder
			if err := read.StreamFieldJSON(t.Context(), name, &output); err != nil {
				t.Fatal(err)
			}
			result[name] = json.RawMessage(output.String())
		}
		return result
	}
	data, found := repo.WorkspaceMetadataFields(workspace, names...)
	if !found {
		t.Fatal("legacy project image missing")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		result[name] = fields[name]
	}
	return result
}

func scaleP21ProjectUpdateRows(t *testing.T, reader *scaleP04Database, workspace string) [][]any {
	t.Helper()
	rows := reader.query(t, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('projects','projectRelations','projectUpdates','users','issueSuggestions') ORDER BY field,record_key`, workspace)
	defer rows.Close()
	result := [][]any{}
	for rows.Next() {
		var field, key string
		var order int64
		var raw []byte
		if err := rows.Scan(&field, &key, &order, &raw); err != nil {
			t.Fatal(err)
		}
		result = append(result, []any{field, key, order, json.RawMessage(raw)})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

// TestScaleP21ProjectUpdateDifferential runs PATCH /api/projects/{id} with
// bounded metadata on against the literal flag-off route, warm and after a
// SQL reload, for every field the edit takes and every effect it has: the
// response, the canonical project, relation, update, user and suggestion
// rows, the installed fields, content rows (reminder notifications), events,
// audits, webhooks and realtime. Validation refusals, a missing project and
// a slug reference compare too, and so does the session and API key matrix
// on a shared and a private-team project and for an outsider. With the flag
// on, the edits boundedProjectEditScope admits never take the COLD slot and
// the others (update schedule, start date, teams, archive) take it.
func TestScaleP21ProjectUpdateDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	testStarted := time.Now().UTC()
	config := scaleP03DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP21M2Setup(t, repo)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("projupd-")
	reader := &scaleP04Database{config: config, key: workspace, workspaceID: f.data.Workspace.ID}
	t.Cleanup(func() {
		if reader.reader != nil {
			_ = reader.reader.Close()
		}
	})
	sqlFixture := scaleP21SW10Fixture{reader: reader}
	sqlFixture.data.Workspace.URLKey = workspace
	projectID, dependencyID, otherID, privateID := prefix+"project", prefix+"dependency", prefix+"other", prefix+"private"
	started, labelID, groupA, groupB := prefix+"started", prefix+"label", prefix+"group-a", prefix+"group-b"
	initiativeID, formerID := prefix+"initiative", f.principals[2].user.ID
	issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Project update suggestion", "teamId": f.public}, workspace, http.StatusCreated)
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	privateTeam := ""
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.project_update_seed", "", nil, func(data *domain.Bootstrap) error {
		for _, team := range data.Teams {
			if team.Private && strings.HasPrefix(team.ID, "m2-private-") {
				privateTeam = team.ID
			}
		}
		status := domain.ProjectStatus{ID: prefix + "backlog", Name: "Backlog", Color: "#999999", Type: "backlog"}
		data.ProjectStatuses = append(data.ProjectStatuses, status, domain.ProjectStatus{ID: started, Name: "Started", Color: "#00aa00", Type: "started"})
		data.LabelGroups = append(data.LabelGroups, domain.LabelGroup{ID: prefix + "group", Name: "Project group", ResourceType: "project"})
		data.Labels = append(data.Labels,
			domain.IssueLabel{ID: labelID, Name: "Project label", ResourceType: "project"},
			domain.IssueLabel{ID: groupA, Name: "Group A", ResourceType: "project", GroupID: prefix + "group"},
			domain.IssueLabel{ID: groupB, Name: "Group B", ResourceType: "project", GroupID: prefix + "group"},
			domain.IssueLabel{ID: prefix + "issue-label", Name: "Issue label"},
		)
		lead := data.Viewer
		data.Projects = append(data.Projects,
			domain.Project{ID: projectID, Name: "Project", SlugID: "project-0123456789ab", Icon: "Project", Color: "#5e6ad2",
				Summary: "before", Description: "Before description", Status: status, Priority: 2, PriorityLabel: "High", Health: "onTrack",
				Lead: &lead, MemberIDs: []string{data.Viewer.ID}, TeamIDs: []string{f.public}, LabelIDs: []string{}, DependencyIDs: []string{dependencyID},
				Initiatives: []string{}, Customers: []string{}, TargetDate: optionalString("2026-12-01"), TargetDateResolution: "month",
				Resources:            []domain.ProjectResource{{ID: prefix + "resource", Title: "Kept resource", URL: "https://example.test"}},
				Milestones:           []domain.ProjectMilestone{{ID: prefix + "milestone", ProjectID: projectID, Name: "Kept milestone"}},
				DescriptionRevisions: []domain.ProjectDescriptionRevision{{ID: prefix + "revision", ProjectID: projectID, Description: "Older", Author: data.Viewer, CreatedAt: clock}},
				UpdateCadence:        "weekly", CreatedAt: clock, UpdatedAt: clock},
			domain.Project{ID: dependencyID, Name: "Dependency", TeamIDs: []string{f.public}, Status: status, CreatedAt: clock, UpdatedAt: clock},
			domain.Project{ID: otherID, Name: "Other", TeamIDs: []string{f.public}, Status: status, Description: strings.Repeat("other body ", 4000), CreatedAt: clock, UpdatedAt: clock},
			domain.Project{ID: privateID, Name: "Private", TeamIDs: []string{privateTeam}, Status: status, CreatedAt: clock, UpdatedAt: clock},
		)
		data.ProjectRelations = append(data.ProjectRelations, domain.ProjectRelation{ID: prefix + "relation", ProjectID: projectID, RelatedProjectID: dependencyID, Type: "blocked_by", CreatedAt: clock, UpdatedAt: clock})
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: initiativeID, Name: "Initiative", Status: "active", CreatedAt: clock, UpdatedAt: clock})
		if data.ProjectUpdates == nil {
			data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
		}
		due := clock.Add(24 * time.Hour)
		data.ProjectUpdates[projectID] = []domain.ProjectUpdate{{ID: prefix + "update", ProjectID: projectID, Body: "Update", Health: "onTrack", User: data.Viewer, DueAt: &due, Missing: true, CreatedAt: clock}}
		data.Notifications = append(data.Notifications, domain.Notification{ID: prefix + "reminder", RecipientID: data.Viewer.ID, Type: "projectUpdateReminder", SourceType: "project", SourceID: projectID, ProjectID: projectID, Actor: data.Viewer, Category: "reminders", CreatedAt: clock, UpdatedAt: clock})
		data.IssueSuggestions = append(data.IssueSuggestions, domain.IssueSuggestion{ID: prefix + "suggestion", IssueID: issue.ID, Type: "project", State: "active", StateChangedAt: clock, SuggestedProjectID: projectID})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if privateTeam == "" {
		t.Fatal("private team fixture missing")
	}
	// The baseline is the loaded (normalized) fixture, so a warm copy and
	// a published image start from the same values.
	if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("project fixture missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	existing := map[string]bool{}
	var collect func(any)
	collect = func(value any) {
		switch value := value.(type) {
		case string:
			existing[value] = true
		case map[string]any:
			for _, child := range value {
				collect(child)
			}
		case []any:
			for _, child := range value {
				collect(child)
			}
		}
	}
	var savedValue any
	_ = json.Unmarshal(saved, &savedValue)
	collect(savedValue)
	tokens := map[string]string{}
	for _, principal := range f.principals {
		_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
		if err != nil {
			t.Fatal(err)
		}
		tokens[principal.user.ID] = token
	}
	schedule := map[string]any{"mode": "never", "frequencyDays": 7, "weekday": 1, "hour": 9, "timezone": "UTC"}
	type projectCase struct {
		name, ref string
		body      map[string]any
		code      int
		cold      bool
	}
	cases := []projectCase{
		{"summary", projectID, map[string]any{"summary": "After"}, 200, false},
		{"name", projectID, map[string]any{"name": "  Renamed project  "}, 200, false},
		{"name-empty", projectID, map[string]any{"name": "  "}, 400, false},
		{"description", projectID, map[string]any{"description": "New description"}, 200, false},
		{"description-same", projectID, map[string]any{"description": "Before description", "summary": "With same description"}, 200, false},
		{"icon-color", projectID, map[string]any{"icon": "Rocket", "color": "#ff0000"}, 200, false},
		{"status", projectID, map[string]any{"statusId": started}, 200, false},
		{"status-invalid", projectID, map[string]any{"statusId": prefix + "no-status"}, 400, false},
		{"priority", projectID, map[string]any{"priority": 0}, 200, false},
		{"priority-invalid", projectID, map[string]any{"priority": 7}, 400, false},
		{"position", projectID, map[string]any{"position": 3.5}, 200, false},
		{"health", projectID, map[string]any{"health": "atRisk"}, 200, false},
		{"health-invalid", projectID, map[string]any{"health": "bad"}, 400, false},
		{"lead", projectID, map[string]any{"leadId": f.principals[1].user.ID}, 200, false},
		{"lead-clear", projectID, map[string]any{"leadId": ""}, 200, false},
		{"lead-invalid", projectID, map[string]any{"leadId": prefix + "nobody"}, 403, false},
		{"lead-former", projectID, map[string]any{"leadId": formerID}, 400, false},
		{"members", projectID, map[string]any{"memberIds": []string{f.data.Viewer.ID, f.principals[1].user.ID}}, 200, false},
		{"members-invalid", projectID, map[string]any{"memberIds": []string{prefix + "nobody"}}, 403, false},
		{"members-former", projectID, map[string]any{"memberIds": []string{formerID}}, 400, false},
		{"labels", projectID, map[string]any{"labelIds": []string{labelID, groupA}}, 200, false},
		{"labels-issue-label", projectID, map[string]any{"labelIds": []string{prefix + "issue-label"}}, 400, false},
		{"labels-same-group", projectID, map[string]any{"labelIds": []string{groupA, groupB}}, 400, false},
		{"dependencies", projectID, map[string]any{"dependencyIds": []string{otherID}}, 200, false},
		{"dependencies-same", projectID, map[string]any{"dependencyIds": []string{dependencyID}}, 200, false},
		{"dependency-self", projectID, map[string]any{"dependencyIds": []string{projectID}}, 400, false},
		{"dependency-missing", projectID, map[string]any{"dependencyIds": []string{prefix + "no-project"}}, 403, false},
		{"dependency-relations", projectID, map[string]any{"dependencyRelations": []map[string]string{{"projectId": otherID, "type": "blocks"}, {"projectId": dependencyID, "type": "blocked_by"}}}, 200, false},
		{"dependency-relations-conflict", projectID, map[string]any{"dependencyRelations": []map[string]string{{"projectId": otherID, "type": "blocks"}, {"projectId": otherID, "type": "blocked_by"}}}, 400, false},
		{"initiatives", projectID, map[string]any{"initiatives": []string{initiativeID, initiativeID}}, 200, false},
		{"initiatives-disabled", projectID, map[string]any{"initiatives": []string{initiativeID}}, 400, false},
		{"customers-unknown", projectID, map[string]any{"customers": []string{" acme ", "acme"}}, 403, false},
		{"target-date", projectID, map[string]any{"targetDate": "2027-01-01", "targetDateResolution": "quarter"}, 200, false},
		{"target-date-clear", projectID, map[string]any{"targetDate": ""}, 200, false},
		{"target-resolution-invalid", projectID, map[string]any{"targetDateResolution": "week"}, 400, false},
		{"start-resolution", projectID, map[string]any{"startDateResolution": "month"}, 200, false},
		{"slack", projectID, map[string]any{"slackChannelId": " C1 ", "slackChannelName": " general "}, 200, false},
		{"cadence", projectID, map[string]any{"updateCadence": "monthly"}, 200, false},
		{"cadence-invalid", projectID, map[string]any{"updateCadence": "daily"}, 400, false},
		{"unarchive", projectID, map[string]any{"archived": false}, 200, false},
		{"archived-false-noop", projectID, map[string]any{"archived": false}, 200, false},
		{"no-op", projectID, map[string]any{"summary": "before"}, 200, false},
		{"empty", projectID, map[string]any{}, 200, false},
		{"missing", prefix + "missing", map[string]any{"summary": "x"}, 403, false},
		{"slug-ref", "project-0123456789ab", map[string]any{"summary": "x"}, 404, false},
		{"many-fields", projectID, map[string]any{"name": "All", "summary": "s", "description": "d", "statusId": started, "priority": 1, "leadId": f.principals[1].user.ID, "targetDate": "2027-02-02", "labelIds": []string{labelID}}, 200, false},
		{"archive", projectID, map[string]any{"archived": true}, 200, true},
		{"update-schedule-never", projectID, map[string]any{"updateSchedule": schedule}, 200, true},
		{"update-schedule-invalid", projectID, map[string]any{"updateSchedule": map[string]any{"mode": "sometimes"}}, 400, true},
		{"start-date", projectID, map[string]any{"startDate": "2026-01-05"}, 200, true},
		{"teams-outside", projectID, map[string]any{"teamIds": []string{f.public, privateTeam}}, 403, true},
		{"summary-and-teams", projectID, map[string]any{"summary": "With teams", "teamIds": []string{f.public}}, 200, true},
	}
	formerSuspended := false
	run := func(t *testing.T, test projectCase, principal domain.User, credential *domain.APIKey) {
		t.Helper()
		var expected any
		for _, mode := range []string{"legacy-warm", "bounded-warm", "legacy-reloaded", "bounded-reloaded"} {
			repo.SetWebhookSink(nil)
			repo.SetRealtimeSink(nil)
			if formerSuspended {
				if err := repo.ResumeMember(t.Context(), f.data.Workspace.ID, formerID); err != nil {
					t.Fatal(err)
				}
				formerSuspended = false
			}
			restore := store.ForceFullMutationsForTesting(true)
			err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.project_update_reset", "", nil, func(data *domain.Bootstrap) error {
				var restored domain.Bootstrap
				if err := json.Unmarshal(saved, &restored); err != nil {
					return err
				}
				*data = restored
				for index := range data.Projects {
					if data.Projects[index].ID != projectID {
						continue
					}
					switch test.name {
					case "unarchive":
						data.Projects[index].ArchivedAt = &clock
					}
				}
				if test.name == "initiatives-disabled" {
					if data.WorkspaceSettings.FeatureFlags == nil {
						data.WorkspaceSettings.FeatureFlags = map[string]bool{}
					}
					data.WorkspaceSettings.FeatureFlags["initiatives"] = false
				}
				return nil
			})
			restore()
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(test.name, "-former") {
				if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, formerID); err != nil {
					t.Fatal(err)
				}
				formerSuspended = true
			}
			if strings.HasSuffix(mode, "reloaded") {
				if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
					t.Fatal(err)
				}
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			source := repo
			if strings.HasPrefix(mode, "bounded") {
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				source = f.probe
			}
			memberDirectories.Delete(fmt.Sprintf("%p:%s", source, f.data.Workspace.ID))
			before := scaleP21SW59SQL(t, sqlFixture)
			priorEvents, err := repo.Events(t.Context(), test.ref)
			if err != nil {
				t.Fatal(err)
			}
			eventIDs := map[string]bool{}
			for _, event := range priorEvents {
				eventIDs[event.ID] = true
			}
			priorAudits, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{ResourceID: projectID})
			if err != nil {
				t.Fatal(err)
			}
			auditIDs := map[string]bool{}
			for _, audit := range priorAudits {
				auditIDs[audit.ID] = true
			}
			got := scaleP21SW59Outcome{}
			source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
				got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, event))
			})
			captureRealtime := func(_ string, event domain.RealtimeEvent) {
				got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, event))
			}
			source.SetRealtimeSink(captureRealtime)
			if source != repo {
				repo.SetRealtimeSink(captureRealtime)
			}
			coldBefore := store.ColdSlotAcquisitionsForTesting()
			began := time.Now().UTC()
			response := scaleP21ProjectUpdateServe(f, source, tokens[principal.ID], principal, credential, test.ref, test.body)
			finished := time.Now().UTC()
			coldTaken := store.ColdSlotAcquisitionsForTesting() - coldBefore
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
			repo.SetRealtimeSink(nil)
			if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("project edit left work admitted", source.MetadataWorkUsedForTesting())
			}
			installed := scaleP21ProjectUpdateInstalled(t, source, workspace)
			got.Code, got.Header, got.Rows = response.Code, response.Header(), scaleP21SW59SQL(t, sqlFixture)
			after, err := repo.Events(t.Context(), test.ref)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range after {
				if !eventIDs[event.ID] {
					got.Events = append(got.Events, event)
				}
			}
			audits, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{ResourceID: projectID})
			if err != nil {
				t.Fatal(err)
			}
			newAudits := []domain.AuditLogEntry{}
			for _, audit := range audits {
				if !auditIDs[audit.ID] {
					newAudits = append(newAudits, audit)
				}
			}
			got.Response, err = json.Marshal(map[string]any{
				"response": json.RawMessage(response.Body.Bytes()), "installed": scaleP21ProjectUpdateEmptyAsNull(t, installed), "audits": newAudits,
				"canonical": scaleP21ProjectUpdateRows(t, reader, workspace),
			})
			if err != nil {
				t.Fatal(err)
			}
			normalized := scaleP21ProjectUpdateNormalize(t, scaleP21SW59Normalized(t, got, before, began, finished), existing, testStarted, finished)
			if strings.HasPrefix(mode, "legacy") {
				// Each bounded mode is compared with the literal answer of
				// the same start (warm, or reloaded with its load
				// normalization).
				expected = normalized
			} else if !reflect.DeepEqual(expected, normalized) {
				t.Fatalf("project update differs: case=%s mode=%s principal=%s status=%d: %s", test.name, mode, principal.ID, response.Code, scaleP21SW480Path(expected, normalized, ""))
			}
			if test.code != 0 && response.Code != test.code {
				t.Fatalf("project update case=%s mode=%s status=%d want %d: %s", test.name, mode, response.Code, test.code, response.Body.String())
			}
			if response.Code >= 300 || test.name == "no-op" || test.name == "empty" || test.name == "archived-false-noop" || test.name == "dependencies-same" {
				if len(got.Events) != 0 || len(newAudits) != 0 {
					t.Fatalf("a refused or unchanged project edit wrote: case=%s events=%d audits=%d", test.name, len(got.Events), len(newAudits))
				}
			} else if len(got.Events) != 1 || len(got.Realtime) == 0 {
				t.Fatalf("a project edit did not publish its one event: case=%s events=%d realtime=%d", test.name, len(got.Events), len(got.Realtime))
			}
			if source.BoundedMetadataEnabled() && test.code == 200 && response.Code == 200 {
				// The router: an admitted edit never takes the COLD slot; the
				// others run the literal dispatch on the COLD facade.
				if !test.cold && coldTaken != 0 {
					t.Fatalf("a HOT project edit took the COLD slot: case=%s acquisitions=%d", test.name, coldTaken)
				}
				if test.cold && coldTaken == 0 && len(got.Events) != 0 {
					t.Fatalf("a COLD project edit ran outside the COLD slot: case=%s", test.name)
				}
			}
			t.Logf("project case=%s mode=%s status=%d cold=%d", test.name, mode, response.Code, coldTaken)
		}
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) { run(t, test, baseline.Viewer, nil) })
	}
	t.Run("permission-shared", func(t *testing.T) {
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			run(t, projectCase{name: "permission-shared", ref: projectID, body: map[string]any{"summary": "Matrix edit"}}, user, key)
		})
	})
	t.Run("permission-private", func(t *testing.T) {
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			run(t, projectCase{name: "permission-private", ref: privateID, body: map[string]any{"summary": "Private edit", "priority": 1}}, user, key)
		})
	})
	t.Run("permission-outsider", func(t *testing.T) {
		outsider := domain.User{ID: prefix + "outsider", Name: "Outsider", Email: prefix + "outsider@example.test", Active: true}
		for _, key := range []*domain.APIKey{{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}, {ID: "m2-read", Scopes: []string{"read"}, TeamRestriction: "all"}} {
			run(t, projectCase{name: "permission-outsider", ref: projectID, body: map[string]any{"summary": "Outsider edit"}}, outsider, key)
		}
	})
}
