package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/publicapi"
)

// strictWebhookDecode decodes a delivery into the contract type of its
// payload type and fails on a field the contract does not describe.
func strictWebhookDecode(t *testing.T, body []byte) (string, bool) {
	t.Helper()
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		t.Fatalf("delivery is not JSON: %s", body)
	}
	for _, event := range publicapi.WebhookEvents {
		if event.Type != head.Type {
			continue
		}
		target := reflect.New(reflect.TypeOf(event.Payload))
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target.Interface()); err != nil {
			t.Errorf("%s delivery does not match publicapi.%s: %v\n%s", head.Type, reflect.TypeOf(event.Payload).Name(), err, body)
		}
		return head.Type, true
	}
	t.Errorf("delivery of type %q has no contract in publicapi.WebhookEvents: %s", head.Type, body)
	return head.Type, false
}

// TestWebhookDeliveriesMatchTheContract replays real mutations through the
// server and checks every delivered body against the published payload types
// (the same types that become the webhooks section of docs/openapi.json).
func TestWebhookDeliveriesMatchTheContract(t *testing.T) {
	s, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", publicapi.WebhookCreateInput{
		Name: ptr("Contract"), URL: receiver.server.URL,
		ResourceTypes: []string{"issues", "comments", "reactions", "attachments", "labels", "relations", "projects", "project_updates", "milestones", "documents", "initiatives", "initiative_updates", "cycles", "customers", "customer_requests", "users"},
	}, http.StatusCreated)
	_ = s
	team := seed.Teams[0]
	post := func(path string, body any, status int) map[string]any {
		return requestJSON[map[string]any](t, handler, http.MethodPost, path+"?workspace=test-workspace", body, status)
	}
	patch := func(path string, body any) {
		requestJSON[map[string]any](t, handler, http.MethodPatch, path+"?workspace=test-workspace", body, http.StatusOK)
	}
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", domain.IssueCreateInput{Title: "Contract", TeamID: team.ID}, http.StatusCreated)
	other := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", domain.IssueCreateInput{Title: "Other", TeamID: team.ID}, http.StatusCreated)
	patch("/api/issues/"+issue.ID, map[string]any{"title": "Contract renamed", "priority": 1})
	comment := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments?workspace=test-workspace", domain.CommentCreateInput{Body: "hello"}, http.StatusCreated)
	post("/api/issues/"+issue.ID+"/comments/"+comment.ID+"/reactions", map[string]any{"emoji": "👍"}, http.StatusOK)
	post("/api/issues/"+issue.ID+"/relations", map[string]any{"type": "blocks", "relatedIssueId": other.ID}, http.StatusCreated)
	post("/api/issues/"+issue.ID+"/link-attachments", publicapi.LinkAttachmentInput{URL: "https://example.com/doc"}, http.StatusCreated)
	project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects?workspace=test-workspace", domain.ProjectMutationInput{Name: ptr("Contract project"), TeamIDs: []string{team.ID}}, http.StatusCreated)
	patch("/api/projects/"+project.ID, domain.ProjectMutationInput{Summary: ptr("summary")})
	post("/api/projects/"+project.ID+"/milestones", domain.ProjectMilestoneMutationInput{Name: ptr("M1")}, http.StatusCreated)
	post("/api/projects/"+project.ID+"/updates", domain.ProjectUpdateCreateInput{Body: "on track", Health: "onTrack"}, http.StatusCreated)
	post("/api/documents", publicapi.DocumentCreateInput{Title: ptr("Doc"), Content: ptr("hello")}, http.StatusCreated)
	initiative := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives?workspace=test-workspace", domain.InitiativeMutationInput{Name: ptr("Contract initiative")}, http.StatusCreated)
	post("/api/initiatives/"+initiative.ID+"/updates", domain.InitiativeUpdateCreateInput{Body: "going", Health: "onTrack"}, http.StatusCreated)
	post("/api/customers", publicapi.CustomerInput{Name: ptr("Acme")}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+other.ID+"?workspace=test-workspace", nil, http.StatusNoContent)

	want := []string{"Issue", "Comment", "Reaction", "IssueRelation", "Attachment", "Project", "ProjectMilestone", "ProjectUpdate", "Document", "Initiative", "InitiativeUpdate", "Customer"}
	deadline := time.Now().Add(8 * time.Second)
	seen := map[string]bool{}
	checked := 0
	for time.Now().Before(deadline) {
		receiver.mu.Lock()
		deliveries := append([]capturedWebhook(nil), receiver.received...)
		receiver.mu.Unlock()
		seen, checked = map[string]bool{}, 0
		for _, delivery := range deliveries {
			var head struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(delivery.body, &head)
			seen[head.Type] = true
			checked++
		}
		if len(seen) >= len(want) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	receiver.mu.Lock()
	deliveries := append([]capturedWebhook(nil), receiver.received...)
	receiver.mu.Unlock()
	for _, delivery := range deliveries {
		strictWebhookDecode(t, delivery.body)
	}
	for _, name := range want {
		if !seen[name] {
			t.Errorf("no %s delivery was produced (got %v)", name, keys(seen))
		}
	}
	if checked < 12 {
		t.Errorf("only %d deliveries checked", checked)
	}
}

