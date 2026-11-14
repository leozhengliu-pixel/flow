package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW16ProjectTemplateListDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_FILE_URL_SIGNING_KEY", "sw16-template-signing")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		stamp := time.Date(2026, 1, 2, 3, 4, 5, 123, time.FixedZone("source-zone", 7*3600+123))
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "sw16.template_list_seed", "", nil, func(data *domain.Bootstrap) error {
			data.ProjectTemplates = []domain.ProjectTemplate{
				{ID: "template-native", Name: "Native\xff", Description: "body <>&\x00\xff /uploads/sw16-native.png",
					CreatedAt: stamp, UpdatedAt: stamp, TeamIDs: []string{}, LabelIDs: nil,
					Creator: domain.User{ID: "creator\x00", Name: "Embedded actor", OutOfOfficeUntil: &stamp, AppScopes: []string{}},
					Issues:  []domain.ProjectTemplateIssue{{ID: "nested", Title: "Exact nested", LabelIDs: []string{}}}},
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		before := scaleP21M2UnrelatedHash(t, t.Context(), repo, f.data.Workspace.URLKey)
		legacy := func(source *store.SQLiteStore, w http.ResponseWriter, r *http.Request) {
			data, found := source.BootstrapFor(workspaceKey(r))
			if !found {
				writeError(w, http.StatusNotFound, "workspace not found")
				return
			}
			writeJSON(w, http.StatusOK, data.ProjectTemplates)
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				want, got := httptest.NewRecorder(), httptest.NewRecorder()
				request := f.request(user, key, nil)
				request.Method, request.URL.Path = http.MethodGet, "/api/project-templates"
				legacy(source, want, request)
				(&server{store: source}).scaleP21SW16ListProjectTemplates(got, request)
				if want.Code != got.Code || !bytes.Equal(want.Body.Bytes(), got.Body.Bytes()) {
					t.Fatalf("same actual source output differs: %d/%d %.120s/%.120s", want.Code, got.Code, want.Body.Bytes(), got.Body.Bytes())
				}
				if source.BoundedMetadataEnabled() {
					dispatched := httptest.NewRecorder()
					(&server{store: source}).listProjectTemplates(dispatched, request)
					if dispatched.Code != got.Code || !bytes.Equal(dispatched.Body.Bytes(), got.Body.Bytes()) {
						t.Fatal("actual bounded dispatch differs from direct adapter")
					}
				}
			}
		})
		for _, signed := range []bool{false, true} {
			request := f.request(f.data.Viewer, nil, nil)
			request.Method, request.URL.Path = http.MethodGet, "/api/project-templates"
			if signed {
				request.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
			}
			want, got := httptest.NewRecorder(), httptest.NewRecorder()
			service := &server{store: repo}
			signingStarted := time.Now()
			service.signedFileURLs(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { legacy(repo, w, r) })).ServeHTTP(want, request)
			service.signedFileURLs(http.HandlerFunc(service.scaleP21SW16ListProjectTemplates)).ServeHTTP(got, request)
			oldBody, newBody := want.Body.Bytes(), got.Body.Bytes()
			if signed {
				normalizeClock := func(raw []byte) []byte {
					t.Helper()
					var templates []domain.ProjectTemplate
					if err := json.Unmarshal(raw, &templates); err != nil || len(templates) != 1 {
						t.Fatal("signed template response invalid", err)
					}
					description := templates[0].Description
					offset := strings.LastIndex(description, " ") + 1
					value, err := url.Parse(description[offset:])
					if err != nil || value.Path != "/api/files/signed/sw16-native.png" {
						t.Fatal("signed template URL path changed", err)
					}
					expires, err := strconv.ParseInt(value.Query().Get("expires"), 10, 64)
					signingKey, keyErr := service.fileSigningKey(t.Context())
					expiry := time.Unix(expires, 0)
					if err != nil || keyErr != nil || expiry.Before(signingStarted.Add(time.Hour).Truncate(time.Second)) ||
						expiry.After(time.Now().Add(time.Hour).Truncate(time.Second)) ||
						value.Query().Get("signature") != fileURLSignature(signingKey, "sw16-native.png", expires) {
						t.Fatal("signed template URL signature or expiry invalid", err, keyErr)
					}
					// Only verified generated clock/signature values vary
					// across calls that straddle a wall-clock second.
					value.RawQuery = ""
					templates[0].Description = description[:offset] + value.String()
					normalized, err := json.Marshal(templates)
					if err != nil {
						t.Fatal(err)
					}
					return normalized
				}
				oldBody, newBody = normalizeClock(oldBody), normalizeClock(newBody)
			}
			if got.Code != want.Code || !bytes.Equal(newBody, oldBody) {
				t.Fatal("signed native template output differs", signed, want.Code, got.Code)
			}
		}
		request := f.request(f.data.Viewer, nil, nil)
		ctx, cancel := context.WithCancel(request.Context())
		cancel()
		want, got := httptest.NewRecorder(), httptest.NewRecorder()
		legacy(repo, want, request.WithContext(ctx))
		(&server{store: repo}).scaleP21SW16ListProjectTemplates(got, request.WithContext(ctx))
		if got.Code != want.Code || !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) {
			t.Fatal("context-insensitive legacy template read changed")
		}
		request.SetPathValue("workspaceKey", "sw16-missing-workspace")
		request.SetPathValue("workspace", "sw16-missing-workspace")
		request.Header.Set("X-Workspace-Key", "sw16-missing-workspace")
		want, got = httptest.NewRecorder(), httptest.NewRecorder()
		legacy(repo, want, request)
		(&server{store: repo}).scaleP21SW16ListProjectTemplates(got, request)
		if got.Code != http.StatusNotFound || got.Code != want.Code || !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) {
			t.Fatal("missing workspace response differs")
		}
		if scaleP21M2UnrelatedHash(t, t.Context(), repo, f.data.Workspace.URLKey) != before || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("list changed unrelated rows or decoded complete workspace")
		}
	}
}

