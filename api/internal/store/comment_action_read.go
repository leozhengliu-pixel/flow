package store

import (
	"context"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

// CommentActionRead owns selected inert proposals through response encoding.
// It does not authorize, validate, or filter their status or target user.
type CommentActionRead struct {
	Actions    []domain.CommentAction
	Validation domain.Bootstrap
	owner      *ResourceRead
	validation *ResourceRead
	once       sync.Once
}

func (r *CommentActionRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Actions = nil
			r.Validation = domain.Bootstrap{}
			if r.validation != nil {
				r.validation.Close()
				r.validation = nil
			}
			if r.owner != nil {
				r.owner.Close()
				r.owner = nil
			}
		})
	}
}

func (r *CommentActionRead) TransferOutput(ctx context.Context) error {
	if r == nil || !registerMetadataOutputClose(ctx, r.Close) {
		r.Close()
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	return nil
}

// LoadValidationHeaders retains raw validation directories from the same
// installed generation as Actions. It preserves row order and map keys, and
// deliberately supplies no FormerMembers lookup or viewer projection.
func (r *CommentActionRead) LoadValidationHeaders(ctx context.Context) (err error) {
	if r == nil || r.owner == nil {
		return errMetadataLogicalClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.validation != nil {
		return nil
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	validation := &ResourceRead{store: r.owner.store}
	defer func() {
		if err != nil {
			if canceled := ctx.Err(); canceled != nil {
				err = canceled
			}
			validation.Close()
		}
	}()
	if err = r.owner.source.Retain(); err != nil {
		return err
	}
	validation.source = r.owner.source
	if err = validation.reserve(ctx, 128<<10); err != nil {
		return err
	}
	directory := &DocumentDirectoryRead{owner: validation}
	headers := func(kind string, names ...string) error {
		index, _, _ := metadataLogicalField(kind)
		node := validation.source.fields[index]
		absent, err := node.isNil(ctx)
		if err != nil {
			return err
		}
		target := reflect.ValueOf(&validation.Data).Elem().Field(index)
		if !absent {
			target.Set(reflect.MakeSlice(target.Type(), 0, 0))
		}
		return validation.nativeArray(ctx, node, func(_ int, row metadataLogicalNode) error {
			value, err := directory.header(ctx, row, names)
			if err == nil {
				target.Set(reflect.Append(target, value))
			}
			return err
		})
	}
	if err = headers("states", "ID", "TeamID"); err != nil {
		return err
	}
	if err = headers("users", "ID", "Active", "App"); err != nil {
		return err
	}
	if err = headers("labels", "ID", "Scope", "ArchivedAt"); err != nil {
		return err
	}
	index, _, _ := metadataLogicalField("teamSettings")
	node := validation.source.fields[index]
	absent, err := node.isNil(ctx)
	if err != nil {
		return err
	}
	if !absent {
		validation.Data.TeamSettings = map[string]domain.TeamSettings{}
	}
	err = node.MapEntries(ctx, func(key, row metadataLogicalNode) error {
		if key.cost > (metadataWorkBytes-256)/8 {
			return ErrMetadataWorkBudget
		}
		cost := key.cost*8 + 256
		if err := validation.reserve(ctx, cost); err != nil {
			return err
		}
		name, err := key.Value(ctx, int(cost))
		if err != nil {
			return err
		}
		value, err := directory.header(ctx, row, []string{"ParentTeamID", "InheritWorkflowStatuses"})
		if err == nil {
			validation.Data.TeamSettings[name.String()] = value.Interface().(domain.TeamSettings)
		}
		return err
	})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	r.Validation, r.validation = validation.Data, validation
	return nil
}

func (r *InstalledMetadataRead) commentActionOwner(ctx context.Context) (_ *ResourceRead, _ context.Context, err error) {
	if r == nil {
		return nil, nil, errMetadataLogicalClosed
	}
	ctx, err = r.Context(ctx)
	if err != nil {
		return nil, nil, err
	}
	owner := &ResourceRead{store: r.store}
	defer func() {
		if err != nil {
			owner.Close()
		}
	}()
	owner.source, err = r.retainSource(ctx, r.workspace)
	if err != nil {
		return nil, nil, err
	}
	if err = owner.reserve(ctx, 128<<10); err != nil {
		return nil, nil, err
	}
	return owner, ctx, nil
}

// ReadCommentActions selects every exact IssueID match in source order,
// including duplicates and all statuses. No match returns a non-nil empty slice.
// The result remains valid after its InstalledMetadataRead is closed.
func (r *InstalledMetadataRead) ReadCommentActions(ctx context.Context, issueID string) (_ *CommentActionRead, err error) {
	owner, ctx, err := r.commentActionOwner(ctx)
	if err != nil {
		return nil, err
	}
	read := &CommentActionRead{Actions: []domain.CommentAction{}, owner: owner}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	err = owner.visitNative(ctx, "commentActions", func(_ int, node metadataLogicalNode) error {
		return owner.withHeader(ctx, node, []string{"IssueID"}, func(header reflect.Value) error {
			if header.Interface().(domain.CommentAction).IssueID != issueID {
				return nil
			}
			value, err := owner.node(ctx, node, true)
			if err == nil {
				read.Actions = append(read.Actions, value.Interface().(domain.CommentAction))
			}
			return err
		})
	})
	if canceled := ctx.Err(); canceled != nil {
		err = canceled
	}
	if err != nil {
		return nil, err
	}
	return read, nil
}

// CommentActionExists tests CommentID globally, regardless of issue, status,
// target user or proposal arguments. It decodes only CommentID headers.
func (r *InstalledMetadataRead) CommentActionExists(ctx context.Context, commentID string) (bool, error) {
	owner, ctx, err := r.commentActionOwner(ctx)
	if err != nil {
		return false, err
	}
	defer owner.Close()
	found := false
	err = owner.visitNative(ctx, "commentActions", func(_ int, node metadataLogicalNode) error {
		return owner.withHeader(ctx, node, []string{"CommentID"}, func(header reflect.Value) error {
			if header.Interface().(domain.CommentAction).CommentID == commentID {
				found = true
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
	return found, nil
}
