package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW24ImportMultipartSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		var upload bytes.Buffer
		form := multipart.NewWriter(&upload)
		file, err := form.CreateFormFile("file", "accepted.csv")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("title\x00field\nvalue\x00text\n")); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/imports/preview", io.NopCloser(bytes.NewReader(upload.Bytes())))
		r.Header.Set("Content-Type", form.FormDataContentType())
		r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
		r = r.WithContext(store.ContextWithActor(context.WithValue(r.Context(), authUserContextKey{}, f.data.Viewer), f.data.Viewer))
		w := httptest.NewRecorder()
		service := &server{store: repo}
		service.previewImport(w, r)
		var created domain.ImportJob
		if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.ID == "" {
			t.Fatalf("ordinary multipart preview status=%d body=%s", w.Code, w.Body.String())
		}
		if len(created.Headers) != 1 || created.Headers[0] != "title\x00field" ||
			len(created.Rows) != 1 || created.Rows[0]["title\x00field"] != "valuetext" {
			t.Fatal("multipart producer did not preserve the header/key and strip only the value", created.Headers, created.Rows)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		hydrated, err := probe.HydrateWorkspaceMetadata(t.Context(), f.data.Workspace.URLKey,
			store.MetadataRecordScope{Read: []store.RecordKey{{Kind: "importJobs", ID: created.ID}}})
		if err != nil || len(hydrated.Data.ImportJobs) != 1 {
			t.Fatal("canonical point control", err)
		}
		canonical := hydrated.Data.ImportJobs[0]
		if len(canonical.Headers) != 1 || canonical.Headers[0] != "titlefield" || canonical.Rows != nil {
			t.Fatalf("canonical control no longer exercises stripped headers and compacted rows: %+v", canonical)
		}
		for range 2 {
			for _, list := range []bool{false, true} {
				request := httptest.NewRequest(http.MethodGet, "/api/imports", nil)
				request.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
				request.SetPathValue("id", created.ID)
				request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, f.data.Viewer))
				response := httptest.NewRecorder()
				var jobs []domain.ImportJob
				if list {
					service.listImports(response, request)
					if json.Unmarshal(response.Body.Bytes(), &jobs) != nil {
						t.Fatal("decode legacy import list", response.Body.String())
					}
				} else {
					service.getImport(response, request)
					jobs = make([]domain.ImportJob, 1)
					if json.Unmarshal(response.Body.Bytes(), &jobs[0]) != nil {
						t.Fatal("decode legacy import point", response.Body.String())
					}
				}
				if response.Code != http.StatusOK || len(jobs) != 1 ||
					len(jobs[0].Headers) != 1 || jobs[0].Headers[0] != "title\x00field" ||
					jobs[0].Rows != nil {
					t.Fatalf("legacy import source hold disappeared list=%v status=%d jobs=%+v; re-review before changing this assertion", list, response.Code, jobs)
				}
				if list && response.Header().Get("X-Total-Count") != "1" {
					t.Fatal("legacy monitor total changed")
				}
			}
		}
		t.Log("ordinary HTTP 201 multipart preview retains NUL header/key and complete Rows; legacy metadata point/list omit Rows but preserve NUL header, while canonical point strips the header")
	}
}

func TestScaleP21SW24ImportHandoffSource(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		service := &server{store: repo}
		for _, list := range []bool{false, true} {
			r := httptest.NewRequest(http.MethodGet, "/api/imports", nil)
			r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
			r.SetPathValue("id", "request-snapshot")
			r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, f.data.Viewer))
			data := domain.Bootstrap{Workspace: f.data.Workspace, ImportJobs: []domain.ImportJob{{
				ID: "request-snapshot", UserID: f.data.Viewer.ID, Filename: "earlier snapshot", Status: "mapping",
				Headers: []string{}, Errors: []string{},
			}}}
			handOffPagedMetadata(r, f.data.Workspace.URLKey, f.data.Viewer.ID, data)
			w := httptest.NewRecorder()
			if list {
				service.listImports(w, r)
			} else {
				service.getImport(w, r)
			}
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"request-snapshot"`) {
				t.Fatalf("request handoff source changed list=%v status=%d body=%s", list, w.Code, w.Body.String())
			}
			if _, found := takePagedMetadata(r, f.data.Workspace.URLKey, f.data.Viewer.ID); found {
				t.Fatal("monitor did not consume its one-shot handoff")
			}
		}
	}
}
