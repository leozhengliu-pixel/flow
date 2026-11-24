package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"flow/api/internal/store"
)

var metadataUpgradeFlags = []string{
	"FLOW_BOUNDED_METADATA", "FLOW_RECORD_HYDRATION", "FLOW_RECORD_JOURNAL", "FLOW_METADATA_CATALOG",
	"FLOW_SYNC_V2", "FLOW_SYNC_MANIFEST", "FLOW_INDEXED_READS", "FLOW_RECORD_PEER_SYNC",
}

func metadataUpgradeServer(t *testing.T, repo *store.SQLiteStore) *server {
	t.Helper()
	s := &server{store: repo, uploadPath: t.TempDir()}
	s.socketAccessSweepStarted.Store(true)
	s.workflowSchedulerStarted.Store(true)
	s.deliverySchedulerStarted.Store(true)
	s.loopReconcilerStarted.Store(true)
	s.agentRunReconcilerStarted.Store(true)
	s.agentLiveSweepStarted.Store(true)
	s.coordinationStarted.Store(true)
	return s
}

func TestDefaultMetadataUpgrade(t *testing.T) {
	for _, flag := range metadataUpgradeFlags {
		t.Setenv(flag, "off")
	}
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32))))
	config := metadataPrepareDatabase(t)
	config.FixtureProfile, config.FixturePassword = "test", "test-password"
	old, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if old != nil {
			_ = old.Close()
		}
	}()
	if old.BoundedMetadataEnabled() || old.RecordJournalReady() {
		t.Fatal("previous release fixture unexpectedly has bounded metadata or a journal")
	}
	_, token, err := old.Login(t.Context(), "admin@example.test", config.FixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	fixture := old.Bootstrap()
	sweep := &sw530Sweep{workspace: fixture.Workspace.URLKey}
	owner := &sw530Principal{name: "owner", token: token}
	handler := func(repo *store.SQLiteStore) http.Handler {
		return newHandler(metadataUpgradeServer(t, repo))
	}
	before := handler(old)
	call := func(h http.Handler, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w, timeout := sweep.call(h, method, path, body, owner, false)
		if timeout || w.Code != want {
			t.Fatalf("%s fixture request returned %d, want %d (timed out=%t)", method, w.Code, want, timeout)
		}
		return w
	}
	create := func(path string, body map[string]any) string {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		w := call(before, http.MethodPost, path, string(raw), http.StatusCreated)
		var value struct{ ID string }
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil || value.ID == "" {
			t.Fatal("upgrade fixture response has no record ID")
		}
		return value.ID
	}
	team := fixture.Teams[0].ID
	project := create("/api/projects", map[string]any{"name": "Existing project", "description": "Project body \u65e7", "teamIds": []string{team}})
	issue := create("/api/issue-records", map[string]any{"title": "Before upgrade", "description": "Existing issue", "teamId": team, "projectId": project})
	document := create("/api/documents", map[string]any{"title": "Existing document", "content": "Document body", "projectId": project})
	create("/api/issue-records/"+issue+"/comments", map[string]any{"body": "Existing issue comment"})
	create("/api/projects/"+project+"/comments", map[string]any{"body": "Existing project comment"})
	create("/api/documents/"+document+"/comments", map[string]any{"body": "Existing document comment"})
	call(before, http.MethodPatch, "/api/account/settings", `{"homeView":"inbox"}`, http.StatusOK)
	call(before, http.MethodPatch, "/api/notification-preferences", `{"emailFormat":"immediate"}`, http.StatusOK)
	if status, err := old.MetadataCatalogStatus(t.Context(), sweep.workspace, "projects"); err != nil || status.Complete {
		t.Fatalf("previous release fixture unexpectedly has catalog coverage: %v", err)
	}

	// Establish the previous release's read behavior on the persisted data.
	// In particular its installed project list can differ right after a write
	// and after reloading the record-backed workspace.
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	old, err = store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	before = handler(old)
	paths := []string{
		"/api/bootstrap", "/api/issue-records/bootstrap", "/api/account/settings", "/api/notification-preferences", "/api/notifications",
		"/api/issue-records", "/api/issue-records?limit=1&includeTotal=true",
		"/api/issues?q=Before+upgrade&limit=1", "/api/issues?filter=" + url.QueryEscape(`{"field":"title","operator":"contains","value":"Before upgrade"}`),
		"/api/issue-records/" + issue, "/api/issue-records/" + issue + "/history", "/api/comments?issueId=" + issue,
		"/api/projects", "/api/projects?includeTotal=true&q=Existing",
		"/api/projects/" + project, "/api/projects/" + project + "/history", "/api/comments?projectId=" + project,
		"/api/documents", "/api/documents/" + document, "/api/documents/" + document + "/permissions", "/api/documents/" + document + "/comments",
		"/api/teams/" + team + "/resources", "/api/reviews",
	}
	read := func(h http.Handler, index int, path string) sw530Outcome {
		t.Helper()
		started := time.Now()
		w, timedOut := sweep.call(h, http.MethodGet, path, "", owner, false)
		if timedOut || w.Code != http.StatusOK {
			t.Fatalf("upgrade read %d returned %d (timed out=%t)", index, w.Code, timedOut)
		}
		body := w.Body.Bytes()
		if !json.Valid(body) {
			t.Fatal("upgrade read did not return JSON")
		}
		return sw530Outcome{
			Status: w.Code,
			Body:   sw530Normalize(sw530CanonicalJSON(body), started),
			Header: map[string]string{"Content-Type": w.Header().Get("Content-Type")},
		}
	}
	want := make([]sw530Outcome, len(paths))
	for i, path := range paths {
		want[i] = read(before, i, path)
		switch path {
		case "/api/account/settings":
			if !strings.Contains(want[i].Body, `"homeView":"inbox"`) {
				t.Fatal("legacy account setting did not survive reload")
			}
		case "/api/notification-preferences":
			if !strings.Contains(want[i].Body, `"emailFormat":"immediate"`) {
				t.Fatal("legacy notification preference did not survive reload")
			}
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	old = nil
	for _, flag := range metadataUpgradeFlags {
		t.Setenv(flag, "")
	}

	// The first replica prepares the original database. The second must
	// reuse the completed checkpoints while admitting its own resident state.
	type opened struct {
		repo *store.SQLiteStore
		err  error
	}
	results := make(chan opened, 2)
	for range 2 {
		go func() {
			repo, err := store.OpenDatabase(config)
			results <- opened{repo, err}
		}()
	}
	var replicas []*store.SQLiteStore
	t.Cleanup(func() {
		for _, repo := range replicas {
			_ = repo.Close()
		}
	})
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Errorf("open with defaults: %v", result.err)
			continue
		}
		replicas = append(replicas, result.repo)
	}
	if t.Failed() {
		t.FailNow()
	}
	for _, repo := range replicas {
		if !repo.BoundedMetadataEnabled() || !repo.RecordJournalReady() {
			t.Fatal("default startup did not admit the prepared database")
		}
		catalogs, err := repo.MetadataCatalogCompleteness(t.Context())
		if err != nil || len(catalogs) == 0 {
			t.Fatalf("catalog incomplete after default startup: %v", err)
		}
		for _, catalog := range catalogs {
			if catalog.Incomplete != 0 {
				t.Errorf("catalog has %d incomplete collections after default startup", catalog.Incomplete)
			}
		}
		after := handler(repo)
		for i, path := range paths {
			got := read(after, i, path)
			if kind, _, _ := sw530Differences(want[i], got); kind != "" {
				// Paths contain record IDs. Report the stable fixture ordinal.
				t.Errorf("legacy read %d changed %s after upgrade", i, kind)
			}
		}
		call(after, http.MethodGet, "/api/sync/manifest", "", http.StatusNotFound)
		call(after, http.MethodGet, "/api/sync/changes?after=0", "", http.StatusOK)
	}

	// The surviving data is writable through the same APIs and a second
	// replica can load the canonical results without another preparation.
	writer, reader := replicas[0], replicas[1]
	write := handler(writer)
	edits := []struct {
		path, field, value string
	}{
		{"/api/issue-records/" + issue, "title", "Issue after upgrade"},
		{"/api/projects/" + project, "name", "Project after upgrade"},
		{"/api/documents/" + document, "title", "Document after upgrade"},
	}
	for _, edit := range edits {
		raw, err := json.Marshal(map[string]string{edit.field: edit.value})
		if err != nil {
			t.Fatal(err)
		}
		call(write, http.MethodPatch, edit.path, string(raw), http.StatusOK)
	}
	if err := reader.ReloadWorkspace(t.Context(), sweep.workspace); err != nil {
		t.Fatal("second replica could not load the updated workspace")
	}
	peer := handler(reader)
	for index, edit := range edits {
		want, got := read(write, index, edit.path), read(peer, index, edit.path)
		var body map[string]any
		if err := json.Unmarshal([]byte(got.Body), &body); err != nil || body[edit.field] != edit.value {
			t.Errorf("upgraded write %d was not visible to the second replica", index)
		}
		if kind, _, _ := sw530Differences(want, got); kind != "" {
			t.Errorf("upgraded read %d changed %s across replicas", index, kind)
		}
	}

	for _, repo := range replicas {
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
	}
	replicas = nil
	for _, flag := range metadataUpgradeFlags {
		t.Setenv(flag, "off")
	}
	rolledBack, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	defer rolledBack.Close()
	if rolledBack.BoundedMetadataEnabled() || rolledBack.RecordJournalReady() {
		t.Fatal("explicit off did not restore legacy operation")
	}
	legacy := handler(rolledBack)
	for index, edit := range edits {
		var body map[string]any
		got := read(legacy, index, edit.path)
		if err := json.Unmarshal([]byte(got.Body), &body); err != nil || body[edit.field] != edit.value {
			t.Errorf("legacy reads after rollback lost edit %d", index)
		}
	}
	call(legacy, http.MethodGet, "/api/sync/changes?after=0", "", http.StatusNotFound)
}
