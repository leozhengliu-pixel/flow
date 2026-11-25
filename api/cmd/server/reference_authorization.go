package main

import (
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// boundedReferencePredicate mirrors resourceReferenceAllowed for favorites
// and subscriptions. The literal path first applies filterBootstrapForAPIKey,
// whose application-scope filter empties customers, customer requests,
// initiatives, releases and pipelines for an app credential without the
// matching scope; that is applied before each kind's own predicate.
//
// A selected-team credential's projection then keeps only key-team resources
// (filterBootstrapForAPIKey + applyAPIKeyProjection); each kind below applies
// that rule to the headers it reads when selected is set.
func (s *server) boundedReferencePredicate(r *http.Request, workspace, userID, kind, id string) scaleP21RecordPredicate {
	return func(access store.AccessContext, _ domain.Bootstrap) (bool, error) {
		key, keyed := r.Context().Value(apiKeyContextKey{}).(domain.APIKey)
		app := keyed && key.Actor == "app"
		selected := keyed && apiKeyTeamRestrictionSelected(key)
		inKey := func(teamID string) bool { return slices.Contains(key.TeamIDs, teamID) }
		contentInKey := func(teamID string) bool { return access.TeamContentVisible(teamID) && inKey(teamID) }
		// visibleProject of the key projection: an exact-ID project with a
		// content team on the key's allow-list.
		keyProject := func(projectID string) (bool, error) {
			data, err := s.boundedRecordHeaders(r, access, "projects", projectID)
			if err != nil {
				return false, err
			}
			return slices.ContainsFunc(data.Projects, func(item domain.Project) bool {
				return item.ID == projectID && slices.ContainsFunc(item.TeamIDs, contentInKey)
			}), nil
		}
		if selected {
			switch kind {
			case "label":
				data, err := s.boundedRecordHeaders(r, access, "labels", id)
				if err != nil {
					return false, err
				}
				return slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool {
					scope := strings.ToLower(strings.TrimSpace(label.Scope))
					return label.ID == id && label.ArchivedAt == nil && (scope == "" || scope == "workspace" || access.TeamRowVisible(label.Scope)) &&
						(workspaceScope(label.Scope) || inKey(label.Scope))
				}), nil
			case "release_pipeline":
				if app && !apiKeyHasScope(key, "release:read") {
					return false, nil
				}
				return s.boundedSelectedPipeline(r, access, id, contentInKey)
			case "project":
				data, err := s.boundedRecordHeaders(r, access, "projectRefs", id)
				if err != nil {
					return false, err
				}
				return slices.ContainsFunc(data.Projects, func(item domain.Project) bool { return slices.ContainsFunc(item.TeamIDs, contentInKey) }), nil
			case "initiative":
				return s.boundedSelectedInitiative(r, access, key, id)
			case "view":
				data, err := s.boundedRecordHeaders(r, access, "savedViews", id)
				if err != nil {
					return false, err
				}
				kept := data.SavedViews[:0:0]
				for _, view := range data.SavedViews {
					if view.Scope == "team" && !inKey(view.TeamID) {
						continue
					}
					if view.ProjectID != "" {
						visible, err := keyProject(view.ProjectID)
						if err != nil {
							return false, err
						}
						if !visible {
							continue
						}
					}
					kept = append(kept, view)
				}
				data.SavedViews = kept
				allowed, _, err := s.boundedViewAllowed(r, access, userID, id, data)
				return allowed, err
			case "dashboard":
				items, err := s.boundedDashboards(r, access)
				if err != nil {
					return false, err
				}
				for index := range items {
					items[index].TeamIDs = slices.DeleteFunc(items[index].TeamIDs, func(teamID string) bool { return !inKey(teamID) })
				}
				items = slices.DeleteFunc(items, func(item domain.Dashboard) bool { return item.Visibility == "team" && len(item.TeamIDs) == 0 })
				return boundedDashboardVisible(access, userID, id, items), nil
			case "release":
				if app && !apiKeyHasScope(key, "release:read") {
					return false, nil
				}
				return s.boundedSelectedRelease(r, access, workspace, userID, id, contentInKey, keyProject)
			case "document":
				return s.boundedSelectedDocument(r, workspace, userID, id, key, app)
			case "customer_request":
				if app && !apiKeyHasScope(key, "customer:read") && !apiKeyHasScope(key, "customer:write") {
					return false, nil
				}
				// Requests whose project the key cannot read are removed; the
				// remaining ones take the literal predicate.
				return s.boundedCustomerRequestAllowedWhere(r, access, workspace, userID, id, func(item domain.CustomerRequest) (bool, error) {
					if item.ProjectID == "" {
						return true, nil
					}
					return keyProject(item.ProjectID)
				})
			}
		}
		switch kind {
		case "issue":
			return s.pagedIssueVisible(r, id), nil
		case "team":
			return access.TeamContentAllowed(id) && access.TeamActiveRow(id), nil
		case "label":
			data, err := s.boundedRecordHeaders(r, access, "labels", id)
			if err != nil {
				return false, err
			}
			return slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool {
				scope := strings.ToLower(strings.TrimSpace(label.Scope))
				return label.ID == id && label.ArchivedAt == nil && (scope == "" || scope == "workspace" || access.TeamRowVisible(label.Scope))
			}), nil
		case "release_pipeline":
			if app && !apiKeyHasScope(key, "release:read") {
				return false, nil
			}
			return s.boundedPipelineAllowed(r, access, id)
		case "review":
			return s.preferenceReviewVisible(r, id), nil
		case "project":
			return s.boundedProjectAllowed(r, access, id)
		case "initiative":
			if app && !apiKeyHasScope(key, "initiative:read") && !apiKeyHasScope(key, "initiative:write") {
				return false, nil
			}
			read, err := s.store.BeginResourceAuthorizationRead(r.Context(), access, "initiatives", id)
			if err != nil {
				return false, err
			}
			allowed := read.Allowed
			read.Close()
			return allowed, nil
		case "view":
			data, err := s.boundedRecordHeaders(r, access, "savedViews", id)
			if err != nil {
				return false, err
			}
			allowed, _, err := s.boundedViewAllowed(r, access, userID, id, data)
			return allowed, err
		case "dashboard":
			items, err := s.boundedDashboards(r, access)
			if err != nil {
				return false, err
			}
			return boundedDashboardVisible(access, userID, id, items), nil
		case "cycle":
			data, err := s.boundedRecordHeaders(r, access, "cycles", id)
			if err != nil {
				return false, err
			}
			return boundedCycleAllowed(id)(access, data)
		case "release":
			if app && !apiKeyHasScope(key, "release:read") {
				return false, nil
			}
			return s.boundedReleaseAllowed(r, access, id)
		case "document":
			return s.boundedDocumentReferenceAllowed(r, workspace, userID, id)
		case "customer":
			if app && !apiKeyHasScope(key, "customer:read") && !apiKeyHasScope(key, "customer:write") {
				return false, nil
			}
			return s.boundedCustomerAllowed(r, access, id)
		case "customer_request":
			if app && !apiKeyHasScope(key, "customer:read") && !apiKeyHasScope(key, "customer:write") {
				return false, nil
			}
			return s.boundedCustomerRequestAllowed(r, access, workspace, userID, id)
		case "ask":
			data, err := s.boundedRecordHeaders(r, access, "asks", id)
			if err != nil {
				return false, err
			}
			return s.boundedAskPredicate(r, id, false)(access, data)
		}
		return false, nil
	}
}

