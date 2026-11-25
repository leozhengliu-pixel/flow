package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21DocumentDirectoryHTTPTable(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("p21-directory-http-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.directory_http", "", nil, func(data *domain.Bootstrap) error {
			private, child := "", ""
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
				if strings.HasPrefix(team.ID, "m2-child-") {
					child = team.ID
				}
			}
			at := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
			data.Documents = []domain.Document{
				{ID: prefix + "first", Title: "Boundary", Content: "Start \u03a9 \u1e9e body-only", ContentState: "state-only", ContentData: map[string]any{"data-only": true}, Creator: data.Viewer, TeamIDs: []string{f.public}, UpdatedAt: at},
				{ID: prefix + "second", Title: "Second", Content: "Second body", Creator: data.Viewer, TeamIDs: []string{f.public}, UpdatedAt: at},
				{ID: prefix + "first", Title: "Duplicate", Content: "Other occurrence", Creator: data.Viewer, TeamIDs: []string{f.public}, UpdatedAt: at},
				{ID: prefix + "private", Title: "Private", Content: "Private body", Creator: data.Viewer, TeamIDs: []string{private}, UpdatedAt: at.Add(time.Hour)},
				{ID: prefix + "inherited", Title: "Inherited", Content: "Inherited body", Creator: data.Viewer, InheritedTeamIDs: []string{private}, UpdatedAt: at},
				{ID: prefix + "child", Title: "Child", Creator: data.Viewer, TeamIDs: []string{child}, UpdatedAt: at},
				{ID: prefix + "grant", Title: "Granted", Creator: data.Viewer, InheritedTeamIDs: []string{private}, Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: f.principals[3].user.ID, Role: "viewer"}}, UpdatedAt: at},
				{ID: prefix + "archived", Title: "Archived", Creator: data.Viewer, TeamIDs: []string{f.public}, ArchivedAt: &at, UpdatedAt: at},
				{ID: prefix + "large", Title: "Large", Content: strings.Repeat("legal body ", 7000), Creator: data.Viewer, TeamIDs: []string{f.public}, UpdatedAt: at},
			}
			for index := range data.Documents {
				data.Documents[index].Revisions = []domain.DocumentRevision{{ID: data.Documents[index].ID + "-history", Content: "Complete history", Author: data.Viewer, CreatedAt: at}}
			}
			data.Favorites = append(data.Favorites, domain.Favorite{ID: prefix + "-favorite", UserID: data.Viewer.ID, ResourceType: "document", ResourceID: prefix + "first"})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		keys := []*domain.APIKey{nil,
			{ID: prefix + "unrestricted", Scopes: []string{"read"}},
			{ID: prefix + "empty", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}},
			{ID: prefix + "selected", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
			{ID: prefix + "app", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
		}
		queries := []string{"", "archived=true", "archived=all", "teamId=" + f.public, "q=body-only", "q=" + url.QueryEscape("boundary start"), "q=" + url.QueryEscape("\u03c9"), "q=state-only", "q=data-only", "q=missing"}
		cases := 0
		for _, state := range []string{"warm", "reloaded", "canonical"} {
			if state == "reloaded" {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if state == "canonical" {
				if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"off", "on"} {
				t.Setenv("FLOW_DOCUMENT_RECORDS", mode)
				for _, principal := range f.principals {
					for _, key := range keys {
						for _, query := range queries {
							serve := func(service *server) *httptest.ResponseRecorder {
								r := f.literalRequest(principal.user, key, nil)
								r.Method, r.URL.Path, r.URL.RawQuery = http.MethodGet, "/api/documents", query
								w := httptest.NewRecorder()
								service.listDocuments(w, r)
								return w
							}
							want, got := serve(&server{store: repo}), serve(&server{store: f.probe})
							var before, after any
							beforeErr, afterErr := json.Unmarshal(want.Body.Bytes(), &before), json.Unmarshal(got.Body.Bytes(), &after)
							a, _ := json.Marshal(before)
							b, _ := json.Marshal(after)
							if want.Code != got.Code || beforeErr != nil || afterErr != nil || string(a) != string(b) {
								t.Fatalf("list differential state=%s mode=%s principal=%s key=%+v query=%s status=%d/%d\nliteral=%s\nbounded=%s", state, mode, principal.user.ID, key, query, want.Code, got.Code, a, b)
							}
							if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
								t.Fatal("list retained output work or decoded complete metadata")
							}
							cases++
						}
					}
				}
			}
		}
		t.Logf("document directory exact HTTP cases=%d", cases)
	}
}

func TestScaleP21DocumentDirectoryEncodingLifetime(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.directory_encoder", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = []domain.Document{{ID: idgen.New("p21-directory-encoder-"), Title: "Owned", Content: strings.Repeat("full body ", 10000), Creator: data.Viewer, TeamIDs: []string{f.public}}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		for _, spool := range []string{"0", "10485760"} {
			for _, exit := range []string{"normal", "canceled", "panic"} {
				t.Run(spool+"/"+exit, func(t *testing.T) {
					t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", spool)
					r := f.literalRequest(f.data.Viewer, nil, nil)
					r.Method, r.URL.Path = http.MethodGet, "/api/documents"
					ctx, cancel := context.WithCancel(r.Context())
					defer cancel()
					r = r.WithContext(ctx)
					w := &scaleP21ResourceRequestWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), resume: make(chan struct{})}
					if exit == "canceled" {
						w.err = ctx.Err
					}
					if exit == "panic" {
						w.panicAt = "directory encoder panic"
					}
					done := make(chan any, 1)
					go func() {
						defer func() { done <- recover() }()
						(&server{store: f.probe}).listDocuments(w, r)
					}()
					select {
					case <-w.entered:
					case value := <-done:
						t.Fatalf("list did not reach writer: %v status=%d", value, w.Code)
					case <-time.After(20 * time.Second):
						t.Fatal("list writer did not start")
					}
					work := f.probe.MetadataWorkUsedForTesting()
					if spool == "0" && work == 0 || spool != "0" && work != 0 {
						close(w.resume)
						<-done
						t.Fatalf("producer/transport ownership differs: spool=%s work=%d", spool, work)
					}
					if exit == "canceled" {
						cancel()
						if f.probe.MetadataWorkUsedForTesting() != work {
							close(w.resume)
							<-done
							t.Fatal("cancellation released a still-active body encoder")
						}
					}
					close(w.resume)
					<-done
					if f.probe.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("directory encoder retained work after completion")
					}
				})
			}
		}
	}
}

func TestScaleP21DocumentDirectoryUnknownDevelopmentWorkspace(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		r := f.literalRequest(f.data.Viewer, nil, nil)
		r.Method, r.URL.Path = http.MethodGet, "/api/documents"
		r.Header.Set("X-Workspace-Key", idgen.New("p21-directory-unknown-"))
		want, got := httptest.NewRecorder(), httptest.NewRecorder()
		(&server{store: repo, authDisabled: true}).listDocuments(want, r.Clone(r.Context()))
		(&server{store: f.probe, authDisabled: true}).listDocuments(got, r.Clone(r.Context()))
		if want.Code != http.StatusOK || got.Code != want.Code || want.Body.String() != "[]\n" || got.Body.String() != want.Body.String() {
			t.Fatalf("unknown development workspace differs: literal=%d %q bounded=%d %q", want.Code, want.Body.String(), got.Code, got.Body.String())
		}
		if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("unknown development workspace retained work or decoded metadata")
		}
	}
}
