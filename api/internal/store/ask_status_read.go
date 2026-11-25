package store

import (
	"context"
	"errors"
	"reflect"

	"flow/api/internal/domain"
)

// AskStatusRead owns the first installed Ask's status and final rejected
// approval note. It is trusted rendering input, not authorization.
type AskStatusRead struct {
	AskFound bool
	Status   string
	Note     string
	source   *InstalledMetadataRead
	owner    *ResourceRead
}

func (r *AskStatusRead) Close() {
	if r != nil {
		r.AskFound, r.Status, r.Note = false, "", ""
		if r.source != nil {
			r.source.Close()
			r.source = nil
		}
		if r.owner != nil {
			r.owner.Close()
			r.owner = nil
		}
	}
}

// BeginAskStatusRead preserves exact first-ID matching, including empty IDs,
// and the containing installed Context generation. Approval decisions are
// streamed; only the last rejected approval's note is decoded.
func (s *SQLiteStore) BeginAskStatusRead(ctx context.Context, workspace, askID string) (_ *AskStatusRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	source, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	read := &AskStatusRead{source: source, owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err = read.owner.reserve(ctx, 64<<10); err != nil {
		return nil, true, err
	}
	err = source.source.VisitFields(ctx, func(name string, collection metadataLogicalNode) error {
		if name != "asks" {
			return nil
		}
		err := collection.Children(ctx, func(_ reflect.Value, ask metadataLogicalNode) error {
			matched := false
			if err := ask.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
				if key.String() == "ID" {
					var err error
					matched, err = value.StringEqual(ctx, askID)
					return err
				}
				return nil
			}); err != nil || !matched {
				return err
			}
			value, err := read.owner.projectNode(ctx, ask, "Status")
			if err != nil {
				return err
			}
			read.AskFound, read.Status = true, value.Interface().(domain.Ask).Status
			if read.Status == "rejected" {
				var last metadataLogicalNode
				if err := ask.Children(ctx, func(key reflect.Value, approvals metadataLogicalNode) error {
					if key.String() != "Approvals" {
						return nil
					}
					return approvals.Children(ctx, func(_ reflect.Value, approval metadataLogicalNode) error {
						rejected := false
						if err := approval.Children(ctx, func(key reflect.Value, decision metadataLogicalNode) error {
							if key.String() == "Decision" {
								var err error
								rejected, err = decision.StringEqual(ctx, "rejected")
								return err
							}
							return nil
						}); err != nil {
							return err
						}
						if rejected {
							last = approval
						}
						return nil
					})
				}); err != nil {
					return err
				}
				if last.typ != nil {
					value, err := read.owner.projectNode(ctx, last, "Note")
					if err != nil {
						return err
					}
					read.Note = value.Interface().(domain.AskApproval).Note
				}
			}
			return ErrMetadataIterationDone
		})
		if errors.Is(err, ErrMetadataIterationDone) {
			err = nil
		}
		return err
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, true, err
	}
	return read, true, nil
}
