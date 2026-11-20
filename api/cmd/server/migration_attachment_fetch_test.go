package main

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type migrationFetchFakeDNS map[string]string

func (d migrationFetchFakeDNS) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if value, ok := d[host]; ok {
		return []net.IPAddr{{IP: net.ParseIP(value)}}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host}
}

// runAttachmentMigration executes a migration whose single issue carries
// attachmentURL and returns the finished job and how many files the
// destination received.
func runAttachmentMigration(t *testing.T, attachmentURL string) (domain.MigrationJob, int32) {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	team := domain.Team{ID: "source_team", Name: "Source team", Key: "SRC"}
	attachment := domain.Attachment{ID: "source_attachment", Title: "report.txt", URL: attachmentURL, ContentType: "text/plain"}
	bundle := domain.MigrationBundle{Version: 1, Source: "flow", ExportedAt: time.Now().UTC(), Workspace: domain.Workspace{ID: "source_workspace", Name: "Source", URLKey: "source"}, Users: []domain.User{bootstrap.Viewer}, Teams: []domain.Team{team}, Issues: []domain.Issue{{ID: "source_issue", Identifier: "SRC-1", Title: "Migrated issue", Priority: 2, PriorityLabel: "High", CreatedAt: time.Now().UTC().Add(-time.Hour), UpdatedAt: time.Now().UTC(), Team: team, State: domain.WorkflowState{ID: "source_state", Name: "Todo", Type: "unstarted"}, Creator: bootstrap.Viewer, SubscriberIDs: []string{bootstrap.Viewer.ID}, Labels: []domain.IssueLabel{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{attachment}}}}
	raw, _ := json.Marshal(bundle)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "bundle.json")
	_, _ = part.Write(raw)
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/migrations/preview", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var job domain.MigrationJob
	_ = json.Unmarshal(recorder.Body.Bytes(), &job)
	var uploads int32
	var destination *httptest.Server
	destination = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			atomic.AddInt32(&uploads, 1)
			w.WriteHeader(http.StatusOK)
			return
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		query, _ := payload["query"].(string)
		var data map[string]any
		switch {
		case strings.Contains(query, "MigrationTarget"):
			data = map[string]any{"viewer": map[string]any{"id": "dest_viewer"}, "team": map[string]any{"id": "dest_team", "name": "Dest", "key": "DST", "members": map[string]any{"nodes": []map[string]any{{"id": "dest_user", "name": bootstrap.Viewer.DisplayName, "email": bootstrap.Viewer.Email}}}, "states": map[string]any{"nodes": []map[string]any{{"id": "dest_state", "name": "Todo", "type": "unstarted"}}}, "labels": map[string]any{"nodes": []any{}}}}
		case strings.Contains(query, "CreateIssue"):
			data = map[string]any{"issueCreate": map[string]any{"success": true, "issue": map[string]any{"id": "dest_issue", "identifier": "DST-1"}}}
		case strings.Contains(query, "commentCreate"):
			data = map[string]any{"commentCreate": map[string]any{"success": true, "comment": map[string]any{"id": "dest_comment"}}}
		case strings.Contains(query, "fileUpload"):
			data = map[string]any{"fileUpload": map[string]any{"success": true, "uploadFile": map[string]any{"uploadUrl": destination.URL + "/put", "assetUrl": "https://assets.example.test/copied", "headers": []any{}}}}
		case strings.Contains(query, "attachmentCreate"):
			data = map[string]any{"attachmentCreate": map[string]any{"success": true, "attachment": map[string]any{"id": "dest_attachment"}}}
		default:
			data = map[string]any{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data})
	}))
	defer destination.Close()
	t.Setenv("FLOW_LINEAR_API_URL", destination.URL)
	job = requestJSON[domain.MigrationJob](t, handler, http.MethodPost, "/api/migrations/"+job.ID+"/execute", map[string]any{"target": "linear", "targetTeamId": "dest_team", "apiToken": "temporary"}, http.StatusOK)
	return job, atomic.LoadInt32(&uploads)
}

func migrationAttachmentErrors(job domain.MigrationJob) string {
	var out []string
	for _, message := range job.Errors {
		if strings.HasPrefix(message, "attachment copy ") {
			out = append(out, message)
		}
	}
	return strings.Join(out, "\n")
}

// routedPolicy resolves files.example.test to a public address and
// internal.example.test to a private one; each address dials its own server.
func routedMigrationPolicy(t *testing.T, public, internal *httptest.Server) {
	t.Helper()
	policy := outboundPolicy.WithResolver(migrationFetchFakeDNS{"files.example.test": "93.184.216.34", "internal.example.test": "10.0.0.5"}).
		WithDialer(func(ctx context.Context, network, address string) (net.Conn, error) {
			target := public
			if strings.HasPrefix(address, "10.0.0.5:") {
				target = internal
			}
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, target.Listener.Addr().String())
		})
	previous := outboundPolicy
	outboundPolicy = policy
	t.Cleanup(func() { outboundPolicy = previous })
}

func TestMigrationAttachmentLoopbackSourceIsRefused(t *testing.T) {
	var hits int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte("secret"))
	}))
	defer internal.Close()
	job, uploads := runAttachmentMigration(t, internal.URL+"/metadata")
	if hits != 0 || uploads != 0 {
		t.Fatalf("a loopback attachment source was fetched: hits=%d uploads=%d", hits, uploads)
	}
	if !strings.Contains(migrationAttachmentErrors(job), "could not be fetched") || job.Status != "completed" {
		t.Fatalf("expected a recorded per-attachment error and a completed job: %s %v", job.Status, job.Errors)
	}
}

func TestMigrationAttachmentRedirectToPrivateAddressIsRefused(t *testing.T) {
	var privateHits int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&privateHits, 1)
		_, _ = w.Write([]byte("secret"))
	}))
	defer internal.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://internal.example.test/secret", http.StatusFound)
	}))
	defer public.Close()
	routedMigrationPolicy(t, public, internal)
	job, uploads := runAttachmentMigration(t, "http://files.example.test/report.txt")
	if privateHits != 0 || uploads != 0 {
		t.Fatalf("the redirect reached a private address: hits=%d uploads=%d", privateHits, uploads)
	}
	if !strings.Contains(migrationAttachmentErrors(job), "could not be fetched") {
		t.Fatalf("missing per-attachment error: %v", job.Errors)
	}
}

func TestMigrationAttachmentOversizedSourceIsRefused(t *testing.T) {
	for name, chunked := range map[string]bool{"content-length": false, "chunked": true} {
		t.Run(name, func(t *testing.T) {
			public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				chunk := bytes.Repeat([]byte("x"), 1<<20)
				for i := 0; i < 21; i++ {
					_, _ = w.Write(chunk)
					if chunked {
						w.(http.Flusher).Flush()
					}
				}
			}))
			defer public.Close()
			routedMigrationPolicy(t, public, public)
			job, uploads := runAttachmentMigration(t, "http://files.example.test/big.bin")
			if uploads != 0 || !strings.Contains(migrationAttachmentErrors(job), "exceeds 20 MB") {
				t.Fatalf("an oversized source was copied: uploads=%d errors=%v", uploads, job.Errors)
			}
		})
	}
}

func TestMigrationAttachmentPublicSourceIsStillCopied(t *testing.T) {
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer public.Close()
	routedMigrationPolicy(t, public, public)
	job, uploads := runAttachmentMigration(t, "http://files.example.test/report.txt")
	if uploads != 1 || migrationAttachmentErrors(job) != "" {
		t.Fatalf("a public attachment was not copied: uploads=%d errors=%v", uploads, job.Errors)
	}
}
