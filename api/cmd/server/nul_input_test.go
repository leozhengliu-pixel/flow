package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestJSONNULReaderDropsOnlyNULEscapes(t *testing.T) {
	for _, test := range []struct{ in, want string }{
		{`{"a":"x\u0000y"}`, `{"a":"xy"}`},
		{`{"a":"\u0000\u0000"}`, `{"a":""}`},
		{`{"a":"\\u0000"}`, `{"a":"\\u0000"}`},           // an escaped backslash, then text
		{`{"a":"\\\u0000"}`, `{"a":"\\"}`},               // an escaped backslash, then NUL
		{`{"a":"\u00001"}`, `{"a":"1"}`},                 // NUL, then text
		{`{"a":"\u0001é\"\u0000"}`, `{"a":"\u0001é\""}`}, // other escapes kept
		{`{"\u0000k":["\u0000",1]}`, `{"k":["",1]}`},
		{`{"a":"x\u000`, `{"a":"x\u000`}, // a cut escape is passed on for the decoder to refuse
	} {
		for _, size := range []int{1, 2, 3, 7, 4096} {
			reader := &jsonNULReader{source: &chunkedReader{data: []byte(test.in), size: size}}
			got, err := io.ReadAll(reader)
			if err != nil || string(got) != test.want {
				t.Errorf("chunks of %d: %s = %s (%v), want %s", size, test.in, got, err, test.want)
			}
		}
		if got := string(store.StripJSONNUL([]byte(test.in))); got != test.want && json.Valid([]byte(test.in)) {
			t.Errorf("StripJSONNUL(%s) = %s, want %s", test.in, got, test.want)
		}
	}
}

type chunkedReader struct {
	data []byte
	size int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p[:min(len(p), r.size)], r.data)
	r.data = r.data[n:]
	return n, nil
}

// Postgres refuses NUL in text and varchar values and \u0000 in JSON it
// parses. Text with NUL sent through the API, MCP, SCIM, search and CSV
// imports is stored without it (2xx), never a 500, on SQLite and on the
// MySQL or Postgres database named by FLOW_TEST_DATABASE_DRIVER/URL; reads
// that parse stored JSON in SQL keep working afterwards.
func TestNULInputIsStoredWithoutNUL(t *testing.T) {
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testNULInput(t, repo) })
	}
}

