package main

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW84ProjectVisibilityBoundary(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate production source")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(source), "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	functions := map[string]*ast.FuncDecl{}
	calls := map[string]map[string][]token.Pos{}
	legacy := map[string][]string{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			functions[name], calls[name] = fn, map[string][]token.Pos{}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				var callee string
				switch function := call.Fun.(type) {
				case *ast.SelectorExpr:
					callee = function.Sel.Name
				case *ast.Ident:
					callee = function.Name
				default:
					return true
				}
				calls[name][callee] = append(calls[name][callee], call.Pos())
				if callee == "ProjectByID" || callee == "MetadataRecord" {
					legacy[callee] = append(legacy[callee], name)
				}
				return true
			})
		}
	}
	for api, want := range map[string][]string{
		"ProjectByID":    {"agentTaskRealtimeVisible", "projectVisible"},
		"MetadataRecord": {"realtimeRecordFor"},
	} {
		slices.Sort(legacy[api])
		if !reflect.DeepEqual(legacy[api], want) {
			t.Errorf("%s callers=%v, want %v", api, legacy[api], want)
		}
	}
	position := func(fn, callee string) token.Pos {
		t.Helper()
		if len(calls[fn][callee]) != 1 {
			t.Fatalf("%s must call %s exactly once, got %d", fn, callee, len(calls[fn][callee]))
		}
		return calls[fn][callee][0]
	}
	for _, seam := range [][3]string{
		{"legacyPagedRealtimeEvent", "boundedAgentTaskRealtimeVisible", "agentTaskRealtimeVisible"},
		{"legacyPagedRealtimeEvent", "boundedPagedPolicyEvent", "pagedPolicyEvent"},
		{"projectVisible", "boundedProjectVisible", "ProjectByID"},
	} {
		bounded, old := position(seam[0], seam[1]), position(seam[0], seam[2])
		guarded := false
		ast.Inspect(functions[seam[0]].Body, func(node ast.Node) bool {
			branch, ok := node.(*ast.IfStmt)
			if ok && branch.Body.Pos() < bounded && bounded < branch.End() && branch.End() < old {
				if _, returns := branch.Body.List[len(branch.Body.List)-1].(*ast.ReturnStmt); !returns {
					return true
				}
				ast.Inspect(branch.Cond, func(node ast.Node) bool {
					selector, ok := node.(*ast.SelectorExpr)
					guarded = guarded || ok && selector.Sel.Name == "BoundedMetadataEnabled"
					return true
				})
			}
			return true
		})
		if !guarded {
			t.Errorf("%s must dispatch %s under the bounded flag before %s", seam[0], seam[1], seam[2])
		}
	}
	sourceBranch, ok := functions["projectVisible"].Body.List[0].(*ast.IfStmt)
	if !ok || sourceBranch.End() >= position("projectVisible", "boundedProjectVisible") {
		t.Fatal("projectVisible must retain its EventSource branch before bounded dispatch")
	}
	condition, ok := sourceBranch.Cond.(*ast.BinaryExpr)
	if !ok || condition.Op != token.NEQ {
		t.Fatal("projectVisible must check its EventSource before bounded dispatch")
	}
	field, ok := condition.X.(*ast.SelectorExpr)
	if !ok || field.Sel.Name != "source" {
		t.Fatal("projectVisible must check its EventSource before bounded dispatch")
	}
	read := position("boundedPagedPolicyEvent", "boundedRealtimeRecordFor")
	project := position("boundedPagedPolicyEvent", "projectPolicyRecord")
	checked := false
	ast.Inspect(functions["boundedPagedPolicyEvent"].Body, func(node ast.Node) bool {
		branch, ok := node.(*ast.IfStmt)
		if !ok || branch.Pos() <= read || branch.End() >= project {
			return true
		}
		condition, ok := branch.Cond.(*ast.BinaryExpr)
		if !ok || condition.Op != token.NEQ {
			return true
		}
		value, ok := condition.X.(*ast.Ident)
		empty, nilOK := condition.Y.(*ast.Ident)
		if ok && value.Name == "err" && nilOK && empty.Name == "nil" && len(branch.Body.List) == 1 {
			result, ok := branch.Body.List[0].(*ast.ReturnStmt)
			if ok && len(result.Results) == 3 {
				failure, ok := result.Results[2].(*ast.Ident)
				checked = ok && failure.Name == "err"
			}
		}
		return true
	})
	if !checked {
		t.Fatal("boundedPagedPolicyEvent must return the bounded record reader error before projection")
	}
}

