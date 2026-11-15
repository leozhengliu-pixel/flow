package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW104DocumentMetadataFailureBoundary(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	expired, expire := context.WithDeadline(t.Context(), time.Unix(1, 0))
	defer expire()
	// The legacy bool lookup treats ordinary SQL/auth lookup failures as absent.
	// Newly introduced bounded refusals must stop delivery, not masquerade as absence.
	for _, test := range []struct {
		name string
		ctx  context.Context
		err  error
		want error
	}{
		{"ordinary-sql", t.Context(), errors.New("metadata SQL unavailable"), nil},
		{"ordinary-missing-row", t.Context(), sql.ErrNoRows, nil},
		{"ordinary-forbidden", t.Context(), store.ErrAuthForbidden, nil},
		{"ordinary-issue-query", t.Context(), store.ErrIssueQuery, nil},
		{"work-budget", t.Context(), store.ErrMetadataWorkBudget, store.ErrMetadataWorkBudget},
		{"coverage", t.Context(), store.ErrBoundedMetadataCoverage, store.ErrBoundedMetadataCoverage},
		{"page-budget", t.Context(), store.ErrMetadataPageBudget, store.ErrMetadataPageBudget},
		{"access-changed", t.Context(), store.ErrResourceAccessChanged, store.ErrResourceAccessChanged},
		{"wrapped-work-budget", t.Context(), fmt.Errorf("metadata selection: %w", store.ErrMetadataWorkBudget), store.ErrMetadataWorkBudget},
		{"wrapped-coverage", t.Context(), fmt.Errorf("metadata selection: %w", store.ErrBoundedMetadataCoverage), store.ErrBoundedMetadataCoverage},
		{"wrapped-page-budget", t.Context(), fmt.Errorf("metadata selection: %w", store.ErrMetadataPageBudget), store.ErrMetadataPageBudget},
		{"revoked-access", t.Context(), errors.Join(store.ErrAuthForbidden, store.ErrResourceAccessChanged), store.ErrResourceAccessChanged},
		{"canceled-sql", ctx, errors.New("metadata SQL unavailable"), context.Canceled},
		{"canceled-budget", ctx, store.ErrMetadataWorkBudget, context.Canceled},
		{"deadline-sql", expired, errors.New("metadata SQL unavailable"), context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := boundedLegacyDocumentMetadataFailure(test.ctx, test.err)
			if test.want == nil && got != nil || test.want != nil && !errors.Is(got, test.want) {
				t.Fatalf("lookup failure=%v, want %v", got, test.want)
			}
			if test.want != nil && test.ctx.Err() == nil && got != test.err {
				t.Fatal("bounded refusal lost its original wrapped error")
			}
		})
	}
}

func scaleP21SW104Text(node ast.Node) string {
	var out bytes.Buffer
	if err := format.Node(&out, token.NewFileSet(), node); err != nil {
		return ""
	}
	return out.String()
}

func scaleP21SW104StoredKey(prefix string, user domain.User, key *domain.APIKey) *domain.APIKey {
	if key == nil {
		return nil
	}
	copy := *key
	copy.ID = prefix + user.ID + "-" + key.TeamRestriction
	if key.TeamRestriction == "selected" {
		if len(key.TeamIDs) == 0 {
			copy.ID += "-empty"
		} else {
			copy.ID += "-set"
		}
	}
	copy.CreatorID = user.ID
	return &copy
}

