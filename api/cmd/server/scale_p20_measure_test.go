package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP20TransferMeasurement(t *testing.T) {
	if os.Getenv("FLOW_P20_MEASURE") == "" {
		t.Skip("set FLOW_P20_MEASURE=legacy or bounded")
	}
	db := newScaleP04Database(t)
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	ctx := store.ContextWithActor(context.Background(), db.users[0])
	if err := db.repo.MutateWorkspace(ctx, db.key, "p20.fixture", "", nil, func(data *domain.Bootstrap) error {
		at := time.Now().UTC().Truncate(time.Second)
		for i := range 1000 {
			doc := domain.Document{ID: fmt.Sprintf("p20-doc-%d", i), SlugID: fmt.Sprintf("p20-slug-%d", i), Title: fmt.Sprintf("P20 document %d", i), Content: strings.Repeat("x", 8192), Creator: data.Viewer, TeamIDs: []string{data.Teams[0].ID}, CreatedAt: at, UpdatedAt: at}
			if i == 0 {
				for revision := range 100 {
					doc.Revisions = append(doc.Revisions, domain.DocumentRevision{ID: fmt.Sprintf("p20-version-%d", revision), DocumentID: doc.ID, Title: "Earlier", Content: strings.Repeat("v", 8192), Author: data.Viewer, CreatedAt: at})
				}
			}
			data.Documents = append(data.Documents, doc)
			doc.ID, doc.SlugID = fmt.Sprintf("p20-deleted-%d", i), fmt.Sprintf("p20-deleted-slug-%d", i)
			doc.Revisions = nil
			raw, _ := json.Marshal(doc)
			data.Trash = append(data.Trash, domain.TrashEntry{ID: fmt.Sprintf("p20-trash-%d", i), ResourceType: "document", ResourceID: doc.ID, Title: doc.Title, Payload: raw, DeletedBy: data.Viewer, DeletedAt: at, ExpiresAt: at.AddDate(10, 0, 0)})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.repo.ReloadWorkspace(t.Context(), db.key); err != nil {
		t.Fatal(err)
	}
	handler := db.handler(t, true)
	paths := []string{"/api/bootstrap", "/api/documents/p20-doc-0/history?limit=200", "/api/documents/p20-deleted-0"}
	if os.Getenv("FLOW_P20_MEASURE") == "bounded" {
		paths = []string{"/api/resource-windows/members?limit=50", "/api/documents/p20-doc-0/history?projection=summary&limit=200", "/api/documents/p20-doc-0/history/p20-version-50", "/api/resource-windows/trash?resourceId=p20-deleted-0", "/api/resource-windows/trash?limit=100"}
	}
	for _, path := range paths {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%.200s", path, w.Code, w.Body.String())
		}
		t.Logf("mode=%s fixture=1000-documents+1000-trash+100-revisions path=%s responseBytes=%d", os.Getenv("FLOW_P20_MEASURE"), path, w.Body.Len())
	}
}
