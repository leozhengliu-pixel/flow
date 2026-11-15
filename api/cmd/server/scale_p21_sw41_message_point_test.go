package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW41MessagePointDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{53}, 32)))
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw41-message-")
		private := ""
		for _, project := range f.data.Projects {
			if project.ID == "m2-unrelated" {
				private = project.TeamIDs[0]
			}
		}
		if private == "" {
			data, _ := repo.WorkspaceMetadata(workspace)
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
			}
		}
		at := time.Date(2026, 11, 14, 8, 9, 10, 11, time.UTC)
		issue := domain.Issue{
			ID: prefix + "issue", Identifier: "SW41-1", Number: 1, Title: "Message <title>", Description: strings.Repeat("Owned complete issue body ", 5000),
			Team: domain.Team{ID: f.public}, State: domain.WorkflowState{ID: prefix + "state", Name: "In progress", Type: "started"},
			Creator: f.data.Viewer, CreatedAt: at, UpdatedAt: at,
		}
		type scenario struct {
			name, provider, language                       string
			private, restricted, archived, hipaa, noSource bool
			missing, unbound, noForm, request              bool
		}
		cases := []scenario{
			{name: "public-reply-chinese", provider: "lark", language: "zh-CN"},
			{name: "personal-card-system-lark", provider: "lark", language: "system", noSource: true, noForm: true},
			{name: "hipaa-link", provider: "feishu", language: "en", hipaa: true},
			{name: "private-personal", provider: "lark", language: "en", private: true},
			{name: "restricted-public-issue", provider: "feishu", restricted: true},
			{name: "archived-personal", provider: "lark", language: "fr", archived: true, request: true},
			{name: "missing-issue", provider: "lark", language: "en", missing: true},
			{name: "recipient-unbound", provider: "lark", unbound: true},
		}
		var active *store.SQLiteStore
		var sent []map[string]any
		mockChatTransport(t, func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Path, "tenant_access_token") {
				return chatTestResponse(http.StatusOK, `{"code":0,"tenant_access_token":"sw41-message-token","expire":7200}`), nil
			}
			if !strings.Contains(r.URL.Path, "/im/v1/messages") {
				t.Fatalf("unexpected message request %s", r.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			sent = append(sent, map[string]any{"method": r.Method, "path": r.URL.Path, "query": r.URL.RawQuery, "body": body})
			if active == f.probe && active.MetadataWorkUsedForTesting() == 0 {
				t.Error("message point result was released before platform output")
			}
			return chatTestResponse(http.StatusOK, `{"code":0,"data":{"message_id":"sw41-message","thread_id":"sw41-thread"}}`), nil
		})
		comparisons := 0
		for index, test := range cases {
			record := domain.ChatIntegrationRecord{
				WorkspaceKey: workspace, ConnectionID: idgen.New("sw41-connection-"), Provider: test.provider,
				AppID: fmt.Sprintf("%sapp-%d", prefix, index), TenantKey: prefix + "tenant", Connected: true,
			}
			if err := repo.PutChatIntegration(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = repo.DeleteConnectorSecret(context.Background(), chatCredentialID(record))
				_ = repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID)
			})
			user := f.principals[2].user
			if test.private {
				user = f.principals[1].user
			}
			identity := domain.ChatIdentity{
				UserID: user.ID, OpenID: prefix + "open", TenantKey: record.TenantKey,
				AuthProvider: "password", AuthenticatedAt: time.Now().UTC(),
			}
			if !test.unbound {
				if err := repo.PutChatIdentity(t.Context(), workspace, record.ConnectionID, identity); err != nil {
					t.Fatal(err)
				}
			}
			secret, _ := json.Marshal(chatCredentials{AppSecret: "sw41-message-secret"})
			if err := repo.PutConnectorSecret(t.Context(), chatCredentialID(record), secret, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw41_message_seed", "", nil, func(data *domain.Bootstrap) error {
				item := issue
				if test.private {
					item.Team.ID = private
				}
				if test.restricted {
					item.Permissions = []domain.IssuePermission{{SubjectType: "user", SubjectID: user.ID, Role: "viewer"}}
				}
				if test.archived {
					item.ArchivedAt = &at
				}
				data.Issues = []domain.Issue{item}
				data.WorkspaceSettings.HIPAACompliance = test.hipaa
				data.UserSettings[user.ID] = domain.UserSettings{Language: test.language}
				data.UserSettings[prefix+"unrelated"] = domain.UserSettings{AgentInstructions: strings.Repeat("Unrelated settings ", 30000)}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			intake := domain.ChatIntakeRequest{
				Kind: "issue", ConnectionID: record.ConnectionID, ChatID: prefix + "group", BindingID: prefix + "binding", MessageID: prefix + fmt.Sprint(index),
				IssueID: issue.ID, RequesterID: user.ID, RequesterOpenID: identity.OpenID, Source: domain.ChatRequestSourceShortcut, Status: domain.ChatRequestCreated,
			}
			if test.request {
				intake.Kind = ""
			}
			if test.missing {
				intake.IssueID = prefix + "missing"
			}
			intake, _, err := repo.ReserveChatIntakeRequest(t.Context(), workspace, intake)
			if err != nil {
				t.Fatal(err)
			}
			{
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				for _, operation := range []string{"result", "exists"} {
					type outcome struct {
						Messages []map[string]any
						Error    string
						Sent     bool
					}
					var outputs []outcome
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						ctx := withChatIdentityAuthentication(f.literalRequest(user, nil, nil).Context(), identity)
						progress := &chatDeliveryProgress{}
						ctx = context.WithValue(ctx, chatDeliveryProgressKey{}, progress)
						active, sent = source, nil
						service := &server{store: source, allowedOrigin: "https://flow.example.test"}
						job := domain.ChatJob{ID: prefix + "job", DedupKey: fmt.Sprintf("%sdedup-%d-%s", prefix, index, operation)}
						var err error
						if operation == "result" {
							payload := chatNotificationJob{IssueID: intake.IssueID, PersonalOpenID: identity.OpenID, SourceMessageID: prefix + "source"}
							if test.noSource {
								payload.SourceMessageID = ""
							}
							err = service.processChatMessageResult(ctx, record, job, payload)
						} else {
							metadata, _ := repo.WorkspaceMetadataFields(workspace, "userSettings")
							form := prefix + "form"
							if test.noForm {
								form = ""
							}
							err = service.chatMessageIntakeExists(ctx, record, identity, metadata, job, intake, form)
						}
						result := outcome{Messages: sent, Sent: progress.sent}
						if err != nil {
							result.Error = fmt.Sprintf("%T: %v", err, err)
						}
						outputs = append(outputs, result)
						after, err := repo.ChatIntakeRequestForMessage(t.Context(), workspace, record.ConnectionID, intake.MessageID)
						if err != nil || !reflect.DeepEqual(after, intake) {
							t.Fatal("point result changed durable intake", err)
						}
						if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
							t.Fatal("message point operation leaked work or hydrated metadata")
						}
					}
					if !reflect.DeepEqual(outputs[0], outputs[1]) {
						t.Fatalf("message point name=%s operation=%s\nlegacy=%+v\nbounded=%+v", test.name, operation, outputs[0], outputs[1])
					}
					if index == 0 && (len(outputs[1].Messages) != 2 || outputs[1].Error != "") {
						t.Fatal("public reply/form fixture produced no successful output", operation, outputs[1])
					}
					comparisons++
				}
			}
			if err := repo.DeleteChatIntegration(t.Context(), workspace, record.ConnectionID); err != nil {
				t.Fatal(err)
			}
		}
		// A refusal to admit a point result is a retryable operation error,
		// never the existing-item fallback or a permanent access revocation.
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, ConnectionID: idgen.New("sw41-failure-"), Provider: "lark", AppID: prefix + "failure-app", TenantKey: prefix + "tenant", Connected: true}
		if err := repo.PutChatIntegration(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID) })
		identity := domain.ChatIdentity{UserID: f.data.Viewer.ID, OpenID: prefix + "failure-open", TenantKey: record.TenantKey, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
		if err := repo.PutChatIdentity(t.Context(), workspace, record.ConnectionID, identity); err != nil {
			t.Fatal(err)
		}
		for _, failure := range []string{"quota", "canceled"} {
			for _, operation := range []string{"result", "exists"} {
				ctx := withChatIdentityAuthentication(t.Context(), identity)
				var owner *store.MetadataJSONRead
				var err error
				var expected error = context.Canceled
				if failure == "quota" {
					owner, err = f.probe.BeginMetadataJSONRead(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if err := owner.ReserveEncoded(ctx, (64<<20)-(64<<10)); err != nil {
						t.Fatal(err)
					}
					expected = store.ErrMetadataWorkBudget
				} else {
					canceled, cancel := context.WithCancel(ctx)
					cancel()
					ctx = canceled
				}
				baseline := f.probe.MetadataWorkUsedForTesting()
				active, sent = f.probe, nil
				service := &server{store: f.probe}
				if operation == "result" {
					err = service.processChatMessageResult(ctx, record, domain.ChatJob{}, chatNotificationJob{IssueID: issue.ID, PersonalOpenID: identity.OpenID})
				} else {
					err = service.chatMessageIntakeExists(ctx, record, identity, domain.Bootstrap{}, domain.ChatJob{}, domain.ChatIntakeRequest{Kind: "issue", IssueID: issue.ID}, "")
				}
				if !errors.Is(err, expected) || len(sent) != 0 || f.probe.MetadataWorkUsedForTesting() != baseline {
					t.Fatal("new point refusal became missing/access-revoked or leaked work", failure, operation, err, len(sent), baseline, f.probe.MetadataWorkUsedForTesting())
				}
				if owner != nil {
					owner.Close()
				}
			}
		}
		t.Logf("exact message point output comparisons=%d plus four bounded refusal checks", comparisons)
	}
}