func TestScaleP21SW104DocumentLegacyBoundary(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate document boundary")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(source), "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]int{
		"DocumentByID": {
			"event_source.go:BuildEventSource": 1, "paged_realtime.go:pagedPresenceFor": 1,
			"realtime.go:documentPresenceAllowed": 1, "realtime_entity_payloads.go:pagedDocumentEvent": 1,
		},
		"documentPresenceAllowed": {
			"realtime.go:presenceDocumentPermission": 1, "paged_realtime.go:legacyPagedRealtimeEvent": 1,
			"paged_realtime.go:agentTaskRealtimeVisible": 1,
		},
		"pagedDocumentEvent":       {"paged_realtime.go:legacyPagedRealtimeEvent": 1},
		"agentTaskRealtimeVisible": {"paged_realtime.go:legacyPagedRealtimeEvent": 1},
	}
	functions := map[string]*ast.FuncDecl{}
	got := map[string]map[string]int{}
	positions := map[string]map[string]token.Pos{}
	for name := range want {
		got[name] = map[string]int{}
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			site := filepath.Base(path) + ":<declaration>"
			var body ast.Node = declaration
			if fn, ok := declaration.(*ast.FuncDecl); ok {
				site = filepath.Base(path) + ":" + fn.Name.Name
				functions[site], body = fn, fn.Body
				if fn.Body == nil {
					continue
				}
			}
			parents := map[ast.Node]ast.Node{}
			var stack []ast.Node
			ast.Inspect(body, func(node ast.Node) bool {
				if node == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				if len(stack) > 0 {
					parents[node] = stack[len(stack)-1]
				}
				stack = append(stack, node)
				return true
			})
			ast.Inspect(body, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok || want[selector.Sel.Name] == nil {
					return true
				}
				name := selector.Sel.Name
				receiver := "s"
				if name == "DocumentByID" {
					receiver = "s.store"
				}
				call, direct := parents[node].(*ast.CallExpr)
				if !direct || call.Fun != node || scaleP21SW104Text(selector.X) != receiver {
					t.Errorf("%s: %s escaped its same-server direct-call boundary", site, name)
					return true
				}
				got[name][site]++
				if positions[site] == nil {
					positions[site] = map[string]token.Pos{}
				}
				positions[site][name] = call.Pos()
				return true
			})
		}
	}
	for name, sites := range want {
		if !reflect.DeepEqual(got[name], sites) {
			t.Errorf("%s production callers=%v, want %v", name, got[name], sites)
		}
	}
	for _, seam := range []struct{ site, name, condition, region, bounded string }{
		{"event_source.go:BuildEventSource", "DocumentByID", "s.store.BoundedMetadataEnabled()", "else", ""},
		{"paged_realtime.go:pagedPresenceFor", "DocumentByID", "s.store.BoundedMetadataEnabled()", "else", ""},
		{"realtime.go:presenceDocumentPermission", "documentPresenceAllowed", "!s.store.BoundedMetadataEnabled()", "body", ""},
		{"paged_realtime.go:legacyPagedRealtimeEvent", "documentPresenceAllowed", `s.store.BoundedMetadataEnabled() && strings.HasPrefix(event.Type, "document.")`, "else", ""},
		{"paged_realtime.go:legacyPagedRealtimeEvent", "pagedDocumentEvent", "s.store.BoundedMetadataEnabled()", "after-return", "boundedPagedDocumentEvent"},
		{"paged_realtime.go:legacyPagedRealtimeEvent", "agentTaskRealtimeVisible", "s.store.BoundedMetadataEnabled()", "after-return", "boundedAgentTaskRealtimeVisible"},
	} {
		fn, position := functions[seam.site], positions[seam.site][seam.name]
		if fn == nil || position == token.NoPos {
			t.Errorf("%s lacks %s", seam.site, seam.name)
			continue
		}
		guarded := false
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			branch, ok := node.(*ast.IfStmt)
			if !ok || scaleP21SW104Text(branch.Cond) != seam.condition {
				return true
			}
			switch seam.region {
			case "else":
				guarded = guarded || branch.Else != nil && branch.Else.Pos() < position && position < branch.Else.End()
			case "body":
				guarded = guarded || branch.Body.Pos() < position && position < branch.Body.End()
			}
			return true
		})
		if seam.region == "after-return" {
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				block, ok := node.(*ast.BlockStmt)
				if !ok {
					return true
				}
				for index, statement := range block.List {
					branch, ok := statement.(*ast.IfStmt)
					if !ok || branch.Else != nil || scaleP21SW104Text(branch.Cond) != seam.condition || len(branch.Body.List) == 0 {
						continue
					}
					if _, returns := branch.Body.List[len(branch.Body.List)-1].(*ast.ReturnStmt); !returns {
						continue
					}
					bounded, escape := false, false
					ast.Inspect(branch.Body, func(node ast.Node) bool {
						if selector, ok := node.(*ast.SelectorExpr); ok {
							bounded = bounded || selector.Sel.Name == seam.bounded && scaleP21SW104Text(selector.X) == "s"
						}
						if jump, ok := node.(*ast.BranchStmt); ok && jump.Tok == token.GOTO {
							escape = true
						}
						return true
					})
					for _, following := range block.List[index+1:] {
						guarded = guarded || bounded && !escape && following.Pos() <= position && position < following.End()
					}
				}
				return true
			})
		}
		if !guarded {
			t.Errorf("%s must keep %s behind its reviewed %s boundary", seam.site, seam.name, seam.region)
		}
	}
}

