package main

import (
	"context"
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type chatBootstrapHeadersKey struct{}

type chatBootstrapHeadersKind uint8

const (
	chatDMHeaders chatBootstrapHeadersKind = iota + 1
	chatWorkflowHeaders
)

// This typed contract supplies the fields consumed by DM forms, project
// cards and group prompts. The generic typed bootstrap keeps its full contract.
func (s *server) chatAuthorizedBootstrapHeaders(ctx context.Context, workspace, userID string, result *domain.Bootstrap) error {
	if s.store.BoundedMetadataEnabled() {
		ctx = context.WithValue(ctx, chatBootstrapHeadersKey{}, chatDMHeaders)
	}
	return s.chatAuthorizedMutation(ctx, workspace, userID, http.MethodGet, "/api/issue-records/bootstrap", nil, result)
}

func (s *server) chatAuthorizedWorkflowHeaders(ctx context.Context, workspace, userID string, result *domain.Bootstrap) error {
	if s.store.BoundedMetadataEnabled() {
		ctx = context.WithValue(ctx, chatBootstrapHeadersKey{}, chatWorkflowHeaders)
	}
	return s.chatAuthorizedMutation(ctx, workspace, userID, http.MethodGet, "/api/issue-records/bootstrap", nil, result)
}

func (s *server) chatBootstrapHeaders(w http.ResponseWriter, r *http.Request, result *domain.Bootstrap, kind chatBootstrapHeadersKind) error {
	if kind != chatDMHeaders && kind != chatWorkflowHeaders {
		return store.ErrBoundedMetadataCoverage
	}
	read, ok := s.beginIssueRecordsBootstrapRead(w, r)
	if !ok {
		return nil
	}
	if result == nil {
		read.Close()
		w.WriteHeader(http.StatusOK)
		return nil
	}
	var data domain.Bootstrap
	if kind == chatDMHeaders {
		data = chatBootstrapHeaderProjection(read.Data)
	} else {
		data = chatWorkflowBootstrapHeaderProjection(read.Data)
	}
	if err := read.TransferOutput(r.Context()); err != nil {
		return err
	}
	*result = data
	w.WriteHeader(http.StatusOK)
	return nil
}

// Trim the already admitted arrays in place. Every retained string and nested
// value remains owned by the bootstrap read until the containing job finishes.
func chatBootstrapHeaderProjection(data domain.Bootstrap) domain.Bootstrap {
	for index, project := range data.Projects {
		data.Projects[index] = domain.Project{
			ID: project.ID, SlugID: project.SlugID, PreviousSlugIDs: project.PreviousSlugIDs,
			Name: project.Name, Summary: project.Summary, Status: project.Status,
			Lead: project.Lead, TargetDate: project.TargetDate,
		}
	}
	return domain.Bootstrap{
		Viewer: data.Viewer, ViewerRole: data.ViewerRole,
		Teams: data.Teams, TeamSettings: data.TeamSettings, TeamMembers: data.TeamMembers,
		Users: data.Users, Projects: data.Projects,
	}
}

func chatWorkflowBootstrapHeaderProjection(data domain.Bootstrap) domain.Bootstrap {
	for index, project := range data.Projects {
		data.Projects[index] = domain.Project{
			ID: project.ID, Name: project.Name, TeamIDs: project.TeamIDs,
			ArchivedAt: project.ArchivedAt,
		}
	}
	for index, template := range data.IssueTemplates {
		data.IssueTemplates[index] = domain.IssueTemplate{
			ID: template.ID, TeamID: template.TeamID, VisibilityTeamID: template.VisibilityTeamID,
			TemplateType: template.TemplateType, Name: template.Name, Title: template.Title,
			StateID: template.StateID, AssigneeID: template.AssigneeID, ProjectID: template.ProjectID,
			Priority: template.Priority, FormFields: template.FormFields,
		}
	}
	return domain.Bootstrap{
		Workspace: data.Workspace, Viewer: data.Viewer, ViewerRole: data.ViewerRole,
		ViewerTeamIDs: data.ViewerTeamIDs,
		Teams:         data.Teams, TeamSettings: data.TeamSettings, TeamMembers: data.TeamMembers,
		States: data.States, Users: data.Users, UserSettings: data.UserSettings,
		Projects: data.Projects, IssueTemplates: data.IssueTemplates, Issues: data.Issues,
	}
}
