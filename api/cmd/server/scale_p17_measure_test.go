package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP17Measurements(t *testing.T) {
	if os.Getenv("FLOW_P17_MEASURE") != "1" {
		t.Skip("set FLOW_P17_MEASURE=1 to measure request allocations")
	}
	db := newScaleP04Database(t)
	ctx := store.ContextWithActor(context.Background(), db.users[0])
	err := db.repo.MutateWorkspace(ctx, db.key, "p17.fixture", "", nil, func(data *domain.Bootstrap) error {
		for i := range 32 {
			doc := domain.Document{ID: fmt.Sprintf("p17-document-%d", i), SlugID: fmt.Sprintf("p17-slug-%d", i), Title: "P17 measurement", Content: strings.Repeat("document body ", 4096), Creator: data.Viewer, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			data.Documents = append(data.Documents, doc)
			raw, _ := json.Marshal(doc)
			data.Trash = append(data.Trash, domain.TrashEntry{ID: fmt.Sprintf("p17-trash-%d", i), ResourceType: "document", ResourceID: doc.ID + "-deleted", Payload: raw, DeletedBy: data.Viewer})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.repo.ReloadWorkspace(t.Context(), db.key); err != nil {
		t.Fatal(err)
	}
	handler := db.handler(t, true)
	for _, operation := range []struct{ name, method, path string }{
		{"edit", "PATCH", "/api/documents/p17-document-0"},
		{"detail", "GET", "/api/documents/p17-document-0"},
		{"list-empty", "GET", "/api/documents?teamId=absent"},
	} {
		var statements atomic.Uint64
		run := func(i int) {
			r := httptest.NewRequest(operation.method, operation.path, bytes.NewBufferString(fmt.Sprintf(`{"icon":"p17-%d"}`, i)))
			r.Header.Set("Content-Type", "application/json")
			r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(string) { statements.Add(1) }))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("%s: status=%d body=%.200s", operation.name, w.Code, w.Body.String())
			}
		}
		run(-1)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		statements.Store(0)
		start := time.Now()
		for i := range 10 {
			run(i)
		}
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		t.Logf("%s mode=%s journal=%s bytes/op=%d allocs/op=%d sql/op=%.1f us/op=%d", operation.name, os.Getenv("FLOW_DOCUMENT_RECORDS"), os.Getenv("FLOW_RECORD_JOURNAL"), (after.TotalAlloc-before.TotalAlloc)/10, (after.Mallocs-before.Mallocs)/10, float64(statements.Load())/10, elapsed.Microseconds()/10)
	}
}
