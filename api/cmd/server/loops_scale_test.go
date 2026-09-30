package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// seedScaleIssues clones the fixture's first issue record count times with a
// direct INSERT so a large workspace takes seconds to build. The store must be
// closed while this runs; it reloads the rows when reopened.
func seedScaleIssues(t *testing.T, path string, count int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statements := []string{
		`CREATE TEMP TABLE scale_template AS SELECT * FROM issue_records WHERE workspace_key='test-workspace' ORDER BY collection_order LIMIT 1`,
		`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?)
		 INSERT INTO issue_records(workspace_key,id,identifier,team_id,state_id,state_type,project_id,assignee_id,creator_id,cycle_id,parent_id,priority,sort_order,title,created_at,updated_at,archived,version,collection_order,data)
		 SELECT t.workspace_key,'scale_'||x,'SCL-'||x,t.team_id,t.state_id,t.state_type,'',t.assignee_id,t.creator_id,'','',t.priority,x,'Scale issue '||x,t.created_at,t.updated_at,0,1,100000+x,
		  json_set(t.data,'$.id','scale_'||x,'$.identifier','SCL-'||x,'$.number',x,'$.title','Scale issue '||x,'$.parentId',NULL,'$.projectId',NULL,'$.cycleId',NULL)
		 FROM n, scale_template t`,
		`UPDATE issue_collection_counts SET total=(SELECT COUNT(*) FROM issue_records WHERE workspace_key='test-workspace') WHERE workspace_key='test-workspace'`,
	}
	for index, statement := range statements {
		var err error
		if index == 1 {
			_, err = db.Exec(statement, count)
		} else {
			_, err = db.Exec(statement)
		}
		if err != nil {
			t.Fatalf("seed statement %d: %v", index, err)
		}
	}
}

// TestLoopEndpointsAtScale times the Loops endpoints against a workspace with
// 100,000 issues. It is opt-in: FLOW_SCALE_TEST=1 go test ./cmd/server -run TestLoopEndpointsAtScale -v
func TestLoopEndpointsAtScale(t *testing.T) {
	if os.Getenv("FLOW_SCALE_TEST") != "1" {
		t.Skip("set FLOW_SCALE_TEST=1 to run the 100k-issue loop timing test")
	}
	count := 100000
	if value, err := strconv.Atoi(os.Getenv("FLOW_SCALE_ISSUES")); err == nil && value > 0 {
		count = value
	}
	for _, auth := range []bool{false, true} {
		name := "authDisabled"
		if auth {
			name = "authEnabled"
		}
		t.Run(name, func(t *testing.T) { runLoopScale(t, count, auth) })
	}
}