func keys(set map[string]bool) string {
	var names []string
	for name := range set {
		names = append(names, name)
	}
	return strings.Join(names, ",")
}

func ptr[T any](value T) *T { return &value }

// TestWebhookTestEventMatchesTheContract covers the WebhookTest payload.
func TestWebhookTestEventMatchesTheContract(t *testing.T) {
	_, handler, _, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	created := requestJSON[publicapi.WebhookWithSecret](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", publicapi.WebhookCreateInput{URL: receiver.server.URL, ResourceTypes: []string{"issues"}}, http.StatusCreated)
	if created.Secret == "" || created.SigningStatus != "active" || created.PayloadVersion != 2 {
		t.Fatalf("created webhook = %#v", created)
	}
	requestJSON[domain.WebhookDelivery](t, handler, http.MethodPost, "/api/webhooks/"+created.ID+"/test?workspace=test-workspace", nil, http.StatusOK)
	_, raw := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool { return envelope["type"] == "WebhookTest" })
	if name, ok := strictWebhookDecode(t, raw.body); !ok || name != "WebhookTest" {
		t.Fatalf("test event: %s", raw.body)
	}
}

// The contract's narrow inputs must be accepted by the structs the handlers
// decode: every field name and type exists on the superset.
func TestContractInputsAreSubsetsOfHandlerInputs(t *testing.T) {
	jsonFields := func(value any) map[string]reflect.Type {
		fields := map[string]reflect.Type{}
		var collect func(reflect.Type)
		collect = func(kind reflect.Type) {
			for index := 0; index < kind.NumField(); index++ {
				field := kind.Field(index)
				name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				if field.Anonymous && name == "" {
					collect(field.Type)
					continue
				}
				if name != "" && name != "-" {
					fields[name] = field.Type
				}
			}
		}
		collect(reflect.TypeOf(value))
		return fields
	}
	for _, pair := range []struct{ contract, handler any }{
		{publicapi.WebhookCreateInput{}, webhookInput{}},
		{publicapi.WebhookUpdateInput{}, webhookInput{}},
		{publicapi.ApplicationWebhookInput{}, applicationWebhookInput{}},
		{publicapi.DocumentCreateInput{}, documentInput{}},
		{publicapi.DocumentUpdateInput{}, documentInput{}},
		{publicapi.LinkAttachmentInput{}, linkAttachmentInput{}},
		{publicapi.IssueFilter{}, issueQueryNode{}},
	} {
		handler := jsonFields(pair.handler)
		for name, kind := range jsonFields(pair.contract) {
			switch name {
			case "createAsUser", "displayIconUrl":
				continue // read from the raw body by the OAuth attribution middleware
			}
			got, ok := handler[name]
			if !ok {
				t.Errorf("%T.%s is not decoded by %T", pair.contract, name, pair.handler)
				continue
			}
			// Required fields are plain values in the contract and pointers in
			// the handler (which supplies its own defaults).
			plain := func(kind reflect.Type) reflect.Type {
				for kind.Kind() == reflect.Pointer {
					kind = kind.Elem()
				}
				return kind
			}
			want, have := plain(kind), plain(got)
			if want != have && !(want.Kind() == reflect.Struct && have.Kind() == reflect.Struct) && !(want.Kind() == reflect.Slice && have.Kind() == reflect.Slice) && want.Kind() != reflect.Interface && have != reflect.TypeOf(json.RawMessage{}) {
				t.Errorf("%T.%s is %s but %T decodes %s", pair.contract, name, kind, pair.handler, got)
			}
		}
	}
}

