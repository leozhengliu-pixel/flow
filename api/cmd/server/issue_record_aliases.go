package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type issueRecordAuthorizationContext struct{}

var errIssueAuthorizationProjectionRefused = errors.New("issue authorization projection refused")

func issueRecordQueryOnly(r *http.Request) bool {
	if pagedRealtimeRequest(r) {
		return true
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.Method == http.MethodGet && (len(parts) == 3 || len(parts) == 4 && (parts[3] == "context" || parts[3] == "history" || parts[3] == "related")) {
		return true
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/issue-records/visibility" {
		return true
	}
	return r.URL.Path == "/api/issue-records" || r.URL.Path == "/api/issue-records/groups" || r.URL.Path == "/api/issue-records/bootstrap"
}

// issueRecordAuthorizationKey carries the authorization data resourceAllowed
// built for this request to the issue-record alias wrapper, which runs right
// after it and before the handler, so both read the same state. Building it
// twice copied the viewer's whole workspace projection twice per comment,
// attachment or relation write.
type issueRecordAuthorizationKey struct{}

type issueRecordAuthorization struct {
	path, workspace, userID string
	data                    domain.Bootstrap
	taken                   atomic.Bool
}

func (s *server) issueRecordAuthorizationData(r *http.Request) (domain.Bootstrap, error) {
	userID := authUser(r).ID
	if cached, ok := r.Context().Value(issueRecordAuthorizationKey{}).(*issueRecordAuthorization); ok && cached.path == r.URL.Path && cached.workspace == workspaceKey(r) && cached.userID == userID && cached.taken.CompareAndSwap(false, true) {
		data := cached.data
		cached.data = domain.Bootstrap{}
		return data, nil
	}
	// The retry rebuilds the request from ctx, so the body holder must already be
	// on it: a body the first attempt read stays available to the second.
	prepareAuthorizationBody(r)
	ctx := r.Context()
	data, err := s.buildIssueRecordAuthorizationData(r)
	for attempt := 1; attempt < 3 && errors.Is(err, store.ErrIssueAuthorizationPolicyChanged) && ctx.Err() == nil; attempt++ {
		// The failed read closed before transferring its output. Rebuild the
		// SQL visibility filter as well as the policy on the next attempt.
		*r = *r.WithContext(context.WithValue(ctx, issueRequestAccessKey{}, nil))
		data, err = s.buildIssueRecordAuthorizationData(r)
	}
	noteAuthorizationBusy(r, err)
	if err == nil {
		cached := &issueRecordAuthorization{path: r.URL.Path, workspace: workspaceKey(r), userID: userID, data: data}
		if _, bounded := r.Context().Value(boundedIssueWriteAuthorizationContext{}).(*store.IssueWriteAuthorizationRead); bounded {
			if !registerRequestReadClose(r.Context(), func() {
				cached.taken.Store(true)
				cached.data = domain.Bootstrap{}
			}) {
				return domain.Bootstrap{}, store.ErrBoundedMetadataCoverage
			}
		}
		*r = *r.WithContext(context.WithValue(r.Context(), issueRecordAuthorizationKey{}, cached))
	}
	return data, err
}

func (s *server) buildIssueRecordAuthorizationData(r *http.Request) (domain.Bootstrap, error) {
	if data, handled, err := s.boundedIssueWriteAuthorizationMetadata(r); handled {
		return data, err
	}
	if s.store.BoundedMetadataEnabled() {
		return s.boundedIssueRecordAuthorizationMetadata(r)
	}
	metadata, query, err := s.issueRecordsQuery(r)
	if err != nil {
		return metadata, err
	}
	commentActionRead := false
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		_, commentActionRead = scaleEscapedRouteParts(r, "/api/issue-records/{id}/comment-actions")
	}
	scoped := false
	if !s.authDisabled {
		if commentActionRead {
			metadata, err = s.store.LegacyIssueViewerPolicy(r.Context(), query.Workspace, authUser(r).ID)
		} else {
			projectIDs, cycleIDs, single, scopeErr := legacyIssueWriteScope(r)
			if scopeErr != nil {
				return metadata, scopeErr
			}
			if single {
				// A single-issue write is authorized against the viewer's
				// policy and the records its body names (issue #244).
				metadata, err = s.store.LegacyIssueWriteAuthorization(r.Context(), query.Workspace, authUser(r).ID, projectIDs, cycleIDs)
				scoped = err == nil
				if errors.Is(err, store.ErrLegacyIssueAuthorizationUnscoped) {
					err = nil
				}
			}
			if !scoped && err == nil {
				// The complete projection copies the whole workspace
				// metadata: it is charged to the shared projection budget.
				if err = acquireIssueAuthorizationProjection(r); err != nil {
					return metadata, err
				}
				metadata, err = s.store.PagedWorkspaceMetadata(r.Context(), query.Workspace, authUser(r).ID)
			}
		}
		if err != nil {
			return metadata, err
		}
		// An issue create or update reads this same projection again before it
		// writes: hand it over (request_metadata.go). Not for API keys, whose
		// filter below edits the projection's collections in place.
		if _, apiKey := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); !apiKey && issueWriteReadsMetadata(r) {
			handOffIssueWriteMetadata(r, query.Workspace, authUser(r).ID, metadata)
		}
		filterBootstrapForAPIKey(&metadata, r)
	}
	data, err := s.issueRecordAuthorizationRows(r, metadata, query, commentActionRead)
	if err == nil && scoped {
		// A handler that reads the viewer's whole projection
		// (workspaceData) still gets it, built when first read.
		*r = *r.WithContext(context.WithValue(r.Context(), issueRecordCompleteProjectionKey{}, &issueRecordCompleteProjection{workspace: query.Workspace, path: r.URL.Path}))
	}
	return data, err
}