func TestScaleP21SW104DocumentEventSourceDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_SYNC_V2", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw104-document-")
		at := time.Date(2026, 11, 15, 1, 2, 3, 0, time.UTC)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw104_seed", "", nil, func(data *domain.Bootstrap) error {
			for _, principal := range f.principals {
				for _, key := range []domain.APIKey{
					{Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"},
					{Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}},
					{Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
				} {
					data.APIKeys = append(data.APIKeys, *scaleP21SW104StoredKey(prefix, principal.user, &key))
				}
			}
			private, child := "", ""
			for _, project := range data.Projects {
				if project.ID == "m2-unrelated" {
					private = project.TeamIDs[0]
				}
			}
			for id, settings := range data.TeamSettings {
				if settings.ParentTeamID == private {
					child = id
				}
			}
			if private == "" || child == "" {
				t.Fatal("document fixture lacks private and inherited teams")
			}
			data.Documents = append(data.Documents,
				domain.Document{ID: prefix + "public", SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "old"}, Title: "Public", Creator: data.Viewer},
				domain.Document{ID: prefix + "private", Title: "Private", Creator: data.Viewer, TeamIDs: []string{private}},
				domain.Document{ID: prefix + "inherited", Title: "Inherited", Creator: data.Viewer, TeamIDs: []string{child}},
				domain.Document{ID: prefix + "nil", Title: "Nil", Permissions: nil},
				domain.Document{ID: prefix + "empty", Title: "Empty", Permissions: []domain.DocumentPermission{}},
				domain.Document{ID: prefix + "granted", Creator: data.Viewer, TeamIDs: []string{private}, Permissions: []domain.DocumentPermission{
					{ID: prefix + "owner", DocumentID: prefix + "granted", SubjectType: "user", SubjectID: data.Viewer.ID, Role: "owner", CreatedAt: at, UpdatedAt: at.Add(time.Hour)},
					{ID: prefix + "guest", DocumentID: prefix + "granted", SubjectType: "user", SubjectID: f.principals[3].user.ID, Role: "viewer", CreatedAt: at, UpdatedAt: at},
				}},
				domain.Document{ID: prefix + "slug-owner", SlugID: prefix + "collision", Creator: data.Viewer, TeamIDs: []string{private}},
				domain.Document{ID: prefix + "collision", Title: "Later exact ID", Creator: data.Viewer},
				domain.Document{ID: prefix + "duplicate", Title: "First duplicate", Creator: data.Viewer},
				domain.Document{ID: prefix + "duplicate", Title: "Last duplicate", Creator: data.Viewer, TeamIDs: []string{private}},
				domain.Document{ID: prefix + "cached-wide", Title: "Cached", Content: strings.Repeat("body", 256<<10), Creator: data.Viewer},
			)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		services := []*server{{store: repo, realtime: newRealtimeHub()}, {store: f.probe, realtime: newRealtimeHub()}}
		cases := []struct{ name, kind, ref, payload string }{
			{"id", "document.updated", prefix + "public", `{}`},
			{"current-slug", "document.updated", prefix + "current", `{}`},
			{"old-slug-ignored", "document.updated", prefix + "old", `{}`},
			{"private", "document.updated", prefix + "private", `{}`},
			{"inherited", "document.updated", prefix + "inherited", `{}`},
			{"nil", "document.updated", prefix + "nil", `{}`},
			{"empty", "document.updated", prefix + "empty", `{}`},
			{"granted", "document.permissions_updated", prefix + "granted", `{}`},
			{"comments", "document.comment_created", prefix + "public", `{"commentId":"missing-comment"}`},
			{"private-scope-changed", "document.updated", prefix + "private", `{"teamIds":["changed"]}`},
			{"collision", "document.updated", prefix + "collision", `{}`},
			{"duplicate", "document.updated", prefix + "duplicate", `{}`},
			{"missing", "document.updated", prefix + "missing", `{}`},
			{"empty-reference", "document.updated", "", `{}`},
			{"unpublished", "document.draft_updated", prefix + "draft", `{}`},
			{"published", "document.draft_published", prefix + "draft", `{"documentId":"` + prefix + `current"}`},
			{"published-empty", "document.draft_published", prefix + "draft", `{}`},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				event := domain.RealtimeEvent{ID: prefix + test.name, Type: test.kind, AggregateID: test.ref, ActorID: f.data.Viewer.ID, CreatedAt: at, Payload: json.RawMessage(test.payload)}
				var sources [2]*EventSource
				var queries [2]int
				templates := [2]map[string]int{{}, {}}
				for i, service := range services {
					var err error
					ctx := store.WithSQLStatementObserver(t.Context(), func(query string) {
						queries[i]++
						templates[i][query]++
					})
					sources[i], err = service.BuildEventSource(ctx, workspace, event)
					if err != nil {
						t.Fatal("build source", i, err)
					}
				}
				t.Logf("source SQL old=%d bounded=%d delta=%d", queries[0], queries[1], queries[1]-queries[0])
				if test.name == "id" {
					t.Logf("first source SQL templates old=%v bounded=%v", templates[0], templates[1])
				}
				if !reflect.DeepEqual(sources[0].document, sources[1].document) ||
					sources[0].documentMissing != sources[1].documentMissing ||
					!reflect.DeepEqual(sources[0].comments, sources[1].comments) {
					t.Fatalf("document source changed: %s", scaleP21M2Diff(sources[0].document, sources[1].document))
				}
				f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
					key = scaleP21SW104StoredKey(prefix, user, key)
					var events [2]domain.RealtimeEvent
					var visible [2]bool
					for i, service := range services {
						access, err := service.store.ResolveEventAccess(t.Context(), workspace, user.ID, key)
						if err != nil {
							t.Fatal("resolve event access", i, err)
						}
						frame, err := ProjectEvent(sources[i], access)
						if err != nil {
							t.Fatal("project source", i, err)
						}
						visible[i] = frame.Visible()
						events[i], err = frame.Event()
						frame.Close()
						if err != nil {
							t.Fatal("decode frame", i, err)
						}
					}
					if visible[0] != visible[1] || !reflect.DeepEqual(events[0], events[1]) {
						t.Fatalf("document frame changed: visible=%v/%v %s", visible[0], visible[1], scaleP21M2Diff(events[0], events[1]))
					}
					request := f.literalRequest(user, key, nil)
					for name, project := range map[string]func(*server) (domain.RealtimeEvent, bool, error){
						"legacy": func(service *server) (domain.RealtimeEvent, bool, error) {
							return service.legacyPagedRealtimeEvent(request, event)
						},
						"fallback": func(service *server) (domain.RealtimeEvent, bool, error) {
							return service.pagedRealtimeEvent(request, event)
						},
					} {
						want, wantVisible, wantErr := project(services[0])
						got, gotVisible, gotErr := project(services[1])
						if wantErr != nil || gotErr != nil || wantVisible != gotVisible ||
							!reflect.DeepEqual(scaleP21M2JSON(t, want, false), scaleP21M2JSON(t, got, false)) {
							t.Fatalf("%s document projection changed: visible=%v/%v errors=%v/%v %s", name, wantVisible, gotVisible, wantErr, gotErr, scaleP21M2Diff(want, got))
						}
					}
				})
				if f.probe.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("source retained installed document selection")
				}
			})
		}
		for _, name := range []string{"public", "private", "inherited", "current", "old", "collision", "duplicate", "missing"} {
			taskID := "mention_" + prefix + name
			if err := repo.CreateMentionTask(t.Context(), domain.AgentTask{
				ID: taskID, WorkspaceKey: workspace, AppUserID: prefix + "application",
				ResourceType: "document", ResourceID: prefix + name,
			}); err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]string{"taskId": taskID})
			if err != nil {
				t.Fatal(err)
			}
			event := domain.RealtimeEvent{ID: prefix + "task-" + name, Type: "agent_task.updated", Payload: payload, CreatedAt: at}
			f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
				key = scaleP21SW104StoredKey(prefix, user, key)
				request := f.literalRequest(user, key, nil)
				want, wantVisible, wantErr := services[0].legacyPagedRealtimeEvent(request, event)
				got, visible, err := services[1].legacyPagedRealtimeEvent(request, event)
				if wantErr != nil || err != nil || wantVisible != visible ||
					!reflect.DeepEqual(scaleP21M2JSON(t, want, false), scaleP21M2JSON(t, got, false)) {
					t.Fatalf("document task %s changed: visible=%v/%v errors=%v/%v %s", name, wantVisible, visible, wantErr, err, scaleP21M2Diff(want, got))
				}
			})
			request := f.literalRequest(domain.User{ID: prefix + "foreign-app", App: true}, nil, nil)
			for _, service := range services {
				if _, visible, err := service.legacyPagedRealtimeEvent(request, event); visible || err != nil {
					t.Fatalf("foreign app received document task: visible=%v err=%v", visible, err)
				}
			}
		}
		for _, test := range []struct {
			name, workspace string
			user            domain.User
		}{
			{"ordinary-foreign-user", workspace, domain.User{ID: prefix + "outsider"}},
			{"ordinary-missing-workspace", prefix + "missing-workspace", f.data.Viewer},
		} {
			t.Run(test.name, func(t *testing.T) {
				request := f.literalRequest(test.user, nil, nil)
				request.Header.Set("X-Workspace-Key", test.workspace)
				request.SetPathValue("workspaceKey", test.workspace)
				request.SetPathValue("workspace", test.workspace)
				want := services[0].documentPresenceAllowed(request, prefix+"public")
				got, err := services[1].boundedLegacyDocumentPresenceAllowed(request, prefix+"public")
				if got != want || err != nil {
					t.Fatalf("ordinary lookup changed: allowed=%v/%v err=%v", want, got, err)
				}
			})
		}
		t.Run("cached-full-document-once", func(t *testing.T) {
			request := f.literalRequest(f.data.Viewer, nil, nil)
			data, _, err := services[0].pagedRealtimeMetadata(request)
			if err != nil {
				t.Fatal(err)
			}
			event := domain.RealtimeEvent{ID: prefix + "cached-event", Type: "document.updated", AggregateID: prefix + "cached-wide", Payload: json.RawMessage(`{"state":"unchanged"}`)}
			want, wantVisible := services[0].pagedDocumentEvent(request, data, event)
			cached := &server{store: f.probe, realtime: newRealtimeHub()}
			for name, service := range map[string]*server{
				"nil-hub": {store: f.probe},
				"cache":   cached,
			} {
				got, visible, err := service.boundedPagedDocumentEvent(request, data, event)
				if err != nil || visible != wantVisible || !reflect.DeepEqual(got, want) {
					t.Fatalf("%s initial payload changed: visible=%v/%v err=%v", name, wantVisible, visible, err)
				}
			}
			blocker, err := f.probe.BeginMetadataJSONRead(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Close()
			if err := blocker.ReserveEncoded(t.Context(), 63<<20); err != nil {
				t.Fatal(err)
			}
			held := f.probe.MetadataWorkUsedForTesting()
			// Leave enough allowance for the policy header, but less than the
			// 1 MiB document's full native clone. A cached delivery must not load it.
			read, found, err := f.probe.BeginPresenceDocumentRead(t.Context(), workspace, event.AggregateID)
			if err != nil || !found || !read.Found {
				t.Fatal("pressure must still admit a header", found, err)
			}
			_, err = read.LoadDocument(t.Context())
			read.Close()
			if !errors.Is(err, store.ErrMetadataWorkBudget) {
				t.Fatal("pressure did not prevent a repeated full load", err)
			}
			for repeat := range 3 {
				got, visible, err := cached.boundedPagedDocumentEvent(request, data, event)
				if err != nil || visible != wantVisible || !reflect.DeepEqual(got, want) {
					t.Fatalf("cached repeat %d reloaded the full value: visible=%v err=%v", repeat, visible, err)
				}
				if f.probe.MetadataWorkUsedForTesting() != held {
					t.Fatal("cached delivery leaked header admission")
				}
			}
			failed := event
			failed.ID += "-retry"
			got, visible, err := cached.boundedPagedDocumentEvent(request, data, failed)
			if visible || !errors.Is(err, store.ErrMetadataWorkBudget) || !reflect.DeepEqual(got, failed) {
				t.Fatal("uncached refusal changed the original event or reported success", visible, err)
			}
			cached.realtime.payloads.mu.Lock()
			_, retainedFailure := cached.realtime.payloads.entries[failed.ID+"|document"]
			cached.realtime.payloads.mu.Unlock()
			if retainedFailure || f.probe.MetadataWorkUsedForTesting() != held {
				t.Fatal("failed nil payload was cached or retained admission")
			}
			blocker.Close()
			got, visible, err = cached.boundedPagedDocumentEvent(request, data, failed)
			if err != nil || !visible || !reflect.DeepEqual(got.Payload, want.Payload) {
				t.Fatal("released admission did not allow the failed build to retry", visible, err)
			}
			if f.probe.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("retry leaked full document admission")
			}
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		request := f.literalRequest(f.data.Viewer, nil, nil).WithContext(ctx)
		if services[0].documentPresenceAllowed(request, prefix+"public") {
			t.Fatal("legacy canceled lookup became visible")
		}
		if visible, err := services[1].boundedLegacyDocumentPresenceAllowed(request, prefix+"public"); visible || !errors.Is(err, context.Canceled) {
			t.Fatalf("bounded cancellation became absence: visible=%v err=%v", visible, err)
		}
		for _, service := range services {
			source, err := service.BuildEventSource(ctx, workspace, domain.RealtimeEvent{Type: "document.updated", AggregateID: prefix + "public"})
			if source != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled source=%v error=%v", source != nil, err)
			}
		}
		if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("document source leaked work or decoded workspace metadata")
		}
	}
}
