package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SlackPost struct {
	method, path, auth string
	body               map[string]string
	release, done      chan struct{}
}

type scaleP21SlackPostBody struct {
	io.Reader
	done chan struct{}
}

func (body scaleP21SlackPostBody) Close() error {
	close(body.done)
	return nil
}

func TestScaleP21TeamSlackNotificationsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{53}, 32)))
	t.Setenv("FLOW_APP_URL", "https://flow.example.test/")
	t.Setenv("FLOW_SLACK_API_URL", "https://slack-p21.example.test/api")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("slack-notifications-")
		project, initiative := prefix+"project", prefix+"initiative"
		team, other, empty := prefix+"team", prefix+"other", ""
		firstConnection, laterConnection := prefix+"first", prefix+"later"
		t.Cleanup(func() {
			_ = repo.DeleteIntegrationOAuthTokens(context.Background(), workspace, firstConnection)
			_ = repo.DeleteIntegrationOAuthTokens(context.Background(), workspace, laterConnection)
		})
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
			map[string]any{"title": "Slack <issue> & title", "teamId": f.public}, workspace, http.StatusCreated)
		posts := make(chan scaleP21SlackPost, 32)
		mockChatTransport(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Host != "slack-p21.example.test" {
				t.Errorf("unexpected Slack platform host %s", request.URL.Host)
				return nil, io.ErrUnexpectedEOF
			}
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
				return nil, err
			}
			post := scaleP21SlackPost{
				method: request.Method, path: request.URL.Path, auth: request.Header.Get("Authorization"), body: body,
				release: make(chan struct{}), done: make(chan struct{}),
			}
			posts <- post
			select {
			case <-post.release:
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
			if body["channel"] == "C-canceled" {
				close(post.done)
				return nil, context.Canceled
			}
			result := `{"ok":true}`
			if body["channel"] == "C-failure" {
				result = `{"ok":false,"error":"channel_not_found"}`
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: scaleP21SlackPostBody{strings.NewReader(result), post.done}}, nil
		})
		cases := []struct {
			name, event, aggregate, variant, state string
			previous                               string
			want                                   int
		}{
			{name: "project-duplicates", event: "project.update_created", aggregate: project, want: 3},
			{name: "project-id-fallback", event: "project.update_created", aggregate: project, variant: "no-slug", want: 3},
			{name: "initiative-deduplicates-teams", event: "initiative.update_created", aggregate: initiative, want: 2},
			{name: "exact-id-not-slug", event: "project.update_created", aggregate: "target-slug", want: 0},
			{name: "missing-target", event: "project.update_created", aggregate: "missing", want: 0},
			{name: "channel-name-fallback", event: "project.update_created", aggregate: project, variant: "channel-name", want: 3},
			{name: "disabled-flags", event: "project.update_created", aggregate: project, variant: "disabled", want: 0},
			{name: "absent-settings", event: "project.update_created", aggregate: project, variant: "absent-settings", want: 0},
			{name: "blank-channel", event: "project.update_created", aggregate: project, variant: "blank-channel", want: 0},
			{name: "no-connection", event: "project.update_created", aggregate: project, variant: "no-connection", want: 0},
			{name: "case-sensitive-connection", event: "project.update_created", aggregate: project, variant: "wrong-case", want: 0},
			{name: "oauth-not-authorized", event: "project.update_created", aggregate: project, variant: "unauthorized", want: 0},
			{name: "no-first-token", event: "project.update_created", aggregate: project, variant: "missing-token", want: 0},
			{name: "failed-post-keeps-owner", event: "project.update_created", aggregate: project, variant: "post-failure", want: 3},
			{name: "canceled-post-keeps-owner", event: "project.update_created", aggregate: project, variant: "post-canceled", want: 3},
			{name: "default-workspace", event: "project.update_created", aggregate: project, variant: "default", want: 3},
			{name: "irrelevant-event", event: "document.updated", aggregate: project, want: 0},
			{name: "comment", event: "comment.created", aggregate: issue.ID, state: "started", want: 1},
			{name: "issue-created", event: "issue.created", aggregate: issue.ID, state: "started", want: 1},
			{name: "issue-triage", event: "issue.created", aggregate: issue.ID, state: "triage", want: 2},
			{name: "issue-completed", event: "issue.updated", aggregate: issue.ID, state: "completed", previous: `{"state":{"id":"before"}}`, want: 2},
			{name: "issue-canceled", event: "issue.updated", aggregate: issue.ID, state: "canceled", previous: `{"state":null}`, want: 2},
			{name: "issue-status-triage", event: "issue.updated", aggregate: issue.ID, state: "triage", previous: `{"state":{}}`, want: 2},
			{name: "issue-no-state-change", event: "issue.updated", aggregate: issue.ID, state: "started", previous: `{"title":"before"}`, want: 0},
			{name: "issue-malformed-previous", event: "issue.updated", aggregate: issue.ID, state: "started", previous: `{`, want: 0},
			{name: "missing-issue", event: "comment.created", aggregate: "missing", want: 0},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.slack_notification_seed", "", nil, func(data *domain.Bootstrap) error {
					unused := strings.Repeat("Ignored Slack metadata ", 15000)
					data.IntegrationConnections = []domain.IntegrationConnection{
						{ID: prefix + "wrong-provider", Provider: "Slack", Status: "connected", OAuthAuthorized: true, Config: map[string]string{"unused": unused}},
						{ID: prefix + "wrong-status", Provider: "slack", Status: "Connected", OAuthAuthorized: true},
						{ID: prefix + "not-authorized", Provider: "slack", Status: "connected"},
						{ID: firstConnection, Provider: "slack", Status: "connected", OAuthAuthorized: true, Config: map[string]string{"unused": unused}},
						{ID: laterConnection, Provider: "slack", Status: "connected", OAuthAuthorized: true},
					}
					if test.variant == "no-connection" {
						data.IntegrationConnections = nil
					}
					if test.variant == "wrong-case" {
						data.IntegrationConnections = data.IntegrationConnections[:2]
					}
					if test.variant == "unauthorized" {
						data.IntegrationConnections = data.IntegrationConnections[2:3]
					}
					data.Projects = []domain.Project{
						{ID: project + "-slug-collision", SlugID: project, Name: "Wrong slug match"},
						{ID: project, SlugID: "target-slug", Name: "First <project> & name", TeamIDs: []string{team, team, other}, Description: unused},
						{ID: project, Name: "Wrong later ID", TeamIDs: []string{empty}},
					}
					data.Initiatives = []domain.Initiative{
						{ID: initiative, SlugID: "initiative-slug", Name: "First <initiative> & name", LeadTeamID: team, ContributingTeamIDs: []string{team, other, "", other}, Summary: unused},
						{ID: initiative, Name: "Wrong later initiative", LeadTeamID: empty},
					}
					if test.variant == "no-slug" {
						data.Projects[1].SlugID = ""
						data.Projects = data.Projects[:2]
					}
					flags := map[string]bool{
						slackTeamProjectUpdate: true, slackTeamIssueCreated: true, slackTeamIssueClosed: true,
						slackTeamIssueStatus: true, slackTeamCommentCreated: true, slackTeamIssueTriage: true,
					}
					data.TeamSettings[team] = domain.TeamSettings{SlackChannelID: "C-team", SlackChannelName: unused, SlackNotifications: flags, AgentGuidance: unused}
					data.TeamSettings[other] = domain.TeamSettings{SlackChannelID: "C-other", SlackNotifications: flags}
					data.TeamSettings[empty] = domain.TeamSettings{SlackChannelID: "C-empty", SlackNotifications: flags}
					settings := data.TeamSettings[f.public]
					settings.SlackChannelID, settings.SlackNotifications = "C-issue", flags
					data.TeamSettings[f.public] = settings
					for _, id := range []string{team, other} {
						settings := data.TeamSettings[id]
						switch test.variant {
						case "channel-name":
							settings.SlackChannelID, settings.SlackChannelName = "", " \t#name-"+id+"\u2003 "
						case "disabled":
							settings.SlackNotifications = map[string]bool{slackTeamProjectUpdate: false}
						case "blank-channel":
							settings.SlackChannelID, settings.SlackChannelName = "", " \t\u2003 "
						case "post-failure":
							settings.SlackChannelID = "C-failure"
						case "post-canceled":
							settings.SlackChannelID = "C-canceled"
						}
						data.TeamSettings[id] = settings
						if test.variant == "absent-settings" {
							delete(data.TeamSettings, id)
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if test.state != "" {
					if _, err := repo.UpdateIssueRecord(t.Context(), workspace, issue.ID, nil, store.IssueMutationScope{}, func(_ *domain.Bootstrap, value *domain.Issue) error {
						value.State = domain.WorkflowState{ID: prefix + test.state, Name: "State " + test.state, Type: test.state}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				if err := repo.DeleteIntegrationOAuthTokens(t.Context(), workspace, firstConnection); err != nil {
					t.Fatal(err)
				}
				if test.variant != "missing-token" {
					if err := repo.PutIntegrationOAuthTokens(t.Context(), workspace, firstConnection, store.IntegrationOAuthTokens{AccessToken: "p21-first-test-token"}); err != nil {
						t.Fatal(err)
					}
				}
				if err := repo.PutIntegrationOAuthTokens(t.Context(), workspace, laterConnection, store.IntegrationOAuthTokens{AccessToken: "p21-wrong-later-token"}); err != nil {
					t.Fatal(err)
				}
				type output struct {
					Method, Path, Auth string
					Body               map[string]string
				}
				var results [][]output
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					key := workspace
					if test.variant == "default" {
						key = ""
					}
					(&server{store: source}).dispatchTeamSlackEvent(key, domain.DomainEvent{Type: test.event, AggregateID: test.aggregate, PreviousValues: json.RawMessage(test.previous)})
					var current []output
					var held []scaleP21SlackPost
					want := test.want
					if test.variant == "no-slug" && source == f.probe {
						want = len(results[0])
					}
					defer func() {
						for _, post := range held {
							select {
							case <-post.release:
							default:
								close(post.release)
							}
						}
					}()
					// This mutation starts from duplicate native IDs. Its
					// canonical rewrite may remove that target; retain the
					// actual old result rather than assuming it still posts.
					if test.variant == "no-slug" && source == repo {
					collectLegacy:
						for {
							select {
							case post := <-posts:
								held = append(held, post)
								current = append(current, output{post.method, post.path, post.auth, post.body})
							case <-time.After(50 * time.Millisecond):
								break collectLegacy
							}
						}
						want = len(current)
					}
					for len(current) < want {
						select {
						case post := <-posts:
							held = append(held, post)
							current = append(current, output{post.method, post.path, post.auth, post.body})
						case <-time.After(3 * time.Second):
							t.Fatal("Slack notification was not posted", len(current), want, "bounded", source.BoundedMetadataEnabled())
						}
					}
					if source == f.probe && want > 0 && source.MetadataWorkUsedForTesting() == 0 {
						t.Fatal("Slack dispatcher released before asynchronous posts completed")
					}
					for index, post := range held {
						close(post.release)
						select {
						case <-post.done:
						case <-time.After(3 * time.Second):
							t.Fatal("Slack response was not closed")
						}
						if source == f.probe && index < len(held)-1 && source.MetadataWorkUsedForTesting() == 0 {
							t.Fatal("first completed Slack post released another active delivery")
						}
					}
					deadline := time.Now().Add(3 * time.Second)
					for source.MetadataWorkUsedForTesting() != 0 && time.Now().Before(deadline) {
						time.Sleep(time.Millisecond)
					}
					if source.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("Slack posts leaked output admission")
					}
					select {
					case post := <-posts:
						close(post.release)
						t.Fatal("unexpected extra Slack notification", post.body["channel"])
					case <-time.After(10 * time.Millisecond):
					}
					slices.SortFunc(current, func(a, b output) int {
						return strings.Compare(a.Body["channel"]+"\n"+a.Body["text"], b.Body["channel"]+"\n"+b.Body["text"])
					})
					for _, item := range current {
						if item.Method != http.MethodPost || item.Path != "/api/chat.postMessage" || item.Auth != "Bearer p21-first-test-token" ||
							strings.Contains(item.Body["text"], "Wrong") {
							t.Fatal("Slack selection, request or escaping changed")
						}
					}
					results = append(results, current)
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatal("Slack platform differential", scaleP21M2Diff(results[0], results[1]))
				}
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("Slack notifications decoded complete workspace metadata")
		}
	}
}
