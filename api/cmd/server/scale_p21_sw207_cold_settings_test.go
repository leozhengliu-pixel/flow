package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW207Outcome struct {
	Code                            int
	Header                          http.Header
	Response                        json.RawMessage
	Root, Metadata, Issues, Content [][]any
	Events, Webhooks, Realtime      []json.RawMessage
}

func scaleP21SW207Normalize(t *testing.T, value any, generated string, started, finished time.Time) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result any
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	var walk func(any) any
	walk = func(value any) any {
		switch item := value.(type) {
		case string:
			if generated != "" && item == generated {
				return "<generated-settings-record>"
			}
			if at, err := time.Parse(time.RFC3339Nano, item); err == nil && !at.Before(started) && !at.After(finished) {
				return "<generated-clock>"
			}
		case []any:
			for index := range item {
				item[index] = walk(item[index])
			}
		case map[string]any:
			for key := range item {
				item[key] = walk(item[key])
			}
		}
		return value
	}
	return walk(result)
}

func scaleP21SW207Rows(t *testing.T, f scaleP21SW10Fixture, query string, arg any, jsonColumns ...int) [][]any {
	t.Helper()
	rows := (&scaleP21TriageWriterFixture{reader: f.reader}).rows(t, query, arg)
	for _, row := range rows {
		for _, column := range jsonColumns {
			switch raw := row[column].(type) {
			case string:
				row[column] = json.RawMessage(raw)
			case nil:
			default:
				t.Fatalf("unexpected JSON SQL value %T", raw)
			}
		}
	}
	return rows
}

