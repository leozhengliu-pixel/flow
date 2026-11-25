package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21HotDocumentReadHTTPMatrix(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
	t.Setenv("FLOW_INDEXED_READS", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("p21-document-http-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.document_http_seed", "", nil, func(data *domain.Bootstrap) error {
			private, child := "", ""
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
				if strings.HasPrefix(team.ID, "m2-child-") {
					child = team.ID
				}
			}
			data.Documents = append(data.Documents,
				domain.Document{ID: prefix + "open", SlugID: prefix + "current-000000000021", PreviousSlugIDs: []string{prefix + "old", prefix + "alias"}, Title: "Open", Content: "Current body", Creator: data.Viewer, TeamIDs: []string{f.public}, ProjectIDs: []string{prefix + "project", prefix + "secret-project"}},
				domain.Document{ID: prefix + "secret", SlugID: prefix + "alias", Title: "Secret", Creator: data.Viewer, TeamIDs: []string{private}},
				domain.Document{ID: prefix + "inherited", Title: "Inherited", Creator: data.Viewer, InheritedTeamIDs: []string{private}, ProjectIDs: []string{prefix + "secret-project"}},
				domain.Document{ID: prefix + "child", Title: "Child", Creator: data.Viewer, TeamIDs: []string{child}},
				domain.Document{ID: prefix + "grant", Title: "Guest grant", Creator: data.Viewer, InheritedTeamIDs: []string{private}, Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: f.principals[3].user.ID, Role: "viewer"}}},
				domain.Document{ID: prefix + "unrelated", Title: "Unrelated", Content: strings.Repeat("unrelated", 100000), Creator: data.Viewer},
			)
			data.Projects = append(data.Projects,
				domain.Project{ID: prefix + "project", TeamIDs: []string{f.public}, Resources: []domain.ProjectResource{{ID: prefix + "open"}}},
				domain.Project{ID: prefix + "secret-project", TeamIDs: []string{private}},
			)
			payload, err := json.Marshal(domain.Document{ID: prefix + "deleted", SlugID: prefix + "deleted-current", PreviousSlugIDs: []string{prefix + "deleted-old"}, Title: "Deleted", Content: "Deleted body", Creator: data.Viewer, TeamIDs: []string{f.public}})
			if err != nil {
				return err
			}
			data.Trash = append(data.Trash, domain.TrashEntry{ID: prefix + "trash", ResourceType: "document", ResourceID: prefix + "deleted", Payload: payload, DeletedBy: data.Viewer, DeletedAt: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)})
			data.Favorites = append(data.Favorites, domain.Favorite{ID: prefix + "favorite", ResourceType: "document", ResourceID: prefix + "open", UserID: data.Viewer.ID})
			for index := range data.Documents {
				if strings.HasPrefix(data.Documents[index].ID, prefix) {
					data.Documents[index].CreatedAt = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
					data.Documents[index].UpdatedAt = data.Documents[index].CreatedAt
					data.Documents[index].Revisions = []domain.DocumentRevision{{ID: data.Documents[index].ID + "-revision", DocumentID: data.Documents[index].ID, Title: "Earlier", Content: "Earlier body", Author: data.Viewer, CreatedAt: data.Documents[index].CreatedAt}}
				}
			}
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
			for _, principal := range f.principals {
				for _, key := range keys {
					for _, ref := range []string{prefix + "open", prefix + "current-000000000021", prefix + "old", "stale-000000000021", prefix + "alias", prefix + "inherited", prefix + "child", prefix + "grant", prefix + "deleted", prefix + "deleted-current", prefix + "deleted-old", prefix + "trash", "missing"} {
						for _, surface := range []string{"detail", "summary", "history", "revision", "trash", "batch"} {
							serve := func(service *server, bounded bool) *httptest.ResponseRecorder {
								var body any
								if surface == "batch" {
									body = map[string]any{"keys": []map[string]string{{"kind": "documents", "id": ref}}, "projection": "detail"}
								}
								r := f.literalRequest(principal.user, key, body)
								r.Method, r.URL.Path = http.MethodGet, "/api/documents/"+ref
								r.SetPathValue("id", ref)
								r.SetPathValue("revisionId", prefix+"open-revision")
								switch surface {
								case "summary":
									r.URL.RawQuery = "history=summary"
								case "history":
									r.URL.Path += "/history"
									r.URL.RawQuery = "projection=summary"
								case "revision":
									r.URL.Path += "/history/" + prefix + "open-revision"
								case "trash":
									r.URL.Path, r.URL.RawQuery = "/api/resource-windows/trash", "resourceId="+ref
								case "batch":
									r.Method, r.URL.Path, r.URL.RawQuery = http.MethodPost, "/api/sync/batch", "surface=resource-window"
								}
								if bounded {
									var close func()
									r, close = beginRequestResourceReads(r)
									defer close()
								}
								w := httptest.NewRecorder()
								switch surface {
								case "detail", "summary":
									service.getDocument(w, r)
								case "history":
									service.documentContentHistory(w, r)
								case "revision":
									service.scaleP20Revision(w, r)
								case "trash":
									service.scaleP20Trash(w, r)
								case "batch":
									service.syncBatch(w, r)
								}
								return w
							}
							want := serve(&server{store: repo}, false)
							got := serve(&server{store: f.probe}, true)
							if got.Code != want.Code || got.Body.String() != want.Body.String() {
								t.Fatalf("state=%s surface=%s principal=%s key=%+v ref=%s\nwant=%d %s\n got=%d %s", state, surface, principal.name, key, ref, want.Code, want.Body, got.Code, got.Body)
							}
							if f.probe.MetadataWorkUsedForTesting() != 0 {
								t.Fatal("HTTP document read retained work")
							}
							cases++
						}
					}
				}
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("HTTP document read decoded complete workspace")
		}
		t.Logf("HTTP document matrix cases=%d", cases)
	}
}

