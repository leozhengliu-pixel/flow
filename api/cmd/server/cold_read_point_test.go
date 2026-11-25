package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW4PointDeletesDifferential(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleP21M3PointDifferential(t, []scaleP21M3PointOperation{
		{
			name: "sw4-project-template-delete", kind: "projectTemplates", id: "sw4-template",
			seed: func(data *domain.Bootstrap, actor domain.User) {
				data.ProjectTemplates = []domain.ProjectTemplate{
					{ID: "sw4-template", Name: "Target", Creator: actor, CreatedAt: now, UpdatedAt: now},
					{ID: "sw4-other-template", Name: "Keep", Creator: actor},
				}
			},
			handler:   (*server).deleteProjectTemplate,
			otherKeys: []store.RecordKey{{Kind: "projectTemplates", ID: "sw4-other-template"}},
		},
		{
			name: "sw4-draft-delete", kind: "drafts", id: "sw4-draft",
			seed: func(data *domain.Bootstrap, actor domain.User) {
				data.Drafts = []domain.Draft{
					{ID: "sw4-draft", UserID: actor.ID, Type: "issue", Body: "Target", CreatedAt: now, UpdatedAt: now},
					{ID: "sw4-other-draft", UserID: actor.ID, Type: "issue", Body: "Keep"},
				}
			},
			handler:   (*server).deleteDraft,
			otherKeys: []store.RecordKey{{Kind: "drafts", ID: "sw4-other-draft"}},
		},
		{
			name: "sw4-request-attachment-delete", kind: "customerRequests", id: "sw4-request",
			seed: func(data *domain.Bootstrap, actor domain.User) {
				data.CustomerRequests = []domain.CustomerRequest{
					{ID: "sw4-request", Body: "Target", Creator: actor, Attachments: []domain.Attachment{
						{ID: "sw4-attachment", Title: "target.txt", URL: "/uploads/sw4-owned-target.txt", Creator: actor, CreatedAt: now},
						{ID: "sw4-keep-attachment", Title: "keep.txt", URL: "/uploads/sw4-keep.txt", Creator: actor, CreatedAt: now},
					}},
					{ID: "sw4-other-request", Body: "Keep", Creator: actor},
				}
			},
			handler: func(s *server, w http.ResponseWriter, r *http.Request) {
				s.uploadPath = t.TempDir()
				storage, err := s.storage()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := storage.Put(r.Context(), "sw4-owned-target.txt", strings.NewReader("owned attachment"), "text/plain"); err != nil {
					t.Fatal(err)
				}
				r.SetPathValue("attachmentId", "sw4-attachment")
				s.deleteCustomerRequestAttachment(w, r)
				reader, _, _, openErr := storage.Open(r.Context(), "sw4-owned-target.txt")
				if reader != nil {
					reader.Close()
				}
				if r.PathValue("id") == "sw4-request" && openErr == nil {
					t.Fatal("successful attachment deletion retained the object")
				}
				if r.PathValue("id") != "sw4-request" && openErr != nil {
					t.Fatal("failed attachment deletion removed the object")
				}
			},
			otherKeys: []store.RecordKey{{Kind: "customerRequests", ID: "sw4-other-request"}},
		},
	})
}

func TestScaleP21SW4ForeignDraftAndPointBudgets(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		foreignID := idgen.New("sw4-foreign-owner-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw4.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Drafts = []domain.Draft{{ID: "sw4-foreign", UserID: foreignID, Body: "Private draft"}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			before := scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "drafts", ID: "sw4-foreign"})
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r, w := f.request(user, key, nil), httptest.NewRecorder()
				r.SetPathValue("id", "sw4-foreign")
				scaleP21M3Call(&server{store: source}, w, r, (*server).deleteDraft)
				if w.Code != http.StatusNotFound {
					t.Fatalf("foreign draft status=%d", w.Code)
				}
			}
			if after := scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "drafts", ID: "sw4-foreign"}); !reflect.DeepEqual(before, after) {
				t.Fatal("foreign draft deletion changed canonical data")
			}
		})
		large := strings.Repeat("large target ", 30000)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw4.large", "", nil, func(data *domain.Bootstrap) error {
			data.Drafts = []domain.Draft{{ID: "sw4-large-draft", UserID: f.data.Viewer.ID, Body: large}}
			data.ProjectTemplates = []domain.ProjectTemplate{{ID: "sw4-large-template", Description: large}}
			data.CustomerRequests = []domain.CustomerRequest{{ID: "sw4-large-request", Body: large, Attachments: []domain.Attachment{{ID: "sw4-large-attachment"}}}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, operation := range []struct {
			kind, id string
			handler  func(*server, http.ResponseWriter, *http.Request)
		}{
			{"drafts", "sw4-large-draft", (*server).deleteDraft},
			{"projectTemplates", "sw4-large-template", (*server).deleteProjectTemplate},
			{"customerRequests", "sw4-large-request", (*server).deleteCustomerRequestAttachment},
		} {
			before := scaleP21M3Fingerprint(t, f.probe, workspace, operation.kind)
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", operation.id)
			r.SetPathValue("attachmentId", "sw4-large-attachment")
			scaleP21M3Call(&server{store: f.probe}, w, r, operation.handler)
			if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) {
				t.Fatalf("%s oversized target status=%d body=%s", operation.kind, w.Code, w.Body.String())
			}
			if scaleP21M3Fingerprint(t, f.probe, workspace, operation.kind) != before {
				t.Fatalf("%s budget failure changed canonical data", operation.kind)
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("point operations decoded full metadata")
		}
	}
}
