package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW40DMPointOutputDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw40-dm-point-")
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{
			"title": "DM exact point title", "description": "Complete point description", "teamId": f.public,
		}, workspace, http.StatusCreated)
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		record := domain.ChatIntegrationRecord{
			WorkspaceKey: workspace, Provider: "lark", ConnectionID: prefix + "connection",
			AppID: prefix + "app", TenantKey: "sw40-tenant", Connected: true,
		}
		if err := repo.PutChatIntegration(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		credentials, _ := json.Marshal(chatCredentials{AppSecret: "sw40-test-secret"})
		if err := repo.PutConnectorSecret(t.Context(), chatCredentialID(record), credentials, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID); err != nil {
				t.Error(err)
			}
			if err := repo.DeleteConnectorSecret(context.Background(), chatCredentialID(record)); err != nil {
				t.Error(err)
			}
		})
		type outcome struct {
			Error string
			Sent  []map[string]any
		}
		var sent []map[string]any
		var selected *store.SQLiteStore
		requireHeld, failPlatform := false, false
		mockChatTransport(t, func(request *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(request.URL.Path, "tenant_access_token"):
				return chatTestResponse(http.StatusOK, `{"code":0,"tenant_access_token":"sw40-token","expire":7200}`), nil
			case strings.Contains(request.URL.Path, "/im/v1/messages"):
				if requireHeld && selected.BoundedMetadataEnabled() && selected.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("point result/refusal was released before the actual platform send")
				}
				var payload map[string]any
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				sent = append(sent, map[string]any{"method": request.Method, "path": request.URL.Path, "query": request.URL.RawQuery, "body": payload})
				if failPlatform {
					return nil, errors.New("sw40 platform unavailable")
				}
				return chatTestResponse(http.StatusOK, `{"code":0,"data":{"message_id":"sw40-message","chat_id":"sw40-chat"}}`), nil
			}
			t.Fatalf("unexpected DM request %s", request.URL.Path)
			return nil, nil
		})
		cases := []struct {
			name, operation, ref, text string
			zh, held, platformFailure  bool
			key                        *domain.APIKey
			unlinked                   bool
		}{
			{name: "issue-preview", operation: "preview", ref: issue.ID, held: true},
			{name: "identifier-preview", operation: "preview", ref: issue.Identifier, held: true},
			{name: "missing-preview", operation: "preview", ref: "missing", held: true},
			{name: "selected-empty-preview", operation: "preview", ref: issue.ID, held: true, key: &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}},
			{name: "reconnect-preview", operation: "preview", ref: issue.ID, unlinked: true},
			{name: "chinese-preview", operation: "preview", ref: issue.ID, zh: true, held: true},
			{name: "platform-refused-preview", operation: "preview", ref: issue.ID, held: true, platformFailure: true},
			{name: "project-caller-preview", operation: "project", ref: prefix + "project"},
			{name: "project-missing-preview", operation: "project", ref: "missing"},
			{name: "foreign-workspace-preview", operation: "foreign", ref: issue.ID},
			{name: "empty-comment", operation: "comment", ref: issue.ID, held: true},
			{name: "missing-comment", operation: "comment", ref: "missing", text: "Reply", held: true},
			{name: "chinese-comment", operation: "comment", ref: issue.ID, zh: true, held: true},
		}
		for _, fixture := range cases {
			var outcomes []outcome
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				selected, requireHeld, failPlatform = source, fixture.held, fixture.platformFailure
				sent = nil
				identity := domain.ChatIdentity{UserID: f.data.Viewer.ID, OpenID: "sw40-open-id", TenantKey: record.TenantKey, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
				if fixture.unlinked {
					identity.AuthProvider, identity.AuthenticatedAt = "", time.Time{}
				}
				dm := chatDMRequest{
					record: record, identity: identity, target: chatDMTarget{Workspace: workspace, Identity: identity},
					command: chatInboundCommand{OpenID: identity.OpenID, MessageID: prefix + fixture.name},
					job:     domain.ChatJob{DedupKey: prefix + fixture.name}, zh: fixture.zh,
					metadata: domain.Bootstrap{Projects: []domain.Project{{ID: prefix + "project", Name: "Caller project", SlugID: prefix + "project-slug", Description: "Caller project description"}}},
				}
				ctx := f.literalRequest(f.data.Viewer, fixture.key, nil).Context()
				service := &server{store: source, allowedOrigin: "https://flow.example.test"}
				var err error
				switch fixture.operation {
				case "comment":
					err = service.chatDMComment(ctx, dm, chatDMThread{IssueID: fixture.ref}, fixture.text)
				case "project":
					err = service.chatDMLinkPreview(ctx, dm, chatDMLink{Workspace: workspace, Kind: "project", Ref: fixture.ref})
				case "foreign":
					err = service.chatDMLinkPreview(ctx, dm, chatDMLink{Workspace: prefix + "unlinked", Kind: "issue", Ref: fixture.ref})
				default:
					err = service.chatDMLinkPreview(ctx, dm, chatDMLink{Workspace: workspace, Kind: "issue", Ref: fixture.ref})
				}
				output := outcome{Sent: append([]map[string]any(nil), sent...)}
				if err != nil {
					output.Error = err.Error()
				}
				outcomes = append(outcomes, output)
				if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("DM point operation retained output admission", fixture.name)
				}
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("DM operation=%s changed:\n%s", fixture.name, scaleP21M2Diff(outcomes[0], outcomes[1]))
			}
		}
		identity := domain.ChatIdentity{UserID: f.data.Viewer.ID, OpenID: "sw40-open-id", TenantKey: record.TenantKey, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
		dm := chatDMRequest{
			record: record, identity: identity, target: chatDMTarget{Workspace: workspace, Identity: identity},
			command: chatInboundCommand{OpenID: identity.OpenID, MessageID: prefix + "post-boundary"},
			job:     domain.ChatJob{DedupKey: prefix + "post-boundary"},
		}
		// The full comment consumer is pending: authorization now succeeds,
		// but its writer boundary remains separate from the GET proof.
		pendingWriterError := `bounded metadata coverage is incomplete: mutation "comment.created" has no migrated point writer`
		var postErrors []string
		for _, variant := range []struct {
			source *store.SQLiteStore
			point  bool
		}{{repo, false}, {f.probe, false}, {f.probe, true}} {
			service := &server{store: variant.source}
			ctx, closeOutputs := beginMetadataOutputReads(dm.authContext(t.Context()))
			var preflight domain.Issue
			path := "/api/issue-records/" + url.PathEscape(issue.ID)
			var err error
			if variant.point {
				err = service.chatAuthorizedPointRead(ctx, workspace, identity.UserID, path, &preflight)
			} else {
				err = service.chatAuthorizedMutation(ctx, workspace, identity.UserID, http.MethodGet, path, nil, &preflight)
			}
			if err != nil || preflight.ID != issue.ID {
				t.Fatal("pending POST boundary lost its successful GET preflight", err)
			}
			var comment domain.Comment
			postCtx := store.WithCommentCreationKey(ctx, "comment_chat_"+chatKey(record.ConnectionID, dm.command.MessageID))
			err = service.chatAuthorizedMutation(postCtx, workspace, identity.UserID, http.MethodPost, path+"/comments", domain.CommentCreateInput{Body: "SW40 unchanged POST reply"}, &comment)
			postError := ""
			if err != nil {
				postError = err.Error()
			}
			postErrors = append(postErrors, postError)
			if variant.source.BoundedMetadataEnabled() {
				var failure *chatMutationError
				if !errors.As(err, &failure) || failure.Status != http.StatusInternalServerError || failure.Message != pendingWriterError {
					t.Fatal("pending POST did not reach its exact unmigrated writer refusal", err)
				}
				t.Logf("pending POST authorization passed; writer status=%d blocker=%q point_GET=%v", failure.Status, failure.Message, variant.point)
			} else if err != nil {
				t.Fatal("literal full-hydration POST failed", err)
			}
			closeOutputs()
		}
		if postErrors[0] != "" || postErrors[1] == "" || postErrors[1] != postErrors[2] {
			t.Fatal("GET migration changed the existing bounded POST boundary", postErrors)
		}
		var postOutputs []outcome
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			selected, requireHeld, failPlatform, sent = source, true, false, nil
			err := (&server{store: source, allowedOrigin: "https://flow.example.test"}).chatDMComment(t.Context(), dm, chatDMThread{IssueID: issue.ID}, "SW40 unchanged POST reply")
			result := outcome{Sent: append([]map[string]any(nil), sent...)}
			if err != nil {
				result.Error = err.Error()
			}
			postOutputs = append(postOutputs, result)
			if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("pending POST notice retained point output")
			}
		}
		oldPost, _ := json.Marshal(postOutputs[0])
		newPost, _ := json.Marshal(postOutputs[1])
		if !bytes.Contains(oldPost, []byte("Comment added")) || postOutputs[0].Error != "" ||
			len(postOutputs[1].Sent) != 0 || postOutputs[1].Error != pendingWriterError {
			t.Fatalf("pending POST card boundary changed:\n%s\n%s", oldPost, newPost)
		}
		comments, _, err := repo.ResourceCommentsPage(t.Context(), workspace, issue.ID, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		created := 0
		for _, comment := range comments {
			if comment.Body == "SW40 unchanged POST reply" {
				created++
			}
		}
		if created != 1 {
			t.Fatal("unchanged POST retry/idempotency behavior changed", created)
		}
		t.Logf("exact DM GET/card/error comparisons=%d; literal POST success card retained; bounded writer500 propagates without card; literal retry created once", len(cases))
	}
}
