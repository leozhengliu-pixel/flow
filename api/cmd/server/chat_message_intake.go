package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"

	"flow/api/internal/domain"
)

func (s *server) chatMessageIntakeResult(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, metadata domain.Bootstrap, job domain.ChatJob, action chatExtendedAction) (bool, error) {
	intake, err := s.store.ChatIntakeRequestForMessage(ctx, record.WorkspaceKey, record.ConnectionID, action.SourceMessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if intake.Status != domain.ChatRequestCreated {
		return false, nil
	}
	return true, s.chatMessageIntakeExists(ctx, record, identity, metadata, job, intake, action.PersonalMessage)
}

// Both shortcut buttons share the message's durable intake reservation.
// Keep the result private and re-check access before naming the existing item.
func (s *server) chatMessageIntakeExists(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, metadata domain.Bootstrap, job domain.ChatJob, intake domain.ChatIntakeRequest, formMessage string) error {
	issueKind := intake.Kind == "issue"
	text, zh := "This message is already a request in Flow.", "这条消息已经提交为 Flow 需求。"
	if issueKind {
		text, zh = "This message already created an issue in Flow.", "这条消息已经创建了 Flow 事项。"
	}
	var issue domain.Issue
	if err := s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(intake.IssueID), nil, &issue); err == nil {
		label := issue.Identifier + " " + chatRequestIssueURL(s, record, issue)
		text, zh = "This message is already a request: "+label, "这条消息已经提交为需求："+label
		if issueKind {
			text, zh = "This message already created an issue: "+label, "这条消息已经创建了事项："+label
		}
		if err := s.chatUpdateMessageFormResult(ctx, record, formMessage, issue, issueKind); err != nil {
			return err
		}
	}
	if chatLanguage(metadata, identity.UserID, record.Provider) == "zh" {
		text = zh
	}
	return s.chatSendPersonalText(ctx, record, identity.OpenID, text, chatKey(job.DedupKey, "exists"))
}

func (s *server) chatUpdateMessageFormResult(ctx context.Context, record domain.ChatIntegrationRecord, messageID string, issue domain.Issue, issueKind bool) error {
	if messageID == "" {
		return nil
	}
	en, zh := "Request submitted", "需求已提交"
	if issueKind {
		en, zh = "Issue created", "事项已创建"
	}
	card := chatCardI18n(en, zh, chatCardText(issue.Identifier), map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatI18nPlain("Open in Flow", "在 Flow 中打开"), "url": chatRequestIssueURL(s, record, issue)}}})
	err := s.chatUpdateCard(ctx, record, messageID, card)
	if chatMessageGone(err) {
		return nil
	}
	return err
}
