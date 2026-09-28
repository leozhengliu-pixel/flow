package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Team Slack notification keys, as stored in TeamSettings.SlackNotifications.
const (
	slackTeamProjectUpdate  = "projectUpdates"
	slackTeamIssueCreated   = "issueCreated"
	slackTeamIssueClosed    = "issueCompleted"
	slackTeamIssueStatus    = "issueStatusChanged"
	slackTeamCommentCreated = "commentCreated"
	slackTeamIssueTriage    = "issueTriage"
)

// dispatchDomainEvent fans a committed domain event out to every outbound
// channel: HTTP webhooks and team Slack channels.
func (s *server) dispatchDomainEvent(workspace string, event domain.DomainEvent) {
	s.dispatchWebhookEvent(workspace, event)
	s.dispatchTeamSlackEvent(workspace, event)
	s.dispatchLoopTriggers(workspace, event)
}

type teamSlackMessage struct {
	teamID string
	key    string
	text   string
}

// dispatchTeamSlackEvent posts to each affected team's Slack channel when the
// team opted into that notification and the workspace has Slack connected.
func (s *server) dispatchTeamSlackEvent(workspace string, event domain.DomainEvent) {
	if !strings.HasPrefix(event.Type, "issue.") && event.Type != "comment.created" && event.Type != "project.update_created" {
		return
	}
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		return
	}
	token := slackBotToken(data)
	if token == "" {
		return
	}
	messages := s.teamSlackMessages(workspace, data, event)
	for _, message := range messages {
		settings, exists := data.TeamSettings[message.teamID]
		// chat.postMessage accepts a channel ID or a channel name.
		channel := settings.SlackChannelID
		if channel == "" {
			channel = strings.TrimSpace(settings.SlackChannelName)
		}
		if !exists || channel == "" || !settings.SlackNotifications[message.key] {
			continue
		}
		text := message.text
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := postSlackMessage(ctx, token, channel, text); err != nil {
				log.Printf("Flow team Slack notification workspace=%s channel=%s event=%s: %v", workspace, channel, event.Type, err)
			}
		}()
	}
}

func (s *server) teamSlackMessages(workspace string, data domain.Bootstrap, event domain.DomainEvent) []teamSlackMessage {
	switch {
	case event.Type == "project.update_created":
		var project *domain.Project
		for index := range data.Projects {
			if data.Projects[index].ID == event.AggregateID {
				project = &data.Projects[index]
				break
			}
		}
		if project == nil {
			return nil
		}
		slugID := project.SlugID
		if slugID == "" {
			slugID = project.ID
		}
		text := fmt.Sprintf("New project update posted for %s", slackLink(fmt.Sprintf("%s/%s/project/%s/overview", slackAppURL(), workspace, slugID), project.Name))
		messages := make([]teamSlackMessage, 0, len(project.TeamIDs))
		for _, teamID := range project.TeamIDs {
			messages = append(messages, teamSlackMessage{teamID: teamID, key: slackTeamProjectUpdate, text: text})
		}
		return messages
	case event.Type == "comment.created":
		issue, err := s.store.IssueRecord(context.Background(), workspace, event.AggregateID)
		if err != nil {
			return nil
		}
		return []teamSlackMessage{{teamID: issue.Team.ID, key: slackTeamCommentCreated, text: fmt.Sprintf("New comment on %s", slackIssueLink(workspace, issue))}}
	case event.Type == "issue.created":
		issue, err := s.store.IssueRecord(context.Background(), workspace, event.AggregateID)
		if err != nil {
			return nil
		}
		messages := []teamSlackMessage{{teamID: issue.Team.ID, key: slackTeamIssueCreated, text: fmt.Sprintf("%s was added to %s", slackIssueLink(workspace, issue), issue.Team.Name)}}
		if issue.State.Type == "triage" {
			messages = append(messages, teamSlackMessage{teamID: issue.Team.ID, key: slackTeamIssueTriage, text: fmt.Sprintf("%s was added to the triage queue", slackIssueLink(workspace, issue))})
		}
		return messages
	case event.Type == "issue.updated":
		if !slackStateChanged(event.PreviousValues) {
			return nil
		}
		issue, err := s.store.IssueRecord(context.Background(), workspace, event.AggregateID)
		if err != nil {
			return nil
		}
		link := slackIssueLink(workspace, issue)
		messages := []teamSlackMessage{{teamID: issue.Team.ID, key: slackTeamIssueStatus, text: fmt.Sprintf("%s moved to %s", link, issue.State.Name)}}
		switch issue.State.Type {
		case "completed", "canceled":
			messages = append(messages, teamSlackMessage{teamID: issue.Team.ID, key: slackTeamIssueClosed, text: fmt.Sprintf("%s was marked %s", link, strings.ToLower(issue.State.Name))})
		case "triage":
			messages = append(messages, teamSlackMessage{teamID: issue.Team.ID, key: slackTeamIssueTriage, text: fmt.Sprintf("%s was added to the triage queue", link)})
		}
		return messages
	}
	return nil
}

// slackStateChanged reports whether an issue update changed its workflow state.
func slackStateChanged(previous json.RawMessage) bool {
	if len(previous) == 0 {
		return false
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(previous, &values) != nil {
		return false
	}
	_, changed := values["state"]
	return changed
}

func slackBotToken(data domain.Bootstrap) string {
	for _, connection := range data.IntegrationConnections {
		if connection.Provider == "slack" && connection.Status == "connected" && connection.OAuthAccessToken != "" {
			return connection.OAuthAccessToken
		}
	}
	return ""
}

func slackAppURL() string {
	if value := strings.TrimRight(os.Getenv("FLOW_APP_URL"), "/"); value != "" {
		return value
	}
	return "http://localhost:5173"
}

func slackIssueLink(workspace string, issue domain.Issue) string {
	return slackLink(fmt.Sprintf("%s/%s/issue/%s", slackAppURL(), workspace, issue.Identifier), fmt.Sprintf("%s %s", issue.Identifier, issue.Title))
}

// slackLink formats a Slack mrkdwn link, escaping the characters Slack reserves.
func slackLink(url, label string) string {
	escape := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return fmt.Sprintf("<%s|%s>", url, escape.Replace(label))
}

func postSlackMessage(ctx context.Context, token, channel, text string) error {
	endpoint := strings.TrimRight(os.Getenv("FLOW_SLACK_API_URL"), "/")
	if endpoint == "" {
		endpoint = "https://slack.com/api"
	}
	body, _ := json.Marshal(map[string]string{"channel": channel, "text": text})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return fmt.Errorf("slack response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("slack: %s", result.Error)
	}
	return nil
}