func TestScaleP21SW16ProjectTemplateLegacyLargeBaseline(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_FILE_URL_SIGNING_KEY", "sw16-template-signing")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		body := strings.Repeat("x", 2<<20) + " /uploads/sw16-description.png"
		r, w := f.literalRequest(f.data.Viewer, nil, map[string]any{"name": "Large template", "description": body}), httptest.NewRecorder()
		(&server{store: repo}).createProjectTemplate(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("legal 2 MiB description rejected by existing writer: %d %.160s", w.Code, w.Body.String())
		}
		var created domain.ProjectTemplate
		if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.Description != body {
			t.Fatal("legacy creation did not preserve the complete description", err)
		}
		call := func(signed bool) *httptest.ResponseRecorder {
			t.Helper()
			r, w := f.literalRequest(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.Method, r.URL.Path = http.MethodGet, "/api/project-templates"
			if signed {
				r.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
			}
			service := &server{store: repo}
			service.signedFileURLs(http.HandlerFunc(service.listProjectTemplates)).ServeHTTP(w, r)
			return w
		}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		baseline := call(false)
		runtime.ReadMemStats(&after)
		t.Logf("legacy large template list bytes=%d allocated-bytes=%d allocations=%d", baseline.Body.Len(), after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs)
		if baseline.Code != http.StatusOK || !bytes.Contains(baseline.Body.Bytes(), []byte(body)) {
			t.Fatal("legacy list truncated legal large description", baseline.Code)
		}
		oldSQL, newSQL := 0, 0
		if _, found := repo.BootstrapForContext(store.WithSQLStatementObserver(t.Context(), func(string) { oldSQL++ }), f.data.Workspace.URLKey); !found {
			t.Fatal("observed uncancelled original Bootstrap implementation failed")
		}
		read, found, err := repo.BeginProjectTemplatesRead(store.WithSQLStatementObserver(t.Context(), func(string) { newSQL++ }), f.data.Workspace.URLKey)
		if err != nil || !found {
			t.Fatal("observed template source failed", found, err)
		}
		read.Close()
		t.Logf("equivalent active-context SQL legacy=%d candidate=%d (BootstrapFor itself replaces context with Background)", oldSQL, newSQL)
		for pair := 0; pair < 3; pair++ {
			for _, candidate := range []bool{false, true} {
				runtime.GC()
				runtime.ReadMemStats(&before)
				sql := 0
				r, w := f.literalRequest(f.data.Viewer, nil, nil), httptest.NewRecorder()
				r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(string) { sql++ }))
				if candidate {
					(&server{store: repo}).scaleP21SW16ListProjectTemplates(w, r)
				} else {
					(&server{store: repo}).listProjectTemplates(w, r)
				}
				runtime.ReadMemStats(&after)
				t.Logf("pair=%d candidate=%t bytes=%d allocs=%d observed-request-SQL=%d", pair, candidate, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, sql)
				if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), baseline.Body.Bytes()) {
					t.Fatal("large admitted native template list differs", candidate, w.Code)
				}
			}
		}
		for _, budget := range []int{0, 64 << 10, 16 << 20} {
			t.Run("spool="+strconv.Itoa(budget), func(t *testing.T) {
				t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", strconv.Itoa(budget))
				plain, signed := call(false), call(true)
				if plain.Code != http.StatusOK || !bytes.Equal(plain.Body.Bytes(), baseline.Body.Bytes()) {
					t.Fatal("spool cap changed unsigned legacy list")
				}
				if signed.Code != http.StatusOK || !json.Valid(signed.Body.Bytes()) ||
					bytes.Contains(signed.Body.Bytes(), []byte(` /uploads/sw16-description.png`)) ||
					!bytes.Contains(signed.Body.Bytes(), []byte("sw16-description.png?")) {
					t.Fatal("legacy signing failed with bounded/no spool", signed.Code)
				}
			})
		}
		t.Run("temporary-directory-unavailable", func(t *testing.T) {
			t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
			for _, signed := range []bool{false, true} {
				response := call(signed)
				if response.Code != http.StatusOK || !json.Valid(response.Body.Bytes()) {
					t.Fatal("temporary directory failure changed legacy list status")
				}
			}
		})
	}
}
