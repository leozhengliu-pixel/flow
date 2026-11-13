package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP20GatesLiveAliasPrecedesTrashBeforeCredentialFiltering(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := apiKeyScopeFixture(t, repo)
			workspace := f.data.Workspace.URLKey
			const alias = "p20-gates-shared-old"
			const deletedAlias = "p20-gates-deleted-old"
			const sentinel = "P20 gates deleted body sentinel"
			at := time.Now().UTC()
			if err := repo.MutateWorkspace(t.Context(), workspace, "p20.gates_alias_fixture", "", nil, func(data *domain.Bootstrap) error {
				doc := domain.Document{ID: "p20-gates-live", SlugID: "p20-gates-live-current", PreviousSlugIDs: []string{alias}, Title: "Live document", Content: "Live body", Creator: data.Viewer, TeamIDs: []string{"scope-public"}, Version: 1, CreatedAt: at, UpdatedAt: at}
				doc.Revisions = []domain.DocumentRevision{{ID: "p20-gates-live-revision", DocumentID: doc.ID, Content: doc.Content, Author: data.Viewer, CreatedAt: at}}
				data.Documents = append(data.Documents, doc)
				doc.ID, doc.SlugID, doc.Content = "p20-gates-deleted", "p20-gates-deleted-current", sentinel
				doc.PreviousSlugIDs = []string{alias, deletedAlias}
				doc.Revisions = []domain.DocumentRevision{{ID: "p20-gates-deleted-revision", DocumentID: doc.ID, Content: sentinel, Author: data.Viewer, CreatedAt: at}}
				raw, _ := json.Marshal(doc)
				data.Trash = append(data.Trash, domain.TrashEntry{ID: "p20-gates-trash", ResourceType: "document", ResourceID: doc.ID, TeamIDs: doc.TeamIDs, Payload: raw, DeletedBy: data.Viewer, DeletedAt: at, ExpiresAt: at.AddDate(0, 0, 30)})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"off", "shadow", "on"} {
				t.Setenv("FLOW_DOCUMENT_RECORDS", mode)
				for _, spec := range []struct {
					name, restriction string
					teams             []string
				}{
					{"selected-empty", "selected", nil},
					{"selected-set", "selected", []string{"scope-public"}},
					{"unrestricted", "all", nil},
				} {
					t.Run(mode+"/"+spec.name, func(t *testing.T) {
						token, _ := apiKeyScopeCredential(t, f, "user", spec.restriction, spec.teams, []string{"read"})
						legacy := scaleP03Read(t, f, nil, token, http.MethodGet, "/api/documents/"+alias, workspace)
						var expected domain.Document
						_ = json.Unmarshal(legacy.Body.Bytes(), &expected)
						revisionID := "p20-gates-live-revision"
						if len(expected.Revisions) > 0 {
							revisionID = expected.Revisions[0].ID
						}
						if spec.name == "selected-empty" {
							want := http.StatusOK
							if mode == "on" {
								want = http.StatusNotFound
							}
							if legacy.Code != want {
								t.Fatalf("legacy alias selection changed in mode=%s: status=%d want=%d", mode, legacy.Code, want)
							}
						}
						for index, path := range []string{
							"/api/documents/" + alias + "?history=summary",
							"/api/documents/" + alias + "/history?projection=summary",
							"/api/documents/" + alias + "/history/" + revisionID,
						} {
							current := scaleP03Read(t, f, nil, token, http.MethodGet, path, workspace)
							legacyDeleted := mode != "on" && expected.ID == "p20-gates-deleted"
							if current.Code != legacy.Code || !legacyDeleted && bytes.Contains(current.Body.Bytes(), []byte(sentinel)) {
								t.Fatalf("alias selection differs: mode=%s path=%s legacy=%d current=%d", mode, path, legacy.Code, current.Code)
							}
							if index == 0 && legacy.Code == http.StatusOK {
								var doc domain.Document
								_ = json.Unmarshal(current.Body.Bytes(), &doc)
								if doc.ID != expected.ID || doc.Content != expected.Content || (doc.DeletedAt == nil) != (expected.DeletedAt == nil) {
									t.Fatal("summary selected a different live/deleted identity")
								}
							}
						}
					})
				}
			}
			for _, ref := range []string{"p20-gates-deleted", deletedAlias} {
				legacy := scaleP03Read(t, f, f.admin, "", http.MethodGet, "/api/documents/"+ref, workspace)
				current := scaleP03Read(t, f, f.admin, "", http.MethodGet, "/api/documents/"+ref+"?history=summary", workspace)
				var doc domain.Document
				_ = json.Unmarshal(current.Body.Bytes(), &doc)
				if legacy.Code != http.StatusOK || current.Code != http.StatusOK || doc.ID != "p20-gates-deleted" || doc.Content != sentinel || doc.DeletedAt == nil {
					t.Fatalf("unambiguous deleted fallback failed: ref=%s legacy=%d current=%d", ref, legacy.Code, current.Code)
				}
			}
		})
	}
}
