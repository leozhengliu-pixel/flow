package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Link preview refresh. The platform caches the preview it got for a link
// and viewer. Flow remembers the preview tokens of the items it described
// (for a week, the latest chatPreviewTokenLimit per item) and, when such an
// item changes, asks the platform to request those previews again
// (im/v2/url_previews/batch_update); the platform then calls
// url.preview.get for them, and each viewer's new answer is built with their
// current access, so a preview that is no longer allowed becomes neutral.

const (
	chatPreviewRefreshKind = "preview_refresh"
	chatPreviewTokenLimit  = 50
	// chatPreviewRefreshBatch is how many tokens one platform call carries.
	chatPreviewRefreshBatch = 20
)

type chatPreviewTokens struct {
	Kind     string              `json:"kind"`
	Resource string              `json:"resource"`
	Tokens   []chatPreviewViewed `json:"tokens"`
}

type chatPreviewViewed struct {
	Token  string    `json:"token"`
	OpenID string    `json:"openId"`
	At     time.Time `json:"at"`
}

func chatPreviewTokensID(record domain.ChatIntegrationRecord, resource string) string {
	return "chat_preview_tokens_" + chatKey(record.ConnectionID, resource)
}

// chatRememberPreviewToken records that the viewer got a preview of the
// resource ("issue:<id>", "project:<id>", …) under the token.
func (s *server) chatRememberPreviewToken(ctx context.Context, record domain.ChatIntegrationRecord, resource, token, openID string) {
	if len(token) > 512 || len(openID) > 191 {
		return
	}
	id := chatPreviewTokensID(record, resource)
	now := time.Now().UTC()
	stored, err := s.store.GetChatAction(ctx, record.WorkspaceKey, record.ConnectionID, id)
	tokens := chatPreviewTokens{Kind: chatPreviewRefreshKind, Resource: resource}
	if err == nil {
		_ = json.Unmarshal(stored.Payload, &tokens)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return
	}
	kept := []chatPreviewViewed{}
	for _, viewed := range tokens.Tokens {
		if viewed.Token != token && now.Sub(viewed.At) < 7*24*time.Hour {
			kept = append(kept, viewed)
		}
	}
	kept = append(kept, chatPreviewViewed{Token: token, OpenID: openID, At: now})
	if len(kept) > chatPreviewTokenLimit {
		kept = kept[len(kept)-chatPreviewTokenLimit:]
	}
	tokens.Tokens = kept
	payload, _ := json.Marshal(tokens)
	if err == nil {
		stored.Payload = payload
		_ = s.store.UpdateChatAction(ctx, stored)
		return
	}
	_ = s.store.PutChatAction(ctx, domain.ChatAction{ID: id, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Payload: payload, ExpiresAt: now.Add(7 * 24 * time.Hour)})
}

// chatPreviewResource is the previewed item a committed event changes.
func chatPreviewResource(event domain.DomainEvent) string {
	if event.AggregateID == "" {
		return ""
	}
	switch {
	case event.Type == "issue.updated" || event.Type == "issue.deleted" || event.Type == "issue.archived":
		return "issue:" + event.AggregateID
	case event.Type == "project.updated" || strings.HasPrefix(event.Type, "project.update_") && !strings.HasPrefix(event.Type, "project.update_comment") && event.Type != "project.update_reaction_toggled":
		return "project:" + event.AggregateID
	case event.Type == "initiative.updated":
		return "initiative:" + event.AggregateID
	case event.Type == "document.updated" || event.Type == "document.deleted":
		return "document:" + event.AggregateID
	}
	return ""
}

// queueChatPreviewRefresh queues a refresh of the previews of what the event
// changed, on every connection that described it.
func (s *server) queueChatPreviewRefresh(ctx context.Context, workspace string, event domain.DomainEvent) error {
	resource := chatPreviewResource(event)
	if resource == "" {
		return nil
	}
	for _, provider := range []string{"feishu", "lark"} {
		record, err := s.store.ChatIntegration(ctx, workspace, provider)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if !record.Connected {
			continue
		}
		if _, err := s.store.GetChatAction(ctx, workspace, record.ConnectionID, chatPreviewTokensID(record, resource)); errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			return err
		}
		key := chatKey(record.ConnectionID, "preview", event.ID)
		raw, _ := json.Marshal(chatNotificationJob{Kind: chatPreviewRefreshKind, PreviewResource: resource})
		if _, err := s.store.EnqueueChatJob(ctx, domain.ChatJob{ID: "chat_job_" + key, WorkspaceKey: workspace, ConnectionID: record.ConnectionID, Kind: "notification", DedupKey: key, Payload: raw, RunAt: time.Now().UTC()}); err != nil {
			return err
		}
	}
	return nil
}

// chatRefreshLinkPreviews asks the platform to request the resource's
// previews again. Tokens the platform refuses are forgotten.
func (s *server) chatRefreshLinkPreviews(ctx context.Context, record domain.ChatIntegrationRecord, resource string) error {
	stored, err := s.store.GetChatAction(ctx, record.WorkspaceKey, record.ConnectionID, chatPreviewTokensID(record, resource))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var tokens chatPreviewTokens
	if json.Unmarshal(stored.Payload, &tokens) != nil {
		return nil
	}
	for start := 0; start < len(tokens.Tokens); start += chatPreviewRefreshBatch {
		batch := tokens.Tokens[start:min(start+chatPreviewRefreshBatch, len(tokens.Tokens))]
		body := map[string]any{"preview_tokens": []string{}, "open_ids": []string{}}
		for _, viewed := range batch {
			body["preview_tokens"] = append(body["preview_tokens"].([]string), viewed.Token)
			body["open_ids"] = append(body["open_ids"].([]string), viewed.OpenID)
		}
		err := s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v2/url_previews/batch_update", body, nil)
		var platform *chatPlatformError
		if errors.As(err, &platform) && !platform.Retryable {
			// Expired tokens, or the app lacks the permission: nothing to refresh.
			_, _ = s.store.ConsumeChatAction(ctx, record.WorkspaceKey, record.ConnectionID, stored.ID)
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}
