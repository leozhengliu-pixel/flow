package store

import "flow/api/internal/domain"

// ProjectByID reads one project from the workspace cache without building a
// viewer projection (realtime authorizes per event and must stay cheap).
func (s *SQLiteStore) ProjectByID(workspace, id string) (domain.Project, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspace]
	if !ok {
		return domain.Project{}, false
	}
	for _, project := range data.Projects {
		if project.ID == id {
			return project, true
		}
	}
	return domain.Project{}, false
}