// TestNestedEntityWebhooksDeliverTheEntityItself covers project updates,
// project milestones and initiative updates: data is the update or milestone
// (with its parent id), for every change including deletes, and url points at it.
func TestNestedEntityWebhooksDeliverTheEntityItself(t *testing.T) {
	_, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	t.Setenv("FLOW_APP_URL", "https://flow.example")
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", publicapi.WebhookCreateInput{
		URL: receiver.server.URL, ResourceTypes: []string{"project_updates", "milestones", "initiative_updates"},
	}, http.StatusCreated)
	call := func(method, path string, body any, status int) map[string]any {
		return requestJSON[map[string]any](t, handler, method, path+"?workspace=test-workspace", body, status)
	}
	project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects?workspace=test-workspace", domain.ProjectMutationInput{Name: ptr("Nested"), TeamIDs: []string{seed.Teams[0].ID}}, http.StatusCreated)
	milestone := call(http.MethodPost, "/api/projects/"+project.ID+"/milestones", domain.ProjectMilestoneMutationInput{Name: ptr("Beta")}, http.StatusCreated)
	call(http.MethodPatch, "/api/projects/"+project.ID+"/milestones/"+milestone["id"].(string), domain.ProjectMilestoneMutationInput{Name: ptr("Beta 2")}, http.StatusOK)
	update := call(http.MethodPost, "/api/projects/"+project.ID+"/updates", domain.ProjectUpdateCreateInput{Body: "first", Health: "onTrack"}, http.StatusCreated)
	call(http.MethodPatch, "/api/projects/"+project.ID+"/updates/"+update["id"].(string), domain.ProjectUpdateMutationInput{Body: ptr("edited")}, http.StatusOK)
	second := call(http.MethodPost, "/api/projects/"+project.ID+"/updates", domain.ProjectUpdateCreateInput{Body: "second", Health: "atRisk"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+project.ID+"/updates/"+second["id"].(string)+"?workspace=test-workspace", nil, http.StatusNoContent)
	two := call(http.MethodPost, "/api/projects/"+project.ID+"/milestones", domain.ProjectMilestoneMutationInput{Name: ptr("Two")}, http.StatusCreated)
	three := call(http.MethodPost, "/api/projects/"+project.ID+"/milestones", domain.ProjectMilestoneMutationInput{Name: ptr("Three")}, http.StatusCreated)
	// [Beta 2, Two, Three] -> [Beta 2, Three, Two]: only Two and Three move.
	requestJSON[any](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/milestones/reorder?workspace=test-workspace", map[string]any{"ids": []string{milestone["id"].(string), three["id"].(string), two["id"].(string)}}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+project.ID+"/milestones/"+milestone["id"].(string)+"?workspace=test-workspace", nil, http.StatusNoContent)
	initiative := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives?workspace=test-workspace", domain.InitiativeMutationInput{Name: ptr("Nested initiative")}, http.StatusCreated)
	initiativeUpdate := call(http.MethodPost, "/api/initiatives/"+initiative.ID+"/updates", domain.InitiativeUpdateCreateInput{Body: "going", Health: "onTrack"}, http.StatusCreated)
	call(http.MethodPatch, "/api/initiatives/"+initiative.ID+"/updates/"+initiativeUpdate["id"].(string), domain.ProjectUpdateMutationInput{Body: ptr("edited")}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodDelete, "/api/initiatives/"+initiative.ID+"/updates/"+initiativeUpdate["id"].(string)+"?workspace=test-workspace", nil, http.StatusNoContent)

	type want struct {
		eventType, action, id, parentKey, parentID, bodyOrName string
	}
	for _, expected := range []want{
		{"project.milestone_created", "create", milestone["id"].(string), "projectId", project.ID, "Beta"},
		{"project.milestone_updated", "update", milestone["id"].(string), "projectId", project.ID, "Beta 2"},
		{"project.update_created", "create", update["id"].(string), "projectId", project.ID, "first"},
		{"project.update_updated", "update", update["id"].(string), "projectId", project.ID, "edited"},
		{"project.update_deleted", "remove", second["id"].(string), "projectId", project.ID, "second"},
		{"project.milestone_deleted", "remove", milestone["id"].(string), "projectId", project.ID, "Beta 2"},
		{"initiative.update_created", "create", initiativeUpdate["id"].(string), "initiativeId", initiative.ID, "going"},
		{"initiative.update_updated", "update", initiativeUpdate["id"].(string), "initiativeId", initiative.ID, "edited"},
		{"initiative.update_deleted", "remove", initiativeUpdate["id"].(string), "initiativeId", initiative.ID, "edited"},
	} {
		envelope, raw := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool { return envelope["eventType"] == expected.eventType })
		strictWebhookDecode(t, raw.body)
		data, _ := envelope["data"].(map[string]any)
		text, _ := data["body"].(string)
		if text == "" {
			text, _ = data["name"].(string)
		}
		if envelope["action"] != expected.action || data["id"] != expected.id || data[expected.parentKey] != expected.parentID || text != expected.bodyOrName {
			t.Errorf("%s: action %v, data %v", expected.eventType, envelope["action"], data)
		}
		if page, _ := envelope["url"].(string); !strings.HasPrefix(page, "https://flow.example/test-workspace/") || !strings.HasSuffix(page, "#"+expected.id) {
			t.Errorf("%s: url %q does not point at the entity", expected.eventType, page)
		}
	}
	// Reordering delivers one update per milestone that moved, with its
	// previous sortOrder in updatedFrom.
	// move is {old position, new position}.
	for id, move := range map[string][2]float64{two["id"].(string): {1, 2}, three["id"].(string): {2, 1}} {
		envelope, raw := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool {
			data, _ := envelope["data"].(map[string]any)
			return envelope["eventType"] == "project.milestone_reordered" && data["id"] == id
		})
		strictWebhookDecode(t, raw.body)
		data, _ := envelope["data"].(map[string]any)
		from, _ := envelope["updatedFrom"].(map[string]any)
		if envelope["action"] != "update" || data["sortOrder"] != move[1] || from["sortOrder"] != move[0] || data["projectId"] != project.ID {
			t.Errorf("reordered milestone %s: %s", id, raw.body)
		}
		if page, _ := envelope["url"].(string); !strings.HasSuffix(page, "#"+id) {
			t.Errorf("reordered milestone url %q", page)
		}
	}
	time.Sleep(200 * time.Millisecond)
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	reordered := 0
	for _, delivery := range receiver.received {
		if strings.Contains(string(delivery.body), "milestones_reordered") || strings.Contains(string(delivery.body), "webhookChild") {
			t.Errorf("unexpected delivery: %s", delivery.body)
		}
		if strings.Contains(string(delivery.body), "project.milestone_reordered") {
			reordered++
		}
	}
	if reordered != 2 {
		t.Errorf("reordering delivered %d events, want 2 (the milestones that moved)", reordered)
	}
}

