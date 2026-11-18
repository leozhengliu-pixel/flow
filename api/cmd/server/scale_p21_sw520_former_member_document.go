package main

import (
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// beginFormerMemberDocumentContent is the bounded form of the literal
// standalone-document read of refuseFormerMemberCollaborationUpdate:
// socketWorkspaceMetadata followed by collaborationDocument. The reviewed
// single-header collaboration reader applies the same principal (none in
// development mode), credential projection and first-match/conflict order,
// and carries only the selected document's ContentState and ContentData.
// A document the projection hides or lacks is errNotFound, as
// collaborationDocument returns. The content is valid until Close.
func (s *server) beginFormerMemberDocumentContent(r *http.Request, workspace, documentID string) (*store.CollaborationDocumentRead, error) {
	userID := ""
	var key *domain.APIKey
	if !s.authDisabled {
		userID = authUser(r).ID
		if credential, found := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); found {
			key = &credential
		}
	}
	read, err := s.store.BeginCollaborationDocumentContentRead(r.Context(), workspace, userID, documentID, key)
	if err != nil {
		return nil, err
	}
	if !read.Found {
		read.Close()
		return nil, errNotFound
	}
	return read, nil
}
