package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21ResourceRequestWriter struct {
	*httptest.ResponseRecorder
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
	err     func() error
	panicAt any
}

func (w *scaleP21ResourceRequestWriter) Write(raw []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.resume
	if w.panicAt != nil {
		panic(w.panicAt)
	}
	if w.err != nil {
		if err := w.err(); err != nil {
			return 0, err
		}
	}
	return w.ResponseRecorder.Write(raw)
}

func scaleP21ResourceRequest(t *testing.T, f scaleP21M2Fixture, client *http.Client, kind, ref string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/"+kind+"/"+ref, nil)
	r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
	r.SetPathValue("id", ref)
	origin, err := url.Parse(f.host.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range client.Jar.Cookies(origin) {
		r.AddCookie(cookie)
	}
	return r
}

func scaleP21ResourceRequestServe(s *server, kind string, w http.ResponseWriter, r *http.Request) {
	if kind == "projects" {
		s.getProjectRecord(w, r)
	} else {
		s.getInitiativeRecord(w, r)
	}
}

func scaleP21ResourceRequestReleased(t *testing.T, repo *store.SQLiteStore, handoff *resourceReadHandoff, owner *store.ResourceRead) {
	t.Helper()
	if handoff == nil || !handoff.released || handoff.owner != nil || handoff.allowed ||
		!reflect.DeepEqual(handoff.data, domain.Bootstrap{}) || !reflect.DeepEqual(handoff.access, store.AccessContext{}) {
		t.Fatal("request retained released detail or authority")
	}
	if owner != nil && (!reflect.DeepEqual(owner.Data, domain.Bootstrap{}) || owner.Allowed || owner.SourceRetainedForTesting()) {
		t.Fatal("closed detail retained data or its source")
	}
	if used := repo.MetadataWorkUsedForTesting(); used != 0 {
		t.Fatalf("request retained %d admitted bytes", used)
	}
}

func TestScaleP21ResourceRequestLifetime(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		prefix := idgen.New("p21-request-resource-")
		projectID, initiativeID := prefix+"project", prefix+"initiative"
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.resource_request_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = append(data.Projects, domain.Project{
				ID: projectID, SlugID: prefix + "project-current", PreviousSlugIDs: []string{prefix + "project-old"},
				Name: "Request project", Description: strings.Repeat("owned detail ", 30000),
				TeamIDs: []string{f.public}, Lead: &data.Viewer,
				Comments: []domain.Comment{{ID: prefix + "comment", Body: "Retained comment", User: data.Viewer}},
			})
			data.Initiatives = append(data.Initiatives, domain.Initiative{
				ID: initiativeID, SlugID: prefix + "initiative-current", PreviousSlugIDs: []string{prefix + "initiative-old"},
				Name: "Request initiative", ProjectIDs: []string{projectID}, ContributingTeamIDs: []string{f.public},
				Creator: data.Viewer, Owner: &data.Viewer,
			})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		service := &server{store: f.probe}
		for _, resource := range []struct{ kind, ref string }{{"projects", projectID}, {"initiatives", initiativeID}} {
			for _, exit := range []string{"normal", "canceled", "panic"} {
				t.Run(resource.kind+"/"+exit, func(t *testing.T) {
					request := scaleP21ResourceRequest(t, f, f.admin, resource.kind, resource.ref)
					ctx, cancel := context.WithCancel(request.Context())
					defer cancel()
					request = request.WithContext(ctx)
					writer := &scaleP21ResourceRequestWriter{
						ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), resume: make(chan struct{}),
					}
					var resume sync.Once
					release := func() { resume.Do(func() { close(writer.resume) }) }
					defer release()
					if exit == "canceled" {
						writer.err = ctx.Err
					}
					if exit == "panic" {
						writer.panicAt = "resource encoding panic"
					}
					type capturedRead struct {
						handoff *resourceReadHandoff
						owner   *store.ResourceRead
						scope   *requestResourceReads
					}
					captured := make(chan capturedRead, 1)
					handler := service.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						handoff, _ := r.Context().Value(resourceReadHandoffKey{}).(*resourceReadHandoff)
						scope, _ := r.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
						var owner *store.ResourceRead
						if handoff != nil {
							owner = handoff.owner
						}
						captured <- capturedRead{handoff, owner, scope}
						scaleP21ResourceRequestServe(service, resource.kind, w, r)
					}))
					done := make(chan any, 1)
					go func() {
						defer func() { done <- recover() }()
						handler.ServeHTTP(writer, request)
					}()
					select {
					case <-writer.entered:
					case value := <-done:
						t.Fatalf("request ended before response encoding: %v", value)
					case <-time.After(10 * time.Second):
						t.Fatal("request did not reach response encoding")
					}
					var held capturedRead
					select {
					case held = <-captured:
					default:
						release()
						<-done
						t.Fatalf("authorization denied fixture: status=%d", writer.Code)
					}
					if held.handoff == nil || held.owner == nil || held.scope == nil ||
						held.handoff.owner != held.owner || held.handoff.released || !held.handoff.allowed ||
						!held.owner.SourceRetainedForTesting() || f.probe.MetadataWorkUsedForTesting() == 0 {
						release()
						<-done
						t.Fatal("authorization/handler handoff released its detail before encoding")
					}
					if resource.kind == "projects" && len(held.owner.Data.Projects) != 1 ||
						resource.kind == "initiatives" && len(held.owner.Data.Initiatives) != 1 {
						release()
						<-done
						t.Fatal("encoding did not retain its selected detail")
					}
					if exit == "canceled" {
						cancel()
						if !held.owner.SourceRetainedForTesting() || f.probe.MetadataWorkUsedForTesting() == 0 {
							release()
							<-done
							t.Fatal("cancellation uncharged a response still being encoded")
						}
					}
					release()
					select {
					case value := <-done:
						if exit == "panic" && value != writer.panicAt || exit != "panic" && value != nil {
							t.Fatalf("unexpected request panic: %v", value)
						}
					case <-time.After(10 * time.Second):
						t.Fatal("request did not finish after response release")
					}
					scaleP21ResourceRequestReleased(t, f.probe, held.handoff, held.owner)
					held.scope.close()
					scaleP21ResourceRequestReleased(t, f.probe, held.handoff, held.owner)
				})
			}
		}
		member := authClient(t)
		authRequest[domain.AuthSession](t, member, http.MethodPost, f.host.URL+"/api/auth/login", map[string]string{"email": f.principals[2].user.Email, "password": "p03-fixture-password"}, "", http.StatusOK)
		t.Run("denied-response", func(t *testing.T) {
			writer := &scaleP21ResourceRequestWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), resume: make(chan struct{})}
			var resume sync.Once
			release := func() { resume.Do(func() { close(writer.resume) }) }
			defer release()
			var next atomic.Bool
			handler := service.authenticate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { next.Store(true) }))
			done := make(chan struct{})
			go func() {
				defer close(done)
				handler.ServeHTTP(writer, scaleP21ResourceRequest(t, f, member, "projects", "m2-unrelated"))
			}()
			select {
			case <-writer.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("denial did not reach encoding")
			}
			if next.Load() || writer.Code != http.StatusForbidden || f.probe.MetadataWorkUsedForTesting() == 0 {
				release()
				<-done
				t.Fatal("denied detail was not held through the denial response")
			}
			release()
			<-done
			if used := f.probe.MetadataWorkUsedForTesting(); used != 0 {
				t.Fatalf("denied request leaked %d bytes", used)
			}
		})
		t.Run("retry-release", func(t *testing.T) {
			mutated, retried := false, false
			var mutationErr error
			var resolveBytes, heldBefore, heldAtRetry int
			request := scaleP21ResourceRequest(t, f, f.admin, "projects", projectID)
			request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(query string) {
				used := f.probe.MetadataWorkUsedForTesting()
				if !mutated && strings.Contains(query, "FROM workspace_memberships m JOIN auth_users") {
					resolveBytes = used
				}
				if !mutated && used > resolveBytes && strings.HasPrefix(query, "SELECT COALESCE((SELECT epoch FROM workspace_authz_epochs") {
					mutated, heldBefore = true, used
					mutationErr = repo.MutateWorkspace(t.Context(), workspace, "workspace.settings_updated", "", nil, func(data *domain.Bootstrap) error {
						data.WorkspaceSettings.RestrictFileUploads = !data.WorkspaceSettings.RestrictFileUploads
						return nil
					})
				}
				if mutated && !retried && strings.Contains(query, "FROM workspace_memberships m JOIN auth_users") {
					retried, heldAtRetry = true, used
				}
			}))
			var handoff *resourceReadHandoff
			var owner *store.ResourceRead
			handler := service.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handoff, _ = r.Context().Value(resourceReadHandoffKey{}).(*resourceReadHandoff)
				if handoff != nil {
					owner = handoff.owner
				}
				service.getProjectRecord(w, r)
			}))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, request)
			if mutationErr != nil || !mutated || !retried || heldBefore <= resolveBytes || heldAtRetry != resolveBytes || w.Code != http.StatusOK {
				t.Fatalf("retry ownership changed: mutation=%v triggered=%t retried=%t bytes=%d/%d resolve=%d status=%d body-bytes=%d", mutationErr, mutated, retried, heldBefore, heldAtRetry, resolveBytes, w.Code, w.Body.Len())
			}
			scaleP21ResourceRequestReleased(t, f.probe, handoff, owner)
		})
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		t.Run("direct-terminals", func(t *testing.T) {
			for _, operation := range []string{"authorization", "denied-authorization", "handler", "shadow-authorization", "shadow-handler"} {
				mode := "on"
				if strings.HasPrefix(operation, "shadow") {
					mode = "shadow"
				}
				t.Setenv("FLOW_RECORD_ACCESS", mode)
				request := f.literalRequest(f.data.Viewer, nil, nil)
				request.Method, request.URL.Path = http.MethodGet, "/api/projects/"+projectID
				request.SetPathValue("id", projectID)
				switch operation {
				case "authorization":
					if !service.resourceAllowed(request, workspace, f.data.Viewer.ID) {
						t.Fatal("direct authorization refused target")
					}
				case "denied-authorization":
					request = f.literalRequest(f.principals[2].user, nil, nil)
					request.Method, request.URL.Path = http.MethodGet, "/api/projects/m2-unrelated"
					if service.resourceAllowed(request, workspace, f.principals[2].user.ID) {
						t.Fatal("direct authorization exposed private detail")
					}
				case "handler":
					w := httptest.NewRecorder()
					if !service.serveResourceRead(w, request, "projects") || w.Code != http.StatusOK {
						t.Fatal("direct detail handler refused target")
					}
				case "shadow-authorization":
					if !service.shadowResourceAuthorization(request, workspace, f.data.Viewer.ID, true) {
						t.Fatal("shadow changed legacy authorization")
					}
				case "shadow-handler":
					service.shadowResourceRecord(request, "projects", domain.Project{ID: projectID})
				}
				handoff, _ := request.Context().Value(resourceReadHandoffKey{}).(*resourceReadHandoff)
				scaleP21ResourceRequestReleased(t, f.probe, handoff, nil)
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("owned request decoded full metadata")
		}
	}
}

