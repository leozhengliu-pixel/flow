package store

import (
	"context"
	"database/sql"
	"sync"

	"flow/api/internal/domain"
)

// AttachmentVisibilityRead owns one installed generation of a workspace for
// the upload visibility check. Raw is the unprojected header graph the
// original check read through WorkspaceMetadata (loops and loop attachments);
// Project applies the literal PagedWorkspaceMetadata projection to that same
// graph. Bodies stay opaque tokens: callers read attachment headers only.
type AttachmentVisibilityRead struct {
	Raw       domain.Bootstrap
	body      *bootstrapBodyProjection
	owner     *ResourceRead
	workspace string
	once      sync.Once
}

func (r *AttachmentVisibilityRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Raw = domain.Bootstrap{}
			if r.body != nil {
				r.body.Close()
				r.body = nil
			}
			if r.owner != nil {
				r.owner.Close()
				r.owner = nil
			}
		})
	}
}

// BeginAttachmentVisibilityRead projects every attachment-bearing collection
// of the installed workspace with its bodies left opaque. The five external
// collections, provider jobs, comment actions, loop runs and loop versions
// carry no upload visibility and are not materialized.
func (s *SQLiteStore) BeginAttachmentVisibilityRead(ctx context.Context, workspace string) (_ *AttachmentVisibilityRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	read := &AttachmentVisibilityRead{owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err := read.owner.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	installed, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, sql.ErrNoRows
	}
	defer installed.Close()
	read.workspace = installed.workspace
	read.owner.source, err = installed.retainSource(ctx, read.workspace)
	if err != nil {
		return nil, err
	}
	read.body, err = s.beginBootstrapBodyProjectionWithOptions(ctx, read.owner.source, bootstrapBodyProjectionOptions{
		OmittedRootFields: []string{
			"issues", "comments", "activities", "notifications", "notificationDeliveries",
			"providerJobs", "commentActions", "loopRuns", "loopVersions",
		},
		StripUpdateSnapshots: true,
	})
	if err != nil {
		return nil, err
	}
	read.Raw = read.body.Data
	read.Raw.Issues = []domain.Issue{}
	read.Raw.Comments = map[string][]domain.Comment{}
	read.Raw.Activities = map[string][]domain.ActivityEvent{}
	read.Raw.Notifications = []domain.Notification{}
	read.Raw.NotificationDeliveries = []domain.NotificationDelivery{}
	return read, ctx.Err()
}

// Project returns PagedWorkspaceMetadata(workspace, userID) of this read's
// generation, under the same SQL snapshot, policy memo and trailing authority
// check as the paged bootstrap read. The result shares this read's lifetime.
func (r *AttachmentVisibilityRead) Project(ctx context.Context, userID string) (domain.Bootstrap, error) {
	if r == nil || r.body == nil || r.owner == nil {
		return domain.Bootstrap{}, ErrBoundedMetadataCoverage
	}
	if userID == "" {
		return domain.Bootstrap{}, ErrAuthForbidden
	}
	s := r.owner.store
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return domain.Bootstrap{}, err
	}
	defer tx.Rollback()
	snapshot := context.WithValue(r.owner.metadataOutputContext(ctx), workspaceReadKey{}, workspaceRead{
		database: s.db, workspace: r.workspace, reader: tx, metadata: r.Raw,
	})
	memoContext, _ := withResourcePolicyMemo(snapshot, r.owner)
	document := &DocumentRead{Data: r.Raw, owner: r.owner}
	if err := document.documentProjectionWork(memoContext, r.Raw.Workspace.ID, userID); err != nil {
		return domain.Bootstrap{}, err
	}
	access, err := s.ResolveAccess(memoContext, r.workspace, userID, nil)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	data, err := s.PagedWorkspaceMetadata(memoContext, r.workspace, userID)
	if outputErr := metadataOutputError(snapshot); outputErr != nil {
		return domain.Bootstrap{}, outputErr
	}
	if err != nil {
		return domain.Bootstrap{}, err
	}
	if err := tx.Rollback(); err != nil {
		return domain.Bootstrap{}, err
	}
	if err := s.ValidateAccess(ctx, access); err != nil {
		return domain.Bootstrap{}, err
	}
	return data, ctx.Err()
}