// boundedDocumentReferenceAllowed reads the document through the reviewed
// document authorization reader (as the document routes do) and applies the
// literal reference predicate: an exact ID or slug with a role.
func (s *server) boundedDocumentReferenceAllowed(r *http.Request, workspace, userID, id string) (bool, error) {
	read, err := s.store.BeginDocumentRead(r.Context(), workspace, userID, documentRecordCredential(r), store.DocumentReadRequest{Ref: id, ReadOnly: true, ProjectionFallback: true})
	if err != nil {
		return false, err
	}
	defer read.Close()
	return slices.ContainsFunc(read.Data.Documents, func(item domain.Document) bool {
		return (item.ID == id || item.SlugID == id) && documentRole(s, read.Data, item) != "none"
	}), nil
}

// boundedSelectedPipeline: the key projection keeps a pipeline with no teams
// or with a content team on the key's allow-list.
func (s *server) boundedSelectedPipeline(r *http.Request, access store.AccessContext, id string, contentInKey func(string) bool) (bool, error) {
	data, err := s.boundedRecordHeaders(r, access, "releasePipelines", id)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool {
		return item.ID == id && (len(item.TeamIDs) == 0 || slices.ContainsFunc(item.TeamIDs, contentInKey))
	}), nil
}

// boundedSelectedRelease: the viewer projection keeps a release of a visible
// pipeline (or none) with visible links; the key projection then keeps it
// for a key-visible pipeline, or without a pipeline when it is unlinked or
// links a key-visible project or any visible issue (paged issues are not
// filtered by the key).
func (s *server) boundedSelectedRelease(r *http.Request, access store.AccessContext, workspace, userID, id string, contentInKey func(string) bool, keyProject func(string) (bool, error)) (bool, error) {
	data, err := s.boundedRecordHeaders(r, access, "releases", id)
	if err != nil {
		return false, err
	}
	for _, item := range data.Releases {
		if item.ID != id {
			continue
		}
		if item.PipelineID != "" {
			visible, err := s.boundedSelectedPipeline(r, access, item.PipelineID, contentInKey)
			if err != nil || visible {
				return visible, err
			}
			continue
		}
		projects := []string{}
		for _, projectID := range item.ProjectIDs {
			visible, err := s.boundedProjectVisibleByID(r, access, projectID)
			if err != nil {
				return false, err
			}
			if visible {
				projects = append(projects, projectID)
			}
		}
		issues, err := s.boundedProjectedIssueVisible(r, workspace, userID, item.IssueIDs)
		if err != nil {
			return false, err
		}
		linkedIssue := slices.ContainsFunc(item.IssueIDs, func(issueID string) bool { return issues[issueID] })
		if len(projects) == 0 && !linkedIssue || linkedIssue {
			return true, nil
		}
		for _, projectID := range projects {
			visible, err := keyProject(projectID)
			if err != nil || visible {
				return visible, err
			}
		}
	}
	return false, nil
}

