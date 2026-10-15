package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestTeamResourceMutationsAtScale times the team overview's resource writes
// (pin and unpin a document, add a link, move a resource, sections) through
// the HTTP handler against a workspace with many issues. These writes touch a
// few metadata records, so their cost must not grow with the issue count.
// Opt-in:
//
//	FLOW_SCALE_TEST=1 FLOW_SCALE_ISSUES=75000 go test ./cmd/server -run TestTeamResourceMutationsAtScale -v
func TestTeamResourceMutationsAtScale(t *testing.T) {
	if os.Getenv("FLOW_SCALE_TEST") != "1" {
		t.Skip("set FLOW_SCALE_TEST=1 to run the team resource mutation timing test")
	}
	count := 75000
	if value, err := strconv.Atoi(os.Getenv("FLOW_SCALE_ISSUES")); err == nil && value > 0 {
		count = value
	}
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := repository.Bootstrap()
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	seedScaleIssues(t, path, count)
	repository, err = store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	// Authenticated, as deployed: every request also runs the workspace
	// authorization projection.
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer api.Close()
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)

	timings := []scaleTiming{}
	timed := func(label, method, url string, input any) map[string]any {
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
		var response *http.Response
		var raw []byte
		elapsed, allocated, peak := measureRequest(func() {
			var err error
			if response, err = client.Do(request); err != nil {
				t.Fatal(err)
			}
			raw, _ = io.ReadAll(response.Body)
			response.Body.Close()
		})
		timings = append(timings, scaleTiming{label: label, elapsed: elapsed, status: response.StatusCode, allocated: allocated, peak: peak})
		if response.StatusCode >= 300 {
			t.Fatalf("%s: status %d: %.300s", label, response.StatusCode, raw)
		}
		decoded := map[string]any{}
		_ = json.Unmarshal(raw, &decoded)
		return decoded
	}
	id := func(value map[string]any) string { text, _ := value["id"].(string); return text }

	teamPath := "/api/teams/" + fixture.Teams[0].ID
	document := timed("document create", http.MethodPost, "/api/documents", map[string]any{"title": "Scale doc", "teamIds": []string{fixture.Teams[0].ID}})
	for i := range 3 {
		n := strconv.Itoa(i)
		pinned := timed("pin document", http.MethodPost, teamPath+"/resources", map[string]any{"resourceType": "document", "resourceId": id(document), "title": "Scale doc"})
		timed("GET resources", http.MethodGet, teamPath+"/resources", nil)
		timed("move resource", http.MethodPatch, teamPath+"/resources/"+id(pinned), map[string]any{"position": 10 + i})
		timed("unpin document", http.MethodDelete, teamPath+"/resources/"+id(pinned), nil)
		timed("GET resources", http.MethodGet, teamPath+"/resources", nil)
		section := timed("section create", http.MethodPost, teamPath+"/resource-sections", map[string]any{"name": "Scale section " + n})
		link := timed("pin link", http.MethodPost, teamPath+"/resources", map[string]any{"resourceType": "link", "url": "https://example.com/" + n, "title": "Example", "sectionId": id(section)})
		timed("section delete", http.MethodDelete, teamPath+"/resource-sections/"+id(section), nil)
		timed("remove link", http.MethodDelete, teamPath+"/resources/"+id(link), nil)
	}

	// For comparison: the workspace metadata reload a pin used to wait for.
	timed("GET issue-records/bootstrap", http.MethodGet, "/api/issue-records/bootstrap", nil)

	var summary strings.Builder
	for _, timing := range timings {
		fmt.Fprintf(&summary, "\n  %-20s %9.1f ms %9.1f MB alloc %9.1f MB peak  %d", timing.label, float64(timing.elapsed.Microseconds())/1000, float64(timing.allocated)/(1<<20), float64(timing.peak)/(1<<20), timing.status)
	}
	t.Logf("team resource timings at %d issues:%s", count, summary.String())
}