// legacyIssueWriteScope reports a write to one existing issue whose
// authorization needs only the viewer's policy, plus the projects and cycles
// its body names. Batch writes and release edits keep the complete
// projection under the projection gate.
func legacyIssueWriteScope(r *http.Request) (projectIDs, cycleIDs []string, ok bool, err error) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || completeIssueWriteReads.Load() {
		return nil, nil, false, nil
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "issue-records" || parts[2] == "" || parts[2] == "batch" || parts[2] == "visibility" {
		return nil, nil, false, nil
	}
	if len(parts) == 4 && parts[3] == "releases" {
		return nil, nil, false, nil
	}
	if r.Method == http.MethodPatch && len(parts) == 3 {
		var input domain.IssueUpdateInput
		if !peekStrictRequestJSON(r, &input) {
			return nil, nil, false, authorizationBodyError(r)
		}
		if input.ProjectID != nil && *input.ProjectID != "" {
			projectIDs = append(projectIDs, *input.ProjectID)
		}
		if input.CycleID != nil && *input.CycleID != "" {
			cycleIDs = append(cycleIDs, *input.CycleID)
		}
	}
	return projectIDs, cycleIDs, true, nil
}

// issueAuthorizationReferences selects the additional issues that a write
// actually names. In particular, a comment's parentId names a comment, and
// an upload does not carry JSON issue references.
func issueAuthorizationReferences(r *http.Request, parts []string) ([]string, error) {
	switch {
	case r.Method == http.MethodPost && len(parts) == 2:
		var input domain.IssueCreateInput
		if !peekRequestJSON(r, &input) {
			return nil, authorizationBodyError(r)
		}
		if input.ParentID != nil && *input.ParentID != "" {
			return []string{*input.ParentID}, nil
		}
	case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "batch":
		var input domain.BatchIssueUpdateInput
		if !peekStrictRequestJSON(r, &input) {
			return nil, authorizationBodyError(r)
		}
		ids := input.IssueIDs
		if input.Update.ParentID != nil && *input.Update.ParentID != "" {
			ids = append(slices.Clone(ids), *input.Update.ParentID)
		}
		return ids, nil
	case r.Method == http.MethodPatch && len(parts) == 3:
		var input domain.IssueUpdateInput
		if !peekStrictRequestJSON(r, &input) {
			return nil, authorizationBodyError(r)
		}
		if input.ParentID != nil && *input.ParentID != "" {
			return []string{*input.ParentID}, nil
		}
	case r.Method == http.MethodPost && len(parts) == 4 && parts[3] == "relations":
		var input domain.RelationCreateInput
		if !peekRequestJSON(r, &input) {
			return nil, authorizationBodyError(r)
		}
		if input.RelatedIssueID != "" {
			return []string{input.RelatedIssueID}, nil
		}
	}
	return nil, nil
}

