package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP16MutationSideEffectsDifferential(t *testing.T) {
	for _, journal := range []string{"off", "on"} {
		t.Run(journal, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_JOURNAL", journal)
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", "off")
			t.Setenv("FLOW_INITIATIVE_RECORD_MUTATIONS", "off")
			old := scaleP16MutationScript(t)
			t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", "on")
			t.Setenv("FLOW_INITIATIVE_RECORD_MUTATIONS", "on")
			got := scaleP16MutationScript(t)
			for name, value := range old {
				if got[name] != value {
					i := 0
					for i < len(value) && i < len(got[name]) && value[i] == got[name][i] {
						i++
					}
					t.Errorf("%s differs at %d: old %.500s new %.500s", name, i, value[i:], got[name][i:])
				}
			}
			if len(old) != len(got) {
				t.Fatal("missing differential outputs")
			}
		})
	}
}

func scaleP16MutationScript(t *testing.T) map[string]string {
	t.Helper()
	db := newScaleP04Database(t)
	baseHandler := db.handler(t, true)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Request-ID", "scale-p16-differential")
		baseHandler.ServeHTTP(w, r)
	})
	fixture, _ := db.repo.BootstrapFor(db.key)
	var replacements []string
	for i, user := range db.users {
		replacements = append(replacements, user.ID, fmt.Sprintf("fixture-user-%d", i), user.Email, fmt.Sprintf("fixture-%d@example.test", i))
	}
	var teams []string
	for id := range fixture.TeamSettings {
		if mutationNanoID.MatchString(id) {
			teams = append(teams, id)
		}
	}
	slices.Sort(teams)
	for i, id := range teams {
		replacements = append(replacements, id, fmt.Sprintf("fixture-team-%d", i))
	}
	replacements = append(replacements, db.workspaceID, "fixture-workspace", db.key, "fixture-key")
	attachmentIDs := map[string]bool{}
	slugSuffix := regexp.MustCompile(`-[a-f0-9]{12}`)
	normalize := func(raw []byte) string {
		identity := strings.NewReplacer(replacements...)
		return scaleP04Normalize([]byte(slugSuffix.ReplaceAllString(identity.Replace(string(raw)), "-fixture-suffix")))
	}
	outputs := map[string][]json.RawMessage{}
	previousValues := 0
	db.repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		raw, _ := json.Marshal([]any{event.Type, event.AggregateID, event.ActorID, event.Payload})
		outputs["realtime"] = append(outputs["realtime"], raw)
	})
	db.repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		if len(event.PreviousValues) > 0 && string(event.PreviousValues) != "null" {
			previousValues++
		}
		raw, _ := json.Marshal([]any{event.Type, event.AggregateID, event.Payload, event.PreviousValues})
		outputs["webhooks"] = append(outputs["webhooks"], raw)
	})
	call := func(method, path string, input any, status int) map[string]any {
		t.Helper()
		value := requestJSON[any](t, handler, method, path, input, status)
		raw, _ := json.Marshal(value)
		outputs["responses"] = append(outputs["responses"], raw)
		object, _ := value.(map[string]any)
		return object
	}
	id := func(value map[string]any) string { id, _ := value["id"].(string); return id }
	host := httptest.NewServer(handler)
	defer host.Close()
	upload := func(path string) map[string]any {
		t.Helper()
		status, raw := postProjectCommentMedia(t, http.DefaultClient, host.URL+path, "p16.txt", "text/plain", "P16 attachment")
		if status != http.StatusCreated {
			t.Fatalf("attachment upload status=%d: %.300s", status, raw)
		}
		var result map[string]any
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		remember := func(attachment map[string]any) {
			key := id(attachment)
			if key != "" && !attachmentIDs[key] {
				attachmentIDs[key] = true
				replacements = append(replacements, key, fmt.Sprintf("fixture-attachment-%d", len(attachmentIDs)))
			}
		}
		if attachments, ok := result["attachments"].([]any); ok {
			for _, attachment := range attachments {
				remember(attachment.(map[string]any))
			}
		} else {
			remember(result)
		}
		outputs["responses"] = append(outputs["responses"], raw)
		return result
	}
	call("POST", "/api/loops", map[string]any{"name": "P16 watcher", "status": "published", "triggerType": "issue", "triggerConfig": map[string]any{"event": "created"}, "instructions": "Watch"}, 201)
	project := call("POST", "/api/projects", map[string]any{"name": "P16 project", "teamIds": []string{fixture.Teams[0].ID}}, 201)
	other := call("POST", "/api/projects", map[string]any{"name": "P16 dependency", "teamIds": []string{fixture.Teams[0].ID}}, 201)
	path := "/api/projects/" + id(project)
	call("PATCH", path, map[string]any{"name": "P16 renamed", "description": "First description", "dependencyIds": []string{id(other)}, "leadId": fixture.Users[1].ID}, 200)
	call("PATCH", path, map[string]any{"description": "Second description", "startDate": "2026-01-01"}, 200)
	call("PATCH", path, map[string]any{"dependencyIds": []string{"missing"}}, 403)
	call("PATCH", path, map[string]any{"priority": 9}, 400)
	resource := call("POST", path+"/resources", map[string]string{"url": "https://example.test/spec"}, 201)
	call("PATCH", path+"/resources/"+id(resource), map[string]string{"title": "Changed"}, 200)
	call("DELETE", path+"/resources/"+id(resource), nil, 204)
	milestone := call("POST", path+"/milestones", map[string]string{"name": "P16 milestone"}, 201)
	call("PATCH", path+"/milestones/"+id(milestone), map[string]string{"description": "Milestone body"}, 200)
	call("POST", path+"/milestones/reorder", map[string]any{"ids": []string{id(milestone)}}, 200)
	call("POST", "/api/issue-records", map[string]string{"title": "P16 linked issue", "teamId": fixture.Teams[0].ID, "projectId": id(project), "projectMilestoneId": id(milestone)}, 201)
	call("DELETE", path+"/milestones/"+id(milestone), nil, 204)
	mention := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "mention", "userId": fixture.Users[1].ID}}}
	for _, item := range []struct{ kind, path string }{{"project", path}, {"initiative", "/api/initiatives/" + id(call("POST", "/api/initiatives", map[string]any{"name": "P16 initiative", "projectIds": []string{id(project)}}, 201))}} {
		upload(item.path + "/comment-attachments")
		comment := call("POST", item.path+"/comments", map[string]any{"body": "Mention", "bodyData": mention}, 201)
		call("PUT", item.path+"/comments/"+id(comment)+"/subscription", map[string]string{"state": "subscribed"}, 200)
		call("DELETE", item.path+"/comments/"+id(comment)+"/subscription", nil, 204)
		call("POST", item.path+"/comments", map[string]any{"body": "Reply", "parentId": id(comment)}, 201)
		call("PATCH", item.path+"/comments/"+id(comment), map[string]string{"body": "Edited"}, 200)
		call("POST", item.path+"/comments/"+id(comment)+"/reactions", map[string]string{"emoji": "+1"}, 200)
		call("PATCH", item.path+"/comments/"+id(comment), map[string]bool{"resolved": true}, 200)
		call("DELETE", item.path+"/comments/"+id(comment), nil, 204)
		call("POST", item.path+"/reminders", map[string]string{"remindAt": time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)}, 201)
		update := call("POST", item.path+"/updates", map[string]string{"body": "Update", "health": "onTrack"}, 201)
		attached := upload(item.path + "/updates/" + id(update) + "/attachments")
		attachment := attached["attachments"].([]any)[0].(map[string]any)
		call("DELETE", item.path+"/updates/"+id(update)+"/attachments/"+id(attachment), nil, 200)
		upload(item.path + "/updates/" + id(update) + "/attachments")
		call("PATCH", item.path+"/updates/"+id(update), map[string]string{"body": "Edited update"}, 200)
		call("POST", item.path+"/updates/"+id(update)+"/comments", map[string]any{"body": "Update comment", "bodyData": mention}, 201)
		call("POST", item.path+"/updates/"+id(update)+"/reactions", map[string]string{"emoji": "+1"}, 200)
		call("DELETE", item.path+"/updates/"+id(update), nil, 204)
		if item.kind == "initiative" {
			document := call("POST", "/api/documents", map[string]string{"title": "P16 initiative resource"}, 201)
			resource := call("POST", item.path+"/resources", map[string]string{"type": "document", "documentId": id(document)}, 201)
			call("PATCH", item.path+"/resources/"+id(resource), map[string]string{"title": "Changed document link"}, 200)
			call("DELETE", item.path+"/resources/"+id(resource), nil, 204)
			parent := call("POST", "/api/initiatives", map[string]string{"name": "P16 parent"}, 201)
			relation := call("POST", item.path+"/relations", map[string]string{"relatedInitiativeId": id(parent), "type": "parent"}, 201)
			call("PATCH", item.path+"/relations/"+id(relation), map[string]string{"type": "related"}, 200)
			call("DELETE", item.path+"/relations/"+id(relation), nil, 204)
			call("PATCH", item.path, map[string]any{"summary": "P16 initiative edit", "projectIds": []string{id(other)}}, 200)
			call("DELETE", item.path, nil, 204)
		}
	}
	relation := call("POST", path+"/relations", map[string]string{"relatedProjectId": id(other), "type": "related"}, 201)
	call("PATCH", path+"/relations/"+id(relation), map[string]string{"type": "blocks"}, 200)
	call("DELETE", path+"/relations/"+id(relation), nil, 204)
	call("DELETE", path, nil, 204)
	for name, query := range map[string]string{
		"rootSQL":           "SELECT data FROM workspace_states WHERE workspace_key=?",
		"metadataSQL":       "SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key",
		"issueSQL":          "SELECT id,data FROM issue_records WHERE workspace_key=? ORDER BY id",
		"contentSQL":        "SELECT kind,resource_id,id,data FROM workspace_content_records WHERE workspace_key=? ORDER BY kind,resource_id,created_at,id",
		"searchSQL":         "SELECT issue_id,content FROM issue_search_documents WHERE workspace_key=? ORDER BY issue_id",
		"metadataSearchSQL": "SELECT field,record_key,content FROM metadata_search_documents WHERE workspace_key=? ORDER BY field,record_key",
		"auditSQL":          "SELECT seq,actor_type,actor_id,user_id,actor,action,resource_type,resource_id,result,metadata FROM audit_events WHERE workspace_id=? ORDER BY seq",
		"eventsSQL":         "SELECT event_type,aggregate_id,payload,previous_values FROM domain_events WHERE workspace_id=? ORDER BY created_at,id",
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
		for rows.Next() {
			values := make([]sql.NullString, len(columns))
			args := make([]any, len(columns))
			for i := range values {
				args[i] = &values[i]
			}
			if err := rows.Scan(args...); err != nil {
				t.Fatal(err)
			}
			record := make([]string, len(columns))
			for i, value := range values {
				record[i] = normalize([]byte(value.String))
			}
			if name == "metadataSQL" && (values[0].String == "projectUpdates" || values[0].String == "initiativeUpdates") {
				var update struct {
					Parent string `json:"parent"`
					Item   struct {
						ID string `json:"id"`
					} `json:"item"`
				}
				if err := json.Unmarshal([]byte(values[3].String), &update); err != nil {
					t.Fatal(err)
				}
				if update.Parent != "" {
					key := []byte("parent:" + update.Parent)
					if update.Item.ID != "" {
						key, _ = json.Marshal([]string{update.Parent, update.Item.ID})
					}
					if values[1].String != fmt.Sprintf("%x", sha256.Sum256(key)) {
						t.Fatal("invalid canonical split-update key")
					}
					record[1] = normalize(key)
				}
			}
			raw, _ := json.Marshal(record)
			outputs[name] = append(outputs[name], raw)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	result := map[string]string{}
	for name, values := range outputs {
		var normalized []string
		for _, raw := range values {
			normalized = append(normalized, normalize(raw))
		}
		if name == "metadataSQL" {
			slices.Sort(normalized)
		}
		raw, _ := json.Marshal(normalized)
		result[name] = string(raw)
	}
	if len(outputs["webhooks"]) == 0 {
		t.Fatal("no webhook oracle output")
	}
	if previousValues == 0 {
		t.Fatal("webhook oracle did not compare previous values")
	}
	return result
}

func TestScaleP16InvalidRecordKeysKeepBusinessErrors(t *testing.T) {
	db := newScaleP04Database(t)
	fixture, _ := db.repo.WorkspaceMetadata(db.key)
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, test := range []struct {
		name, id string
		input    domain.ProjectMutationInput
		want     error
	}{
		{"long-dependency", fixture.Projects[0].ID, domain.ProjectMutationInput{DependencyIDs: []string{strings.Repeat("x", 192)}}, errInvalid},
		{"nul-dependency", fixture.Projects[0].ID, domain.ProjectMutationInput{DependencyIDs: []string{"invalid\x00key"}}, errInvalid},
		{"long-target", strings.Repeat("x", 192), domain.ProjectMutationInput{}, errNotFound},
		{"nul-target", "invalid\x00key", domain.ProjectMutationInput{}, errNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, mode := range []string{"off", "on"} {
				t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", mode)
				ctx := store.ContextWithActor(t.Context(), fixture.Viewer)
				ctx = projectRecordScope(ctx, "project", "edit", test.id, test.input)
				err := db.repo.MutateWorkspace(ctx, db.key, "project.updated", test.id, test.input, func(data *domain.Bootstrap) error {
					project, err := fullProjectByID(data, test.id)
					if err != nil {
						return err
					}
					return applyProjectUpdate(data, project, test.input)
				})
				if !errors.Is(err, test.want) {
					t.Fatalf("mode=%s: got %v, want %v", mode, err, test.want)
				}
			}
		})
	}
}
