package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type chatAuthenticationContextKey struct{}
type chatCommentContextKey struct{}

// This context is set only after a verified platform identity has been resolved
// to its current Flow binding. It carries the Flow authentication established
// at consent; it never supplies a role or skips resource authorization.
func withChatIdentityAuthentication(ctx context.Context, identity domain.ChatIdentity) context.Context {
	return context.WithValue(ctx, chatAuthenticationContextKey{}, identity)
}

func chatIdentityFromRequest(r *http.Request) (domain.ChatIdentity, bool) {
	identity, ok := r.Context().Value(chatAuthenticationContextKey{}).(domain.ChatIdentity)
	if !ok || identity.UserID == "" || identity.UserID != authUser(r).ID || identity.AuthProvider == "" {
		return domain.ChatIdentity{}, false
	}
	return identity, true
}

func chatIdentityAuthentication(r *http.Request) (store.SessionAuthentication, bool) {
	identity, ok := chatIdentityFromRequest(r)
	if !ok {
		return store.SessionAuthentication{}, false
	}
	return store.SessionAuthentication{Provider: identity.AuthProvider, Issuer: identity.AuthIssuer, MFAVerifiedAt: identity.MFAVerifiedAt}, true
}

// workspaceSessionDuration is the workspace's maximum session age. A chat
// binding is held to it like a browser session, measured from consent.
func workspaceSessionDuration(settings domain.WorkspaceSettings) time.Duration {
	days := settings.SessionDurationDays
	if days < 1 {
		days = 30
	}
	return time.Duration(days) * 24 * time.Hour
}

func chatIdentityWithinSession(identity domain.ChatIdentity, duration time.Duration, now time.Time) bool {
	return !identity.AuthenticatedAt.IsZero() && now.Before(identity.AuthenticatedAt.Add(duration))
}

const chatReconnectMessage = "Your linked chat account has expired. Reconnect it in Settings > Connected accounts."

type chatMutationError struct {
	Status  int
	Message string
}

func (e *chatMutationError) Error() string { return e.Message }

// chatOperationError is an internal chat operation that cannot succeed by
// retrying it: its response exceeded the limit, or could not be decoded. The
// job fails at once and the integration records it (chatJobFinal).
type chatOperationError struct {
	Path   string
	Reason string // response_too_large or response_invalid
}

func (e *chatOperationError) Error() string {
	return "chat operation " + e.Path + ": " + e.Reason
}

// chatOperationResponseLimit caps an internal chat operation's response.
var chatOperationResponseLimit = 4 << 20

type chatMutationResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
	// overflow is the size the response reached when it exceeded the limit;
	// the body is then incomplete and the operation fails.
	overflow int
}

func (w *chatMutationResponse) Header() http.Header { return w.header }
func (w *chatMutationResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *chatMutationResponse) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if w.overflow > 0 || w.body.Len()+len(data) > chatOperationResponseLimit {
		w.overflow = max(w.overflow, w.body.Len()) + len(data)
		return 0, errors.New("chat operation response exceeds limit")
	}
	return w.body.Write(data)
}

