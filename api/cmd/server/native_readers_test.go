package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW24ClassicHTTPConfiguration(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, id := f.data.Workspace.URLKey, idgen.New("sw24-classic-")
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "integrationConnections"), workspace, "scale_p21_sw24.seed", "", nil, func(data *domain.Bootstrap) error {
			data.IntegrationConnections = []domain.IntegrationConnection{{ID: id, Provider: "github", Status: "configured", Config: map[string]string{}}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, spec := range []struct {
			config map[string]string
			want   bool
		}{
			{map[string]string{"webhookSecret": " \x00 "}, false},
			{map[string]string{"webhookSecret\x00": "kept"}, true},
			{map[string]string{"webhookSecret": "\\u0000"}, true},
			{map[string]string{"webhookSecret": "\u2000\u2007\u2028\u3000"}, false},
			{map[string]string{"webhookSecretEnv": " FLOW_GITHUB_SECRET "}, true},
		} {
			r, w := f.request(f.data.Viewer, nil, map[string]any{"config": spec.config}), httptest.NewRecorder()
			r.SetPathValue("id", id)
			r.SetPathValue("provider", "github")
			(&server{store: repo}).updateIntegration(w, r)
			if w.Code != http.StatusOK {
				t.Fatal("config HTTP write failed", w.Code, w.Body.String())
			}
			data, ok := repo.WorkspaceMetadata(workspace)
			if !ok || len(data.IntegrationConnections) != 1 {
				t.Fatal("missing updated integration")
			}
			for key, value := range data.IntegrationConnections[0].Config {
				if strings.ContainsRune(key+value, '\x00') {
					t.Fatal("HTTP input retained NUL in the warm legacy cache")
				}
			}
			raw, err := json.Marshal(data.IntegrationConnections[0])
			if err != nil || !reflect.DeepEqual(raw, store.StripJSONNUL(raw)) {
				t.Fatal("supported integration write did not preserve canonical NUL invariant", err)
			}
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				got, err := (&server{store: source}).classicGitHubSync(t.Context(), workspace)
				if err != nil || got != spec.want {
					t.Fatalf("classic sync predicate=%v err=%v want=%v", got, err, spec.want)
				}
			}
		}
		events := 0
		f.probe.SetWebhookSink(func(string, domain.DomainEvent) { events++ })
		f.probe.SetRealtimeSink(func(string, domain.RealtimeEvent) { events++ })
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := (&server{store: f.probe}).applyGitHubAppWorkspaceEvent(ctx, workspace, "pull_request", codehost.WebhookEvent{}, []byte("{bad JSON"))
		if !errors.Is(err, context.Canceled) || events != 0 {
			t.Fatal("native query failure became false or reached review side effects", err)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("classic sync configuration decoded complete metadata")
		}
	}
}