func TestScaleP21ResourceRequestOffContextAndHTTPParity(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		prefix := idgen.New("p21-resource-wire-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.resource_wire_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = append(data.Projects, domain.Project{ID: prefix + "project", SlugID: prefix + "current-project", PreviousSlugIDs: []string{prefix + "old-project"}, Name: "Project", Description: "Owned wire detail", TeamIDs: []string{f.public}, Comments: []domain.Comment{{ID: prefix + "comment", Body: "Comment", User: data.Viewer}}})
			data.Initiatives = append(data.Initiatives, domain.Initiative{ID: prefix + "initiative", SlugID: prefix + "current-initiative", PreviousSlugIDs: []string{prefix + "old-initiative"}, Name: "Initiative", ProjectIDs: []string{prefix + "project"}, Creator: data.Viewer})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		type contextMarker struct{}
		marker := &struct{}{}
		for _, state := range []string{"warm", "reloaded", "canonical"} {
			if state == "reloaded" {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			for _, resource := range []struct{ kind, ref string }{{"projects", prefix + "old-project"}, {"initiatives", prefix + "old-initiative"}} {
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					service := &server{store: source}
					request := scaleP21ResourceRequest(t, f, f.admin, resource.kind, resource.ref)
					ctx, cancel := context.WithTimeout(context.WithValue(request.Context(), contextMarker{}, marker), time.Minute)
					defer cancel()
					request = request.WithContext(ctx)
					originalDeadline, _ := request.Context().Deadline()
					drifted, canonicalQueries := false, 0
					var driftErr error
					if state == "canonical" && source.BoundedMetadataEnabled() {
						request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(query string) {
							if !drifted && strings.Contains(query, "FROM workspace_states CROSS JOIN record_journal_mode") {
								drifted = true
								driftErr = repo.MutateWorkspace(t.Context(), workspace, "workspace.settings_updated", "", nil, func(data *domain.Bootstrap) error {
									data.WorkspaceSettings.RestrictFileUploads = !data.WorkspaceSettings.RestrictFileUploads
									return nil
								})
							}
							if drifted && source.MetadataWorkUsedForTesting() > 0 && strings.Contains(query, "workspace_metadata_records") {
								canonicalQueries++
							}
						}))
					}
					var offFailure string
					handler := service.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						deadline, ok := r.Context().Deadline()
						if r.Context().Value(contextMarker{}) != marker || !ok || deadline != originalDeadline {
							offFailure = "request context value or deadline changed"
						}
						if !source.BoundedMetadataEnabled() {
							if r.Context().Value(requestResourceReadsKey{}) != nil {
								offFailure = "off mode injected a resource owner scope"
							}
							if result, _ := r.Context().Value(resourceReadHandoffKey{}).(*resourceReadHandoff); result == nil || result.owner != nil || result.bounded {
								offFailure = "off mode replaced the literal unowned accessor"
							}
						}
						scaleP21ResourceRequestServe(service, resource.kind, w, r)
					}))
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, request)
					if offFailure != "" || w.Code != http.StatusOK {
						t.Fatalf("state=%s kind=%s bounded=%t context=%s status=%d body=%s", state, resource.kind, source.BoundedMetadataEnabled(), offFailure, w.Code, w.Body.String())
					}
					if state == "canonical" && source.BoundedMetadataEnabled() && (driftErr != nil || !drifted || canonicalQueries == 0) {
						t.Fatalf("canonical HTTP fallback was not exercised: drift=%t err=%v selected SQL=%d", drifted, driftErr, canonicalQueries)
					}
					var payload any
					if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
						t.Fatal(err)
					}
					outcomes = append(outcomes, payload)
					if source.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("HTTP parity request leaked owned work")
					}
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("state=%s kind=%s HTTP output changed: %s", state, resource.kind, scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			}
		}
		t.Setenv("FLOW_RECORD_ACCESS", "off")
		request := f.literalRequest(f.data.Viewer, nil, nil)
		request.Method, request.URL.Path = http.MethodGet, "/api/projects/"+prefix+"project"
		original := request.Context()
		if (&server{store: repo}).serveResourceRead(httptest.NewRecorder(), request, "projects") || request.Context() != original {
			t.Fatal("literal off handler changed its input context")
		}
		cancelled, cancel := context.WithCancel(original)
		cancel()
		request = request.WithContext(cancelled)
		if (&server{store: repo}).serveResourceRead(httptest.NewRecorder(), request, "projects") || request.Context() != cancelled || !errors.Is(request.Context().Err(), context.Canceled) {
			t.Fatal("literal off handler changed cancellation")
		}
	}
}
