package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Start after authentication policy so contention reaches the resource read.
func resourceAuthorizationStage(s *server, mux *http.ServeMux, workspace string, user domain.User) http.Handler {
	return s.withColdMetadataRouteMux(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(store.ContextWithActor(r.Context(), user), authUserContextKey{}, user)
		r, release := beginRequestResourceReads(r.WithContext(ctx))
		defer release()
		if s.authorizeResourceRequest(w, r, workspace, user.ID) {
			mux.ServeHTTP(w, r)
		}
	}))
}

func resourceAuthorizationRequest(t *testing.T, handler http.Handler, workspace, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	r.Header.Set("X-Workspace-Key", workspace)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestBoundedSavedViewMutationAuthorizationBudget(t *testing.T) {
	peers, f, _, project := scaleActivationHTTPPeers(t, "current")
	peer := peers[0]
	s := peer.service
	repo, workspace := s.store, f.data.Workspace.URLKey
	restore := store.SetMetadataWorkRetryWaitForTesting(50 * time.Millisecond)
	defer restore()

	for _, tc := range []struct {
		method, body, event string
		handler             http.HandlerFunc
		want                int
	}{
		{http.MethodPatch, `{"name":"Renamed view"}`, "view.updated", s.updateSavedView, http.StatusOK},
		{http.MethodDelete, "", "view.deleted", s.deleteSavedView, http.StatusNoContent},
	} {
		t.Run(tc.method, func(t *testing.T) {
			view := authRequest[domain.SavedView](t, f.admin, http.MethodPost, peer.URL+"/api/views",
				map[string]string{"name": "Authorization budget view", "projectId": project}, workspace, http.StatusCreated)
			pattern, path := tc.method+" /api/views/{id}", "/api/views/"+view.ID
			mux := http.NewServeMux()
			mux.HandleFunc(pattern, tc.handler)
			stage := resourceAuthorizationStage(s, mux, workspace, f.data.Viewer)
			events := func() int { return loadErrorsEventCount(t, repo, tc.event) }
			before := events()
			run := func(handler http.Handler, path string) *httptest.ResponseRecorder {
				return resourceAuthorizationRequest(t, handler, workspace, tc.method, path, tc.body)
			}
			if denied := run(stage, "/api/views/unavailable-view"); denied.Code != http.StatusForbidden || denied.Header().Get("Retry-After") != "" {
				t.Fatalf("unavailable view returned %d, Retry-After=%q", denied.Code, denied.Header().Get("Retry-After"))
			}

			// Leave enough work to resolve access, then refuse the view headers.
			release := holdLoadErrorsWork(t, repo, 2<<20)
			busy := run(stage, path)
			release()
			if retry := busy.Header().Get("Retry-After"); busy.Code != http.StatusServiceUnavailable || retry != "2" || events() != before {
				t.Fatalf("held work returned %d, Retry-After=%q, events=%d want %d", busy.Code, retry, events(), before)
			}

			// Force the writer to refresh an obsolete set of view headers.
			retryMux := http.NewServeMux()
			var releaseRetry func()
			retryMux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
				proof, ok := r.Context().Value(savedViewAuthorizationKey{}).(*store.SavedViewAuthorization)
				if !ok {
					t.Fatal("missing authorization proof")
				}
				bindSavedViewAuthorization(r, proof.Access, proof.Ref, domain.Bootstrap{})
				ctx := store.WithSQLStatementObserver(r.Context(), func(query string) {
					if releaseRetry != nil || !strings.Contains(query, "FROM workspace_states CROSS JOIN record_journal_mode") {
						return
					}
					// ResolveAccess holds its first lease at this query.
					// Force a stale policy read that needs a second lease.
					if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
						t.Fatal(err)
					}
					releaseRetry = holdLoadErrorsWork(t, repo, 0)
				})
				tc.handler(w, r.WithContext(ctx))
			})
			busy = run(resourceAuthorizationStage(s, retryMux, workspace, f.data.Viewer), path)
			if releaseRetry == nil {
				t.Fatal("writer did not refresh authorization")
			}
			releaseRetry()
			if retry := busy.Header().Get("Retry-After"); busy.Code != http.StatusServiceUnavailable || retry != "2" || events() != before {
				t.Fatalf("held refresh returned %d, Retry-After=%q, events=%d want %d", busy.Code, retry, events(), before)
			}
			if ok := run(stage, path); ok.Code != tc.want || events() != before+1 {
				t.Fatalf("released work returned %d, want %d and one event", ok.Code, tc.want)
			}
		})
	}
}

