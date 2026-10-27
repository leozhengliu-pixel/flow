package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type chatAuthenticationContextKey struct{}

// This context is set only after a verified platform identity has been resolved
// to its current Flow binding. It carries the Flow authentication established
// at consent; it never supplies a role or skips resource authorization.
func withChatIdentityAuthentication(ctx context.Context, identity domain.ChatIdentity) context.Context {
	return context.WithValue(ctx, chatAuthenticationContextKey{}, identity)
}

func chatIdentityAuthentication(r *http.Request) (store.SessionAuthentication, bool) {
	identity, ok := r.Context().Value(chatAuthenticationContextKey{}).(domain.ChatIdentity)
	if !ok || identity.UserID == "" || identity.UserID != authUser(r).ID || identity.AuthProvider == "" {
		return store.SessionAuthentication{}, false
	}
	return store.SessionAuthentication{Provider: identity.AuthProvider, Issuer: identity.AuthIssuer, MFAVerifiedAt: identity.MFAVerifiedAt}, true
}

type chatMutationError struct {
	Status  int
	Message string
}

func (e *chatMutationError) Error() string { return e.Message }

type chatMutationResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *chatMutationResponse) Header() http.Header { return w.header }
func (w *chatMutationResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *chatMutationResponse) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if w.body.Len()+len(data) > 4<<20 {
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
		return &chatMutationError{Status: http.StatusForbidden, Message: "Reconnect your Flow account to authorize this action"}
	}
	var handler http.HandlerFunc
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case path == "/api/issue-records/bootstrap" && method == http.MethodGet:
		handler = s.issueRecordsBootstrap
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
	if result != nil {
		return json.Unmarshal(w.body.Bytes(), result)
	}
	return nil
}