// Webhooks still on the version 1 envelope keep the mutation input as data
// (and still get the milestone reordering); the internal child marker is not
// part of it.
func TestLegacyWebhookKeepsMutationInputForNestedEntities(t *testing.T) {
	_, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/webhooks?workspace=test-workspace", map[string]any{
		"name": "Legacy", "url": receiver.server.URL, "resourceTypes": []string{"projects", "project_updates"}, "payloadVersion": 1,
	}, http.StatusCreated)
	project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects?workspace=test-workspace", domain.ProjectMutationInput{Name: ptr("Legacy nested"), TeamIDs: []string{seed.Teams[0].ID}}, http.StatusCreated)
	milestone := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/milestones?workspace=test-workspace", domain.ProjectMilestoneMutationInput{Name: ptr("M")}, http.StatusCreated)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/updates?workspace=test-workspace", domain.ProjectUpdateCreateInput{Body: "legacy body", Health: "onTrack"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/milestones/reorder?workspace=test-workspace", map[string]any{"ids": []string{milestone["id"].(string)}}, http.StatusOK)
	envelope, raw := receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool {
		return envelope["eventType"] == "project.update_created"
	})
	data, _ := envelope["data"].(map[string]any)
	if data["body"] != "legacy body" || data["health"] != "onTrack" || data["id"] != nil || envelope["url"] != nil || strings.Contains(string(raw.body), "webhookChild") {
		t.Fatalf("version 1 data = %s", raw.body)
	}
	receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool {
		return envelope["eventType"] == "project.milestones_reordered"
	})
}
