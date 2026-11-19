package main

import (
	"errors"
	"net/http"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// listLabelsBounded answers the label settings lists (GET /api/labels and
// GET /api/teams/{id}/labels) with bounded metadata on. Their literal bodies
// read workspaceData's BootstrapForUser: the viewer's projection with every
// visible issue hydrated, narrowed by the request's API key, whose labels
// carry refreshResourceCounts' counts. Here the labels come from the reviewed
// resource directory read (the same viewer projection of the installed
// labels, projects and initiatives, without issues), narrowed by the same
// filterBootstrapForAPIKey, each loaded whole in source order; keep selects
// the route's labels. Their counts replay refreshResourceCounts: the visible
// unarchived issues carrying the label (VisibleLabelIssueCounts over the
// request's issue access), plus the unarchived projects and the initiatives
// of the projection that carry it, each resource counting a label once. A
// team-restricted key recounts after its narrowing, as applyAPIKeyProjection
// does; any other key keeps the viewer's counts.
func (s *server) listLabelsBounded(w http.ResponseWriter, r *http.Request, keep func(domain.IssueLabel) bool) {
	workspace := workspaceKey(r)
	userID := ""
	var key *domain.APIKey
	var handed *domain.Bootstrap
	var access *store.IssueRecordAccess
	if !s.authDisabled {
		userID = authUser(r).ID
		if credential, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
			key = &credential
		}
		_, current, err := s.requestIssueQueryAccess(r)
		if err != nil {
			s.labelsBoundedError(w, err)
			return
		}
		access = &current
		if data, ok := takePagedMetadata(r, workspace, userID); ok {
			handed = &data
		} else if !acquireProjection(r) {
			if retryAfter, refused := projectionRefused(r); refused {
				writeProjectionBusy(w, retryAfter)
			}
			return
		}
	}
	// A concurrent access change (another request's key use stamp, a
	// membership edit) restarts the read on the changed access, as the
	// other bounded resource reads do; the literal read has no such check.
	// Each label load revalidates the access, so a change that lands while
	// the labels are visited restarts the whole read too: nothing is
	// written before the list is complete.
	var result []domain.IssueLabel
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		var read *store.ResourceDirectoryRead
		read, result, err = s.readLabelsBounded(r, workspace, userID, key, handed, access, keep)
		if err == nil {
			// The list's reservation stays held until it is written.
			defer read.Close()
			break
		}
		if !errors.Is(err, store.ErrResourceAccessChanged) {
			break
		}
	}
	if err != nil {
		s.labelsBoundedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// readLabelsBounded is one attempt of listLabelsBounded: the directory read,
// its counts and the visited labels. On success the caller closes the
// returned read once the list is written; on an error it is closed here.
func (s *server) readLabelsBounded(r *http.Request, workspace, userID string, key *domain.APIKey, handed *domain.Bootstrap, access *store.IssueRecordAccess, keep func(domain.IssueLabel) bool) (_ *store.ResourceDirectoryRead, _ []domain.IssueLabel, err error) {
	ctx := r.Context()
	read, err := s.store.BeginResourceDirectoryRead(ctx, workspace, userID, key, "labels", handed)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	issues, err := s.store.VisibleLabelIssueCounts(ctx, read.Data.Workspace.URLKey, access, nil)
	if err != nil {
		return nil, nil, err
	}
	counts := labelResourceCounts(read.Data, issues)
	if !s.authDisabled {
		filterBootstrapForAPIKey(&read.Data, r)
		if key != nil && apiKeyTeamRestrictionSelected(*key) {
			issues, err = s.store.VisibleLabelIssueCounts(ctx, read.Data.Workspace.URLKey, access, key.TeamIDs)
			if err != nil {
				return nil, nil, err
			}
			counts = labelResourceCounts(read.Data, issues)
		}
	}
	result := []domain.IssueLabel{}
	err = read.Visit(ctx, func(value *store.ResourceDirectoryValue) error {
		if !keep(value.Label) {
			return nil
		}
		loaded, err := read.Load(ctx, value.Token)
		if err != nil {
			return err
		}
		defer loaded.Close()
		label := loaded.Label
		label.ID, label.Name, label.Color, label.Description = strings.Clone(label.ID), strings.Clone(label.Name), strings.Clone(label.Color), strings.Clone(label.Description)
		label.Scope, label.ResourceType, label.GroupID, label.CreatorID = strings.Clone(label.Scope), strings.Clone(label.ResourceType), strings.Clone(label.GroupID), strings.Clone(label.CreatorID)
		label.IssueCount = counts[label.ID]
		if err := read.Reserve(ctx, uint64(len(label.ID)+len(label.Name)+len(label.Color)+len(label.Description)+len(label.Scope)+len(label.ResourceType)+len(label.GroupID)+len(label.CreatorID))+256); err != nil {
			return err
		}
		result = append(result, label)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return read, result, nil
}

// labelResourceCounts is refreshResourceCounts' label counts of data (whose
// issues are paged out) with the issue term given.
func labelResourceCounts(data domain.Bootstrap, issues map[string]int) map[string]int {
	counts := make(map[string]int, len(issues))
	for id, count := range issues {
		counts[id] = count
	}
	add := func(ids []string) {
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			if id != "" && !seen[id] {
				seen[id] = true
				counts[id]++
			}
		}
	}
	for _, project := range data.Projects {
		if project.ArchivedAt == nil {
			add(project.LabelIDs)
		}
	}
	for _, initiative := range data.Initiatives {
		add(initiative.LabelIDs)
	}
	return counts
}

// A literal list whose projection failed answers an empty list; a bounded
// read that cannot run now answers busy, as the other bounded lists do.
func (s *server) labelsBoundedError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrAuthForbidden) && !errors.Is(err, store.ErrResourceAccessChanged) {
		writeJSON(w, http.StatusOK, []domain.IssueLabel{})
		return
	}
	if errors.Is(err, store.ErrResourceAccessChanged) {
		writeAPIError(w, http.StatusServiceUnavailable, apiErrorInternal, "Workspace snapshot is busy; retry later")
		return
	}
	publicResourceDirectoryError(w, err)
}