func testNULInput(t *testing.T, repo *store.SQLiteStore) {
	service := &server{store: repo, uploadPath: t.TempDir(), authDisabled: true}
	service.agent.ToolsEnabled, service.agent.WriteTools = true, true
	handler := newHandler(service)
	data := repo.Bootstrap()
	workspace := data.Workspace.URLKey
	team := data.Teams[0].ID
	suffix := fmt.Sprint(time.Now().UnixNano())
	ok := func(what string, status int, body map[string]any) {
		t.Helper()
		if status >= 300 {
			t.Fatalf("%s = %d %v, want 2xx", what, status, body)
		}
		raw, _ := json.Marshal(body)
		if bytes.Contains(raw, []byte(`\u0000`)) {
			t.Fatalf("%s answered with NUL: %s", what, raw)
		}
	}

	status, issue := limitCall(t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Nul\x00 title " + suffix, "description": "line\x00break", "teamId": team}, nil)
	ok("issue create", status, issue)
	if issue["title"] != "Nul title "+suffix || issue["description"] != "linebreak" {
		t.Fatalf("issue stored as %q / %q", issue["title"], issue["description"])
	}
	id, _ := issue["id"].(string)
	status, body := limitCall(t, handler, http.MethodPatch, "/api/issues/"+id, map[string]any{"title": "Nul\x00 again " + suffix}, nil)
	ok("issue update", status, body)
	status, body = limitCall(t, handler, http.MethodPost, "/api/issues/"+id+"/comments", map[string]any{"body": "comment\x00 body"}, nil)
	ok("comment", status, body)
	status, body = limitCall(t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Label\x00" + suffix}, nil)
	ok("label", status, body)
	status, body = limitCall(t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Project\x00" + suffix, "teamIds": []string{team}}, nil)
	ok("project", status, body)
	status, body = limitCall(t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Doc\x00" + suffix, "content": "# Doc\n\nbody\x00text"}, nil)
	ok("document", status, body)
	key := "N" + suffix[len(suffix)-4:]
	status, body = limitCall(t, handler, http.MethodPost, "/api/workspaces/"+workspace+"/teams", map[string]any{"name": "Team\x00" + suffix, "key": key[:2] + "\x00" + key[2:]}, nil)
	if status >= 500 {
		t.Fatalf("team = %d %v", status, body)
	}

	// Query strings are not JSON: the store drops NUL before Postgres sees it.
	status, body = limitCall(t, handler, http.MethodGet, "/api/search?q="+url.QueryEscape("Nul\x00 title"), nil, nil)
	ok("search", status, body)
	status, body = limitCall(t, handler, http.MethodGet, "/api/issue-records?limit=5&q="+url.QueryEscape("a\x00b"), nil, nil)
	ok("issue records text query", status, body)

	// MCP tools, as the built-in agent and MCP clients call them.
	ctx := context.WithValue(context.Background(), authUserContextKey{}, data.Viewer)
	ctx = store.ContextWithActor(ctx, data.Viewer)
	request := httptest.NewRequest(http.MethodPost, "/api/agent?workspace="+workspace, nil).WithContext(ctx)
	saved, err := service.executeAgentTool(request, repo.Bootstrap(), domain.AgentToolCall{Name: "save_issue", Arguments: json.RawMessage(`{"title":"Agent\u0000 issue ` + suffix + `","team":"` + team + `","description":"agent\u0000text"}`)})
	if err != nil || bytes.Contains(saved, []byte(`\u0000`)) {
		t.Fatalf("save_issue = %s %v", saved, err)
	}

	// SCIM.
	token := requestJSON[store.SCIMToken](t, handler, http.MethodPost, "/api/scim/tokens?workspace="+workspace, map[string]any{"name": "idp"}, http.StatusCreated)
	status, body = limitCall(t, handler, http.MethodPost, "/scim/v2/"+workspace+"/Users", map[string]any{"schemas": []string{scimSchemaUser}, "userName": "nul\x00user-" + suffix, "displayName": "Nul\x00 Person"}, map[string]string{"Authorization": "Bearer " + token.Secret})
	ok("SCIM user", status, body)
	if body["userName"] != "nuluser-"+suffix {
		t.Fatalf("SCIM userName stored as %q", body["userName"])
	}

	// CSV import (multipart, not JSON): text columns and the issue JSON lose NUL.
	job := previewImportRequest(t, handler, "nul.csv", "Title,Description\nCSV\x00 row "+suffix+",csv\x00description\n")
	requestJSON[domain.ImportJob](t, handler, http.MethodPost, "/api/imports/"+job.ID+"/commit", map[string]any{"teamId": team, "mapping": map[string]string{"title": "Title", "description": "Description"}}, http.StatusAccepted)
	if job = waitForImport(t, handler, job.ID); job.Imported != 1 {
		t.Fatalf("CSV import = %d imported, errors %v", job.Imported, job.Errors)
	}

	// Reads that parse the stored issue JSON in SQL (descriptions in MCP
	// list_issues) still work with all of the above stored.
	listed, err := service.executeAgentTool(request, repo.Bootstrap(), domain.AgentToolCall{Name: "list_issues", Arguments: json.RawMessage(`{"limit":250}`)})
	if err != nil {
		t.Fatalf("list_issues = %v", err)
	}
	for _, want := range []string{"Nul again " + suffix, "Agent issue " + suffix, "CSV row " + suffix} {
		if !strings.Contains(string(listed), want) {
			t.Fatalf("list_issues lacks %q", want)
		}
	}
	if bytes.Contains(listed, []byte(`\u0000`)) {
		t.Fatal("list_issues answered with NUL")
	}
	status, body = limitCall(t, handler, http.MethodGet, "/api/search?q="+url.QueryEscape("CSV row "+suffix), nil, nil)
	ok("search after import", status, body)
}
