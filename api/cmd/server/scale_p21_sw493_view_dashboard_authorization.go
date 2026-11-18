package main

import (
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// boundedProjectVisibleByID is the literal projection's visibleProjects[id]:
// some project with exactly this ID survives the viewer's team filter.
func (s *server) boundedProjectVisibleByID(r *http.Request, access store.AccessContext, id string) (bool, error) {
	read, err := s.store.BeginRecordAuthorizationRead(r.Context(), access, "projects", id)
	if err != nil {
		return false, err
	}
	defer read.Close()
	return slices.ContainsFunc(read.Data.Projects, func(item domain.Project) bool { return item.ID == id && access.ProjectVisible(item) }), nil
}

// boundedProjectAllowed is the literal projectAllowed (slug-ranked visible
// project, then teamAllowed) through the reviewed SW255 point reader.
func (s *server) boundedProjectAllowed(r *http.Request, access store.AccessContext, ref string) (bool, error) {
	read, err := s.store.BeginResourceAuthorizationRead(r.Context(), access, "projects", ref)
	if err != nil {
		return false, err
	}
	allowed := read.Allowed
	read.Close()
	return allowed, nil
}

// boundedViewPredicate mirrors resourceAllowedLegacy's "views" case for a
// saved-view point (ref != "") or the create root (create).
func (s *server) boundedViewPredicate(r *http.Request, userID, ref string, create bool) scaleP21RecordPredicate {
	return func(access store.AccessContext, data domain.Bootstrap) (bool, error) {
		role := access.ViewerRole()
		if create {
			if role == "guest" {
				return false, nil
			}
			var input domain.SavedViewMutationInput
			if !peekRequestJSON(r, &input) {
				return false, nil
			}
			if input.TeamID != nil && *input.TeamID != "" && !access.TeamContentAllowed(*input.TeamID) {
				return false, nil
			}
			if input.ProjectID != nil && *input.ProjectID != "" {
				return s.boundedProjectAllowed(r, access, *input.ProjectID)
			}
			return true, nil
		}
		allowed, projected, err := s.boundedViewAllowed(r, access, userID, ref, data)
		if err != nil {
			return false, err
		}
		if !allowed {
			return false, nil
		}
		if r.Method == http.MethodPatch || r.Method == http.MethodDelete || strings.HasSuffix(r.URL.Path, "/share") {
			item, err := savedViewByID(&projected, ref)
			if err != nil || (item.OwnerID != userID && !workspaceAdminRole(role)) {
				return false, nil
			}
			if r.Method == http.MethodPatch {
				var input domain.SavedViewMutationInput
				if !peekRequestJSON(r, &input) {
					return false, nil
				}
				if input.OwnerID != nil && *input.OwnerID != "" && *input.OwnerID != item.OwnerID && !workspaceAdminRole(role) {
					return false, nil
				}
				if input.TeamID != nil && *input.TeamID != "" && !access.TeamContentAllowed(*input.TeamID) {
					return false, nil
				}
				if input.ProjectID != nil && *input.ProjectID != "" {
					return s.boundedProjectAllowed(r, access, *input.ProjectID)
				}
			}
		}
		return true, nil
	}
}

// boundedViewAllowed is the literal viewAllowed over the literal projection
// of the headers named by ref, which it also returns in collection order.
func (s *server) boundedViewAllowed(r *http.Request, access store.AccessContext, userID, ref string, data domain.Bootstrap) (bool, domain.Bootstrap, error) {
	role := access.ViewerRole()
	projected := domain.Bootstrap{ViewerRole: role}
	for _, view := range data.SavedViews {
		if view.Scope == "team" && !access.TeamContentVisible(view.TeamID) || view.Scope == "personal" && view.OwnerID != userID {
			continue
		}
		if view.ProjectID != "" {
			visible, err := s.boundedProjectVisibleByID(r, access, view.ProjectID)
			if err != nil {
				return false, projected, err
			}
			if !visible {
				continue
			}
		}
		projected.SavedViews = append(projected.SavedViews, view)
	}
	for _, item := range projected.SavedViews {
		if item.ID != ref && item.SlugID != ref {
			continue
		}
		allowed := false
		if item.Scope == "personal" {
			allowed = item.OwnerID == userID || workspaceAdminRole(role)
		} else if item.Scope == "team" && item.TeamID != "" && !access.TeamContentAllowed(item.TeamID) {
			allowed = false
		} else if item.ProjectID == "" {
			allowed = true
		} else {
			var err error
			if allowed, err = s.boundedProjectAllowed(r, access, item.ProjectID); err != nil {
				return false, projected, err
			}
		}
		if allowed {
			return true, projected, nil
		}
	}
	return false, projected, nil
}

// boundedDashboards reads and projects the dashboards setting with the
// store's own settings filter and decodes it with settingCollection.
func (s *server) boundedDashboards(r *http.Request, access store.AccessContext) ([]domain.Dashboard, error) {
	read, err := s.store.BeginSettingAuthorizationRead(r.Context(), access, dashboardsSettingsKey)
	if err != nil {
		return nil, err
	}
	data := read.Data
	read.Close()
	access.FilterSettingsByVisibility(&data)
	return settingCollection[domain.Dashboard](data, dashboardsSettingsKey), nil
}

// boundedDashboardVisible is the literal dashboardAllowed: dashboardVisible
// sees the projected team rows.
func boundedDashboardVisible(access store.AccessContext, userID, ref string, items []domain.Dashboard) bool {
	for _, item := range items {
		if item.ID != ref {
			continue
		}
		rows := domain.Bootstrap{ViewerRole: access.ViewerRole()}
		for _, id := range item.TeamIDs {
			if access.TeamRowVisible(id) {
				rows.Teams = append(rows.Teams, domain.Team{ID: id})
			}
		}
		if dashboardVisible(rows, userID, item) {
			return true
		}
	}
	return false
}

// boundedDashboardPredicate mirrors the "dashboards" case. The setting is
// projected by the store's own settings filter and decoded by the literal
// settingCollection; dashboardVisible sees the projected team rows.
func (s *server) boundedDashboardPredicate(r *http.Request, userID, ref string, create bool) scaleP21RecordPredicate {
	return func(access store.AccessContext, _ domain.Bootstrap) (bool, error) {
		role := access.ViewerRole()
		if create {
			if role == "guest" {
				return false, nil
			}
			var input dashboardInput
			if !peekRequestJSON(r, &input) {
				return false, nil
			}
			return input.TeamIDs == nil || !slices.ContainsFunc(*input.TeamIDs, func(id string) bool { return !access.TeamContentAllowed(id) }), nil
		}
		items, err := s.boundedDashboards(r, access)
		if err != nil {
			return false, err
		}
		visible := boundedDashboardVisible(access, userID, ref, items)
		if !visible {
			return false, nil
		}
		if r.Method == http.MethodPatch || r.Method == http.MethodDelete || strings.HasSuffix(r.URL.Path, "/share") {
			if !slices.ContainsFunc(items, func(item domain.Dashboard) bool {
				return item.ID == ref && (item.OwnerID == userID || workspaceAdminRole(role))
			}) {
				return false, nil
			}
			if r.Method == http.MethodPatch {
				var input dashboardInput
				if !peekRequestJSON(r, &input) {
					return false, nil
				}
				if input.OwnerID != nil && *input.OwnerID != "" && *input.OwnerID != userID && !workspaceAdminRole(role) {
					return false, nil
				}
				if input.TeamIDs != nil && slices.ContainsFunc(*input.TeamIDs, func(id string) bool { return !access.TeamContentAllowed(id) }) {
					return false, nil
				}
			}
		}
		return true, nil
	}
}
