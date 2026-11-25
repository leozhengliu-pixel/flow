package store

import (
	"context"
	"errors"
	"reflect"

	"flow/api/internal/domain"
)

type ChatEventPolicyRequest struct {
	AskID       *string
	TemplateIDs []string
}

// ChatEventTemplateHeader contains only the existing chat binding predicate's
// inputs. FormFields is the exact count, including empty field records.
type ChatEventTemplateHeader struct {
	ID, TemplateType, TeamID, VisibilityTeamID, ProjectID string
	FormFields                                            int
}

// ChatEventPolicyRead owns selected installed headers through their last use.
// It is trusted input, not authorization or a mutation preimage.
type ChatEventPolicyRead struct {
	AskFound   bool
	AskIssueID string
	Templates  []ChatEventTemplateHeader
	source     *InstalledMetadataRead
	owner      *ResourceRead
}

func (r *ChatEventPolicyRead) Close() {
	if r != nil {
		r.AskFound, r.AskIssueID, r.Templates = false, "", nil
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

// BeginChatEventPolicyRead honors a containing installed Context pin. Ask
// selection stops at the first ID match; template selection keeps every matching
// duplicate in installed order so a later disqualifying match still refuses.
func (s *SQLiteStore) BeginChatEventPolicyRead(ctx context.Context, workspace string, request ChatEventPolicyRequest) (_ *ChatEventPolicyRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	source, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	r := &ChatEventPolicyRead{source: source, owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if len(request.TemplateIDs) > metadataWorkBytes/256 {
		return nil, true, ErrMetadataWorkBudget
	}
	if err = r.owner.reserve(ctx, 64<<10+uint64(len(request.TemplateIDs))*256); err != nil {
		return nil, true, err
	}
	err = source.source.VisitFields(ctx, func(name string, collection metadataLogicalNode) error {
		if name == "asks" && request.AskID != nil {
			err := collection.Children(ctx, func(_ reflect.Value, ask metadataLogicalNode) error {
				matched := false
				if err := ask.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
					if key.String() == "ID" {
						var err error
						matched, err = node.StringEqual(ctx, *request.AskID)
						return err
					}
					return nil
				}); err != nil || !matched {
					return err
				}
				value, err := r.owner.projectNode(ctx, ask, "IssueID")
				if err != nil {
					return err
				}
				r.AskFound, r.AskIssueID = true, value.Interface().(domain.Ask).IssueID
				return ErrMetadataIterationDone
			})
			if errors.Is(err, ErrMetadataIterationDone) {
				err = nil
			}
			return err
		}
		if name != "issueTemplates" || len(request.TemplateIDs) == 0 {
			return nil
		}
		return collection.Children(ctx, func(_ reflect.Value, template metadataLogicalNode) error {
			matched := false
			if err := template.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
				if key.String() != "ID" {
					return nil
				}
				for _, id := range request.TemplateIDs {
					equal, err := node.StringEqual(ctx, id)
					if err != nil {
						return err
					}
					matched = matched || equal
					if matched {
						break
					}
				}
				return nil
			}); err != nil || !matched {
				return err
			}
			value, err := r.owner.projectNode(ctx, template, "ID", "TemplateType", "TeamID", "VisibilityTeamID", "ProjectID")
			if err != nil {
				return err
			}
			item := value.Interface().(domain.IssueTemplate)
			header := ChatEventTemplateHeader{ID: item.ID, TemplateType: item.TemplateType, TeamID: item.TeamID, VisibilityTeamID: item.VisibilityTeamID, ProjectID: item.ProjectID}
			if err := template.Children(ctx, func(key reflect.Value, fields metadataLogicalNode) error {
				if key.String() != "FormFields" {
					return nil
				}
				return fields.Children(ctx, func(_ reflect.Value, _ metadataLogicalNode) error {
					header.FormFields++
					return nil
				})
			}); err != nil {
				return err
			}
			r.Templates = append(r.Templates, header)
			return nil
		})
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, true, err
	}
	return r, true, nil
}
