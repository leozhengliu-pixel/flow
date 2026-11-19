package main

import (
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// boundedProjectedUser is the literal knownPerson's data.Users: every
// member's user, and for a guest only themselves and members of their
// visible teams.
func (s *server) boundedProjectedUser(r *http.Request, access store.AccessContext, userID, id string) (bool, error) {
	members, _ := s.store.ListMembers(r.Context(), access.WorkspaceID)
	if !slices.ContainsFunc(members, func(member domain.WorkspaceMember) bool { return member.User.ID == id }) {
		return false, nil
	}
	if access.ViewerRole() != "guest" || id == userID {
		return true, nil
	}
	teams, _ := s.store.ListTeamMembers(r.Context(), access.WorkspaceID)
	return slices.ContainsFunc(teams, func(membership domain.TeamMember) bool {
		return membership.UserID == id && access.TeamRowVisible(membership.TeamID)
	}), nil
}

// boundedInitiativeVisibleByID is "some projected initiative has this ID":
// the literal projection removes every initiative for guests and hides one
// scoped only to resources the viewer cannot read.
func (s *server) boundedInitiativeVisibleByID(r *http.Request, access store.AccessContext, id string) (bool, error) {
	if access.ViewerRole() == "guest" {
		return false, nil
	}
	data, err := s.boundedRecordHeaders(r, access, "initiatives", id)
	if err != nil {
		return false, err
	}
	for _, item := range data.Initiatives {
		if item.ID != id || item.LeadTeamID != "" && !access.TeamContentVisible(item.LeadTeamID) {
			continue
		}
		if item.LeadTeamID != "" || len(item.ContributingTeamIDs) == 0 && len(item.ProjectIDs) == 0 || slices.ContainsFunc(item.ContributingTeamIDs, access.TeamContentVisible) {
			return true, nil
		}
		for _, project := range item.ProjectIDs {
			visible, err := s.boundedProjectVisibleByID(r, access, project)
			if err != nil || visible {
				return visible, err
			}
		}
	}
	return false, nil
}

func (s *server) boundedInitiativeAllowed(r *http.Request, access store.AccessContext, ref string) (bool, error) {
	read, err := s.store.BeginResourceAuthorizationRead(r.Context(), access, "initiatives", ref)
	if err != nil {
		return false, err
	}
	allowed := read.Allowed
	read.Close()
	return allowed, nil
}

func (s *server) boundedAllProjectsAllowed(r *http.Request, access store.AccessContext, ids []string, trim bool) (bool, error) {
	for _, id := range ids {
		if trim {
			id = strings.TrimSpace(id)
		}
		if allowed, err := s.boundedProjectAllowed(r, access, id); err != nil || !allowed {
			return false, err
		}
	}
	return true, nil
}

func (s *server) boundedAllInitiativesAllowed(r *http.Request, access store.AccessContext, ids []string) (bool, error) {
	for _, id := range ids {
		if allowed, err := s.boundedInitiativeAllowed(r, access, id); err != nil || !allowed {
			return false, err
		}
	}
	return true, nil
}

// The literal projectMutationAllowed.
func (s *server) boundedProjectMutationAllowed(r *http.Request, access store.AccessContext, workspace, userID string, input domain.ProjectMutationInput) (bool, error) {
	if slices.ContainsFunc(input.TeamIDs, func(id string) bool { return !access.TeamContentAllowed(id) }) {
		return false, nil
	}
	if allowed, err := s.boundedAllProjectsAllowed(r, access, input.DependencyIDs, false); err != nil || !allowed {
		return false, err
	}
	knownPerson := func(id string) (bool, error) {
		known, err := s.boundedProjectedUser(r, access, userID, id)
		if err != nil || known {
			return known, err
		}
		return s.formerMemberOutsideWrite(r.Context(), workspace, id), nil
	}
	if input.LeadID != nil && *input.LeadID != "" {
		if known, err := knownPerson(*input.LeadID); err != nil || !known {
			return false, err
		}
	}
	for _, id := range input.MemberIDs {
		if known, err := knownPerson(id); err != nil || !known {
			return false, err
		}
	}
	for _, id := range input.Initiatives {
		if visible, err := s.boundedInitiativeVisibleByID(r, access, id); err != nil || !visible {
			return false, err
		}
	}
	for _, id := range input.Customers {
		if access.ViewerRole() == "guest" {
			return false, nil
		}
		data, err := s.boundedRecordHeaders(r, access, "customers", id)
		if err != nil {
			return false, err
		}
		if !slices.ContainsFunc(data.Customers, func(item domain.Customer) bool { return item.ID == id }) {
			return false, nil
		}
	}
	return true, nil
}

// boundedProjectBodyPredicate mirrors the "projects" body branches: the
// create root, POST .../relations and PATCH (other than a relation ID).
func (s *server) boundedProjectBodyPredicate(r *http.Request, workspace, userID, ref string, create bool) scaleP21RecordPredicate {
	return func(access store.AccessContext, _ domain.Bootstrap) (bool, error) {
		var input domain.ProjectMutationInput
		if create {
			if !peekRequestJSON(r, &input) {
				return false, nil
			}
			return s.boundedProjectMutationAllowed(r, access, workspace, userID, input)
		}
		if allowed, err := s.boundedProjectAllowed(r, access, ref); err != nil || !allowed {
			return false, err
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 4 && parts[3] == "relations" && r.Method == http.MethodPost {
			var relation domain.ProjectRelation
			if !peekRequestJSON(r, &relation) {
				return false, nil
			}
			return s.boundedProjectAllowed(r, access, relation.RelatedProjectID)
		}
		if !peekRequestJSON(r, &input) {
			return false, nil
		}
		return s.boundedProjectMutationAllowed(r, access, workspace, userID, input)
	}
}

func boundedTeamsAllowed(access store.AccessContext, ids *[]string) bool {
	return ids == nil || !slices.ContainsFunc(*ids, func(id string) bool { return !access.TeamContentAllowed(id) })
}

// boundedInitiativeBodyPredicate mirrors the "initiatives" body branches.
func (s *server) boundedInitiativeBodyPredicate(r *http.Request, ref string, create bool) scaleP21RecordPredicate {
	return func(access store.AccessContext, _ domain.Bootstrap) (bool, error) {
		var input domain.InitiativeMutationInput
		if create {
			if access.ViewerRole() == "guest" || !peekRequestJSON(r, &input) {
				return false, nil
			}
			if input.LeadTeamID != nil && *input.LeadTeamID != "" && !access.TeamContentAllowed(*input.LeadTeamID) || !boundedTeamsAllowed(access, input.ContributingTeamIDs) {
				return false, nil
			}
			if input.ProjectIDs != nil {
				if allowed, err := s.boundedAllProjectsAllowed(r, access, *input.ProjectIDs, false); err != nil || !allowed {
					return false, err
				}
			}
			if input.ParentInitiativeIDs != nil {
				return s.boundedAllInitiativesAllowed(r, access, *input.ParentInitiativeIDs)
			}
			return true, nil
		}
		// An embedded favorite PATCH first applies filterBootstrapForAPIKey,
		// which removes every initiative for an app credential without an
		// initiative scope.
		if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && embeddedFavoriteRequest(r) {
			if key.Actor == "app" && !apiKeyHasScope(key, "initiative:read") && !apiKeyHasScope(key, "initiative:write") {
				return false, nil
			}
			// A selected credential's projection decides initiativeAllowed;
			// the favorite-only body carries no other reference.
			if apiKeyTeamRestrictionSelected(key) {
				return s.boundedSelectedInitiative(r, access, key, ref)
			}
		}
		if allowed, err := s.boundedInitiativeAllowed(r, access, ref); err != nil || !allowed {
			return false, err
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 4 && parts[3] == "relations" && r.Method == http.MethodPost {
			var relation domain.InitiativeRelation
			if !peekRequestJSON(r, &relation) {
				return false, nil
			}
			return s.boundedInitiativeAllowed(r, access, relation.RelatedInitiativeID)
		}
		if !peekRequestJSON(r, &input) {
			return false, nil
		}
		if input.LeadTeamID != nil && *input.LeadTeamID != "" && !access.TeamContentAllowed(*input.LeadTeamID) || !boundedTeamsAllowed(access, input.ContributingTeamIDs) {
			return false, nil
		}
		if input.ParentInitiativeIDs != nil {
			if allowed, err := s.boundedAllInitiativesAllowed(r, access, *input.ParentInitiativeIDs); err != nil || !allowed {
				return false, err
			}
		}
		if input.ProjectIDs != nil {
			return s.boundedAllProjectsAllowed(r, access, *input.ProjectIDs, false)
		}
		return true, nil
	}
}

// boundedDocumentCreatePredicate mirrors POST /api/documents.
func (s *server) boundedDocumentCreatePredicate(r *http.Request) scaleP21RecordPredicate {
	return func(access store.AccessContext, _ domain.Bootstrap) (bool, error) {
		var input documentInput
		if !peekRequestJSON(r, &input) {
			return false, nil
		}
		if input.TeamIDs != nil && slices.ContainsFunc(*input.TeamIDs, func(id string) bool { return !access.TeamContentAllowed(strings.TrimSpace(id)) }) {
			return false, nil
		}
		if input.ProjectIDs != nil {
			if allowed, err := s.boundedAllProjectsAllowed(r, access, *input.ProjectIDs, true); err != nil || !allowed {
				return false, err
			}
		}
		if input.IssueID != nil && strings.TrimSpace(*input.IssueID) != "" && !s.pagedIssueVisible(r, strings.TrimSpace(*input.IssueID)) {
			return false, nil
		}
		return true, nil
	}
}