// completeIssueWriteReads restores the complete reads single-issue writes
// made before issue #244 (authorization, the issue update's checks and the
// triage switch), for the differential that compares both paths. Production
// never sets it.
var completeIssueWriteReads atomic.Bool

// acquireIssueAuthorizationProjection takes the request's projection slot
// before an issue authorization builds the complete projection. Reads are
// refused when the gate is full; writes wait for a slot or for the client to
// leave.
func acquireIssueAuthorizationProjection(r *http.Request) error {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if acquireProjection(r) {
			return nil
		}
		if err := r.Context().Err(); err != nil {
			return err
		}
		return errIssueAuthorizationProjectionRefused
	}
	if acquireProjectionWaiting(r) {
		return nil
	}
	if err := r.Context().Err(); err != nil {
		return err
	}
	return context.Canceled
}

// issueRecordCompleteProjectionKey marks a request whose issue authorization
// was scoped. The handoff (issueRecordAuthorizationContext) then holds the
// scoped projection, and workspaceData builds the projection the handler read
// before: the viewer's complete projection with the authorized issue rows.
type issueRecordCompleteProjectionKey struct{}

type issueRecordCompleteProjection struct {
	workspace, path string
	once            sync.Once
	data            domain.Bootstrap
	err             error
}

func (s *server) completeIssueRecordProjection(r *http.Request, complete *issueRecordCompleteProjection, scoped domain.Bootstrap) (domain.Bootstrap, error) {
	complete.once.Do(func() {
		if complete.err = acquireIssueAuthorizationProjection(r); complete.err != nil {
			return
		}
		data, err := s.store.PagedWorkspaceMetadata(r.Context(), complete.workspace, authUser(r).ID)
		if err != nil {
			complete.err = err
			return
		}
		filterBootstrapForAPIKey(&data, r)
		data.Issues = scoped.Issues
		parts := strings.Split(strings.Trim(complete.path, "/"), "/")
		if len(parts) > 4 && parts[3] == "comments" {
			if comments, ok := scoped.Comments[parts[2]]; ok {
				data.Comments[parts[2]] = comments
			}
		}
		complete.data = data
	})
	return complete.data, complete.err
}

func (s *server) issueRecordAuthorizationRows(r *http.Request, metadata domain.Bootstrap, query store.IssueRecordQuery, commentActionRead bool) (domain.Bootstrap, error) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	ids := []string{}
	if len(parts) > 2 {
		if parts[2] != "batch" {
			ids = append(ids, parts[2])
			if issueIdentifierPattern.MatchString(parts[2]) {
				// The path names the issue by an identifier: its current one or
				// one it had before it moved to another team.
				if id, err := s.store.AuthorizedIssueRecordID(r.Context(), query, parts[2]); err == nil {
					ids = append(ids, id)
				}
			}
		}
	}
	targets, err := issueAuthorizationReferences(r, parts)
	if err != nil {
		return metadata, err
	}
	ids = append(ids, targets...)
	query.Filter = store.IssueFilter{Field: "id", Values: ids}
	query.Archived = "all"
	if len(ids) > 1000 {
		return metadata, store.ErrIssueQuery
	}
	// The legacy release filter clones workspace metadata.
	if commentActionRead && len(query.ReleaseIDs) != 0 && !acquireProjection(r) {
		if err := r.Context().Err(); err != nil {
			return metadata, err
		}
		return metadata, errIssueAuthorizationProjectionRefused
	}
	query.Limit = 500
	query.Cursor = ""
	query.GroupValue, query.GroupValues = nil, nil
	query.TeamIDs = nil
	query.ProjectIDs = nil
	query.StateIDs = nil
	query.Text = ""
	page, err := s.store.QueryIssueRecords(r.Context(), query)
	if err != nil {
		return metadata, err
	}
	metadata.Issues = page.Items
	if page.HasMore {
		query.Cursor = page.NextCursor
		next, err := s.store.QueryIssueRecords(r.Context(), query)
		if err != nil {
			return metadata, err
		}
		metadata.Issues = append(metadata.Issues, next.Items...)
	}
	seen := map[string]bool{}
	for _, issue := range page.Items {
		seen[issue.ID] = true
		parent := issue.ParentID
		for parent != nil && *parent != "" && !seen[*parent] && len(seen) < 64 {
			ancestor, err := s.store.IssueRecord(r.Context(), query.Workspace, *parent)
			if err != nil {
				break
			}
			seen[ancestor.ID] = true
			metadata.Issues = append(metadata.Issues, ancestor)
			parent = ancestor.ParentID
		}
	}
	if len(parts) > 4 && parts[3] == "comments" {
		comment, err := s.store.ResourceComment(r.Context(), query.Workspace, parts[2], parts[4])
		if errors.Is(err, sql.ErrNoRows) {
			return metadata, nil
		}
		if err != nil {
			return metadata, err
		}
		metadata.Comments[parts[2]] = []domain.Comment{comment}
	}
	return metadata, nil
}