func TestScaleActivationPointResourceAuthorizationBudget(t *testing.T) {
	f := harnessSetup(t, nil)
	repo, workspace, user := f.f.probe, f.workspace, f.f.data.Viewer
	if err := repo.RefreshInstalledMetadataViewForTesting(t.Context(), f.repo, workspace); err != nil {
		t.Fatal(err)
	}
	s := &server{store: repo}
	project := "/api/projects/" + f.ids["pub-project"]
	initiative := "/api/initiatives/" + f.ids["pub-initiative"]
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects/{id}", s.getProjectRecord)
	mux.HandleFunc("GET /api/initiatives/{id}", s.getInitiativeRecord)
	mux.HandleFunc("GET /api/projects/{id}/relations", s.listProjectRelations)
	stage := resourceAuthorizationStage(s, mux, workspace, user)

	restore := store.SetMetadataWorkRetryWaitForTesting(50 * time.Millisecond)
	defer restore()
	for _, mode := range []string{"on", "off", "shadow"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_ACCESS", mode)
			for _, tc := range []struct{ method, path string }{
				{http.MethodGet, project}, {http.MethodHead, initiative}, {http.MethodGet, project + "/relations"},
			} {
				release := holdLoadErrorsWork(t, repo, 1<<20)
				busy := resourceAuthorizationRequest(t, stage, workspace, tc.method, tc.path, "")
				release()
				if retry := busy.Header().Get("Retry-After"); busy.Code != http.StatusServiceUnavailable || retry != "2" {
					t.Fatalf("%s %s with held work returned %d, Retry-After=%q", tc.method, tc.path, busy.Code, retry)
				}
				if ok := resourceAuthorizationRequest(t, stage, workspace, tc.method, tc.path, ""); ok.Code != http.StatusOK {
					t.Fatalf("%s %s after release returned %d", tc.method, tc.path, ok.Code)
				}
			}
			denied := resourceAuthorizationRequest(t, stage, workspace, http.MethodGet, "/api/projects/unavailable-project", "")
			if denied.Code != http.StatusForbidden || denied.Header().Get("Retry-After") != "" {
				t.Fatalf("unavailable project returned %d, Retry-After=%q", denied.Code, denied.Header().Get("Retry-After"))
			}
		})
	}
	t.Run("record-handler", func(t *testing.T) {
		t.Setenv("FLOW_RECORD_ACCESS", "on")
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, user)))
		})
		release := holdLoadErrorsWork(t, repo, 1<<20)
		busy := resourceAuthorizationRequest(t, handler, workspace, http.MethodGet, project, "")
		release()
		if retry := busy.Header().Get("Retry-After"); busy.Code != http.StatusServiceUnavailable || retry != "2" {
			t.Fatalf("record handler returned %d, Retry-After=%q", busy.Code, retry)
		}
	})
	t.Run("record-mutations", func(t *testing.T) {
		mux := http.NewServeMux()
		reached := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
		mux.HandleFunc("PATCH /api/projects/{id}", reached)
		mux.HandleFunc("POST /api/documents", reached)
		stage := resourceAuthorizationStage(s, mux, workspace, user)
		for _, tc := range []struct{ method, path, body string }{
			{http.MethodPatch, project, `{"name":"Renamed project"}`},
			{http.MethodPost, "/api/documents", `{"title":"New document","projectIds":["` + f.ids["pub-project"] + `"]}`},
		} {
			release := holdLoadErrorsWork(t, repo, 2<<20)
			busy := resourceAuthorizationRequest(t, stage, workspace, tc.method, tc.path, tc.body)
			release()
			if retry := busy.Header().Get("Retry-After"); busy.Code != http.StatusServiceUnavailable || retry != "2" {
				t.Fatalf("%s %s with held work returned %d, Retry-After=%q", tc.method, tc.path, busy.Code, retry)
			}
			if ok := resourceAuthorizationRequest(t, stage, workspace, tc.method, tc.path, tc.body); ok.Code != http.StatusNoContent {
				t.Fatalf("%s %s after release returned %d", tc.method, tc.path, ok.Code)
			}
		}
	})
}
