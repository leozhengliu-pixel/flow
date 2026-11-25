package main

import (
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// boundedRecordHeaders reads one kind's unfiltered headers named by ref.
func (s *server) boundedRecordHeaders(r *http.Request, access store.AccessContext, kind, ref string) (domain.Bootstrap, error) {
	read, err := s.store.BeginRecordAuthorizationRead(r.Context(), access, kind, ref)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	data := read.Data
	read.Close()
	return data, nil
}

// boundedProjectedIssueVisible is the literal paged projection's issue
// visibility (IssueQueryAccess + VisibleIssueRecordIDs for the viewer, not
// the credential) that keeps reviews and customer requests.
func (s *server) boundedProjectedIssueVisible(r *http.Request, workspace, userID string, ids []string) (map[string]bool, error) {
	if len(ids) == 0 {
		return map[string]bool{}, nil
	}
	_, access, err := s.store.IssueQueryAccess(r.Context(), workspace, userID)
	if err != nil {
		return nil, err
	}
	return s.store.VisibleIssueRecordIDs(r.Context(), store.IssueRecordQuery{Workspace: workspace, Access: &access}, ids)
}

// reviews: the paged projection keeps a review with no linked issues or with
// a visible one, keeping only visible links; the literal predicate then needs
// ID == ref and (no projected links or issueAllowed of one of them).
func (s *server) boundedReviewPredicate(r *http.Request, workspace, userID, ref string) scaleP21RecordPredicate {
	return func(_ store.AccessContext, data domain.Bootstrap) (bool, error) {
		for _, item := range data.Reviews {
			if item.ID != ref {
				continue
			}
			if len(item.IssueIDs) == 0 {
				return true, nil
			}
			visible, err := s.boundedProjectedIssueVisible(r, workspace, userID, item.IssueIDs)
			if err != nil {
				return false, err
			}
			if slices.ContainsFunc(item.IssueIDs, func(id string) bool { return visible[id] && s.pagedIssueVisible(r, id) }) {
				return true, nil
			}
		}
		return false, nil
	}
}

// pipelineAllowed: a projected pipeline has no teams or a content team.
func (s *server) boundedPipelineAllowed(r *http.Request, access store.AccessContext, id string) (bool, error) {
	data, err := s.boundedRecordHeaders(r, access, "releasePipelines", id)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool {
		return item.ID == id && (len(item.TeamIDs) == 0 || slices.ContainsFunc(item.TeamIDs, access.TeamContentVisible))
	}), nil
}

// releaseAllowed: a projected release has no pipeline or a visible one.
func (s *server) boundedReleaseAllowed(r *http.Request, access store.AccessContext, id string) (bool, error) {
	data, err := s.boundedRecordHeaders(r, access, "releases", id)
	if err != nil {
		return false, err
	}
	for _, item := range data.Releases {
		if item.ID != id {
			continue
		}
		if item.PipelineID == "" {
			return true, nil
		}
		visible, err := s.boundedPipelineAllowed(r, access, item.PipelineID)
		if err != nil || visible {
			return visible, err
		}
	}
	return false, nil
}

func (s *server) boundedReleaseMutationAllowed(r *http.Request, access store.AccessContext, input releaseInput) (bool, error) {
	if input.PipelineID != nil && strings.TrimSpace(*input.PipelineID) != "" {
		if allowed, err := s.boundedPipelineAllowed(r, access, strings.TrimSpace(*input.PipelineID)); err != nil || !allowed {
			return false, err
		}
	}
	if input.ProjectIDs != nil {
		for _, id := range *input.ProjectIDs {
			if strings.TrimSpace(id) == "" {
				continue
			}
			if allowed, err := s.boundedProjectAllowed(r, access, strings.TrimSpace(id)); err != nil || !allowed {
				return false, err
			}
		}
	}
	if input.IssueIDs != nil && slices.ContainsFunc(*input.IssueIDs, func(id string) bool {
		return strings.TrimSpace(id) != "" && !s.pagedIssueVisible(r, strings.TrimSpace(id))
	}) {
		return false, nil
	}
	return true, nil
}

