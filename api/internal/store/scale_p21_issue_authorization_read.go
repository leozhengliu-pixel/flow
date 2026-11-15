package store

import (
	"context"
	"slices"
	"sync"

	"flow/api/internal/domain"
)

// IssueWriteAuthorizationRequest selects the same child-write inputs as the
// issue-record alias. Query retains the caller's original SQL visibility.
type IssueWriteAuthorizationRequest struct {
	Query                     IssueRecordQuery
	IDs                       []string
	CommentIssueID, CommentID string
	FullPermissions           bool
	CommentActionValidation   bool
}

// IssueWriteAuthorizationRead owns authorization headers, not a mutation
// preimage. Callers must not use Data as a complete workspace projection.
type IssueWriteAuthorizationRead struct {
	Data                    domain.Bootstrap
	owner                   *ResourceRead
	installed               *InstalledMetadataRead
	query                   IssueRecordQuery
	fullPermissions         bool
	commentActionValidation bool
	once                    sync.Once
}

func (r *IssueWriteAuthorizationRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Data = domain.Bootstrap{}
			r.query = IssueRecordQuery{}
			r.owner.Close()
			r.installed.Close()
		})
	}
}

func (r *IssueWriteAuthorizationRead) Context(ctx context.Context) (context.Context, error) {
	return r.installed.Context(ctx)
}

func (r *IssueWriteAuthorizationRead) TransferOutput(ctx context.Context) error {
	if r == nil || !registerMetadataOutputClose(ctx, r.Close) {
		r.Close()
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	return nil
}

func (s *SQLiteStore) BeginIssueWriteAuthorizationRead(ctx context.Context, userID string, key *domain.APIKey, request IssueWriteAuthorizationRequest) (_ *IssueWriteAuthorizationRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	if len(request.IDs) > 1000 {
		return nil, ErrIssueQuery
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	installed, found, err := s.BeginInstalledMetadataRead(ctx, request.Query.Workspace)
	if err != nil || !found {
		if err == nil {
			err = ErrAuthForbidden
		}
		return nil, err
	}
	source, err := installed.retainSource(ctx, installed.workspace)
	if err != nil {
		installed.Close()
		return nil, err
	}
	read := &IssueWriteAuthorizationRead{owner: &ResourceRead{store: s, source: source}, installed: installed, query: request.Query, fullPermissions: request.FullPermissions, commentActionValidation: request.CommentActionValidation}
	if read.query.Workspace == "" {
		read.query.Workspace = installed.workspace
	}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err = read.owner.reserve(ctx, 128<<10); err != nil {
		return nil, err
	}
	cost := uint64(len(request.IDs))*32 + 4096
	for _, id := range request.IDs {
		if uint64(len(id)) > (metadataWorkBytes-cost)/8 {
			return nil, ErrMetadataWorkBudget
		}
		cost += uint64(len(id)) * 8
	}
	if err = read.owner.reserve(ctx, cost); err != nil {
		return nil, err
	}
	read.query.Filter = IssueFilter{Field: "id", Values: slices.Clone(request.IDs)}
	read.query.Archived, read.query.Limit, read.query.Cursor = "all", 500, ""
	read.query.GroupValue, read.query.GroupValues = nil, nil
	read.query.TeamIDs, read.query.ProjectIDs, read.query.StateIDs = nil, nil, nil
	read.query.Text = ""
	if err = read.loadPolicy(ctx, userID, key); err != nil {
		return nil, err
	}
	read.Data.Comments = map[string][]domain.Comment{}
	if err = read.loadIssueHeaders(ctx); err != nil {
		return nil, err
	}
	if request.CommentID != "" {
		if err = read.loadCommentHeader(ctx, request.CommentIssueID, request.CommentID); err != nil {
			return nil, err
		}
	}
	return read, nil
}
