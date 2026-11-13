package store

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP20CanonicalDirectoryAndCursorBindings(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	for driver, repo := range scaleP03Stores(t) {
		t.Run(driver, func(t *testing.T) {
			workspace, principal := repo.data.Workspace.URLKey, repo.data.Viewer.ID
			if err := repo.MutateWorkspace(ContextWithActor(t.Context(), repo.data.Viewer), workspace, "p20.fixture", "", nil, func(data *domain.Bootstrap) error {
				at := time.Now().UTC()
				doc := domain.Document{ID: "p20-doc", Title: "P20 header", Creator: data.Viewer, CreatedAt: at, UpdatedAt: at, Content: strings.Repeat("body", 1<<18), Revisions: []domain.DocumentRevision{{ID: "p20-history", Content: strings.Repeat("history", 1<<17)}}}
				data.Documents = append(data.Documents, doc)
				raw, _ := json.Marshal(doc)
				data.Trash = append(data.Trash, domain.TrashEntry{ID: "p20-trash", ResourceType: "document", ResourceID: doc.ID, Title: doc.Title, DeletedBy: data.Viewer, DeletedAt: time.Now().UTC(), Payload: raw})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"users", "documents", "trash"} {
				installed, revision, err := repo.ResourceWindowMetadata(t.Context(), workspace, principal, nil, kind)
				if err != nil {
					t.Fatal(kind, err)
				}
				canonical, err := repo.resourceWindowCanonicalMetadata(t.Context(), workspace, kind)
				if err != nil {
					t.Fatal(kind, err)
				}
				stamp, err := readMetadataStamp(t.Context(), repo.db, workspace)
				if err != nil {
					t.Fatal(err)
				}
				canonical, _, err = repo.resourceWindowProject(t.Context(), workspace, principal, nil, canonical, stamp)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "documents" && (!reflect.DeepEqual(installed.Documents, canonical.Documents) || canonical.Documents[len(canonical.Documents)-1].Content != "") {
					a, _ := json.Marshal(installed.Documents)
					b, _ := json.Marshal(canonical.Documents)
					t.Fatalf("canonical summary differs or fetched a body: installed=%s canonical=%s", a, b)
				}
				if kind == "users" && !reflect.DeepEqual(installed.Users, canonical.Users) {
					t.Fatal("canonical directory differs")
				}
				if kind == "trash" && (len(canonical.Trash) != len(installed.Trash) || len(canonical.Trash[len(canonical.Trash)-1].Payload) > 4096) {
					t.Fatal("trash headers include a restore body")
				}
				access, err := repo.ResolveAccess(t.Context(), workspace, principal, nil)
				if err != nil {
					t.Fatal(err)
				}
				query := PageRequest{Kind: kind, Filter: "search"}
				cursor, err := SealResourceWindowCursor(access, query, revision, 7)
				if err != nil {
					t.Fatal(err)
				}
				query.Cursor = cursor
				if index, err := ResourceWindowCursor(access, query, revision); err != nil || index != 7 {
					t.Fatal("cursor did not round trip", err)
				}
				if _, err := ResourceWindowCursor(access, query, revision+"1"); err == nil {
					t.Fatal("cursor crossed a revision")
				}
				query.Filter = "different"
				if _, err := ResourceWindowCursor(access, query, revision); err == nil {
					t.Fatal("cursor crossed a query")
				}
			}
		})
	}
}
