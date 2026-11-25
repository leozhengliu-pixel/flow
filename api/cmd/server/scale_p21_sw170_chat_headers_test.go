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

type scaleP21SW170Output struct {
	Error        string
	FormError    string
	PreviewError []string
	Cards        []map[string]any
	HeaderCards  []map[string]any
	Group        chatGroupAgentContext
	RequestScope *chatGroupRequestScope
}

func scaleP21SW170Call(t *testing.T, source *store.SQLiteStore, ctx context.Context, workspace, userID string, record domain.ChatIntegrationRecord, prefix string, render bool, cards *[]map[string]any, statements ...*int) scaleP21SW170Output {
	t.Helper()
	owned, closeOutput := beginMetadataOutputReads(ctx)
	defer closeOutput()
	service := &server{store: source, allowedOrigin: "https://flow.example.test"}
	service.agent.ToolsEnabled, service.agent.WriteTools = true, true
	metadata := domain.Bootstrap{ViewerRole: "preexisting-destination"}
	readContext := owned
	if len(statements) == 1 {
		readContext = store.WithSQLStatementObserver(owned, func(string) { *statements[0] += 1 })
	}
	var err error
	if source.BoundedMetadataEnabled() {
		err = service.chatAuthorizedBootstrapHeaders(readContext, workspace, userID, &metadata)
	} else {
		err = service.chatAuthorizedMutation(readContext, workspace, userID, http.MethodGet, "/api/issue-records/bootstrap", nil, &metadata)
	}
	output := scaleP21SW170Output{Error: scaleP21ChatPointError(err)}
	if err != nil {
		if metadata.ViewerRole != "preexisting-destination" {
			t.Fatal("refused chat header read changed its destination")
		}
		if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("chat header refusal retained work before owner close")
		}
		return output
	}
	if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("successful chat headers escaped their containing owner")
	}
	for _, project := range metadata.Projects {
		if project.ID == prefix+"project" || project.ID == prefix+"nil-project" {
			output.HeaderCards = append(output.HeaderCards, service.chatDMProjectCard(workspace, project, false))
		}
	}
	if render {
		*cards = nil
		identity := domain.ChatIdentity{UserID: userID, OpenID: prefix + "open", TenantKey: record.TenantKey, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
		dm := chatDMRequest{
			record: record, identity: identity, metadata: metadata,
			target:  chatDMTarget{Workspace: workspace, Name: "Owned header workspace", Identity: identity},
			command: chatInboundCommand{OpenID: identity.OpenID, MessageID: ""},
			job:     domain.ChatJob{DedupKey: prefix + userID + "-form"},
		}
		output.FormError = scaleP21ChatPointError(service.chatDMNewIssueForm(owned, dm, "Header-preserving issue"))
		for _, ref := range []string{prefix + "project", prefix + "current-project", prefix + "old-project"} {
			output.PreviewError = append(output.PreviewError, scaleP21ChatPointError(service.chatDMLinkPreview(owned, dm, chatDMLink{
				Workspace: workspace, Kind: "project", Ref: ref,
			})))
		}
		dm.group = &chatGroupReply{ChatID: prefix + "group", ChatName: "Header group", Root: prefix + "root"}
		dm.command.ChatID, dm.command.MessageID = dm.group.ChatID, prefix+"mention"
		output.Group = service.chatGroupContext(owned, dm)
		output.RequestScope = service.chatGroupRequestScopeFor(owned, dm)
		output.Cards = append([]map[string]any(nil), (*cards)...)
	}
	closeOutput()
	closeOutput()
	if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
		t.Fatal("chat header operation leaked work or decoded complete metadata")
	}
	return output
}

