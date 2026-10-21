package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// linkPreview is what Notion shows for a pasted Flow link (the Notion
// connection of Settings › Account › Connected accounts).
type linkPreview struct {
	Kind       string    `json:"kind"` // issue | project
	URL        string    `json:"url"`
	Identifier string    `json:"identifier,omitempty"`
	Title      string    `json:"title"`
	Status     string    `json:"status,omitempty"`
	StatusType string    `json:"statusType,omitempty"`
	Assignee   string    `json:"assignee,omitempty"` // an issue's assignee, a project's lead
	Team       string    `json:"team,omitempty"`
	Priority   string    `json:"priority,omitempty"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// getLinkPreview — GET /api/notion/link-preview?url=: the preview of a link to an
// issue or a project of this deployment, for the signed-in member (their
// session, or the OAuth token Notion holds for them). It answers only while
// the member has the Notion connection on, and shows only what they can open:
// the lookups are the issue and project reads' own authorization.
func (s *server) getLinkPreview(w http.ResponseWriter, r *http.Request) {
	user := authUser(r)
	if user.ID == "" {
		writeError(w, http.StatusUnauthorized, "sign in to preview links")
		return
	}
	if _, err := s.store.PersonalConnectionFor(r.Context(), user.ID, domain.PersonalConnectionNotion); err != nil {
		if errors.Is(err, store.ErrPersonalConnectionNotFound) {
			writeError(w, http.StatusForbidden, "Turn on Notion in Settings › Connected accounts to preview links")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not read the connection")
		return
	}
	link, err := url.Parse(strings.TrimSpace(r.URL.Query().Get("url")))
	base, baseErr := url.Parse(appBaseURL())
	if err != nil || baseErr != nil || link.User != nil || !strings.EqualFold(link.Scheme, base.Scheme) || !strings.EqualFold(link.Host, base.Host) {
		writeError(w, http.StatusBadRequest, "Only links to this Flow workspace can be previewed")
		return
	}
	segments := strings.Split(strings.Trim(link.Path, "/"), "/")
	if len(segments) < 3 || segments[0] == "" || segments[2] == "" {
		writeError(w, http.StatusNotFound, "There is no preview for this link")
		return
	}
	workspace, kind, ref := segments[0], segments[1], segments[2]
	if unescaped, err := url.PathUnescape(workspace); err == nil {
		workspace = unescaped
	}
	if unescaped, err := url.PathUnescape(ref); err == nil {
		ref = unescaped
	}
	scoped := r.Clone(r.Context())
	scopedURL := *r.URL
	values := scopedURL.Query()
	values.Set("workspace", workspace)
	scopedURL.RawQuery = values.Encode()
	scoped.URL = &scopedURL
	canonical := base.Scheme + "://" + base.Host + "/" + url.PathEscape(workspace) + "/" + kind + "/" + url.PathEscape(ref)

	switch kind {
	case "issue":
		_, query, err := s.issueRecordsQuery(scoped)
		if err != nil {
			issueRecordsError(w, err)
			return
		}
		issue, err := s.store.AuthorizedIssueRecord(r.Context(), query, ref)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "issue not found")
			return
		}
		if err != nil {
			issueRecordsError(w, err)
			return
		}
		preview := linkPreview{Kind: "issue", URL: canonical, Identifier: issue.Identifier, Title: issue.Title, Status: issue.State.Name, StatusType: issue.State.Type, Team: issue.Team.Name, Priority: issue.PriorityLabel, UpdatedAt: issue.UpdatedAt}
		if issue.Assignee != nil {
			preview.Assignee = firstNonEmpty(issue.Assignee.DisplayName, issue.Assignee.Name)
		}
		writeJSON(w, http.StatusOK, preview)
	case "project":
		data, ok := s.publicMetadata(w, scoped)
		if !ok {
			return
		}
		index := domain.ProjectIndexBySlugRef(data.Projects, ref)
		if index < 0 {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		project := data.Projects[index]
		preview := linkPreview{Kind: "project", URL: canonical, Title: project.Name, Status: project.Status.Name, StatusType: project.Status.Type, Priority: project.PriorityLabel, UpdatedAt: project.UpdatedAt}
		if project.Lead != nil {
			preview.Assignee = firstNonEmpty(project.Lead.DisplayName, project.Lead.Name)
		}
		writeJSON(w, http.StatusOK, preview)
	default:
		writeError(w, http.StatusNotFound, "There is no preview for this link")
	}
}
