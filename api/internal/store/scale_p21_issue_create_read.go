package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// IssueCreateTemplateRead owns the template headers the create route validates.
// Body, form fields and child descriptions are never decoded by this read.
type IssueCreateTemplateRead struct {
	Template *domain.IssueTemplate
	owner    *ResourceRead
}

func (r *IssueCreateTemplateRead) Close() {
	if r != nil {
		r.Template = nil
		r.owner.Close()
	}
}

func (r *InstalledMetadataRead) ReadIssueCreateTemplate(ctx context.Context, id string, scope *ViewerProjectionScope, credential func(string) bool) (_ *IssueCreateTemplateRead, err error) {
	owner, ctx, err := r.commentActionOwner(ctx)
	if err != nil {
		return nil, err
	}
	read := &IssueCreateTemplateRead{owner: owner}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	err = owner.visitNative(ctx, "issueTemplates", func(_ int, record metadataLogicalNode) error {
		var template domain.IssueTemplate
		if err := owner.withHeader(ctx, record, []string{"ID", "TeamID"}, func(value reflect.Value) error {
			template = value.Interface().(domain.IssueTemplate)
			return nil
		}); err != nil {
			return err
		}
		if template.ID != id || template.TeamID != "" && (scope != nil && !scope.Teams[template.TeamID] || credential != nil && !credential(template.TeamID)) {
			return nil
		}
		children, err := issueHotField(ctx, record, "SubIssues")
		if err != nil {
			return err
		}
		if err := children.Children(ctx, func(_ reflect.Value, child metadataLogicalNode) error {
			value, err := owner.projectNode(ctx, child, "TeamID")
			if err != nil {
				return err
			}
			template.SubIssues = append(template.SubIssues, value.Interface().(domain.TemplateSubIssue))
			return nil
		}); err != nil {
			return err
		}
		read.Template = &template
		return ErrMetadataIterationDone
	})
	if err != nil {
		return nil, err
	}
	return read, nil
}
