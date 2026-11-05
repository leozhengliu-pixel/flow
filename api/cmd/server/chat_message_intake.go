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
	language := chatLanguage(metadata, identity.UserID, record.Provider)
	issueKind := intake.Kind == "issue"
	key := "message_is_request"
	if issueKind {
		key = "message_is_issue"
	}
	text := chatT(language, key)
	var issue domain.Issue
	if err := s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(intake.IssueID), nil, &issue); err == nil {
		text = chatT(language, key+"_link", issue.Identifier+" "+chatRequestIssueURL(s, record, issue))
		if err := s.chatUpdateMessageFormResult(ctx, record, language, formMessage, issue, issueKind); err != nil {
			return err
		}
	}
	return s.chatSendPersonalText(ctx, record, identity.OpenID, text, chatKey(job.DedupKey, "exists"))
}

// chatUpdateMessageFormResult redraws the member's form as its result, in
// their language.
func (s *server) chatUpdateMessageFormResult(ctx context.Context, record domain.ChatIntegrationRecord, language, messageID string, issue domain.Issue, issueKind bool) error {
	if messageID == "" {
		return nil
	}
	heading := chatT(language, "request_submitted")
	if issueKind {
		heading = chatT(language, "issue.created")
	}
	card := chatCard(heading, chatCardText(issue.Identifier), map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatPlain(chatT(language, "open_in_flow")), "url": chatRequestIssueURL(s, record, issue)}}})
	err := s.chatUpdateCard(ctx, record, messageID, card)
	if chatMessageGone(err) {
		return nil
	}
	return err
}
