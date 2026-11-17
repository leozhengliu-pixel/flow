package main

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP21SW186CreateOptions struct {
	authorization bool
	creationKey   string
}

func scaleP21SW186Create(t *testing.T, f scaleP21IssueUpdateFixture, service *server, user domain.User, key *domain.APIKey, body map[string]any, development, cancelled bool, options ...scaleP21SW186CreateOptions) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/issue-records", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
	r.SetPathValue("workspaceKey", f.data.Workspace.URLKey)
	r.SetPathValue("workspace", f.data.Workspace.URLKey)
	ctx := context.WithValue(r.Context(), authUserContextKey{}, user)
	if !development {
		ctx = store.ContextWithActor(ctx, user)
	}
	if key != nil {
		ctx = context.WithValue(ctx, apiKeyContextKey{}, *key)
	}
	if len(options) != 0 && options[0].creationKey != "" {
		ctx = store.WithIssueCreationKey(ctx, options[0].creationKey)
	}
	if cancelled {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}
	r, closeOutput := beginRequestResourceReads(r.WithContext(ctx))
	defer closeOutput()
	if service.authDisabled != development {
		service.authDisabled = development
	}
	w := httptest.NewRecorder()
	if len(options) != 0 && options[0].authorization {
		if _, err := service.buildIssueRecordAuthorizationData(r); err != nil {
			issueRecordsError(w, err)
			return w
		}
	}
	service.createIssueRecord(w, r)
	return w
}

func TestScaleP21SW186BorrowedQueryServerFields(t *testing.T) {
	wanted := map[string]bool{"issueRecordsQuery": false, "issueRecordAccess": false, "requestIssueQueryAccess": false}
	allowed := map[string]bool{
		"store": true, "authDisabled": true,
		"requestIssueQueryAccess": true, "issueRecordAccess": true,
	}
	allowedStore := map[string]bool{
		"BoundedMetadataEnabled": true, "WorkspaceMetadata": true,
		"IssueAccessMetadata": true, "IssueQueryAccess": true,
		// SW215 pins fresh cold reads in the existing legacy-only branches.
		"RefreshColdMetadataRead": true,
	}
	for _, path := range []string{"issue_records_api.go", "issue_request_access.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if _, exists := wanted[function.Name.Name]; !exists {
				continue
			}
			wanted[function.Name.Name] = true
			ast.Inspect(function.Body, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok {
					if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "s" && !allowed[selector.Sel.Name] {
						t.Errorf("%s reads unreviewed server field/helper %s; update the borrowed query dependency audit", function.Name.Name, selector.Sel.Name)
					}
					if receiver, ok := selector.X.(*ast.SelectorExpr); ok && receiver.Sel.Name == "store" {
						if server, ok := receiver.X.(*ast.Ident); ok && server.Name == "s" && !allowedStore[selector.Sel.Name] {
							t.Errorf("%s calls unreviewed store helper %s in its borrowed query", function.Name.Name, selector.Sel.Name)
						}
					}
				}
				if call, ok := node.(*ast.CallExpr); ok {
					for _, argument := range call.Args {
						if argument, ok := argument.(*ast.Ident); ok && argument.Name == "s" {
							t.Errorf("%s passes its server to an unreviewed transitive helper", function.Name.Name)
						}
					}
				}
				return true
			})
		}
	}
	for name, found := range wanted {
		if !found {
			t.Errorf("borrowed query dependency %s disappeared without updating its audit", name)
		}
	}
}

