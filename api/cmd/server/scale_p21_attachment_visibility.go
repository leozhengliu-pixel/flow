package main

import (
	"context"
	"errors"
	"slices"
	"sync"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// boundedAttachmentVisible is attachmentVisible over owned reads. Per
// membership it keeps the original order and outcome: indexed issue
// attachments under the viewer's issue access, then loop attachments of the
// unprojected workspace, then the other families of the viewer's paged
// projection. A failed step skips the workspace, as the original accessor
// errors did; a work-budget refusal does the same and is recorded for the
// caller, which may answer it as retryable.
type uploadVisibilityRefusalKey struct{}

type uploadVisibilityRefusal struct {
	mu  sync.Mutex
	err error
}

// withUploadVisibilityRefusal lets serveUpload tell a bounded work refusal
// (retry later) from "not visible". Only the bounded check records one, so
// the original check never changes the answer.
func withUploadVisibilityRefusal(ctx context.Context) (context.Context, func() error) {
	holder := &uploadVisibilityRefusal{}
	return context.WithValue(ctx, uploadVisibilityRefusalKey{}, holder), func() error {
		holder.mu.Lock()
		defer holder.mu.Unlock()
		return holder.err
	}
}

func recordUploadVisibilityRefusal(ctx context.Context, err error) {
	holder, _ := ctx.Value(uploadVisibilityRefusalKey{}).(*uploadVisibilityRefusal)
	if holder == nil || !errors.Is(err, store.ErrMetadataWorkBudget) && !errors.Is(err, store.ErrMetadataPageBudget) {
		return
	}
	holder.mu.Lock()
	defer holder.mu.Unlock()
	if holder.err == nil {
		holder.err = err
	}
}

func (s *server) boundedAttachmentVisible(ctx context.Context, account domain.AccountBootstrap, userID, url string) bool {
	if agentUploadVisible(userID, url) {
		return true
	}
	for _, membership := range account.Workspaces {
		data, access, err := s.store.IssueQueryAccess(ctx, membership.Workspace.URLKey, userID)
		if err != nil {
			recordUploadVisibilityRefusal(ctx, err)
			continue
		}
		visible, err := s.store.IssueAttachmentVisible(ctx, store.IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access}, url)
		if err == nil && visible {
			return true
		}
		recordUploadVisibilityRefusal(ctx, err)
		if s.boundedWorkspaceAttachmentVisible(ctx, data.Workspace.URLKey, userID, url) {
			return true
		}
	}
	return false
}

func (s *server) boundedWorkspaceAttachmentVisible(ctx context.Context, workspace, userID, url string) bool {
	read, err := s.store.BeginAttachmentVisibilityRead(ctx, workspace)
	if err != nil {
		recordUploadVisibilityRefusal(ctx, err)
		return false
	}
	defer read.Close()
	if loopAttachmentVisible(read.Raw, userID, url) {
		return true
	}
	// The projection only removes records and fields; an upload that no
	// unprojected family names cannot become visible through it.
	if !projectedAttachmentFamiliesName(read.Raw, url) {
		return false
	}
	data, err := read.Project(ctx, userID)
	if err != nil {
		recordUploadVisibilityRefusal(ctx, err)
		return false
	}
	return s.projectedAttachmentVisible(data, userID, url)
}

func attachmentsName(attachments []domain.Attachment, url string) bool {
	return slices.ContainsFunc(attachments, func(attachment domain.Attachment) bool { return attachment.URL == url })
}

// projectedAttachmentFamiliesName is a superset of projectedAttachmentVisible
// on the unprojected graph: every family, without the viewer predicates.
func projectedAttachmentFamiliesName(data domain.Bootstrap, url string) bool {
	for _, request := range data.CustomerRequests {
		if attachmentsName(request.Attachments, url) {
			return true
		}
	}
	for _, updates := range data.ProjectUpdates {
		for _, update := range updates {
			if attachmentsName(update.Attachments, url) {
				return true
			}
		}
	}
	for _, document := range data.Documents {
		if attachmentsName(document.Attachments, url) {
			return true
		}
	}
	for _, session := range data.AgentSessions {
		if slices.ContainsFunc(messageAttachments(session.Messages), func(attachment domain.AgentMessageAttachment) bool { return attachment.URL == url }) {
			return true
		}
	}
	for _, project := range data.Projects {
		if attachmentsName(project.CommentAttachments, url) {
			return true
		}
	}
	for _, initiative := range data.Initiatives {
		if attachmentsName(initiative.CommentAttachments, url) {
			return true
		}
	}
	for _, review := range data.Reviews {
		if attachmentsName(review.CommentAttachments, url) {
			return true
		}
	}
	for _, updates := range data.InitiativeUpdates {
		for _, update := range updates {
			if attachmentsName(update.Attachments, url) {
				return true
			}
		}
	}
	return false
}

// projectedAttachmentVisible is the original check of the viewer's paged
// projection, family by family.
func (s *server) projectedAttachmentVisible(data domain.Bootstrap, userID, url string) bool {
	for _, request := range data.CustomerRequests {
		if attachmentsName(request.Attachments, url) {
			return true
		}
	}
	for _, updates := range data.ProjectUpdates {
		for _, update := range updates {
			if attachmentsName(update.Attachments, url) {
				return true
			}
		}
	}
	if documentAttachmentVisible(s, data, url) {
		return true
	}
	// Files sent with an agent chat are visible to the chat's owner only.
	for _, session := range data.AgentSessions {
		if session.UserID == userID && session.DeletedAt == nil && slices.ContainsFunc(messageAttachments(session.Messages), func(attachment domain.AgentMessageAttachment) bool { return attachment.URL == url }) {
			return true
		}
	}
	for _, project := range data.Projects {
		if attachmentsName(project.CommentAttachments, url) {
			return true
		}
	}
	for _, initiative := range data.Initiatives {
		if attachmentsName(initiative.CommentAttachments, url) {
			return true
		}
	}
	for _, review := range data.Reviews {
		if attachmentsName(review.CommentAttachments, url) {
			return true
		}
	}
	for _, updates := range data.InitiativeUpdates {
		for _, update := range updates {
			if attachmentsName(update.Attachments, url) {
				return true
			}
		}
	}
	return false
}
