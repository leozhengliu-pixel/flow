package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// AuthorizationProjectionScope borrows the literal viewer scope already
// computed by this read, before the API-key filter. The maps remain immutable
// and are valid only until the read closes.
func (r *IssueWriteAuthorizationRead) AuthorizationProjectionScope(ctx context.Context) (ViewerProjectionScope, error) {
	if r == nil {
		return ViewerProjectionScope{}, ErrBoundedMetadataCoverage
	}
	if _, err := r.Context(ctx); err != nil {
		return ViewerProjectionScope{}, err
	}
	return r.projectionScope, nil
}

// IssueAuthorizationReleaseVisible selects only the release and pipeline
// headers read by resourceAllowed's release existence test. Pipeline filtering
// is sequential: first the viewer's content teams, then the key's team scope.
func (r *InstalledMetadataRead) IssueAuthorizationReleaseVisible(ctx context.Context, scope *ViewerProjectionScope, id string, credential func(string) bool) (bool, error) {
	owner, ctx, err := r.commentActionOwner(ctx)
	if err != nil {
		return false, err
	}
	defer owner.Close()
	visible := false
	err = owner.visitNative(ctx, "releases", func(_ int, node metadataLogicalNode) error {
		return owner.withHeader(ctx, node, []string{"ID", "PipelineID"}, func(header reflect.Value) error {
			release := header.Interface().(domain.Release)
			if release.ID != id {
				return nil
			}
			if scope == nil || release.PipelineID == "" {
				visible = true
				return ErrMetadataIterationDone
			}
			err := owner.visitNative(ctx, "releasePipelines", func(_ int, node metadataLogicalNode) error {
				return owner.withHeader(ctx, node, []string{"ID", "TeamIDs"}, func(header reflect.Value) error {
					pipeline := header.Interface().(domain.ReleasePipeline)
					if pipeline.ID != release.PipelineID {
						return nil
					}
					if len(pipeline.TeamIDs) == 0 {
						visible = true
						return ErrMetadataIterationDone
					}
					for _, teamID := range pipeline.TeamIDs {
						if scope.Content[teamID] && (credential == nil || credential(teamID)) {
							visible = true
							return ErrMetadataIterationDone
						}
					}
					return nil
				})
			})
			if err == nil && visible {
				return ErrMetadataIterationDone
			}
			return err
		})
	})
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	return visible, err
}