func TestScaleP21SW84ProjectVisibilityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_SYNC_V2", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw84-project-")
		private := ""
		cases := []struct {
			name, project string
		}{
			{"public", prefix + "public"},
			{"private", prefix + "private"},
			{"unscoped", prefix + "unscoped"},
			{"missing", prefix + "missing"},
			{"current-slug-only", prefix + "current-slug"},
			{"previous-slug-only", prefix + "previous-slug"},
			{"slug-collision-exact-id", prefix + "collision"},
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw84_project_visibility", "", nil, func(data *domain.Bootstrap) error {
			for _, project := range data.Projects {
				if project.ID == "m2-unrelated" && len(project.TeamIDs) > 0 {
					private = project.TeamIDs[0]
				}
			}
			if private == "" {
				return errors.New("private project fixture missing")
			}
			data.Projects = append(data.Projects,
				domain.Project{ID: cases[0].project, TeamIDs: []string{f.public}},
				domain.Project{ID: cases[1].project, TeamIDs: []string{private}},
				domain.Project{ID: cases[2].project},
				domain.Project{ID: prefix + "slug-owner", SlugID: cases[4].project, PreviousSlugIDs: []string{cases[5].project}, TeamIDs: []string{f.public}},
				domain.Project{ID: prefix + "collision-slug-owner", SlugID: cases[6].project, TeamIDs: []string{private}},
				domain.Project{ID: cases[6].project, TeamIDs: []string{f.public}},
			)
			for _, test := range cases {
				data.SavedViews = append(data.SavedViews, domain.SavedView{
					ID: prefix + "view-" + test.name, Scope: "workspace", Resource: "issues", ProjectID: test.project,
				})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, test := range cases {
			if err := repo.CreateMentionTask(t.Context(), domain.AgentTask{
				ID: "mention_" + prefix + test.name, WorkspaceKey: workspace, AppUserID: prefix + "application",
				ResourceType: "project", ResourceID: test.project,
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
		events := make([]domain.RealtimeEvent, 0, len(cases)*2+1)
		for _, test := range cases {
			payload, err := json.Marshal(map[string]string{"taskId": "mention_" + prefix + test.name})
			if err != nil {
				t.Fatal(err)
			}
			events = append(events,
				domain.RealtimeEvent{ID: prefix + "agent-" + test.name, Type: "agent_task.updated", AggregateID: test.project, Payload: payload, CreatedAt: at},
				// A scope change may produce an anonymous event for outsiders.
				// Reader errors must still stop delivery instead of taking that path.
				domain.RealtimeEvent{ID: prefix + "view-" + test.name, Type: "view.updated", AggregateID: prefix + "view-" + test.name, Payload: json.RawMessage(`{"projectId":"changed"}`), CreatedAt: at},
			)
		}
		events = append(events, domain.RealtimeEvent{ID: prefix + "missing-task", Type: "agent_task.updated", Payload: json.RawMessage(`{"taskId":"missing-task"}`), CreatedAt: at})
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			r := f.literalRequest(user, key, nil)
			r.Method, r.URL.Path = http.MethodGet, "/api/realtime/events"
			services := []*server{
				{store: repo, realtime: newRealtimeHub()},
				{store: f.probe, realtime: newRealtimeHub()},
			}
			for _, service := range services {
				service.realtime.node = "sw84-node"
			}
			for _, event := range events {
				want, wantVisible, wantErr := services[0].legacyPagedRealtimeEvent(r, event)
				got, visible, err := services[1].legacyPagedRealtimeEvent(r, event)
				if wantErr != nil || err != nil || visible != wantVisible || !reflect.DeepEqual(scaleP21M2JSON(t, want, false), scaleP21M2JSON(t, got, false)) {
					t.Fatalf("event=%s/%s visible=%v/%v errors=%v/%v difference=%s", event.Type, event.ID, wantVisible, visible, wantErr, err, scaleP21M2Diff(want, got))
				}
				a, b := httptest.NewRecorder(), httptest.NewRecorder()
				okA := services[0].writeProjectedRealtime(a, r, event)
				okB := services[1].writeProjectedRealtime(b, r, event)
				if !okA || !okB || a.Body.String() != b.Body.String() {
					t.Fatalf("SSE output changed for %s: accepted=%v/%v\nold=%s\nbounded=%s", event.ID, okA, okB, a.Body, b.Body)
				}
				if f.probe.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("project visibility retained metadata admission after delivery")
				}
			}
		})
		// Application ownership precedes the project header and metadata reads.
		// Even an app pointing at another app's missing project sees nothing.
		foreign := domain.User{ID: prefix + "foreign-application", App: true}
		foreignRequest := f.literalRequest(foreign, nil, nil)
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			service := &server{store: source, realtime: newRealtimeHub()}
			for index := 0; index < len(cases)*2; index += 2 {
				if _, visible, err := service.legacyPagedRealtimeEvent(foreignRequest, events[index]); err != nil || visible {
					t.Fatalf("foreign app received project task: bounded=%v event=%s visible=%v err=%v", source.BoundedMetadataEnabled(), events[index].ID, visible, err)
				}
			}
		}
		service := &server{store: f.probe, realtime: newRealtimeHub()}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		r := f.literalRequest(f.principals[2].user, nil, nil).WithContext(ctx)
		r.Method, r.URL.Path = http.MethodGet, "/api/realtime/events"
		// The existing task lookup treats every failure as invisible, including
		// cancellation before the new project reader can be reached.
		if _, visible, err := service.legacyPagedRealtimeEvent(r, events[0]); visible || err != nil {
			t.Fatalf("task lookup error behavior changed: visible=%v err=%v", visible, err)
		}
		// The new header reader itself reports cancellation instead of absence.
		read, _, err := f.probe.BeginProjectVisibilityRead(ctx, workspace, cases[0].project)
		if read != nil {
			read.Close()
		}
		if read != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("project header swallowed cancellation: read=%v err=%v", read != nil, err)
		}
		literal := &server{store: repo, realtime: newRealtimeHub()}
		want, wantVisible, wantErr := literal.legacyPagedRealtimeEvent(r, events[1])
		got, visible, err := service.legacyPagedRealtimeEvent(r, events[1])
		if wantVisible || visible || wantErr == nil || err == nil ||
			reflect.TypeOf(wantErr) != reflect.TypeOf(err) || wantErr.Error() != err.Error() ||
			!reflect.DeepEqual(scaleP21M2JSON(t, want, false), scaleP21M2JSON(t, got, false)) {
			t.Fatalf("canceled policy behavior changed: visible=%v/%v errors=%T %v/%T %v", wantVisible, visible, wantErr, wantErr, err, err)
		}
		oldOutput, output := httptest.NewRecorder(), httptest.NewRecorder()
		oldAccepted := literal.writeProjectedRealtime(oldOutput, r, events[1])
		accepted := service.writeProjectedRealtime(output, r, events[1])
		if oldAccepted || accepted || oldOutput.Body.Len() != 0 || output.Body.Len() != 0 ||
			oldOutput.Body.String() != output.Body.String() {
			t.Fatalf("canceled policy emitted a successful or anonymous signal: accepted=%v/%v old=%s bounded=%s", oldAccepted, accepted, oldOutput.Body, output.Body)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 || f.probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("project visibility decoded full metadata or leaked work")
		}
	}
}
