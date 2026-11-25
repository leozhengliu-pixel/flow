package store

import (
	"context"
	"errors"
	"slices"

	"flow/api/internal/domain"
)

// ErrLegacyIssueAuthorizationUnscoped reports that the installed workspace is
// not readable here (bounded metadata, a COLD view or a resident skeleton).
// The caller then builds the complete projection under the projection gate.
var ErrLegacyIssueAuthorizationUnscoped = errors.New("legacy issue authorization needs the complete projection")

// LegacyIssueWriteAuthorization is the viewer projection a single-issue write
// is authorized against: the workspace and its settings, the teams with their
// settings, the viewer with role, memberships and directory, the viewer's own
// settings, and only the projects and cycles the request names. It applies
// the projection of PagedWorkspaceMetadata (projectBootstrapForUser) to those
// inputs, so every record it returns equals that record in the complete
// projection. Unrelated projects, their progress histories and the other
// workspace collections are never copied. The caller adds the selected issue
// rows; the issue and comment collections start empty, as in the complete
// projection.
func (s *SQLiteStore) LegacyIssueWriteAuthorization(ctx context.Context, workspace, userID string, projectIDs, cycleIDs []string) (domain.Bootstrap, error) {
	if s.BoundedMetadataEnabled() || s.metadataResidency != nil || coldCapable(ctx) {
		return domain.Bootstrap{}, ErrLegacyIssueAuthorizationUnscoped
	}
	var source domain.Bootstrap
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db && (workspace == "" || workspace == snapshot.workspace) {
		source = legacyIssueWriteInputs(snapshot.metadata, userID, projectIDs, cycleIDs)
	} else {
		s.mu.RLock()
		if workspace == "" {
			workspace = s.lastWorkspaceKey
		}
		installed, ok := s.workspaces[workspace]
		if ok {
			// Copied while the read lock is held, as WorkspaceMetadataForRead does.
			source = legacyIssueWriteInputs(installed, userID, projectIDs, cycleIDs)
		}
		s.mu.RUnlock()
		if !ok {
			return domain.Bootstrap{}, ErrAuthForbidden
		}
	}
	data, ok, err := s.projectBootstrapForUser(ctx, source, userID)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	if !ok {
		return domain.Bootstrap{}, ErrAuthForbidden
	}
	data.Issues = []domain.Issue{}
	data.Activities = map[string][]domain.ActivityEvent{}
	data.Comments = map[string][]domain.Comment{}
	data.Notifications = []domain.Notification{}
	data.NotificationDeliveries = []domain.NotificationDelivery{}
	return data, nil
}

// legacyIssueWriteInputs returns an owned copy of the policy inputs of one
// issue write. Records are selected before they are copied.
func legacyIssueWriteInputs(installed domain.Bootstrap, userID string, projectIDs, cycleIDs []string) domain.Bootstrap {
	data := domain.Bootstrap{
		Workspace:         installed.Workspace,
		WorkspaceSettings: installed.WorkspaceSettings,
		Teams:             installed.Teams,
		TeamSettings:      installed.TeamSettings,
	}
	if settings, ok := installed.UserSettings[userID]; ok {
		data.UserSettings = map[string]domain.UserSettings{userID: settings}
	}
	for _, project := range installed.Projects {
		if slices.Contains(projectIDs, project.ID) {
			data.Projects = append(data.Projects, project)
		}
	}
	for _, cycle := range installed.Cycles {
		if slices.Contains(cycleIDs, cycle.ID) {
			data.Cycles = append(data.Cycles, cycle)
		}
	}
	return cloneBootstrap(data)
}
