package store

import (
	"context"
	"errors"
	"slices"

	"flow/api/internal/domain"
)

// DocumentRecordDirectory owns its policy and summaries. The immutable source
// records live only until the legacy response producer finishes; a slow socket
// is served from P15's detached response spool.
type DocumentRecordDirectory struct {
	Data      domain.Bootstrap
	documents map[string]domain.Document
	favorites map[string]bool
}

func (d DocumentRecordDirectory) Load(id string) (domain.Document, bool) {
	favorite, visible := d.favorites[id]
	if !visible {
		return domain.Document{}, false
	}
	document, ok := d.documents[id]
	if !ok {
		return domain.Document{}, false
	}
	owned := cloneBootstrap(domain.Bootstrap{Documents: []domain.Document{document}}).Documents[0]
	owned.Favorite = favorite
	return owned, true
}

func (s *SQLiteStore) DocumentDirectoryMetadata(ctx context.Context, workspace, userID string, key *domain.APIKey, fields []string) (DocumentRecordDirectory, error) {
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	stamp, err := readMetadataStamp(ctx, s.db, workspace)
	if err != nil {
		return DocumentRecordDirectory{}, err
	}
	scope := MetadataRecordScope{Fields: append(slices.Clone(fields), "documents")}
	if err := validateMetadataRecordScope(scope); err != nil {
		return DocumentRecordDirectory{}, err
	}
	s.mu.RLock()
	source, exists := s.workspaces[workspace]
	known, exact := s.copyRevisionLocked(workspace)
	if !exists || !exact || known.workspaceID != stamp.workspaceID || known.revision != stamp.revision {
		s.mu.RUnlock()
		return DocumentRecordDirectory{}, ErrResourceAccessChanged
	}
	result := DocumentRecordDirectory{documents: make(map[string]domain.Document, len(source.Documents)), favorites: map[string]bool{}}
	headers := make([]domain.Document, len(source.Documents))
	for i, document := range source.Documents {
		if _, duplicate := result.documents[document.ID]; duplicate {
			s.mu.RUnlock()
			return DocumentRecordDirectory{}, ErrResourceAccessChanged
		}
		result.documents[document.ID] = document
		document.Content, document.ContentState = "", ""
		document.ContentData, document.Revisions, document.Attachments = nil, nil, nil
		headers[i] = document
	}
	source.Documents = headers
	result.Data = metadataRecordSnapshot(source, scope, metadataRecordFields(scope))
	s.mu.RUnlock()
	if userID != "" {
		result.Data, err = s.projectDocumentRecordMetadata(ctx, workspace, userID, key, result.Data)
		if err != nil {
			return DocumentRecordDirectory{}, err
		}
	} else {
		result.Data.ViewerRole = "admin"
	}
	current, err := readMetadataStamp(ctx, s.db, workspace)
	if err != nil {
		return DocumentRecordDirectory{}, err
	}
	if current != stamp {
		return DocumentRecordDirectory{}, errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
	}
	for _, document := range result.Data.Documents {
		result.favorites[document.ID] = document.Favorite
	}
	return result, nil
}
