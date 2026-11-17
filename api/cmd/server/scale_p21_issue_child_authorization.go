package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type boundedIssueWriteAuthorizationContext struct{}

// GET patterns also serve HEAD, so a HEAD permission list takes the same
// retained read as its GET instead of the literal workspace bootstrap.
func boundedIssuePermissionReadRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	path, absolute := strings.CutPrefix(r.URL.Path, "/")
	if !absolute {
		return false
	}
	parts := strings.Split(path, "/")
	return len(parts) == 4 && parts[0] == "api" && (parts[1] == "issue-records" || parts[1] == "issues") && parts[2] != "" && parts[3] == "permissions"
}

func boundedIssueCommentActionReadRequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	path, absolute := strings.CutPrefix(r.URL.Path, "/")
	if !absolute {
		return false
	}
	parts := strings.Split(path, "/")
	return len(parts) == 4 && parts[0] == "api" && (parts[1] == "issue-records" || parts[1] == "issues") && parts[2] != "" && parts[3] == "comment-actions"
}

// Keep this aligned with the issue-record child mutation routes. Other issue
// routes retain the original authorization builder and its complete inputs.
func boundedIssueChildWriteRequest(r *http.Request) bool {
	path, absolute := strings.CutPrefix(r.URL.Path, "/")
	if !absolute {
		return false
	}
	parts := strings.Split(path, "/")
	if len(parts) < 4 || len(parts) > 6 || parts[0] != "api" || parts[1] != "issue-records" {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	switch len(parts) {
	case 4:
		if r.Method != http.MethodPost {
			return false
		}
		switch parts[3] {
		case "comments", "reactions", "relations", "attachments", "links":
			return true
		}
	case 5:
		switch parts[3] {
		case "comments":
			return r.Method == http.MethodPatch || r.Method == http.MethodDelete
		case "relations":
			return r.Method == http.MethodDelete
		case "attachments":
			return r.Method == http.MethodDelete || r.Method == http.MethodPost && parts[4] == "from-url"
		}
	case 6:
		if parts[3] != "comments" {
			return false
		}
		switch parts[5] {
		case "reactions":
			return r.Method == http.MethodPost
		case "subscription":
			return r.Method == http.MethodPut || r.Method == http.MethodDelete
		}
	}
	return false
}

func (s *server) boundedIssueWriteAuthorizationMetadata(r *http.Request) (domain.Bootstrap, bool, error) {
	permissionRead := boundedIssuePermissionReadRequest(r)
	commentActionRead := boundedIssueCommentActionReadRequest(r)
	if !s.store.BoundedMetadataEnabled() || !boundedIssueChildWriteRequest(r) && !permissionRead && !commentActionRead {
		return domain.Bootstrap{}, false, nil
	}
	metadata, query, err := s.issueRecordsQuery(r)
	if err != nil {
		return metadata, true, err
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	ids := []string{}
	if parts[2] != "batch" {
		ids = append(ids, parts[2])
		if issueIdentifierPattern.MatchString(parts[2]) {
			if id, err := s.store.AuthorizedIssueRecordID(r.Context(), query, parts[2]); err == nil {
				ids = append(ids, id)
			}
		}
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var input map[string]json.RawMessage
		if peekRequestJSON(r, &input) {
			var batchIDs []string
			if json.Unmarshal(input["issueIds"], &batchIDs) == nil {
				ids = append(ids, batchIDs...)
			}
			for _, field := range []string{"parentId", "relatedIssueId"} {
				var id string
				if json.Unmarshal(input[field], &id) == nil && id != "" {
					ids = append(ids, id)
				}
			}
		}
	}
	request := store.IssueWriteAuthorizationRequest{Query: query, IDs: ids, FullPermissions: permissionRead, CommentActionValidation: commentActionRead}
	if len(parts) > 4 && parts[3] == "comments" {
		request.CommentIssueID, request.CommentID = parts[2], parts[4]
	}
	userID := ""
	var credential *domain.APIKey
	if !s.authDisabled {
		userID = authUser(r).ID
		if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
			credential = &key
		}
	}
	read, err := s.store.BeginIssueWriteAuthorizationRead(r.Context(), userID, credential, request)
	if err != nil {
		return domain.Bootstrap{}, true, err
	}
	ctx, err := read.Context(r.Context())
	if err != nil {
		read.Close()
		return domain.Bootstrap{}, true, err
	}
	if err := read.TransferOutput(ctx); err != nil {
		return domain.Bootstrap{}, true, err
	}
	ctx = context.WithValue(ctx, boundedIssueWriteAuthorizationContext{}, read)
	*r = *r.WithContext(ctx)
	return read.Data, true, nil
}
