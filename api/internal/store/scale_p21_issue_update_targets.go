package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// CycleVisibleInProjection reports whether any occurrence of id in this
// generation survives PagedWorkspaceMetadata's cycle filter for scope (the
// cycle's team is a content team) and, with a non-nil credential, a
// selected-team credential filter applied after it (the credential allows
// that team). It decodes only the ID and TeamID of cycles.
func (r *InstalledMetadataRead) CycleVisibleInProjection(ctx context.Context, scope ViewerProjectionScope, id string, credential func(teamID string) bool) (bool, error) {
	owner, ctx, err := r.commentActionOwner(ctx)
	if err != nil {
		return false, err
	}
	defer owner.Close()
	visible := false
	err = owner.visitNative(ctx, "cycles", func(_ int, node metadataLogicalNode) error {
		return owner.withHeader(ctx, node, []string{"ID", "TeamID"}, func(header reflect.Value) error {
			cycle := header.Interface().(domain.Cycle)
			if cycle.ID == id && scope.Content[cycle.TeamID] && (credential == nil || credential(cycle.TeamID)) {
				visible = true
				return ErrMetadataIterationDone
			}
			return nil
		})
	})
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	if err != nil {
		return false, err
	}
	return visible, nil
}

// RecordIDPresent reports whether the installed projects, cycles or users
// collection holds an entry with exactly this ID, as a lookup over the
// complete unprojected collection does (a request without authorization).
// It decodes only IDs.
func (r *InstalledMetadataRead) RecordIDPresent(ctx context.Context, kind, id string) (bool, error) {
	if kind != "projects" && kind != "cycles" && kind != "users" {
		return false, ErrMetadataRecordQuery
	}
	owner, ctx, err := r.commentActionOwner(ctx)
	if err != nil {
		return false, err
	}
	defer owner.Close()
	present := false
	err = owner.visitNative(ctx, kind, func(_ int, node metadataLogicalNode) error {
		return owner.withHeader(ctx, node, []string{"ID"}, func(header reflect.Value) error {
			if header.FieldByName("ID").String() == id {
				present = true
				return ErrMetadataIterationDone
			}
			return nil
		})
	})
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	if err != nil {
		return false, err
	}
	return present, nil
}