// boundedReleasePredicate mirrors the "releases" case (create root, reorder,
// point, PATCH body).
func (s *server) boundedReleasePredicate(r *http.Request, ref string, create bool) scaleP21RecordPredicate {
	return func(access store.AccessContext, _ domain.Bootstrap) (bool, error) {
		if create {
			var input releaseInput
			if !peekRequestJSON(r, &input) {
				return false, nil
			}
			return s.boundedReleaseMutationAllowed(r, access, input)
		}
		if ref == "reorder" {
			var input reorderInput
			if !peekRequestJSON(r, &input) {
				return false, nil
			}
			if input.PipelineID != "" {
				if allowed, err := s.boundedPipelineAllowed(r, access, input.PipelineID); err != nil || !allowed {
					return false, err
				}
			}
			for _, id := range input.IDs {
				if allowed, err := s.boundedReleaseAllowed(r, access, id); err != nil || !allowed {
					return false, err
				}
			}
			return true, nil
		}
		if allowed, err := s.boundedReleaseAllowed(r, access, ref); err != nil || !allowed {
			return false, err
		}
		if r.Method == http.MethodPatch {
			var input releaseInput
			if !peekRequestJSON(r, &input) {
				return false, nil
			}
			return s.boundedReleaseMutationAllowed(r, access, input)
		}
		return true, nil
	}
}

func pipelineMutationTeamsAllowed(access store.AccessContext, input releasePipelineInput) bool {
	return input.TeamIDs == nil || !slices.ContainsFunc(*input.TeamIDs, func(id string) bool {
		return strings.TrimSpace(id) != "" && !access.TeamContentAllowed(strings.TrimSpace(id))
	})
}

// boundedPipelinePredicate mirrors the "release-pipelines" case.
func (s *server) boundedPipelinePredicate(r *http.Request, ref string, create bool) scaleP21RecordPredicate {
	return func(access store.AccessContext, _ domain.Bootstrap) (bool, error) {
		if create {
			var input releasePipelineInput
			return peekRequestJSON(r, &input) && pipelineMutationTeamsAllowed(access, input), nil
		}
		if ref == "reorder" {
			var input reorderInput
			if !peekRequestJSON(r, &input) {
				return false, nil
			}
			for _, id := range input.IDs {
				if allowed, err := s.boundedPipelineAllowed(r, access, id); err != nil || !allowed {
					return false, err
				}
			}
			return true, nil
		}
		if allowed, err := s.boundedPipelineAllowed(r, access, ref); err != nil || !allowed {
			return false, err
		}
		if r.Method == http.MethodPatch {
			var input releasePipelineInput
			return peekRequestJSON(r, &input) && pipelineMutationTeamsAllowed(access, input), nil
		}
		return true, nil
	}
}

// customerAllowed: guests read no customers; others read every customer.
func (s *server) boundedCustomerAllowed(r *http.Request, access store.AccessContext, id string) (bool, error) {
	if access.ViewerRole() == "guest" {
		return false, nil
	}
	data, err := s.boundedRecordHeaders(r, access, "customers", id)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(data.Customers, func(item domain.Customer) bool { return item.ID == id }), nil
}

func (s *server) boundedCustomerPredicate(r *http.Request, ref string) scaleP21RecordPredicate {
	return func(access store.AccessContext, _ domain.Bootstrap) (bool, error) {
		return s.boundedCustomerAllowed(r, access, ref)
	}
}

func (s *server) boundedCustomerRequestTargetsAllowed(r *http.Request, access store.AccessContext, input customerRequestInput) (bool, error) {
	if access.ViewerRole() == "guest" {
		return false, nil
	}
	if id := trimmedValue(input.CustomerID); id != "" {
		if allowed, err := s.boundedCustomerAllowed(r, access, id); err != nil || !allowed {
			return false, err
		}
	}
	if input.IssueID != nil && *input.IssueID != "" && !s.pagedIssueVisible(r, *input.IssueID) {
		return false, nil
	}
	if input.ProjectID == nil || *input.ProjectID == "" {
		return true, nil
	}
	return s.boundedProjectAllowed(r, access, *input.ProjectID)
}