func TestScaleP21HotDocumentReadEncodingLifetime(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
	t.Setenv("FLOW_INDEXED_READS", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		target := idgen.New("p21-document-lifetime-")
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.document_lifetime", "", nil, func(data *domain.Bootstrap) error {
			document := domain.Document{ID: target, Title: "Lifetime", Content: strings.Repeat("body ", 100000), Creator: data.Viewer, TeamIDs: []string{f.public}, Revisions: []domain.DocumentRevision{{ID: target + "-revision", Content: "Earlier body", Author: data.Viewer}}}
			data.Documents = append(data.Documents, document)
			document.ID += "-deleted"
			raw, err := json.Marshal(document)
			if err != nil {
				return err
			}
			data.Trash = append(data.Trash, domain.TrashEntry{ID: target + "-trash", ResourceType: "document", ResourceID: document.ID, Payload: raw, DeletedBy: data.Viewer, DeletedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		for _, surface := range []string{"detail", "summary", "history", "revision", "trash"} {
			for _, exit := range []string{"normal", "canceled", "panic"} {
				t.Run(surface+"/"+exit, func(t *testing.T) {
					r := f.literalRequest(f.data.Viewer, nil, nil)
					r.Method, r.URL.Path = http.MethodGet, "/api/documents/"+target
					r.SetPathValue("id", target)
					r.SetPathValue("revisionId", target+"-revision")
					switch surface {
					case "summary":
						r.URL.RawQuery = "history=summary"
					case "history":
						r.URL.Path, r.URL.RawQuery = r.URL.Path+"/history", "projection=summary"
					case "revision":
						r.URL.Path += "/history/" + target + "-revision"
					case "trash":
						r.URL.Path, r.URL.RawQuery = "/api/resource-windows/trash", "resourceId="+target+"-deleted"
					}
					ctx, cancel := context.WithCancel(r.Context())
					defer cancel()
					r, release := beginRequestResourceReads(r.WithContext(ctx))
					w := &scaleP21ResourceRequestWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), resume: make(chan struct{})}
					if exit == "canceled" {
						w.err = ctx.Err
					}
					if exit == "panic" {
						w.panicAt = "document encoder panic"
					}
					done := make(chan any, 1)
					go func() {
						defer func() { done <- recover() }()
						defer release()
						service := &server{store: f.probe}
						switch surface {
						case "detail", "summary":
							service.getDocument(w, r)
						case "history":
							service.documentContentHistory(w, r)
						case "revision":
							service.scaleP20Revision(w, r)
						case "trash":
							service.scaleP20Trash(w, r)
						}
					}()
					select {
					case <-w.entered:
					case value := <-done:
						t.Fatalf("document did not reach encoder: %v status=%d", value, w.Code)
					case <-time.After(10 * time.Second):
						t.Fatal("document encoder did not start")
					}
					if f.probe.MetadataWorkUsedForTesting() == 0 {
						close(w.resume)
						<-done
						t.Fatal("document uncharged before encoding")
					}
					cancel()
					if f.probe.MetadataWorkUsedForTesting() == 0 {
						close(w.resume)
						<-done
						t.Fatal("cancellation uncharged active encoder")
					}
					close(w.resume)
					<-done
					if f.probe.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("completed document encoder retained admission")
					}
				})
			}
		}
	}
}

func TestScaleP21HotDocumentReadBatch50(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_INDEXED_READS", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		target := idgen.New("p21-document-batch-")
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.document_batch", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = append(data.Documents, domain.Document{ID: target, Title: "Large batch detail", Content: strings.Repeat("body ", 140000), Creator: data.Viewer, TeamIDs: []string{f.public}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		keys := make([]map[string]string, 50)
		for index := range keys {
			keys[index] = map[string]string{"kind": "documents", "id": target}
		}
		serve := func(service *server, bounded bool) *httptest.ResponseRecorder {
			r := f.literalRequest(f.data.Viewer, nil, map[string]any{"keys": keys, "projection": "detail"})
			r.Method, r.URL.Path, r.URL.RawQuery = http.MethodPost, "/api/sync/batch", "surface=resource-window"
			if bounded {
				var close func()
				r, close = beginRequestResourceReads(r)
				defer close()
			}
			w := httptest.NewRecorder()
			service.syncBatch(w, r)
			return w
		}
		for _, state := range []string{"reloaded", "canonical"} {
			if state == "canonical" {
				if err := repo.ForgetInstalledMetadataRevisionForTesting(f.data.Workspace.URLKey); err != nil {
					t.Fatal(err)
				}
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
					t.Fatal(err)
				}
			}
			want, got := serve(&server{store: repo}, false), serve(&server{store: f.probe}, true)
			if got.Code != want.Code || got.Body.String() != want.Body.String() {
				t.Fatalf("state=%s batch did not preserve bounded omission/status: literal=%d bounded=%d\nliteral=%.500s\nbounded=%.500s", state, want.Code, got.Code, want.Body, got.Body)
			}
			if state == "reloaded" {
				if got.Code != http.StatusOK {
					t.Fatalf("legal large batch status=%d body=%s", got.Code, got.Body)
				}
				var output struct {
					Items []struct{ Omitted bool }
				}
				if err := json.Unmarshal(got.Body.Bytes(), &output); err != nil || len(output.Items) != 50 {
					t.Fatal("batch shape changed", err)
				}
				for _, item := range output.Items {
					if !item.Omitted {
						t.Fatal("large batch detail was silently truncated")
					}
				}
			}
			if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("batch retained detail ownership or decoded workspace")
			}
		}
	}
}