func scaleP21SW207State(t *testing.T, f scaleP21SW10Fixture) scaleP21SW207Outcome {
	t.Helper()
	key := f.data.Workspace.URLKey
	return scaleP21SW207Outcome{
		Root:     scaleP21SW207Rows(t, f, `SELECT data FROM workspace_states WHERE workspace_key=?`, key, 0),
		Metadata: scaleP21SW207Rows(t, f, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key`, key, 3),
		Issues:   scaleP21SW207Rows(t, f, `SELECT id,version,collection_order,data FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`, key, 3),
		Content:  scaleP21SW207Rows(t, f, `SELECT kind,resource_id,id,created_at,owner_id,parent_id,status,next_attempt_at,data FROM workspace_content_records WHERE workspace_key=? ORDER BY kind,resource_id,created_at,id`, key, 8),
	}
}

func scaleP21SW207RestoreOrders(t *testing.T, f scaleP21SW10Fixture, orders [][]any) {
	t.Helper()
	query := `UPDATE workspace_metadata_records SET collection_order=? WHERE workspace_key=? AND field=? AND record_key=?`
	if f.reader.config.Driver == "postgres" || f.reader.config.Driver == "postgresql" {
		query = `UPDATE workspace_metadata_records SET collection_order=$1 WHERE workspace_key=$2 AND field=$3 AND record_key=$4`
	}
	tx, err := f.reader.reader.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, row := range orders {
		if _, err := tx.ExecContext(t.Context(), query, row[2], f.data.Workspace.URLKey, row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW207Serve(t *testing.T, source *store.SQLiteStore, workspace, viewer, session, method, path, body string, modify func(context.Context) context.Context) *httptest.ResponseRecorder {
	t.Helper()
	service := &server{store: source}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/project-statuses", service.createProjectStatus)
	mux.HandleFunc("PATCH /api/project-statuses/{id}", service.updateProjectStatus)
	mux.HandleFunc("DELETE /api/project-statuses/{id}", service.deleteProjectStatus)
	mux.HandleFunc("POST /api/project-statuses/reorder", service.reorderProjectStatuses)
	mux.HandleFunc("POST /api/issue-templates", service.createWorkspaceIssueTemplate)
	mux.HandleFunc("PATCH /api/issue-templates/{id}", service.updateWorkspaceIssueTemplate)
	mux.HandleFunc("DELETE /api/issue-templates/{id}", service.deleteWorkspaceIssueTemplate)
	mux.HandleFunc("POST /api/document-templates", service.createDocumentTemplate)
	mux.HandleFunc("PATCH /api/document-templates/{id}", service.updateDocumentTemplate)
	mux.HandleFunc("DELETE /api/document-templates/{id}", service.deleteDocumentTemplate)
	var held *store.ColdMetadataRead
	var scope *requestResourceReads
	entered, outputChecks := false, 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered = true
		if authUser(r).ID != viewer {
			t.Fatal("real settings cookie authenticated the wrong viewer")
		}
		if source.BoundedMetadataEnabled() {
			route, selected := scaleP21ColdAuthorizationRoute(r)
			if !selected || !strings.HasPrefix(route.consumer, "server/settings_features.go:") {
				t.Fatal("settings authentication did not select its exact registered COLD route")
			}
			held, _ = r.Context().Value(coldAuthorizationReadKey{}).(*store.ColdMetadataRead)
			scope, _ = r.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
			if held == nil || held.Store() == nil || scope == nil || scope.closed {
				t.Fatal("settings handler lost authentication's COLD output ownership")
			}
		}
		mux.ServeHTTP(w, r)
	})
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Workspace-Key", workspace)
	request.Header.Set("X-Client-ID", "sw207-client")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	if modify != nil {
		request = request.WithContext(modify(request.Context()))
	}
	response := httptest.NewRecorder()
	writer := &scaleP21SW215ColdOutputWriter{ResponseRecorder: response, check: func() {
		outputChecks++
		if source.BoundedMetadataEnabled() && (held == nil || held.Store() == nil || scope == nil || scope.closed) {
			t.Error("settings response encoded after its COLD lifetime ended")
		}
	}}
	service.withColdMetadataRouteMux(mux, service.authenticate(next)).ServeHTTP(writer, request)
	if !entered || outputChecks == 0 {
		t.Fatal("settings route did not execute after real authentication")
	}
	if source.BoundedMetadataEnabled() && (held.Store() != nil || !scope.closed || source.MetadataWorkUsedForTesting() != 0) {
		t.Fatal("settings request retained COLD ownership after output")
	}
	return response
}

func TestScaleP21SW207ColdSettingsRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "on")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	_, session, err := repo.Login(t.Context(), f.data.Viewer.Email, "p03-fixture-password")
	if err != nil {
		t.Fatal("mint real settings fixture session", err)
	}
	prefix := idgen.New("sw207-")
	a, b, c := prefix+"planned-a", prefix+"planned-b", prefix+"started"
	issueTemplate, documentTemplate := prefix+"issue-template", prefix+"document-template"
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	reset := func() {
		t.Helper()
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw207_seed", "", nil, func(data *domain.Bootstrap) error {
			data.ProjectStatuses = []domain.ProjectStatus{
				{ID: a, Name: "Planned\x00 A", Type: "planned", Color: "#123456", Position: 0},
				{ID: b, Name: "Planned B", Type: "planned", Color: "#123456", Position: 1},
				{ID: c, Name: "Started", Type: "started", Color: "#123456", Position: 2},
			}
			data.Projects = slices.DeleteFunc(data.Projects, func(item domain.Project) bool { return item.ID == prefix+"project" })
			data.Projects = append(data.Projects, domain.Project{ID: prefix + "project", Name: "Status reference", TeamIDs: []string{f.public}, Status: data.ProjectStatuses[0], Description: strings.Repeat("unchanged project ", 30000)})
			data.IssueTemplates = []domain.IssueTemplate{
				{ID: issueTemplate, Name: "Before", Scope: "workspace", Creator: f.data.Viewer, Body: "keep\x00body", LabelIDs: []string{}, FormFields: []domain.TemplateFormField{}, CreatedAt: at, UpdatedAt: at},
				{ID: prefix + "team-template", Name: "Retain team", Scope: "team", TeamID: f.public, VisibilityTeamID: f.public, Creator: f.data.Viewer, Body: strings.Repeat("untouched template ", 20000), CreatedAt: at, UpdatedAt: at},
			}
			data.DocumentTemplates = []domain.DocumentTemplate{
				{ID: documentTemplate, Name: "Before", Creator: f.data.Viewer, Content: "keep\x00content", ContentData: map[string]any{"type": "doc", "retained": true}, CreatedAt: at, UpdatedAt: at},
				{ID: prefix + "other-document-template", Name: "Retained", TeamID: f.public, Creator: f.data.Viewer, Content: strings.Repeat("untouched template ", 20000), CreatedAt: at, UpdatedAt: at},
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
	}
	ordered, _ := json.Marshal(map[string]any{"ids": []string{b, a, c}})
	tests := []struct {
		name, method, path, body string
		handler                  func(*server, http.ResponseWriter, *http.Request)
		code, events             int
	}{
		{"status-create", http.MethodPost, "/api/project-statuses", `{"name":" New planned ","description":" Exact ","color":"#123456","type":"planned"}`, (*server).createProjectStatus, http.StatusCreated, 1},
		{"status-update-cascade", http.MethodPatch, "/api/project-statuses/" + a, `{"name":" After ","description":"new","color":"#654321"}`, (*server).updateProjectStatus, http.StatusOK, 1},
		{"status-delete", http.MethodDelete, "/api/project-statuses/" + b, "", (*server).deleteProjectStatus, http.StatusNoContent, 1},
		{"status-reorder-cascade", http.MethodPost, "/api/project-statuses/reorder", string(ordered), (*server).reorderProjectStatuses, http.StatusOK, 1},
		{"issue-template-create", http.MethodPost, "/api/issue-templates", `{"name":" New issue template ","title":"Prefix: ","body":"unchanged semantics"}`, (*server).createWorkspaceIssueTemplate, http.StatusCreated, 1},
		{"issue-template-update", http.MethodPatch, "/api/issue-templates/" + issueTemplate, `{"name":"After","title":"Prefix: ","body":"after","priority":2}`, (*server).updateWorkspaceIssueTemplate, http.StatusOK, 1},
		{"issue-template-delete", http.MethodDelete, "/api/issue-templates/" + issueTemplate, "", (*server).deleteWorkspaceIssueTemplate, http.StatusNoContent, 1},
		{"document-template-create", http.MethodPost, "/api/document-templates", `{"name":"New document template","title":"Title","content":"Body","contentState":"literal-state","contentData":{"type":"doc","content":[]}}`, (*server).createDocumentTemplate, http.StatusCreated, 1},
		{"document-template-update", http.MethodPatch, "/api/document-templates/" + documentTemplate, `{"name":"After","content":"after","contentData":{"retained":false},"icon":"memo","color":"#123456"}`, (*server).updateDocumentTemplate, http.StatusOK, 1},
		{"document-template-delete", http.MethodDelete, "/api/document-templates/" + documentTemplate, "", (*server).deleteDocumentTemplate, http.StatusNoContent, 1},
		{"status-bad-json", http.MethodPost, "/api/project-statuses", `{`, (*server).createProjectStatus, http.StatusBadRequest, 0},
		{"status-invalid-type", http.MethodPost, "/api/project-statuses", `{"name":"New","type":"unknown"}`, (*server).createProjectStatus, http.StatusBadRequest, 0},
		{"status-type-immutable", http.MethodPatch, "/api/project-statuses/" + a, `{"type":"started"}`, (*server).updateProjectStatus, http.StatusBadRequest, 0},
		{"status-still-used", http.MethodDelete, "/api/project-statuses/" + a, "", (*server).deleteProjectStatus, http.StatusBadRequest, 0},
		{"status-last-of-type", http.MethodDelete, "/api/project-statuses/" + c, "", (*server).deleteProjectStatus, http.StatusBadRequest, 0},
		{"status-bad-order", http.MethodPost, "/api/project-statuses/reorder", `{"ids":[]}`, (*server).reorderProjectStatuses, http.StatusBadRequest, 0},
		{"issue-template-empty-name", http.MethodPost, "/api/issue-templates", `{"name":" "}`, (*server).createWorkspaceIssueTemplate, http.StatusBadRequest, 0},
		{"issue-template-noop", http.MethodPatch, "/api/issue-templates/" + issueTemplate, `{}`, (*server).updateWorkspaceIssueTemplate, http.StatusOK, 0},
		{"issue-template-private-team", http.MethodDelete, "/api/issue-templates/" + prefix + "team-template", "", (*server).deleteWorkspaceIssueTemplate, http.StatusNotFound, 0},
		{"document-template-invalid-team", http.MethodPatch, "/api/document-templates/" + documentTemplate, `{"teamId":"missing"}`, (*server).updateDocumentTemplate, http.StatusBadRequest, 0},
		{"document-template-missing", http.MethodDelete, "/api/document-templates/" + prefix + "missing", "", (*server).deleteDocumentTemplate, http.StatusNotFound, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var expected any
			var expectedOrders [][]any
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				reset()
				orders := scaleP21SW207Rows(t, f, `SELECT field,record_key,collection_order FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key`, workspace)
				if source == repo {
					expectedOrders = orders
				} else {
					// Delta reset preserves holes and appends restored rows;
					// restore only the fixture's exact pre-request ordinals.
					scaleP21SW207RestoreOrders(t, f, expectedOrders)
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					orders = scaleP21SW207Rows(t, f, `SELECT field,record_key,collection_order FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key`, workspace)
					if !reflect.DeepEqual(expectedOrders, orders) {
						t.Fatal("literal and bounded settings legs began with different canonical collection orders")
					}
				}
				prior := map[any]bool{}
				for _, row := range scaleP21SW207Rows(t, f, `SELECT id FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID) {
					prior[row[0]] = true
				}
				got := scaleP21SW207Outcome{}
				source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
					got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, map[string]any{"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID, "payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot}))
				})
				source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
					got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, map[string]any{"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID, "clientId": event.ClientID, "payload": event.Payload}))
				})
				started := time.Now().UTC()
				response := scaleP21SW207Serve(t, source, workspace, f.data.Viewer.ID, session, test.method, test.path, test.body, nil)
				finished := time.Now().UTC()
				source.SetWebhookSink(nil)
				source.SetRealtimeSink(nil)
				state := scaleP21SW207State(t, f)
				state.Code, state.Header = response.Code, response.Header().Clone()
				if response.Body.Len() != 0 {
					state.Response = bytes.Clone(response.Body.Bytes())
				}
				state.Webhooks, state.Realtime = got.Webhooks, got.Realtime
				for _, row := range scaleP21SW207Rows(t, f, `SELECT id,event_type,aggregate_id,payload,previous_values FROM domain_events WHERE workspace_id=? ORDER BY created_at,id`, f.data.Workspace.ID, 3, 4) {
					if !prior[row[0]] {
						state.Events = append(state.Events, scaleP21SW106Envelope(t, row[1:]))
					}
				}
				if state.Code != test.code || len(state.Events) != test.events || len(state.Webhooks) != test.events || len(state.Realtime) != test.events {
					t.Fatalf("bounded=%t status=%d want=%d events=%d/%d/%d want=%d response=%s", source.BoundedMetadataEnabled(), state.Code, test.code, len(state.Events), len(state.Webhooks), len(state.Realtime), test.events, response.Body.String())
				}
				generated := ""
				if test.code == http.StatusCreated {
					var item struct {
						ID string `json:"id"`
					}
					if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil || item.ID == "" {
						t.Fatal("successful create missing generated identity", err)
					}
					generated = item.ID
				}
				actual := scaleP21SW207Normalize(t, state, generated, started, finished)
				if source == repo {
					expected = actual
				} else if !reflect.DeepEqual(expected, actual) {
					t.Fatalf("cold settings output/effects differ: %s", scaleP21M2Diff(expected, actual))
				}
				if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
					t.Fatal("cold settings route leaked output or entered HOT hydration")
				}
			}
		})
	}
	reset()
	// These contexts select unsupported writer families. The route bridge must
	// pass them intact so the proven dispatch refuses before its callback.
	for _, test := range []struct {
		name    string
		context func(context.Context) context.Context
	}{
		{"mutation-scope", func(ctx context.Context) context.Context { return store.WithMutationScope(ctx, store.MutationScope{}) }},
		{"issue-record", func(ctx context.Context) context.Context { return store.WithIssueRecordMutations(ctx) }},
		{"project-record", func(ctx context.Context) context.Context {
			return store.WithProjectRecordSelection(ctx, func(domain.Bootstrap) (store.MetadataRecordScope, bool) {
				t.Error("unsupported selector invoked")
				return store.MetadataRecordScope{}, true
			})
		}},
		{"verification", func(ctx context.Context) context.Context { return store.WithMetadataRecordVerification(ctx) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := scaleP21SW207State(t, f)
			response := scaleP21SW207Serve(t, f.probe, workspace, f.data.Viewer.ID, session, http.MethodPatch, "/api/project-statuses/"+a, `{"name":"must not write"}`, test.context)
			if response.Code < 400 || !strings.Contains(response.Body.String(), store.ErrBoundedMetadataCoverage.Error()) || !reflect.DeepEqual(before, scaleP21SW207State(t, f)) {
				t.Fatal("unsupported context lost its refusal or changed metadata", response.Code, response.Body.String())
			}
		})
	}
}
