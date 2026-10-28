package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func postWorkspace(handler http.Handler, name, urlKey string) (int, string) {
	raw, _ := json.Marshal(map[string]string{"name": name, "urlKey": urlKey, "region": "us"})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder.Code, recorder.Body.String()
}

// The browser suite creates e2e-core-desktop and e2e-core-mobile at the same
// moment; two simultaneous creations must both succeed and must not share ids.
func TestConcurrentWorkspaceCreationsAllSucceed(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	const workspaces = 8
	var wg sync.WaitGroup
	statuses := make([]int, workspaces)
	bodies := make([]string, workspaces)
	start := make(chan struct{})
	for i := 0; i < workspaces; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			statuses[i], bodies[i] = postWorkspace(handler, "Core workflow workspace", fmt.Sprintf("e2e-core-%d", i))
		}(i)
	}
	close(start)
	wg.Wait()

	ids := map[string]string{}
	for i := 0; i < workspaces; i++ {
		if statuses[i] != http.StatusCreated {
			t.Fatalf("workspace %d: status %d, want 201: %s", i, statuses[i], bodies[i])
		}
		var created domain.Bootstrap
		if err := json.Unmarshal([]byte(bodies[i]), &created); err != nil {
			t.Fatal(err)
		}
		values := []string{created.Workspace.ID}
		for _, team := range created.Teams {
			values = append(values, team.ID)
		}
		for _, id := range values {
			if other, ok := ids[id]; ok {
				t.Fatalf("id %q shared by %s and e2e-core-%d", id, other, i)
			}
			ids[id] = fmt.Sprintf("e2e-core-%d", i)
		}
	}
}

// A test retry (or a second client) creating a workspace whose URL is taken is a
// conflict the caller can recover from, not an internal error.
func TestWorkspaceCreationWithTakenKeyIsAConflict(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	if status, body := postWorkspace(handler, "First", "taken-key"); status != http.StatusCreated {
		t.Fatalf("first create status %d: %s", status, body)
	}
	status, body := postWorkspace(handler, "Second", "taken-key")
	if status != http.StatusConflict {
		t.Fatalf("duplicate create status %d, want 409: %s", status, body)
	}
	if status, body = postWorkspace(handler, "Third", "free-key"); status != http.StatusCreated {
		t.Fatalf("create after conflict status %d: %s", status, body)
	}
}
