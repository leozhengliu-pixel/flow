package main

import (
	"context"
	"net/http"
	"path/filepath"
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
}

// A PATCH that repeats the current values writes nothing: same version and
// updatedAt, no domain event, no realtime push. A real change still writes
// once, and a stale expectedVersion is a 409 even when the values match.
func TestRepeatedPatchWritesNothing(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, authDisabled: true, uploadPath: t.TempDir()}
	handler := newHandler(s)
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

	document := create("/api/documents", map[string]any{"title": "Unchanged writes"})
	issue := create("/api/issue-records", map[string]any{"title": "Unchanged writes", "teamId": team.ID})
	project := create("/api/projects", map[string]any{"name": "Unchanged writes", "teamIds": []string{team.ID}})
	initiative := create("/api/initiatives", map[string]any{"name": "Unchanged writes"})
	label := create("/api/labels", map[string]any{"name": "Unchanged writes"})
	view := create("/api/views", map[string]any{"name": "Unchanged writes"})
	issueComment := create("/api/issues/"+id(issue)+"/comments", map[string]any{"body": "First"})
	documentComment := create("/api/documents/"+id(document)+"/comments", map[string]any{"body": "First"})
	projectComment := create("/api/projects/"+id(project)+"/comments", map[string]any{"body": "First"})
	initiativeComment := create("/api/initiatives/"+id(initiative)+"/comments", map[string]any{"body": "First"})
	milestone := create("/api/projects/"+id(project)+"/milestones", map[string]any{"name": "Unchanged writes"})
	customer := create("/api/customers", map[string]any{"name": "Unchanged writes"})
	teamLabel := create("/api/teams/"+team.ID+"/labels", map[string]any{"name": "Unchanged writes"})
	labelGroup := create("/api/label-groups", map[string]any{"name": "Unchanged writes"})
	state := data.States[0]

	cases := []struct {
		name, path string
		change     map[string]any
		versioned  bool
	}{
		{"document", "/api/documents/" + id(document), map[string]any{"title": "Renamed", "content": "Body", "archived": false}, true},
		{"issue", "/api/issue-records/" + id(issue), map[string]any{"title": "Renamed", "priority": 2, "estimate": 3, "labelIds": []string{}}, true},
		{"issue comment", "/api/issues/" + id(issue) + "/comments/" + id(issueComment), map[string]any{"body": "Edited"}, true},
		{"issue comment resolve", "/api/issues/" + id(issue) + "/comments/" + id(issueComment), map[string]any{"resolved": true}, true},
		{"document comment", "/api/documents/" + id(document) + "/comments/" + id(documentComment), map[string]any{"body": "Edited"}, true},
		{"project comment", "/api/projects/" + id(project) + "/comments/" + id(projectComment), map[string]any{"body": "Edited"}, true},
		{"initiative comment", "/api/initiatives/" + id(initiative) + "/comments/" + id(initiativeComment), map[string]any{"body": "Edited"}, true},
		{"project", "/api/projects/" + id(project), map[string]any{"name": "Renamed", "description": "Why", "priority": 2, "archived": false, "memberIds": []string{}}, false},
		{"initiative", "/api/initiatives/" + id(initiative), map[string]any{"name": "Renamed", "description": "Why", "status": "planned"}, false},
		{"label", "/api/labels/" + id(label), map[string]any{"name": "Renamed", "color": "#123456"}, false},
		{"view", "/api/views/" + id(view), map[string]any{"name": "Renamed", "filters": []any{}}, false},
		{"team", "/api/workspaces/" + data.Workspace.URLKey + "/teams/" + team.ID, map[string]any{"name": "Renamed team", "color": "#123456"}, false},
		{"milestone", "/api/projects/" + id(project) + "/milestones/" + id(milestone), map[string]any{"name": "Renamed", "description": "Why"}, false},
		{"customer", "/api/customers/" + id(customer), map[string]any{"name": "Renamed"}, false},
		{"team label", "/api/teams/" + team.ID + "/labels/" + id(teamLabel), map[string]any{"name": "Renamed", "color": "#123456"}, false},
		{"label group", "/api/label-groups/" + id(labelGroup), map[string]any{"name": "Renamed", "description": "Why"}, false},
		{"workflow state", "/api/teams/" + team.ID + "/states/" + state.ID, map[string]any{"name": "Renamed state", "type": state.Type}, false},
	}
	if len(data.Cycles) == 0 {
		t.Fatal("the fixture has no cycle")
	}
	cases = append(cases, struct {
		name, path string
		change     map[string]any
		versioned  bool
	}{"cycle", "/api/cycles/" + data.Cycles[0].ID, map[string]any{"name": "Renamed", "description": "Why"}, false})
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			events, pushes := counts()
			changed := requestJSON[map[string]any](t, handler, http.MethodPatch, item.path, item.change, http.StatusOK)
			afterEvents, afterPushes := counts()
			if afterEvents != events+1 || afterPushes != pushes+1 {
				t.Fatalf("a change wrote %d events and %d pushes, want 1 each", afterEvents-events, afterPushes-pushes)
			}
			repeated := requestJSON[map[string]any](t, handler, http.MethodPatch, item.path, item.change, http.StatusOK)
			if finalEvents, finalPushes := counts(); finalEvents != afterEvents || finalPushes != afterPushes {
				t.Fatalf("a repeated PATCH wrote %d events and %d pushes", finalEvents-afterEvents, finalPushes-afterPushes)
			}
			if repeated["version"] != changed["version"] || repeated["updatedAt"] != changed["updatedAt"] || repeated["editedAt"] != changed["editedAt"] {
				t.Fatalf("a repeated PATCH moved version %v -> %v, updatedAt %v -> %v", changed["version"], repeated["version"], changed["updatedAt"], repeated["updatedAt"])
			}
			if !item.versioned {
				return
			}
			version := changed["version"].(float64)
			stale := map[string]any{"expectedVersion": version - 1}
			for key, value := range item.change {
				stale[key] = value
			}
			requestJSON[map[string]any](t, handler, http.MethodPatch, item.path, stale, http.StatusConflict)
			current := map[string]any{"expectedVersion": version}
			for key, value := range item.change {
				current[key] = value
			}
			if same := requestJSON[map[string]any](t, handler, http.MethodPatch, item.path, current, http.StatusOK); same["version"] != changed["version"] {
				t.Fatalf("a matching expectedVersion moved the version to %v", same["version"])
			}
		})
	}
}