func TestScaleP21SW170ChatBootstrapHeadersDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{70}, 32)))
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw170-headers-")
		at := time.Date(2026, 11, 15, 2, 3, 4, 567, time.UTC)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw170_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams,
				domain.Team{ID: prefix + "archived", Name: "Archived must stay absent", Key: "ARCH", ArchivedAt: &at},
				domain.Team{ID: prefix + "retired", Name: "Retired must stay absent", Key: "RET", RetiredAt: &at})
			for index := range data.Teams {
				if data.Teams[index].ID == f.public {
					data.Teams[index].Name, data.Teams[index].Key = "Visible team <>&", "VISIBLE"
				}
			}
			data.Projects = append(data.Projects,
				domain.Project{ID: prefix + "project", SlugID: prefix + "current-project", PreviousSlugIDs: []string{prefix + "old-project"},
					Name: "Project header <>&", Summary: strings.Repeat("Readable summary \u4e2d\u6587 ", 40), TeamIDs: []string{f.public},
					Status: domain.ProjectStatus{Name: "In progress"}, Lead: &data.Viewer, TargetDate: new("2026-12-01"),
					Description: strings.Repeat("Unconsumed project body ", 30000)},
				domain.Project{ID: prefix + "nil-project", SlugID: prefix + "nil-project", Name: "Nil header fields", TeamIDs: []string{f.public}},
			)
			data.Users = append(data.Users,
				domain.User{ID: prefix + "inactive-directory", Name: "Inactive directory entry", Active: false},
				domain.User{ID: prefix + "application-directory", Name: "Application directory entry", Active: true, App: true})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		record := domain.ChatIntegrationRecord{
			WorkspaceKey: workspace, Provider: "lark", ConnectionID: prefix + "connection",
			AppID: prefix + "app", TenantKey: prefix + "tenant", Connected: true,
		}
		if err := repo.PutChatIntegration(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		secret, _ := json.Marshal(chatCredentials{AppSecret: "sw170-test-secret"})
		if err := repo.PutConnectorSecret(t.Context(), chatCredentialID(record), secret, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = repo.DeleteConnectorSecret(context.Background(), chatCredentialID(record))
			_ = repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID)
		})
		if err := repo.PutChatBinding(t.Context(), workspace, domain.ChatBinding{
			ID: prefix + "binding", ConnectionID: record.ConnectionID, ChatID: prefix + "group", ChatName: "Header group",
			TeamID: f.public, ProjectIDs: []string{prefix + "project"}, Enabled: true, AllowCreate: true, CreatedAt: at,
			Requests:      &domain.ChatBindingRequests{Enabled: true, Emoji: "OK"},
			Authorization: &domain.ChatBindingAuthorization{UserID: f.data.Viewer.ID, Role: domain.ChatBindingRoleWorkspaceAdmin, AuthorizedAt: at},
		}); err != nil {
			t.Fatal(err)
		}
		var active *store.SQLiteStore
		var cards []map[string]any
		mockChatTransport(t, func(request *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(request.URL.Path, "tenant_access_token"):
				return chatTestResponse(http.StatusOK, `{"code":0,"tenant_access_token":"sw170-token","expire":7200}`), nil
			case request.URL.Path == "/open-apis/im/v1/chats/"+prefix+"group" && request.Method == http.MethodGet:
				return chatTestResponse(http.StatusOK, fmt.Sprintf(`{"code":0,"data":{"name":"Header group","external":false,"tenant_key":%q,"chat_mode":"group"}}`, record.TenantKey)), nil
			case strings.Contains(request.URL.Path, "/im/v1/messages") && request.Method == http.MethodGet:
				return chatTestResponse(http.StatusOK, `{"code":0,"data":{"items":[],"has_more":false}}`), nil
			case strings.Contains(request.URL.Path, "/im/v1/messages"):
				if active.BoundedMetadataEnabled() && active.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("chat form/card send outlived header ownership")
				}
				var payload struct {
					Content string `json:"content"`
				}
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				var card map[string]any
				if err := json.Unmarshal([]byte(payload.Content), &card); err != nil {
					t.Fatal(err)
				}
				cards = append(cards, card)
				return chatTestResponse(http.StatusOK, `{"code":0,"data":{"message_id":"sw170-message","chat_id":"sw170-personal-chat"}}`), nil
			}
			t.Fatalf("unexpected chat header rendering request %s", request.URL.Path)
			return nil, nil
		})
		pair := func(t *testing.T, ctx context.Context, key, userID string, render bool) {
			t.Helper()
			var results []scaleP21SW170Output
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				active = source
				results = append(results, scaleP21SW170Call(t, source, ctx, key, userID, record, prefix, render, &cards))
			}
			if !reflect.DeepEqual(results[0], results[1]) {
				t.Fatal("chat header authorization/rendering differs", scaleP21M2Diff(results[0], results[1]))
			}
		}
		for _, records := range []string{"off", "on"} {
			t.Run("records="+records, func(t *testing.T) {
				t.Setenv("FLOW_INTERNAL_CHAT_RECORDS", records)
				for _, reloaded := range []bool{false, true} {
					t.Run(fmt.Sprint("reloaded=", reloaded), func(t *testing.T) {
						if reloaded {
							if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
								t.Fatal(err)
							}
						}
						if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
							t.Fatal(err)
						}
						f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
							ctx := scaleP21ChatPointIdentity(f.literalRequest(user, key, nil).Context(), user.ID)
							pair(t, ctx, workspace, user.ID, true)
						})
					})
				}
			})
		}
		t.Run("sql-and-rendered-branch-witness", func(t *testing.T) {
			ctx := scaleP21ChatPointIdentity(t.Context(), f.data.Viewer.ID)
			var outputs []scaleP21SW170Output
			var counts [2]int
			for index, source := range []*store.SQLiteStore{repo, f.probe} {
				active = source
				outputs = append(outputs, scaleP21SW170Call(t, source, ctx, workspace, f.data.Viewer.ID, record, prefix, true, &cards, &counts[index]))
			}
			if !reflect.DeepEqual(outputs[0], outputs[1]) {
				t.Fatal("measured header rendering changed", scaleP21M2Diff(outputs[0], outputs[1]))
			}
			got := outputs[1]
			if got.Error != "" || got.FormError != "" || len(got.Cards) != 4 || got.Group.Requests == nil || got.RequestScope == nil ||
				!strings.Contains(got.Group.Prompt, "Visible team <>&") || !strings.Contains(got.Group.Prompt, "Project header <>&") {
				t.Fatal("successful form/project/group header branch was not reached", got.Error, got.FormError, len(got.Cards), got.Group)
			}
			for _, err := range got.PreviewError {
				if err != "" {
					t.Fatal("project ID/current slug/old slug did not reach preview", got.PreviewError)
				}
			}
			form, _ := json.Marshal(got.Cards[0])
			teamLabel, _ := json.Marshal("Visible team <>&")
			if !bytes.Contains(form, teamLabel) || !bytes.Contains(form, []byte(f.data.Viewer.ID)) {
				t.Fatal("real team or preferred-user picker metadata is missing")
			}
			card, _ := json.Marshal(got.Cards[1])
			summary, _ := json.Marshal(chatTruncateText(chatDMRedact(strings.Repeat("Readable summary \u4e2d\u6587 ", 40), false), 400))
			if !bytes.Contains(card, summary) {
				t.Fatal("project card did not retain the real consumed Summary")
			}
			if counts[0] == 0 || counts[1] == 0 {
				t.Fatal("header SQL measurement missed a read", counts)
			}
			t.Logf("successful_header_SQL_legacy=%d bounded=%d", counts[0], counts[1])
		})
		// The handed SQL snapshot must remain the metadata source; the new
		// helper must not silently swap to its installed generation.
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw170_new_generation", "", nil, func(data *domain.Bootstrap) error {
			for index := range data.Projects {
				if data.Projects[index].ID == prefix+"project" {
					data.Projects[index].Name = "New handed generation must beat old installed metadata"
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		// The probe deliberately retains the earlier generation. Opening a
		// snapshot only after the write avoids write-lock upgrades and needs
		// no second connection for an in-snapshot mutation.
		ctx := scaleP21ChatPointIdentity(t.Context(), f.data.Viewer.ID)
		snapshot, closeSnapshot, err := repo.BeginWorkspaceRead(ctx, workspace)
		if err != nil {
			t.Fatal(err)
		}
		pair(t, snapshot, workspace, f.data.Viewer.ID, false)
		closeSnapshot()

		inactive, err := repo.ProvisionSCIMUser(t.Context(), f.data.Workspace.ID, idgen.New("sw170-inactive-"), idgen.New("sw170-inactive-"), "Inactive header actor", idgen.New("sw170-inactive-")+"@example.test", "", "member", false)
		if err != nil {
			t.Fatal(err)
		}
		f.resources.users = append(f.resources.users, inactive.User)
		app, err := repo.InstallApplication(store.ContextWithActor(t.Context(), f.data.Viewer), workspace, domain.ApplicationInstallation{
			ClientID: idgen.New("sw170-app-"), Name: "Header application", Active: true, InstalledBy: f.data.Viewer.ID,
			TeamIDs: []string{f.public}, Scopes: []string{"read"}, WebhookURL: "https://example.test/header-app",
		}, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"missing-identity", "mismatched-identity", "expired", "missing-provider", "missing-workspace", "empty-workspace", "missing-user", "empty-user", "inactive", "application", "canceled"} {
			t.Run(name, func(t *testing.T) {
				userID, selectedWorkspace := f.data.Viewer.ID, workspace
				identity := domain.ChatIdentity{UserID: userID, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
				switch name {
				case "mismatched-identity":
					identity.UserID += "-mismatch"
				case "expired":
					identity.AuthenticatedAt = time.Now().UTC().Add(-40 * 24 * time.Hour)
				case "missing-provider":
					identity.AuthProvider = ""
				case "missing-workspace":
					selectedWorkspace += "-missing"
				case "empty-workspace":
					selectedWorkspace = ""
				case "missing-user":
					userID += "-missing"
				case "empty-user":
					userID = ""
				case "inactive":
					userID, identity.UserID = inactive.User.ID, inactive.User.ID
				case "application":
					userID, identity.UserID = app.UserID, app.UserID
				}
				ctx := t.Context()
				if name != "missing-identity" {
					ctx = withChatIdentityAuthentication(ctx, identity)
				}
				if name == "canceled" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				pair(t, ctx, selectedWorkspace, userID, false)
			})
		}
		member := f.principals[2].user
		if err := repo.RemoveMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		pair(t, scaleP21ChatPointIdentity(t.Context(), member.ID), workspace, member.ID, false)

		for _, state := range []string{"missing", "closed"} {
			t.Run("owner="+state, func(t *testing.T) {
				ctx := scaleP21ChatPointIdentity(t.Context(), f.data.Viewer.ID)
				if state == "closed" {
					var closeOutput func()
					ctx, closeOutput = beginMetadataOutputReads(ctx)
					closeOutput()
				}
				before := domain.Bootstrap{ViewerRole: "unchanged"}
				err := (&server{store: f.probe}).chatAuthorizedBootstrapHeaders(ctx, workspace, f.data.Viewer.ID, &before)
				if !errors.Is(err, store.ErrBoundedMetadataCoverage) || before.ViewerRole != "unchanged" || f.probe.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("chat headers escaped absent/closed ownership", state, err)
				}
			})
		}
	}
}