func TestScaleP21SW186CreationProjectionScope(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	for _, source := range []string{"warm", "reloaded"} {
		service := f.reset(t, source)
		for _, principal := range f.principals {
			t.Run(source+"/"+principal.name, func(t *testing.T) {
				read, found, err := service.store.BeginInstalledMetadataRead(t.Context(), f.data.Workspace.URLKey)
				if err != nil || !found {
					t.Fatal("installed projection", err)
				}
				defer read.Close()
				var oldSQL, oldScopeSQL, newSQL int
				old, err := read.ProjectedWorkspace(store.WithSQLStatementObserver(t.Context(), func(string) { oldSQL++ }), principal.user.ID)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				want, err := read.ViewerProjectionScope(store.WithSQLStatementObserver(t.Context(), func(string) { oldScopeSQL++ }), principal.user.ID)
				if err != nil {
					t.Fatal(err)
				}
				next, err := read.ProjectedWorkspaceWithScope(store.WithSQLStatementObserver(t.Context(), func(string) { newSQL++ }), principal.user.ID)
				if err != nil {
					t.Fatal(err)
				}
				defer next.Close()
				got, err := next.ProjectionScope(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(old.Data, next.Data) || !reflect.DeepEqual(want, got) {
					t.Fatal("capturing the scope changed the literal projection or its original scope")
				}
				if oldSQL == 0 || oldScopeSQL == 0 || newSQL != oldSQL {
					t.Fatalf("projection SQL before=%d+%d after=%d", oldSQL, oldScopeSQL, newSQL)
				}
				t.Logf("projection SQL before=%d+%d after=%d", oldSQL, oldScopeSQL, newSQL)
				cancelled, cancel := context.WithCancel(t.Context())
				cancel()
				if _, err := next.ProjectionScope(cancelled); err == nil {
					t.Fatal("cancelled scope succeeded")
				}
				next.Close()
				if _, err := next.ProjectionScope(t.Context()); err == nil {
					t.Fatal("released scope succeeded")
				}
			})
		}
		if service.store.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("creation scope retained work after its lifetime")
		}
	}
}

