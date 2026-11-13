package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW4AttachmentJSON(t *testing.T, value any, attachment domain.Attachment) any {
	t.Helper()
	decoded := scaleP21M2JSON(t, value, false)
	var normalize func(any) any
	normalize = func(value any) any {
		switch value := value.(type) {
		case string:
			if attachment.ID != "" && value == attachment.ID {
				return "<generated-attachment>"
			}
			if attachment.URL != "" && value == attachment.URL {
				return "/uploads/<generated-attachment>"
			}
		case map[string]any:
			for key, item := range value {
				value[key] = normalize(item)
			}
		case []any:
			for i, item := range value {
				value[i] = normalize(item)
			}
		}
		return value
	}
	return normalize(decoded)
}

func TestScaleP21SW4AttachmentCreationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		unrelated := scaleP21M3Fingerprint(t, f.probe, workspace, "projects")
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, mode := range []string{"created", "missing", "cancelled"} {
				var results []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw4.attachment_seed", "", nil, func(data *domain.Bootstrap) error {
						data.CustomerRequests = []domain.CustomerRequest{
							{ID: "sw4-request", Body: "Target", Creator: user, Attachments: []domain.Attachment{}},
							{ID: "sw4-other-request", Body: "Keep", Creator: user},
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					s := &server{store: source, uploadPath: t.TempDir()}
					var event domain.DomainEvent
					var realtime domain.RealtimeEvent
					source.SetWebhookSink(func(_ string, value domain.DomainEvent) { event = value })
					source.SetRealtimeSink(func(_ string, value domain.RealtimeEvent) { realtime = value })
					var body bytes.Buffer
					writer := multipart.NewWriter(&body)
					part, err := writer.CreateFormFile("file", "p21.txt")
					if err != nil {
						t.Fatal(err)
					}
					const payload = "P21 owned attachment"
					if _, err := io.WriteString(part, payload); err != nil {
						t.Fatal(err)
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					r := f.request(user, key, nil)
					r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body.Bytes())), int64(body.Len())
					r.Header.Set("Content-Type", writer.FormDataContentType())
					r.SetPathValue("id", "sw4-request")
					if mode == "missing" {
						r.SetPathValue("id", "sw4-missing")
					}
					if mode == "cancelled" {
						ctx, cancel := context.WithCancel(r.Context())
						cancel()
						r = r.WithContext(ctx)
					}
					w := httptest.NewRecorder()
					started := time.Now().UTC()
					s.createCustomerRequestAttachment(w, r)
					var created domain.Attachment
					if mode == "created" {
						if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil {
							t.Fatalf("attachment creation status=%d", w.Code)
						}
						if created.Creator.ID != user.ID || created.Title != "p21.txt" || created.Size != int64(len(payload)) {
							t.Fatal("created attachment lost actor or upload metadata")
						}
						storage, err := s.storage()
						if err != nil {
							t.Fatal(err)
						}
						reader, _, _, err := storage.Open(t.Context(), strings.TrimPrefix(created.URL, "/uploads/"))
						if err != nil {
							t.Fatal(err)
						}
						content, readErr := io.ReadAll(reader)
						reader.Close()
						if readErr != nil || string(content) != payload {
							t.Fatal("uploaded attachment bytes differ")
						}
					} else if mode == "missing" && w.Code != http.StatusNotFound {
						t.Fatalf("missing request status=%d", w.Code)
					} else if mode == "cancelled" && w.Code < http.StatusBadRequest {
						t.Fatalf("cancelled request succeeded: %d", w.Code)
					}
					files, err := os.ReadDir(s.uploadPath)
					if err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
					wantFiles := 0
					if mode == "created" {
						wantFiles = 1
					}
					if len(files) != wantFiles {
						t.Fatalf("upload cleanup left %d files, want %d", len(files), wantFiles)
					}
					rows := scaleP21M2Canonical(t, repo, workspace,
						store.RecordKey{Kind: "customerRequests", ID: "sw4-request"},
						store.RecordKey{Kind: "customerRequests", ID: "sw4-other-request"})
					entries, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{Since: started})
					if err != nil {
						t.Fatal(err)
					}
					for i := range entries {
						entries[i].ID, entries[i].Hash, entries[i].Seq, entries[i].CreatedAt = "", "", 0, time.Time{}
					}
					results = append(results, scaleP21SW4AttachmentJSON(t, []any{
						w.Code, scaleP21M2Response(t, w, false), rows,
						event.Type, event.AggregateID, event.Payload, event.PreviousValues,
						realtime.Type, realtime.Payload, entries,
					}, created))
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatalf("attachment %s differential: %s", mode, scaleP21M2Diff(results[0], results[1]))
				}
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 || scaleP21M3Fingerprint(t, f.probe, workspace, "projects") != unrelated {
			t.Fatal("attachment operations decoded or changed unrelated metadata")
		}
	}
}