// boundedSelectedDocument applies the key projection's team, team-setting,
// team-member and document filters to the reviewed document read before the
// literal reference predicate.
func (s *server) boundedSelectedDocument(r *http.Request, workspace, userID, id string, key domain.APIKey, app bool) (bool, error) {
	read, err := s.store.BeginDocumentRead(r.Context(), workspace, userID, documentRecordCredential(r), store.DocumentReadRequest{Ref: id, ReadOnly: true, ProjectionFallback: true})
	if err != nil {
		return false, err
	}
	defer read.Close()
	inKey := func(teamID string) bool { return slices.Contains(key.TeamIDs, teamID) }
	data := read.Data
	data.Teams = slices.DeleteFunc(slices.Clone(data.Teams), func(team domain.Team) bool { return !inKey(team.ID) })
	settings := map[string]domain.TeamSettings{}
	for teamID, value := range data.TeamSettings {
		if inKey(teamID) {
			settings[teamID] = value
		}
	}
	data.TeamSettings = settings
	data.TeamMembers = slices.DeleteFunc(slices.Clone(data.TeamMembers), func(member domain.TeamMember) bool { return !inKey(member.TeamID) })
	data.Documents = slices.DeleteFunc(slices.Clone(data.Documents), func(item domain.Document) bool {
		if slices.ContainsFunc(item.TeamIDs, inKey) {
			return false
		}
		return !app || len(item.TeamIDs) > 0 || !slices.ContainsFunc(item.InheritedTeamIDs, inKey)
	})
	linkScope := store.NewDocumentLinkScope(data.Teams, data.TeamSettings)
	data.Documents = slices.DeleteFunc(data.Documents, func(item domain.Document) bool {
		if len(item.TeamIDs) > 0 {
			return !slices.ContainsFunc(item.TeamIDs, inKey)
		}
		closed := linkScope.ClosedTeams(item)
		return len(closed) > 0 && !slices.ContainsFunc(closed, inKey)
	})
	return slices.ContainsFunc(data.Documents, func(item domain.Document) bool {
		return (item.ID == id || item.SlugID == id) && documentRole(s, data, item) != "none"
	}), nil
}

// boundedSelectedInitiative is initiativeAllowed after a selected credential's
// projection, which keeps an initiative only when one of its visible projects
// has a content team on the key's allow-list (and removes all for guests).
func (s *server) boundedSelectedInitiative(r *http.Request, access store.AccessContext, key domain.APIKey, id string) (bool, error) {
	if key.Actor == "app" && !apiKeyHasScope(key, "initiative:read") && !apiKeyHasScope(key, "initiative:write") || access.ViewerRole() == "guest" {
		return false, nil
	}
	contentInKey := func(teamID string) bool {
		return access.TeamContentVisible(teamID) && slices.Contains(key.TeamIDs, teamID)
	}
	data, err := s.boundedRecordHeaders(r, access, "initiativeRefs", id)
	if err != nil {
		return false, err
	}
	for _, item := range data.Initiatives {
		if item.LeadTeamID != "" && !contentInKey(item.LeadTeamID) {
			continue
		}
		for _, projectID := range item.ProjectIDs {
			projects, err := s.boundedRecordHeaders(r, access, "projects", projectID)
			if err != nil {
				return false, err
			}
			if slices.ContainsFunc(projects.Projects, func(project domain.Project) bool {
				return project.ID == projectID && slices.ContainsFunc(project.TeamIDs, contentInKey)
			}) {
				return true, nil
			}
		}
	}
	return false, nil
}