func runLoopScale(t *testing.T, count int, auth bool) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	seedScaleIssues(t, path, count)
	repository, err = store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	t.Logf("seeded %d issues and reopened in %s", count, time.Since(started).Round(time.Millisecond))
	if auth {
		t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	}
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: !auth}))
	defer api.Close()
	client := authClient(t)
	if auth {
		authRequest[domain.AuthSession](t, client, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	}
	timed := func(label, method, url string, input any, want int) []byte {
		t.Helper()
		var body io.Reader
		if input != nil {
			raw, _ := json.Marshal(input)
			body = bytes.NewReader(raw)
		}
		request, _ := http.NewRequest(method, api.URL+url, body)
		request.Header.Set("X-Workspace-Key", "test-workspace")
		if input != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		begin := time.Now()
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		elapsed := time.Since(begin)
		if response.StatusCode != want {
			t.Fatalf("%s: status %d want %d: %.300s", label, response.StatusCode, want, raw)
		}
		t.Logf("%-28s %10s %10d bytes", label, elapsed.Round(time.Microsecond), len(raw))
		return raw
	}
	decode := func(raw []byte) domain.Loop {
		var loop domain.Loop
		if err := json.Unmarshal(raw, &loop); err != nil {
			t.Fatal(err)
		}
		return loop
	}
	// A few existing loops, including event-triggered ones (they make every
	// mutation compute webhook previous values).
	ids := []string{}
	for index := 0; index < 5; index++ {
		trigger := map[string]any{"triggerType": "issue", "triggerConfig": map[string]any{"event": "created"}}
		if index%2 == 0 {
			trigger = map[string]any{"triggerType": "schedule"}
		}
		input := map[string]any{"name": "Seed loop " + strconv.Itoa(index), "instructions": "Triage SCL-42 and SCL-" + strconv.Itoa(count) + " daily.", "status": "published"}
		for key, value := range trigger {
			input[key] = value
		}
		ids = append(ids, decode(timed("POST /api/loops (seed)", http.MethodPost, "/api/loops", input, http.StatusCreated)).ID)
	}
	created := decode(timed("POST /api/loops (draft)", http.MethodPost, "/api/loops", map[string]any{"name": "Timing"}, http.StatusCreated))
	published := decode(timed("POST /api/loops (published)", http.MethodPost, "/api/loops", map[string]any{"name": "Published", "status": "published", "instructions": "Review SCL-7 weekly.", "triggerType": "schedule"}, http.StatusCreated))
	if !bytes.Contains(mustJSON(t, published.InstructionsData), []byte(`"SCL-7"`)) {
		t.Fatalf("issue reference was not resolved into a mention: %s", mustJSON(t, published.InstructionsData))
	}
	timed("PATCH /api/loops/{id} name", http.MethodPatch, "/api/loops/"+created.ID, map[string]any{"name": "Timing renamed"}, http.StatusOK)
	timed("PATCH /api/loops/{id} instr", http.MethodPatch, "/api/loops/"+published.ID, map[string]any{"instructions": "Review SCL-8 weekly."}, http.StatusOK)
	timed("PATCH /api/loops/{id} toggle", http.MethodPatch, "/api/loops/"+ids[1], map[string]any{"enabled": false}, http.StatusOK)
	duplicate := decode(timed("POST /api/loops/{id}/duplicate", http.MethodPost, "/api/loops/"+ids[0]+"/duplicate", nil, http.StatusCreated))
	timed("GET /api/loops", http.MethodGet, "/api/loops", nil, http.StatusOK)
	timed("GET /api/loops/{id}", http.MethodGet, "/api/loops/"+published.ID, nil, http.StatusOK)
	timed("GET /api/loops/{id}/runs", http.MethodGet, "/api/loops/"+published.ID+"/runs", nil, http.StatusOK)
	timed("GET /api/loops/{id}/versions", http.MethodGet, "/api/loops/"+published.ID+"/versions", nil, http.StatusOK)
	timed("DELETE /api/loops/{id}", http.MethodDelete, "/api/loops/"+duplicate.ID, nil, http.StatusNoContent)
	timed("DELETE /api/loops/{id} (2)", http.MethodDelete, "/api/loops/"+created.ID, nil, http.StatusNoContent)
	timed("GET /api/bootstrap", http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
}


// Loop mutations run on workspace metadata without the issue collection;
// issue identifiers in markdown instructions still become mention chips.
func TestLoopMarkdownInstructionsResolveIssueMentions(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Issues) == 0 {
		t.Skip("fixture has no issues")
	}
	issue := bootstrap.Issues[0]
	created := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Mentions", "status": "published", "triggerType": "schedule", "instructions": "Review " + issue.Identifier + " and NOPE-99999."}, http.StatusCreated)
	mentions := loopMentionsIn(created.InstructionsData)
	if len(mentions) != 1 || mentions[0]["id"] != issue.ID || mentions[0]["label"] != issue.Identifier {
		t.Fatalf("create mentions = %v", mentions)
	}
	updated := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+created.ID, map[string]any{"instructions": "Link [it](/" + bootstrap.Workspace.URLKey + "/issue/" + issue.Identifier + ") please."}, http.StatusOK)
	mentions = loopMentionsIn(updated.InstructionsData)
	if len(mentions) != 1 || mentions[0]["id"] != issue.ID {
		t.Fatalf("update mentions = %v", mentions)
	}
}