func TestScaleP21SW24OwnedExportsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw24-export-")
		stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		targetFor := func(user domain.User, index int) string { return fmt.Sprintf("%s-%s-%03d", prefix, user.ID, index) }
		hidden := prefix + "-hidden"
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "exportJobs"), workspace, "scale_p21_sw24.seed", "", nil, func(data *domain.Bootstrap) error {
			data.ExportJobs = []domain.ExportJob{}
			for _, principal := range f.principals {
				for index := range 123 {
					data.ExportJobs = append(data.ExportJobs, domain.ExportJob{
						ID: targetFor(principal.user, index), UserID: principal.user.ID, Format: "csv", IncludePrivate: index%2 == 0,
						Status: "completed", Filename: fmt.Sprintf("owned-%03d.csv", index), CreatedAt: stamp, CompletedAt: &stamp,
					})
				}
			}
			data.ExportJobs[0].Data = strings.Repeat("artifact bytes never serialized ", 30000)
			data.ExportJobs = append(data.ExportJobs, domain.ExportJob{ID: hidden, UserID: prefix + "-other-owner", Status: "failed", Error: strings.Repeat("hidden job body ", 30000), CreatedAt: stamp})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "exportJobs")
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			other := f.data.Viewer
			if other.ID == user.ID {
				other = f.principals[1].user
			}
			for _, spec := range []struct {
				name, id, query string
				list            bool
				want            int
			}{
				{name: "point", id: targetFor(user, 0), want: http.StatusOK},
				{name: "missing", id: targetFor(user, 0) + "-missing", want: http.StatusNotFound},
				{name: "other-owner-oversized", id: hidden, want: http.StatusNotFound},
				{name: "other-member-owner", id: targetFor(other, 0), want: http.StatusNotFound},
				{name: "long-reference", id: strings.Repeat("x", 192), want: http.StatusNotFound},
				{name: "nul-reference", id: targetFor(user, 0) + "\x00", want: http.StatusNotFound},
				{name: "default-page", list: true, want: http.StatusOK},
				{name: "page-two", list: true, query: "?limit=100&cursor=" + base64.RawURLEncoding.EncodeToString([]byte("50")), want: http.StatusOK},
				{name: "invalid-cursor", list: true, query: "?cursor=invalid!&limit=101", want: http.StatusOK},
				{name: "negative-cursor", list: true, query: "?cursor=" + base64.RawURLEncoding.EncodeToString([]byte("-2")), want: http.StatusOK},
				{name: "past-end-cursor", list: true, query: "?cursor=" + base64.RawURLEncoding.EncodeToString([]byte("124")), want: http.StatusOK},
				{name: "at-end-cursor", list: true, query: "?cursor=" + base64.RawURLEncoding.EncodeToString([]byte("123")), want: http.StatusOK},
			} {
				t.Run(spec.name, func(t *testing.T) {
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						r, w := f.request(user, key, nil), httptest.NewRecorder()
						r.Method, r.URL.Path, r.URL.RawQuery = http.MethodGet, "/api/exports", strings.TrimPrefix(spec.query, "?")
						r.SetPathValue("id", spec.id)
						handler := (*server).getExport
						if spec.list {
							handler = (*server).listExports
						} else {
							r.URL.Path += "/" + spec.id
						}
						scaleP21M3Call(&server{store: source}, w, r, handler)
						if w.Code != spec.want {
							t.Fatalf("export status=%d want=%d body=%s", w.Code, spec.want, w.Body.String())
						}
						body := scaleP21M3JSON(t, w.Body.Bytes())
						if spec.list && w.Header().Get("X-Total-Count") != "123" {
							t.Fatal("owned total changed", w.Header())
						}
						if strings.Contains(w.Body.String(), "artifact bytes") || strings.Contains(w.Body.String(), "hidden job body") {
							t.Fatal("export metadata released artifact or another owner's content")
						}
						outcomes = append(outcomes, []any{w.Code, body, w.Header().Get("X-Total-Count"), w.Header().Get("X-Next-Cursor")})
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("export differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
		})
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			key := &domain.APIKey{ID: "sw24-read-only", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}
			r, w := f.request(f.data.Viewer, key, nil), httptest.NewRecorder()
			r.SetPathValue("id", targetFor(f.data.Viewer, 0))
			(&server{store: source}).getExport(w, r)
			if w.Code != http.StatusOK {
				t.Fatal("read-only selected-empty key lost owned export metadata")
			}
		}
		// An admin's ordinary read still belongs to that user; auth-disabled
		// mode deliberately retains its older all-owner view.
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", targetFor(f.principals[2].user, 0))
			(&server{store: source, authDisabled: true}).getExport(w, r)
			if w.Code != http.StatusOK {
				t.Fatal("development all-owner export behavior changed")
			}
		}
		member := f.principals[2].user
		queued := f.request(member, nil, nil)
		queued.SetPathValue("id", targetFor(member, 0))
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replayed"} {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				for _, handler := range []func(*server, http.ResponseWriter, *http.Request){(*server).getExport, (*server).listExports} {
					w := httptest.NewRecorder()
					scaleP21M3Call(&server{store: source}, w, queued, handler)
					if w.Code != http.StatusForbidden {
						t.Fatalf("%s export reused revoked membership", phase)
					}
				}
			}
		}
		if before != scaleP21M3Fingerprint(t, repo, workspace, "exportJobs") || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("export reader changed canonical rows or decoded complete metadata")
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW24 export foreign", idgen.New("sw24-export-foreign-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		f.probe, err = repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			for _, list := range []bool{false, true} {
				r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
				r.Method, r.URL.Path = http.MethodGet, "/api/exports"
				r.Header.Set("X-Workspace-Key", foreign.Workspace.URLKey)
				r.SetPathValue("workspaceKey", foreign.Workspace.URLKey)
				r.SetPathValue("id", targetFor(f.data.Viewer, 0))
				handler := (*server).getExport
				want := http.StatusNotFound
				if list {
					handler, want = (*server).listExports, http.StatusOK
				}
				scaleP21M3Call(&server{store: source}, w, r, handler)
				if w.Code != want || list && (w.Header().Get("X-Total-Count") != "0" || w.Body.String() != "[]\n") {
					t.Fatal("export metadata escaped its workspace", w.Code, w.Body.String())
				}
			}
		}
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "exportJobs"), workspace, "scale_p21_sw24.large", "", nil, func(data *domain.Bootstrap) error {
			data.ExportJobs[0].Error = strings.Repeat("selected job error ", 25000)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, handler := range []func(*server, http.ResponseWriter, *http.Request){(*server).getExport, (*server).listExports} {
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", targetFor(f.data.Viewer, 0))
			handler(&server{store: f.probe}, w, r)
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) {
				t.Fatal("oversized selected export did not report its budget", w.Code, w.Body.String())
			}
		}
		r, w := f.request(f.principals[3].user, nil, nil), httptest.NewRecorder()
		r.SetPathValue("id", targetFor(f.data.Viewer, 0))
		(&server{store: f.probe}).getExport(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatal("unowned oversized export disclosed its size")
		}
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "exportJobs"), workspace, "scale_p21_sw24.reset", "", nil, func(data *domain.Bootstrap) error {
			data.ExportJobs[0].Error = ""
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		r, w = f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
		r.SetPathValue("id", targetFor(f.data.Viewer, 0))
		(&server{store: f.probe}).getExport(w, r)
		if w.Code != http.StatusOK {
			t.Fatal("export after budget failure did not recover")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		r, w = f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
		r.SetPathValue("id", targetFor(f.data.Viewer, 0))
		(&server{store: f.probe}).getExport(w, r.WithContext(ctx))
		if w.Body.Len() > 0 && w.Code < http.StatusBadRequest || strings.Contains(w.Body.String(), `"format"`) || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("cancelled export released owned data or decoded complete metadata")
		}
	}
}
