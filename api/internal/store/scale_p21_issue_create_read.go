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
		if err := owner.withHeader(ctx, record, []string{"ID", "TeamID", "VisibilityTeamID"}, func(value reflect.Value) error {
			template = value.Interface().(domain.IssueTemplate)
			return nil
		}); err != nil {
			return err
		}
		// The projection's rule (FilterTeamScopedCollections): every team the
		// template names is a visible team row within the key's teams.
		visible := func(teamID string) bool {
			return (scope == nil || scope.Teams[teamID]) && (credential == nil || credential(teamID))
		}
		if template.ID != id || !IssueTemplateTeamsVisible(template, visible) {
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
		if !IssueTemplateTeamsVisible(template, visible) {
			// A sub-issue names a hidden team: the projection drops it.
			return nil
		}
		read.Template = &template
		return ErrMetadataIterationDone
	})
	if err != nil {
		return nil, err
	}
	return read, nil
}