// chatAuthorizedMutation uses the same bounded handlers as the web app. The
// allowlist is intentionally small: a platform action cannot dispatch an
// arbitrary internal route or mint a credential for the bound user.
func (s *server) chatAuthorizedMutation(ctx context.Context, workspace, userID, method, path string, input, result any) error {
	if userID == "" || workspace == "" {
		return &chatMutationError{Status: http.StatusForbidden, Message: "Bind your Flow account before performing this action"}
	}
	user, err := s.store.UserByID(ctx, userID)
	if err != nil || !user.Active {
		return &chatMutationError{Status: http.StatusForbidden, Message: "The linked Flow account is unavailable"}
	}
	ctx = context.WithValue(ctx, authUserContextKey{}, user)
	ctx = context.WithValue(ctx, workspaceKeyContextKey{}, workspace)
	ctx = store.ContextWithActor(ctx, user)
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	r, err := http.NewRequestWithContext(ctx, method, path, body)
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Workspace-Key", workspace)
	if _, ok := chatIdentityAuthentication(r); !ok && !s.authDisabled {
		return &chatMutationError{Status: http.StatusForbidden, Message: chatReconnectMessage}
	}
	var handler http.HandlerFunc
	parts := strings.Split(strings.Trim(path, "/"), "/")
	inProcess := false
	switch {
	case path == "/api/issue-records/bootstrap" && method == http.MethodGet:
		// The workspace metadata, read in process: the same data and
		// authorization as the route, without encoding many MiB of JSON for
		// a check or a form.
		metadata, ok := result.(*domain.Bootstrap)
		if result != nil && !ok {
			return errors.New("unsupported chat operation")
		}
		if metadata == nil {
			// Only the authorization is wanted.
			metadata = &domain.Bootstrap{}
		}
		inProcess = true
		handler = func(w http.ResponseWriter, r *http.Request) {
			if data, ok := s.issueRecordsBootstrapData(w, r); ok {
				*metadata = data
				w.WriteHeader(http.StatusOK)
			}
		}
	case path == "/api/issue-records" && method == http.MethodPost:
		handler = s.createIssueRecord
	case len(parts) == 3 && parts[0] == "api" && parts[1] == "issue-records" && parts[2] != "":
		r.SetPathValue("id", parts[2])
		switch method {
		case http.MethodGet:
			handler = s.getIssueRecord
		case http.MethodPatch:
			handler = s.updateIssueRecord
		}
	case len(parts) == 4 && parts[0] == "api" && parts[1] == "issue-records" && parts[2] != "" && parts[3] == "comments" && method == http.MethodPost:
		r = r.WithContext(context.WithValue(r.Context(), chatCommentContextKey{}, true))
		r.SetPathValue("id", parts[2])
		handler = s.issueRecordAlias(s.createComment)
	}
	if handler == nil || r.URL.RawQuery != "" || r.URL.Fragment != "" {
		return errors.New("unsupported chat operation")
	}
	w := &chatMutationResponse{header: make(http.Header)}
	if s.authorizeWorkspaceRequest(w, r, user) {
		if key := store.ChatMutationKey(ctx); key != "" && method == http.MethodPatch {
			issueID, applied, receiptErr := s.store.ChatMutationReceipt(ctx, workspace, userID, key)
			if receiptErr != nil {
				return receiptErr
			}
			if applied {
				if issueID != r.PathValue("id") {
					return errors.New("chat operation receipt does not match this issue")
				}
				r.Method = http.MethodGet
				handler = s.getIssueRecord
			}
		}
		handler(w, r)
	}
	if w.status == 0 {
		return &chatMutationError{Status: http.StatusForbidden, Message: "This action is not allowed by your workspace"}
	}
	if w.overflow > 0 {
		// Never decoded: the body is cut off at the limit.
		slog.WarnContext(ctx, "chat operation response exceeds limit", "method", method, "endpoint", chatOperationEndpoint(path), "status", w.status, "size_bytes_at_least", w.overflow, "limit_bytes", chatOperationResponseLimit)
		return &chatOperationError{Path: chatOperationEndpoint(path), Reason: "response_too_large"}
	}
	if w.status < http.StatusOK || w.status >= http.StatusMultipleChoices {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(w.body.Bytes(), &payload)
		if payload.Error == "" {
			payload.Error = fmt.Sprintf("Flow could not complete this action (HTTP %d)", w.status)
		}
		return &chatMutationError{Status: w.status, Message: payload.Error}
	}
	if result != nil && !inProcess {
		if err := json.Unmarshal(w.body.Bytes(), result); err != nil {
			slog.WarnContext(ctx, "chat operation response invalid", "method", method, "endpoint", chatOperationEndpoint(path), "status", w.status, "size_bytes", w.body.Len())
			return &chatOperationError{Path: chatOperationEndpoint(path), Reason: "response_invalid"}
		}
	}
	return nil
}

// chatOperationEndpoint names an internal chat operation's route for the
// logs, without the resource IDs.
func chatOperationEndpoint(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "issue-records" && parts[2] != "bootstrap" {
		parts[2] = "{id}"
	}
	return "/" + strings.Join(parts, "/")
}
