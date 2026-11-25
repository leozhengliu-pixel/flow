package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP17MutationSideEffectsDifferential(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_DOCUMENT_RECORD_VERIFY", "off")
	legacy := scaleP17SideEffects(t, "off")
	selected := scaleP17SideEffects(t, "on")
	compareMutationLists(t, "document realtime", selected.events, legacy.events)
	compareMutationLists(t, "document webhook", selected.webhooks, legacy.webhooks)
	for key, value := range legacy.state {
		if selected.state[key] != value {
			t.Errorf("canonical document mutation state differs: %s", key)
		}
	}
	if !reflect.DeepEqual(legacy.state, selected.state) {
		t.Fatal("selected writes changed canonical SQL or side effects")
	}
}

func scaleP17SideEffects(t *testing.T, mode string) mutationScriptResult {
	t.Helper()
	t.Setenv("FLOW_DOCUMENT_RECORDS", mode)
	t.Setenv("FLOW_TRASH_RECORDS", mode)
	db := newScaleP04Database(t)
	handler := db.handler(t, true)
	initial, _ := db.repo.BootstrapFor(db.key)
	if err := db.repo.MutateWorkspace(t.Context(), db.key, "p17.fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: "p17-webhook", Name: "P17", URL: "https://example.test/p17", Enabled: true, ResourceTypes: []string{"Document"}})
		preferences := defaultPreferences(db.users[1].ID)
		preferences.Email.Enabled, preferences.Desktop.Enabled = true, true
		data.NotificationPreferences[db.users[1].ID] = preferences
		data.PushSubscriptions = append(data.PushSubscriptions, domain.PushSubscription{ID: "p17-push", UserID: db.users[1].ID, Enabled: true})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var events []domain.RealtimeEvent
	var webhooks []domain.DomainEvent
	db.repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { events = append(events, event) })
	db.repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { webhooks = append(webhooks, event) })
	project := requestJSON[domain.Project](t, handler, "POST", "/api/projects", map[string]any{"name": "P17 side effects", "teamIds": []string{initial.Teams[0].ID}}, http.StatusCreated)
	document := requestJSON[domain.Document](t, handler, "POST", "/api/documents", map[string]any{"title": "P17 source", "content": "Original P17 text", "projectIds": []string{project.ID}, "subscriberIds": []string{db.users[1].ID}}, http.StatusCreated)
	requestJSON[any](t, handler, "PUT", "/api/favorites/document/"+document.ID, nil, http.StatusOK)
	requestJSON[any](t, handler, "PUT", "/api/subscriptions/document/"+document.ID, nil, http.StatusOK)
	root := requestJSON[domain.Comment](t, handler, "POST", "/api/documents/"+document.ID+"/comments", map[string]any{"body": "P17 root"}, http.StatusCreated)
	reply := requestJSON[domain.Comment](t, handler, "POST", "/api/documents/"+document.ID+"/comments", map[string]any{"body": "P17 reply", "parentId": root.ID}, http.StatusCreated)
	requestJSON[domain.Comment](t, handler, "POST", "/api/documents/"+document.ID+"/comments/"+root.ID+"/reactions", map[string]string{"emoji": "+1"}, http.StatusOK)
	requestJSON[domain.ThreadSubscription](t, handler, "PUT", "/api/documents/"+document.ID+"/comments/"+root.ID+"/subscription", map[string]string{"state": "subscribed"}, http.StatusOK)
	saved := requestJSON[domain.Document](t, handler, "PATCH", "/api/documents/"+document.ID, map[string]any{"title": "P17 edited", "content": "Edited P17 text"}, http.StatusOK)
	requestJSON[[]domain.DocumentPermission](t, handler, "PUT", "/api/documents/"+document.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "workspace", "role": "editor"}, {"subjectType": "user", "subjectId": db.users[1].ID, "role": "owner"}}}, http.StatusOK)
	requestJSON[any](t, handler, "DELETE", "/api/documents/"+document.ID, nil, http.StatusNoContent)
	data, _ := db.repo.WorkspaceMetadata(db.key)
	entry := slices.IndexFunc(data.Trash, func(item domain.TrashEntry) bool { return item.ResourceID == document.ID })
	if entry < 0 {
		t.Fatal("document was not trashed")
	}
	requestJSON[domain.Document](t, handler, "POST", "/api/trash/"+data.Trash[entry].ID+"/restore", nil, http.StatusOK)
	data, _ = db.repo.BootstrapFor(db.key)
	pairs := []string{document.SlugID, "p17-original-slug", saved.SlugID, "p17-edited-slug", document.ID, "p17-document", project.ID, "p17-project", root.ID, "p17-root", reply.ID, "p17-reply", db.key, "p17-workspace", db.workspaceID, "p17-workspace-id"}
	for i, user := range db.users {
		pairs = append(pairs, user.ID, fmt.Sprintf("p17-user-%d", i), user.Email, fmt.Sprintf("p17-user-%d@example.test", i))
	}
	teamIDs := []string{}
	for id := range initial.TeamSettings {
		if mutationNanoID.MatchString(id) {
			teamIDs = append(teamIDs, id)
		}
	}
	slices.Sort(teamIDs)
	for i, id := range teamIDs {
		pairs = append(pairs, id, fmt.Sprintf("p17-fixture-team-%d", i))
	}
	identities := strings.NewReplacer(pairs...)
	normalize := func(raw []byte) string { return scaleP04Normalize([]byte(identities.Replace(string(raw)))) }
	result := mutationScriptResult{state: map[string]string{}}
	for _, event := range events {
		result.events = append(result.events, event.Type+" "+normalize([]byte(event.AggregateID))+" "+normalize(event.Payload))
	}
	previous := false
	for _, event := range webhooks {
		result.webhooks = append(result.webhooks, event.Type+" "+normalize([]byte(event.AggregateID))+" "+normalize(event.Payload)+" "+normalize(event.PreviousValues))
		previous = previous || event.Type == "document.updated" && len(event.PreviousValues) > 0
	}
	if !previous || len(data.Notifications) == 0 || len(data.NotificationDeliveries) == 0 {
		t.Fatal("differential fixture did not exercise webhook previous values and notification deliveries")
	}
	for key, value := range map[string]any{"documents": data.Documents, "projects": data.Projects, "trash": data.Trash, "comments": data.Comments, "notifications": data.Notifications, "deliveries": data.NotificationDeliveries, "audit": data.AuditLog, "favorites": data.Favorites, "subscriptions": data.Subscriptions, "threads": data.ThreadSubscriptions} {
		raw, _ := json.Marshal(value)
		result.state[key] = normalize(raw)
	}
	for name, query := range map[string]string{
		"rootSQL":     "SELECT data FROM workspace_states WHERE workspace_key=?",
		"metadataSQL": "SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key",
		"contentSQL":  "SELECT kind,resource_id,id,data FROM workspace_content_records WHERE workspace_key=? ORDER BY kind,resource_id,created_at,id",
		"searchSQL":   "SELECT field,record_key,content FROM metadata_search_documents WHERE workspace_key=? ORDER BY field,record_key",
		"eventsSQL":   "SELECT event_type,aggregate_id,payload,previous_values FROM domain_events WHERE workspace_id=? ORDER BY created_at,id",
		"auditSQL":    "SELECT action,resource_type,resource_id,actor,user_id,metadata,result FROM audit_events WHERE workspace_id=? ORDER BY seq",
	} {
		arg := db.key
		if name == "eventsSQL" || name == "auditSQL" {
			arg = db.workspaceID
		}
		rows := db.query(t, query, arg)
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		records := [][]string{}
		for rows.Next() {
			values := make([]sql.NullString, len(columns))
			args := make([]any, len(columns))
			for i := range values {
				args[i] = &values[i]
			}
			if err := rows.Scan(args...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			record := make([]string, len(columns))
			for i, value := range values {
				record[i] = normalize([]byte(value.String))
			}
			records = append(records, record)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(records)
		result.state[name] = string(raw)
	}
	return result
}
