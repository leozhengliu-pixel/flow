//go:build loadtest

package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"flow/api/internal/store"
)

type scaleP20TransferCounter struct {
	header http.Header
	status int
	bytes  int64
}

func (w *scaleP20TransferCounter) Header() http.Header { return w.header }
func (w *scaleP20TransferCounter) WriteHeader(status int) {
	w.status = status
}
func (w *scaleP20TransferCounter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.bytes += int64(len(data))
	return len(data), nil
}

func TestScaleP20ProgrammeTenfoldTransfer(t *testing.T) {
	if os.Getenv("FLOW_P20_MEASURE_TENFOLD") != "1" {
		t.Skip("set FLOW_P20_MEASURE_TENFOLD=1 for the P01 tenfold transfer fixture")
	}
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_SYNC_MANIFEST", "on")
	t.Setenv("FLOW_INDEXED_READS", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	db := newScaleP04Database(t)
	core, ok := db.repo.WorkspaceMetadata(db.key)
	if !ok || len(core.Teams) == 0 {
		t.Fatal("fixture workspace missing")
	}
	spec := store.SyntheticMetadata{
		Projects: 2321, ProjectBytes: 12000,
		Trash: 210, TrashBytes: 41000,
		Documents: 78, DocumentBytes: 64000,
		Conversations: 400, ConversationBytes: 2000,
		TeamIDs: []string{core.Teams[0].ID}, CountScale: 10, BodyScale: 1, Relations: true,
	}
	if err := db.repo.SeedSyntheticMetadata(t.Context(), db.key, spec); err != nil {
		t.Fatal(err)
	}
	t.Log("fixture=P01 countScale=10 bodyScale=1 projects=23210 trash=2100 documents=780 conversations=4000 relations=true")
	handler := db.handler(t, true)
	for _, path := range []string{
		"/api/bootstrap",
		"/api/sync/manifest?surface=resource-window",
		"/api/sync/records?surface=resource-window&kind=documents&limit=100",
		"/api/resource-windows/members?limit=50",
		"/api/resource-windows/trash?limit=100",
		"/api/resource-windows/trash?resourceId=lt-trash-res-0",
		"/api/documents/lt-doc-0000/history?limit=200",
		"/api/documents/lt-doc-0000/history?projection=summary&limit=200",
		"/api/documents/lt-doc-0000/history/lt-doc-revision-00000",
	} {
		response := &scaleP20TransferCounter{header: http.Header{}}
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.status != http.StatusOK {
			t.Fatalf("%s: status=%d", path, response.status)
		}
		if strings.Contains(path, "limit=100") || strings.Contains(path, "/members?") || strings.Contains(path, "/sync/manifest?") {
			if response.bytes > store.MetadataRecordPageMaxBytes {
				t.Fatalf("%s exceeded page budget: %d", path, response.bytes)
			}
		}
		t.Logf("fixture=P01-10x path=%s responseBytes=%d", path, response.bytes)
	}
}