// issuePathReference splits an issue route (/api/issue-records/{ref}... or
// /api/issues/{ref}...) into its prefix, the issue reference and the rest of
// the path.
func issuePathReference(path string) (prefix, reference, rest string, ok bool) {
	for _, prefix := range []string{"/api/issue-records/", "/api/issues/"} {
		tail, found := strings.CutPrefix(path, prefix)
		if !found {
			continue
		}
		reference, rest, found = strings.Cut(tail, "/")
		if found {
			rest = "/" + rest
		}
		return prefix, reference, rest, reference != ""
	}
	return "", "", "", false
}

// resolveIssuePathReference rewrites an issue route that names the issue by
// an identifier (its current one, or one it had before it moved to another
// team) to the issue's internal id, so every route under /api/issues/{id} and
// /api/issue-records/{id}, its authorization and its handler see the id. The
// lookup is the viewer's authorized point read (indexed by id, identifier and
// alias); a reference the viewer cannot read is left as it is and answers as
// an unknown id does.
func (s *server) resolveIssuePathReference(r *http.Request) {
	prefix, reference, rest, ok := issuePathReference(r.URL.Path)
	if !ok || !issueIdentifierPattern.MatchString(reference) {
		return
	}
	data, access, err := s.requestIssueQueryAccess(r)
	if err != nil || data.Workspace.URLKey == "" {
		return
	}
	query := store.IssueRecordQuery{Workspace: data.Workspace.URLKey}
	query.Access, query.AllowedTeamIDs = s.issueRecordAccess(r, data, access)
	id, err := s.store.AuthorizedIssueRecordID(r.Context(), query, reference)
	if err != nil || id == "" || id == reference {
		return
	}
	target := *r.URL
	target.Path, target.RawPath = prefix+id+rest, ""
	r.URL = &target
}

func (s *server) issueRecordAlias(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := s.issueRecordAuthorizationData(r)
		if err != nil {
			if !writeAuthorizationBodyError(w, r) && (!errors.Is(err, errIssueAuthorizationProjectionRefused) || !writeProjectionRefusal(w, r)) {
				issueRecordsError(w, err)
			}
			return
		}
		ids := []string{}
		if id := r.PathValue("id"); id != "" {
			ids = append(ids, id)
		}
		if relationID := r.PathValue("relationId"); relationID != "" {
			for _, issue := range data.Issues {
				if issue.ID == r.PathValue("id") {
					for _, relation := range issue.Relations {
						if relation.ID == relationID {
							ids = append(ids, relation.RelatedIssueID)
						}
					}
				}
			}
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		targets, err := issueAuthorizationReferences(r, parts)
		if err != nil {
			writeAuthorizationBodyError(w, r)
			return
		}
		ids = append(ids, targets...)
		var commentID string
		if strings.Contains(r.URL.Path, "/comments") {
			commentID = r.PathValue("commentId")
			if r.Method == http.MethodPost && len(parts) == 4 && parts[3] == "comments" {
				var input domain.CommentCreateInput
				if !peekRequestJSON(r, &input) {
					writeAuthorizationBodyError(w, r)
					return
				}
				if input.ParentID != nil {
					commentID = *input.ParentID
				}
			}
		}
		ctx := store.WithIssueRecordMutations(r.Context(), ids...)
		if strings.Contains(r.URL.Path, "/comments") {
			ctx = store.WithIssueDiscussionMutation(r.Context(), r.PathValue("id"), commentID)
		}
		if _, bounded := ctx.Value(boundedIssueWriteAuthorizationContext{}).(*store.IssueWriteAuthorizationRead); !bounded {
			ctx = context.WithValue(ctx, issueRecordAuthorizationContext{}, data)
		}
		handler(w, r.WithContext(ctx))
	}
}