// customerRequestAllowed over the paged projection, which keeps a request
// only for non-guests, with a visible linked issue and an exact-ID visible
// linked project.
func (s *server) boundedCustomerRequestAllowed(r *http.Request, access store.AccessContext, workspace, userID, id string) (bool, error) {
	return s.boundedCustomerRequestAllowedWhere(r, access, workspace, userID, id, nil)
}

// boundedCustomerRequestAllowedWhere also drops requests keep refuses (a
// selected credential's projection) before the literal predicate.
func (s *server) boundedCustomerRequestAllowedWhere(r *http.Request, access store.AccessContext, workspace, userID, id string, keep func(domain.CustomerRequest) (bool, error)) (bool, error) {
	if access.ViewerRole() == "guest" {
		return false, nil
	}
	data, err := s.boundedRecordHeaders(r, access, "customerRequests", id)
	if err != nil {
		return false, err
	}
	for _, item := range data.CustomerRequests {
		if item.ID != id {
			continue
		}
		if keep != nil {
			kept, err := keep(item)
			if err != nil {
				return false, err
			}
			if !kept {
				continue
			}
		}
		if item.IssueID != "" {
			visible, err := s.boundedProjectedIssueVisible(r, workspace, userID, []string{item.IssueID})
			if err != nil {
				return false, err
			}
			if !visible[item.IssueID] || !s.pagedIssueVisible(r, item.IssueID) {
				continue
			}
		}
		if item.ProjectID != "" {
			visible, err := s.boundedProjectVisibleByID(r, access, item.ProjectID)
			if err != nil {
				return false, err
			}
			if !visible {
				continue
			}
		}
		if item.CustomerID != "" {
			if allowed, err := s.boundedCustomerAllowed(r, access, item.CustomerID); err != nil || !allowed {
				if err != nil {
					return false, err
				}
				continue
			}
		}
		if item.ProjectID != "" {
			allowed, err := s.boundedProjectAllowed(r, access, item.ProjectID)
			if err != nil {
				return false, err
			}
			if !allowed {
				continue
			}
		}
		return true, nil
	}
	return false, nil
}

// boundedCustomerRequestPredicate mirrors the "customer-requests" case.
func (s *server) boundedCustomerRequestPredicate(r *http.Request, workspace, userID, ref string, create bool) scaleP21RecordPredicate {
	return func(access store.AccessContext, _ domain.Bootstrap) (bool, error) {
		if create {
			if access.ViewerRole() == "guest" {
				return false, nil
			}
			var input customerRequestInput
			if !peekRequestJSON(r, &input) {
				return false, nil
			}
			return s.boundedCustomerRequestTargetsAllowed(r, access, input)
		}
		if len(strings.Split(strings.Trim(r.URL.Path, "/"), "/")) == 3 && r.Method == http.MethodPatch {
			var input customerRequestInput
			if !peekRequestJSON(r, &input) {
				return false, nil
			}
			if allowed, err := s.boundedCustomerRequestTargetsAllowed(r, access, input); err != nil || !allowed {
				return false, err
			}
		}
		return s.boundedCustomerRequestAllowed(r, access, workspace, userID, ref)
	}
}

// asks: the projection keeps unscoped asks and asks of content teams; the
// literal askAllowed then requires teamAllowed of a scoped ask's team.
func (s *server) boundedAskPredicate(r *http.Request, ref string, create bool) scaleP21RecordPredicate {
	return func(access store.AccessContext, data domain.Bootstrap) (bool, error) {
		if create {
			if access.ViewerRole() == "guest" {
				return false, nil
			}
			var input domain.Ask
			if !peekRequestJSON(r, &input) {
				return false, nil
			}
			return input.TeamID == "" || access.TeamContentAllowed(input.TeamID), nil
		}
		return slices.ContainsFunc(data.Asks, func(item domain.Ask) bool {
			return item.ID == ref && (item.TeamID == "" || access.TeamContentAllowed(item.TeamID))
		}), nil
	}
}
