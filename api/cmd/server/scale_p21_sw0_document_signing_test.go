package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW0DocumentDirectorySigningFallback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	t.Setenv("FLOW_FILE_URL_SIGNING_KEY", "document-directory-signing-fixture")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.document_signing", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = []domain.Document{{
				ID: idgen.New("signed-document-"), Title: "/uploads/title.png",
				Content: "![before](/uploads/before.png) " + strings.Repeat("large legal body ", 10000) + " /uploads/after.png",
				Creator: data.Viewer, TeamIDs: []string{f.public},
			}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		for _, spool := range []string{"0", "1024", "4194304"} {
			t.Run(spool, func(t *testing.T) {
				t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", spool)
				request := func() *http.Request {
					r := f.literalRequest(f.data.Viewer, nil, nil)
					r.Method, r.URL.Path = http.MethodGet, "/api/documents"
					r.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
					return r
				}
				literal := httptest.NewRecorder()
				(&server{store: repo}).listDocuments(literal, request())
				got := httptest.NewRecorder()
				var signing *hydratedURLSigning
				service := &server{store: f.probe}
				service.signedFileURLs(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					signing, _ = r.Context().Value(hydratedURLSigningKey{}).(*hydratedURLSigning)
					service.listDocuments(w, r)
				})).ServeHTTP(got, request())
				if signing == nil || signing.key == nil {
					t.Fatal("signing context unavailable")
				}
				want := rewriteUploadURLs(literal.Body.Bytes(), signing.key, signing.base, signing.expires)
				if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), want) {
					t.Fatalf("document list signed bytes changed: status=%d", got.Code)
				}
				if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("signed directory retained work or decoded full metadata")
				}
			})
		}
	}
}