func scaleP21SW186Installed(t *testing.T, repo *store.SQLiteStore, workspace string) json.RawMessage {
	t.Helper()
	names := []string{"workflowDefinitions", "workflowRuns", "triageResponsibilities", "triageAssignments", "issueSlas", "slaEvents"}
	values := map[string]json.RawMessage{}
	if repo.BoundedMetadataEnabled() {
		read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("creation installed image", err)
		}
		defer read.Close()
		for _, name := range names {
			var output strings.Builder
			if err := read.StreamFieldJSON(t.Context(), name, &output); err != nil {
				t.Fatal(err)
			}
			values[name] = json.RawMessage(output.String())
		}
	} else {
		data, ok := repo.WorkspaceMetadataFields(workspace, names...)
		if !ok {
			t.Fatal("creation installed image missing")
		}
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			values[name] = fields[name]
		}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestScaleP21SW186IssueCreationAutomationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	member := f.principals[2].user
	for _, test := range []struct {
		name        string
		body        map[string]any
		edit        func(*domain.Bootstrap)
		setup       func(*testing.T)
		creationKey string
	}{
		{name: "sequence-overrides-installed-counter", body: map[string]any{"title": "Sequence", "teamId": f.public}, edit: func(data *domain.Bootstrap) {
			data.NextIssueNumber = 900
		}},
		{name: "template-children-and-defaults", body: map[string]any{"templateId": "sw186-template"}, edit: func(data *domain.Bootstrap) {
			data.IssueTemplates = []domain.IssueTemplate{{
				ID: "sw186-template", TeamID: f.public, Name: "From template", Body: "Template body",
				AssigneeID: member.ID, ProjectID: "triage-p-public", LabelIDs: []string{"triage-l-public"},
				SubIssues: []domain.TemplateSubIssue{{ID: "a", Title: "Child", TeamID: "tm-sub", AssigneeID: member.ID, LabelIDs: []string{"tm-l-shared"}}},
			}}
		}},
		{name: "template-hidden-child", body: map[string]any{"templateId": "sw186-template"}, edit: func(data *domain.Bootstrap) {
			data.IssueTemplates = []domain.IssueTemplate{{ID: "sw186-template", Name: "Hidden child", TeamID: f.public, SubIssues: []domain.TemplateSubIssue{{ID: "a", Title: "Hidden", TeamID: f.private}}}}
		}},
		{name: "admin-private-template-on-public-team", body: map[string]any{"templateId": "sw186-template", "teamId": f.public}, edit: func(data *domain.Bootstrap) {
			data.IssueTemplates = []domain.IssueTemplate{{ID: "sw186-template", Name: "Private template", TeamID: f.private, Body: "Private template body"}}
		}},
		{name: "workflow-order-and-actions", body: map[string]any{"title": "Workflow", "teamId": f.public}, edit: func(data *domain.Bootstrap) {
			data.WorkflowDefinitions = []domain.WorkflowDefinition{
				{ID: "sw186-disabled", Trigger: "issueCreated", Enabled: false},
				{ID: "sw186-other", Trigger: "schedule", Enabled: true},
				{ID: "sw186-workflow", Trigger: "issueCreated", Enabled: true, Actions: []domain.WorkflowAction{
					{Type: "assignIssue", Config: map[string]string{"userId": member.ID}},
					{Type: "addIssueLabel", Config: map[string]string{"labelId": "tm-l-shared"}},
					{Type: "notify", Config: map[string]string{"recipientId": member.ID}},
					{Type: "createIssue", Config: map[string]string{"teamId": "tm-parent", "title": "Workflow child"}},
				}},
			}
			data.WorkflowRuns = []domain.WorkflowRun{{ID: "sw186-prior-run", WorkflowID: "sw186-disabled", Status: "succeeded"}}
		}},
		{name: "triage-round-robin-and-sla", body: map[string]any{"title": "Triage", "teamId": f.public}, edit: func(data *domain.Bootstrap) {
			settings := data.TeamSettings[f.public]
			settings.TriageEnabled, settings.TriageAction, settings.TriageActionUserIDs = true, "notify", []string{member.ID}
			data.TeamSettings[f.public] = settings
			data.TriageRoutingRules = []domain.TriageRoutingRule{{ID: "sw186-rule", TeamID: f.public, Enabled: true, ResponsibilityID: "sw186-responsibility", LabelIDs: []string{"triage-l-public"}}}
			data.TriageResponsibilities = []domain.TriageResponsibility{{ID: "sw186-responsibility", TeamID: f.public, Enabled: true, Mode: "roundRobin", UserIDs: []string{f.former.ID, member.ID}, Cursor: 0}}
			data.TriageAssignments = []domain.TriageAssignment{{ID: "sw186-prior-assignment", IssueID: f.triage.ID}}
			data.Settings["sla"] = map[string]any{"enabled": true}
			data.SLARules = []domain.SLARule{{ID: "sw186-sla", Enabled: true, TeamIDs: []string{f.public}, TargetMinutes: 60}}
			data.SLAEvents = []domain.SLAEvent{{ID: "sw186-prior-event", IssueID: f.triage.ID}}
		}},
		{name: "large-description-and-unrelated-body", body: map[string]any{"title": "Large", "teamId": f.public, "description": strings.Repeat("Body <>& ", 40<<10)}, edit: func(data *domain.Bootstrap) {
			data.IssueTemplates = []domain.IssueTemplate{{ID: "sw186-unrelated", Body: strings.Repeat("Unrelated ", 200<<10)}}
		}},
		{name: "legal-four-megabyte-description", body: map[string]any{"title": "Legal large description", "teamId": f.public, "description": strings.Repeat("x", 4<<20)}, edit: func(*domain.Bootstrap) {}},
		{name: "legal-escaped-near-packet-limit", body: map[string]any{"title": "Legal escaped description", "teamId": f.public, "description": strings.Repeat("<>&", 700<<10)}, edit: func(*domain.Bootstrap) {}},
		{name: "stale-sequence-existing-row", body: map[string]any{"title": "Stale sequence", "teamId": f.public}, edit: func(*domain.Bootstrap) {}, setup: func(t *testing.T) {
			f.exec(t, `UPDATE issue_number_sequences SET last_number=0 WHERE workspace_key=?`, f.data.Workspace.URLKey)
		}},
		{name: "custom-key-child-id-collision", creationKey: "issue_" + strconv.FormatInt(f.sequence+2, 10), body: map[string]any{"templateId": "sw186-template", "teamId": f.public}, edit: func(data *domain.Bootstrap) {
			data.IssueTemplates = []domain.IssueTemplate{{ID: "sw186-template", TeamID: f.public, Name: "Root", SubIssues: []domain.TemplateSubIssue{{Title: "Child", TeamID: f.public}}}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			seed := f.prepare(scaleP21IssueUpdateCase{})
			f.scaleP21TriageWriterFixture.prepare = func(t *testing.T) {
				seed(t)
				if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw186_seed", "", nil, func(data *domain.Bootstrap) error {
					test.edit(data)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if test.setup != nil {
					test.setup(t)
				}
			}
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			var previous string
			for _, source := range []string{"legacy", "warm", "reloaded"} {
				var installed json.RawMessage
				got, started := f.run(t, source, func(service *server) *httptest.ResponseRecorder {
					w := scaleP21SW186Create(t, f, service, f.data.Viewer, nil, test.body, false, false, scaleP21SW186CreateOptions{creationKey: test.creationKey})
					installed = scaleP21SW186Installed(t, service.store, f.data.Workspace.URLKey)
					return w
				})
				reloaded, err := store.OpenDatabase(f.reader.config)
				if err != nil {
					t.Fatal(err)
				}
				reloadedImage := scaleP21SW186Installed(t, reloaded, f.data.Workspace.URLKey)
				if err := reloaded.Close(); err != nil {
					t.Fatal(err)
				}
				if scaleP21IssueUpdateNormalize(t, installed, started) != scaleP21IssueUpdateNormalize(t, reloadedImage, started) {
					t.Fatalf("%s installed automation differs from SQL reload: %s", source, scaleP21M2Diff(string(installed), string(reloadedImage)))
				}
				outcome := struct {
					Result    scaleP21IssueUpdateOutcome
					Metadata  [][]any
					Installed json.RawMessage
				}{got, f.rows(t, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,collection_order,record_key`, f.data.Workspace.URLKey), installed}
				normalized := scaleP21IssueUpdateNormalize(t, outcome, started)
				if previous != "" && previous != normalized {
					t.Fatalf("%s changed: %s", source, scaleP21M2Diff(previous, normalized))
				}
				previous = normalized
				if got.Code >= 500 {
					t.Fatalf("%s returned %d: %s", source, got.Code, got.Body)
				}
			}
		})
	}
}

func TestScaleP21SW186ConcurrentCreationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	seed := f.prepare(scaleP21IssueUpdateCase{})
	f.scaleP21TriageWriterFixture.prepare = func(t *testing.T) {
		seed(t)
		if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw186_concurrent_seed", "", nil, func(data *domain.Bootstrap) error {
			// The separate postcommit heuristic sees whichever concurrent
			// creations have committed by its turn; its output is not a
			// deterministic creation-transaction oracle.
			data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = false
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	var expected string
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		var codes []int
		got, started := f.run(t, source, func(service *server) *httptest.ResponseRecorder {
			var group sync.WaitGroup
			var mu sync.Mutex
			for range 3 {
				group.Add(1)
				go func() {
					defer group.Done()
					w := scaleP21SW186Create(t, f, service, f.data.Viewer, nil, map[string]any{"title": "Concurrent creation", "teamId": f.public}, false, false)
					mu.Lock()
					codes = append(codes, w.Code)
					mu.Unlock()
				}()
			}
			group.Wait()
			return nil
		})
		for _, code := range codes {
			if code != http.StatusCreated {
				t.Fatalf("%s concurrent creation status %d", source, code)
			}
		}
		got.Webhooks = scaleP21IssueUpdateSorted(t, got.Webhooks, started)
		got.Realtime = scaleP21IssueUpdateSorted(t, got.Realtime, started)
		slices.SortFunc(got.State.Events, func(a, b []any) int {
			left, _ := json.Marshal(a)
			right, _ := json.Marshal(b)
			return strings.Compare(scaleP21IssueUpdateNormalize(t, json.RawMessage(left), started), scaleP21IssueUpdateNormalize(t, json.RawMessage(right), started))
		})
		normalized := scaleP21IssueUpdateNormalize(t, got, started)
		if expected != "" && expected != normalized {
			t.Fatalf("%s concurrent creation changed: %s", source, scaleP21M2Diff(expected, normalized))
		}
		expected = normalized
	}
}

func TestScaleP21SW186IssueCreationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	owner, member, guest, sub := f.data.Viewer, f.principals[2].user, f.principals[3].user, f.principals[4].user
	selected := &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}
	empty := &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}}
	unrestricted := &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}}
	cases := []struct {
		name          string
		user          domain.User
		key           *domain.APIKey
		body          map[string]any
		development   bool
		cancelled     bool
		authorization bool
		creationKey   string
	}{
		{name: "owner", user: owner, body: map[string]any{"title": "Created", "teamId": f.public}},
		{name: "member", user: member, body: map[string]any{"title": "Created", "teamId": f.public}},
		{name: "guest", user: guest, body: map[string]any{"title": "Created", "teamId": f.public}},
		{name: "private-outsider", user: owner, body: map[string]any{"title": "Created", "teamId": f.private}},
		{name: "private-member", user: f.principals[1].user, body: map[string]any{"title": "Created", "teamId": f.private}},
		{name: "subteam", user: sub, body: map[string]any{"title": "Created", "teamId": "tm-sub"}},
		{name: "default-team", user: member, body: map[string]any{"title": "Created"}},
		{name: "selected", user: owner, key: selected, body: map[string]any{"title": "Created", "teamId": f.public}},
		{name: "unrestricted", user: owner, key: unrestricted, body: map[string]any{"title": "Created", "teamId": f.public}},
		{name: "selected-empty", user: owner, key: empty, body: map[string]any{"title": "Created", "teamId": f.public}},
		{name: "project", user: owner, body: map[string]any{"title": "Created", "teamId": f.public, "projectId": "triage-p-public"}},
		{name: "hidden-project", user: member, body: map[string]any{"title": "Created", "teamId": f.public, "projectId": "triage-p-private"}},
		{name: "selected-project", user: owner, key: selected, body: map[string]any{"title": "Created", "teamId": f.public, "projectId": "triage-p-public"}},
		{name: "cycle", user: owner, body: map[string]any{"title": "Created", "teamId": f.public, "cycleId": "iu-cycle-public"}},
		{name: "selected-cycle", user: owner, key: selected, body: map[string]any{"title": "Created", "teamId": f.public, "cycleId": "iu-cycle-public"}},
		{name: "development-selected-project", user: owner, key: selected, body: map[string]any{"title": "Created", "teamId": f.public, "projectId": "triage-p-public"}, development: true},
		{name: "old-project-slug-remains-an-id-refusal", user: owner, body: map[string]any{"title": "Created", "teamId": f.public, "projectId": "old-name"}},
		{name: "assignee", user: owner, body: map[string]any{"title": "Created", "teamId": f.public, "assigneeId": member.ID}},
		{name: "former-assignee", user: owner, body: map[string]any{"title": "Created", "teamId": f.public, "assigneeId": f.former.ID}},
		{name: "parent", user: owner, body: map[string]any{"title": "Created", "teamId": f.public, "parentId": f.triage.ID}},
		{name: "description", user: owner, body: map[string]any{"title": "Created", "teamId": f.public, "description": "Hello", "descriptionData": mentioningDocument("Hello", member.ID)}},
		{name: "content-state-member", user: owner, body: map[string]any{"title": "Created", "teamId": f.public, "description": "Editor", "contentState": mentioningCollaborationState(member.ID)}},
		{name: "content-state-former-refused", user: owner, body: map[string]any{"title": "Created", "teamId": f.public, "contentState": mentioningCollaborationState(f.former.ID)}},
		{name: "content-state-empty", user: owner, body: map[string]any{"title": "Created", "teamId": f.public, "contentState": ""}},
		{name: "content-state-invalid-base64", user: owner, body: map[string]any{"title": "Created", "teamId": f.public, "contentState": "not-base64"}},
		{name: "content-state-invalid-update", user: owner, body: map[string]any{"title": "Created", "teamId": f.public, "contentState": "AQ=="}},
		{name: "custom-creation-key", user: owner, body: map[string]any{"title": "Custom ID", "teamId": f.public}, creationKey: "sw186-new-custom-key"},
		{name: "custom-creation-key-existing", user: owner, body: map[string]any{"title": "Idempotent", "teamId": f.public}, creationKey: f.triage.ID},
		{name: "authorization-handoff", user: owner, body: map[string]any{"title": "Created", "teamId": f.public}, authorization: true},
		{name: "authorization-handoff-assignee", user: owner, body: map[string]any{"title": "Created", "teamId": f.public, "assigneeId": member.ID}, authorization: true},
		{name: "authorization-handoff-default-team", user: member, body: map[string]any{"title": "Created"}, authorization: true},
		{name: "authorization-handoff-selected", user: owner, key: selected, body: map[string]any{"title": "Created", "teamId": f.public, "projectId": "triage-p-public"}, authorization: true},
		{name: "development", user: owner, body: map[string]any{"title": "Created", "teamId": f.public}, development: true},
		{name: "cancelled", user: owner, body: map[string]any{"title": "Created", "teamId": f.public}, cancelled: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f.scaleP21TriageWriterFixture.prepare = f.prepare(scaleP21IssueUpdateCase{})
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				return scaleP21SW186Create(t, f, service, test.user, test.key, test.body, test.development, test.cancelled, scaleP21SW186CreateOptions{authorization: test.authorization, creationKey: test.creationKey})
			})
		})
	}
}
