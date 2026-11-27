package main

import (
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) deleteIssue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	workspace := workspaceKey(r)
	bounded := s.store.BoundedMetadataEnabled()
	if workspace == "" {
		if bounded {
			metadata, _ := s.store.WorkspaceSettingsMetadata("")
			workspace = metadata.Workspace.URLKey
		} else {
			workspace = s.store.Bootstrap().Workspace.URLKey
		}
	}
	keep := r.URL.Query().Get("subIssues") == "keep"
	records := store.UsesIssueRecordMutations(ctx)
	var trashed, documentIDs []string
	payload := map[string]any{"id": id}
	mutate := func(data *domain.Bootstrap) error {
		if issueIndexByID(data, id) < 0 {
			return errNotFound
		}
		if records {
			// The legacy scoped writer gathers its rows from a map.
			slices.SortFunc(data.Issues, func(a, b domain.Issue) int { return strings.Compare(a.ID, b.ID) })
			slices.SortStableFunc(data.Notifications, func(a, b domain.Notification) int {
				return strings.Compare(issueDeleteNotificationKey(a), issueDeleteNotificationKey(b))
			})
		}
		// Trash sub-issues deepest first. Keep detaches direct children.
		var descendants, children []string
		if !keep {
			descendants = issueDescendants(data, id)
			for _, issue := range data.Issues {
				if issue.ParentID != nil && *issue.ParentID == id {
					children = append(children, issue.ID)
				}
			}
		}
		// Each retry captures the audience and cleanup from its own preimage.
		trashed, documentIDs = nil, nil
		delete(payload, "issueIds")
		delete(payload, "issueAccess")
		access := deletedIssueAccessEntries(data, append([]string{id}, descendants...))
		for index := len(descendants) - 1; index >= 0; index-- {
			documentID, err := trashIssue(data, descendants[index], id, nil)
			if err != nil {
				return err
			}
			trashed, documentIDs = append(trashed, descendants[index]), append(documentIDs, documentID)
		}
		documentID, err := trashIssue(data, id, "", children)
		if err != nil {
			return err
		}
		trashed, documentIDs = append(trashed, id), append(documentIDs, documentID)
		payload["issueIds"] = slices.Clone(trashed)
		payload["issueAccess"] = access
		return nil
	}
	var err error
	// boundedLegacyIssueWrites sends both production routes through the
	// record alias before request authorization and dispatch.
	if bounded && records {
		selection := store.IssueDeleteSelection{ID: id, KeepChildren: keep}
		if !s.authDisabled {
			selection.Principal = authUser(r).ID
			if key, ok := ctx.Value(apiKeyContextKey{}).(domain.APIKey); ok {
				selection.Key = &key
			}
		}
		err = s.store.DeleteIssueScoped(ctx, workspace, selection, payload, func(data domain.Bootstrap, issue domain.Issue) bool {
			return issuePermissionRank(issueRole(s, data, issue)) >= issuePermissionRank("editor")
		}, mutate)
	} else {
		if records && !keep {
			// The scoped writer loads the whole sub-issue family, not only
			// the direct children, so nested sub-issues are trashed too.
			ctx = store.WithIssueDeleteFamily(ctx)
		}
		err = s.store.MutateWorkspace(ctx, workspace, "issue.deleted", id, payload, mutate)
	}
	if errors.Is(err, store.ErrAuthForbidden) && errors.Is(err, store.ErrResourceAccessChanged) {
		writeError(w, http.StatusForbidden, "This resource is outside your teams")
		return
	}
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	for _, documentID := range documentIDs {
		if err := s.store.DeleteDocumentCollaborationDocument(ctx, workspace, documentID); err != nil {
			log.Print("could not delete issue collaboration document")
		}
	}
	s.pruneIssueSuggestionTargets(ctx, workspace, func(item domain.IssueSuggestion) bool {
		return (item.Type == "similarIssue" || item.Type == "relatedIssue") && slices.Contains(trashed, item.SuggestedIssueID)
	})
	w.WriteHeader(http.StatusNoContent)
}

func issueDeleteNotificationKey(notification domain.Notification) string {
	if notification.IssueID == "" {
		return notification.SourceID
	}
	return notification.IssueID
}
