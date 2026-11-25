package main

import (
	"context"

	"flow/api/internal/store"
)

func (s *server) issueAttachmentContext(ctx context.Context) context.Context {
	if !s.store.BoundedMetadataEnabled() {
		return ctx
	}
	return store.ContextWithIssueAttachmentMutation(ctx)
}

func (s *server) issueAttachmentUpsertContext(ctx context.Context) context.Context {
	if !s.store.BoundedMetadataEnabled() {
		return ctx
	}
	return store.ContextWithIssueAttachmentUpsertMutation(ctx)
}
