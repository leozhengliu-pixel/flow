package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Write-back: comments, replies and thread resolution made in Flow are sent
// to the code host. The local change is always kept; until the code host
// confirms it the event carries Sync (pending, or failed with the error),
// and POST /api/reviews/{id}/sync retries it.

var (
	errReviewParentNotSynced = errors.New("the thread this replies to has not reached the code host yet")
	// errReviewNotResolvable: only inline code threads resolve on the code
	// host; conversation comments resolve in Flow only.
	errReviewNotResolvable = errors.New("only code comment threads can be resolved on the code host")
)

// reviewPushResult is the outcome of sending one local change.
type reviewPushResult struct {
	op       string
	remote   *domain.ReviewEventRemote
	threadID string
	err      error
}

func reviewEventByID(review domain.CodeReview, id string) (domain.ReviewEvent, bool) {
	index := slices.IndexFunc(review.Events, func(event domain.ReviewEvent) bool { return event.ID == id })
	if index < 0 {
		return domain.ReviewEvent{}, false
	}
	return review.Events[index], true
}

// pushReviewComment creates a comment, reply or inline comment on the code host.
func pushReviewComment(ctx context.Context, remote reviewRemote, review domain.CodeReview, event domain.ReviewEvent) (*domain.ReviewEventRemote, error) {
	var root *domain.ReviewEvent
	if event.ParentID != "" {
		parent, ok := reviewEventByID(review, event.ParentID)
		if !ok || parent.Remote == nil {
			return nil, errReviewParentNotSynced
		}
		root = &parent
	}
	if remote.Provider() == domain.CodeHostGitLab {
		return pushGitLabComment(ctx, remote, review, event, root)
	}
	number := strconv.Itoa(review.Number)
	switch {
	case root != nil && root.Remote.Kind == reviewRemoteReviewComment:
		var out githubAPIReviewComment
		if err := remote.REST(ctx, http.MethodPost, "/pulls/"+number+"/comments/"+url.PathEscape(root.Remote.ID)+"/replies", map[string]any{"body": event.Body}, &out); err != nil {
			return nil, err
		}
		return &domain.ReviewEventRemote{Provider: domain.CodeHostGitHub, Kind: reviewRemoteReviewComment, ID: strconv.FormatInt(out.ID, 10), ThreadID: root.Remote.ThreadID, URL: out.HTMLURL}, nil
	case root == nil && event.Path != "" && event.Line > 0:
		commit := review.HeadSHA
		if commit == "" {
			var pull githubAPIPull
			if err := remote.REST(ctx, http.MethodGet, "/pulls/"+number, nil, &pull); err != nil {
				return nil, err
			}
			commit = pull.Head.SHA
		}
		body := map[string]any{"body": event.Body, "commit_id": commit, "path": event.Path, "line": event.Line, "side": githubSide(event.Side)}
		if event.StartLine > 0 && (event.StartLine != event.Line || githubSide(event.StartSide) != githubSide(event.Side)) {
			body["start_line"], body["start_side"] = event.StartLine, githubSide(cmpOr(event.StartSide, event.Side))
		}
		var out githubAPIReviewComment
		if err := remote.REST(ctx, http.MethodPost, "/pulls/"+number+"/comments", body, &out); err != nil {
			return nil, err
		}
		return &domain.ReviewEventRemote{Provider: domain.CodeHostGitHub, Kind: reviewRemoteReviewComment, ID: strconv.FormatInt(out.ID, 10), URL: out.HTMLURL}, nil
	default:
		// Conversation comments, and replies to reviews or conversation
		// comments (GitHub does not thread those).
		var out githubAPIIssueComment
		if err := remote.REST(ctx, http.MethodPost, "/issues/"+number+"/comments", map[string]any{"body": event.Body}, &out); err != nil {
			return nil, err
		}
		return &domain.ReviewEventRemote{Provider: domain.CodeHostGitHub, Kind: reviewRemoteIssueComment, ID: strconv.FormatInt(out.ID, 10), URL: out.HTMLURL}, nil
	}
}

func pushGitLabComment(ctx context.Context, remote reviewRemote, review domain.CodeReview, event domain.ReviewEvent, root *domain.ReviewEvent) (*domain.ReviewEventRemote, error) {
	base := "/merge_requests/" + strconv.Itoa(review.Number)
	if root != nil {
		if root.Remote.ThreadID == "" {
			return nil, errReviewParentNotSynced
		}
		var note gitlabAPINote
		if err := remote.REST(ctx, http.MethodPost, base+"/discussions/"+url.PathEscape(root.Remote.ThreadID)+"/notes", map[string]any{"body": event.Body}, &note); err != nil {
			return nil, err
		}
		return &domain.ReviewEventRemote{Provider: domain.CodeHostGitLab, Kind: reviewRemoteNote, ID: strconv.FormatInt(note.ID, 10), ThreadID: root.Remote.ThreadID}, nil
	}
	body := map[string]any{"body": event.Body}
	if event.Path != "" && event.Line > 0 {
		baseSHA, startSHA, headSHA := review.BaseSHA, review.StartSHA, review.HeadSHA
		if baseSHA == "" || startSHA == "" || headSHA == "" {
			var request gitlabAPIMergeRequest
			if err := remote.REST(ctx, http.MethodGet, base, nil, &request); err != nil {
				return nil, err
			}
			if request.DiffRefs == nil {
				return nil, errors.New("GitLab did not return the merge request's diff refs")
			}
			baseSHA, startSHA, headSHA = request.DiffRefs.BaseSHA, request.DiffRefs.StartSHA, request.DiffRefs.HeadSHA
		}
		oldPath, patch := event.Path, ""
		if index := slices.IndexFunc(review.Files, func(file domain.ReviewFile) bool { return file.Path == event.Path }); index >= 0 {
			oldPath, patch = cmpOr(review.Files[index].PreviousPath, event.Path), review.Files[index].Patch
		}
		position := map[string]any{"position_type": "text", "base_sha": baseSHA, "start_sha": startSHA, "head_sha": headSHA, "new_path": event.Path, "old_path": oldPath}
		// Unchanged lines need both numbers on GitLab.
		counterpart := patchCounterpartLine(patch, githubSide(event.Side), event.Line)
		if githubSide(event.Side) == "LEFT" {
			position["old_line"] = event.Line
			if counterpart > 0 {
				position["new_line"] = counterpart
			}
		} else {
			position["new_line"] = event.Line
			if counterpart > 0 {
				position["old_line"] = counterpart
			}
		}
		body["position"] = position
	}
	var discussion gitlabAPIDiscussion
	if err := remote.REST(ctx, http.MethodPost, base+"/discussions", body, &discussion); err != nil {
		return nil, err
	}
	ref := &domain.ReviewEventRemote{Provider: domain.CodeHostGitLab, Kind: reviewRemoteNote, ThreadID: discussion.ID}
	if len(discussion.Notes) > 0 {
		ref.ID = strconv.FormatInt(discussion.Notes[0].ID, 10)
	}
	return ref, nil
}

// patchCounterpartLine returns, for an unchanged line of a unified patch,
// its number on the other side (0 for added/removed or unknown lines).
func patchCounterpartLine(patch, side string, line int) int {
	oldLine, newLine := 0, 0
	for _, text := range strings.Split(patch, "\n") {
		if strings.HasPrefix(text, "@@") {
			var oldStart, newStart int
			header := strings.Fields(text)
			if len(header) >= 3 {
				oldStart, _ = strconv.Atoi(strings.SplitN(strings.TrimPrefix(header[1], "-"), ",", 2)[0])
				newStart, _ = strconv.Atoi(strings.SplitN(strings.TrimPrefix(header[2], "+"), ",", 2)[0])
			}
			oldLine, newLine = oldStart, newStart
			continue
		}
		if oldLine == 0 && newLine == 0 {
			continue
		}
		switch {
		case strings.HasPrefix(text, "+"):
			newLine++
		case strings.HasPrefix(text, "-"):
			oldLine++
		case strings.HasPrefix(text, "\\"):
		default:
			if side == "LEFT" && oldLine == line {
				return newLine
			}
			if side != "LEFT" && newLine == line {
				return oldLine
			}
			oldLine++
			newLine++
		}
	}
	return 0
}

const (
	githubResolveThreadMutation   = `mutation($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { id isResolved } } }`
	githubUnresolveThreadMutation = `mutation($id: ID!) { unresolveReviewThread(input: {threadId: $id}) { thread { id isResolved } } }`
)

// pushReviewThreadResolution resolves or unresolves the code host thread of
// root and returns its thread id.
func pushReviewThreadResolution(ctx context.Context, remote reviewRemote, review domain.CodeReview, root domain.ReviewEvent, resolved bool) (string, error) {
	if root.Remote == nil {
		return "", errReviewParentNotSynced
	}
	if remote.Provider() == domain.CodeHostGitLab {
		if root.Remote.ThreadID == "" {
			return "", errReviewNotResolvable
		}
		path := "/merge_requests/" + strconv.Itoa(review.Number) + "/discussions/" + url.PathEscape(root.Remote.ThreadID) + "?resolved=" + strconv.FormatBool(resolved)
		return root.Remote.ThreadID, remote.REST(ctx, http.MethodPut, path, nil, nil)
	}
	if root.Remote.Kind != reviewRemoteReviewComment {
		return "", errReviewNotResolvable
	}
	threadID := root.Remote.ThreadID
	if threadID == "" {
		threads, err := fetchGitHubReviewThreads(ctx, remote, review)
		if err != nil {
			return "", err
		}
		for _, thread := range threads {
			if slices.ContainsFunc(thread.Comments.Nodes, func(node githubThreadComment) bool { return strconv.FormatInt(node.DatabaseID, 10) == root.Remote.ID }) {
				threadID = thread.ID
				break
			}
		}
		if threadID == "" {
			return "", errors.New("GitHub has no review thread for this comment yet")
		}
	}
	mutation := githubUnresolveThreadMutation
	if resolved {
		mutation = githubResolveThreadMutation
	}
	return threadID, remote.GraphQL(ctx, mutation, map[string]any{"id": threadID}, nil)
}

// pushPendingReviewChanges sends every local change still waiting for the
// code host, oldest first (so thread roots exist before their replies).
func pushPendingReviewChanges(ctx context.Context, remote reviewRemote, review domain.CodeReview) map[string]reviewPushResult {
	results := map[string]reviewPushResult{}
	working := review
	working.Events = slices.Clone(review.Events)
	order := make([]int, 0)
	for index, event := range working.Events {
		if event.Sync != nil {
			order = append(order, index)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int { return working.Events[a].CreatedAt.Compare(working.Events[b].CreatedAt) })
	for _, index := range order {
		event := working.Events[index]
		switch event.Sync.Op {
		case "create":
			if event.Remote != nil {
				results[event.ID] = reviewPushResult{op: "create", remote: event.Remote}
				continue
			}
			ref, err := pushReviewComment(ctx, remote, working, event)
			results[event.ID] = reviewPushResult{op: "create", remote: ref, err: err}
			if err == nil {
				working.Events[index].Remote = ref
			}
		case "resolve", "unresolve":
			threadID, err := pushReviewThreadResolution(ctx, remote, working, event, event.Sync.Op == "resolve")
			results[event.ID] = reviewPushResult{op: event.Sync.Op, threadID: threadID, err: err}
		}
	}
	return results
}

// applyReviewPushResults records push outcomes on the review: refs of
// created comments (dropping a copy a webhook may have synced meanwhile),
// found thread ids, and failures with their error.
func applyReviewPushResults(review *domain.CodeReview, provider string, results map[string]reviewPushResult, now time.Time) {
	if len(results) == 0 {
		return
	}
	events := slices.Clone(review.Events)
	drop := map[string]string{} // synced duplicate id → local event id
	for index := range events {
		event := events[index]
		result, ok := results[event.ID]
		if !ok {
			continue
		}
		if event.Sync == nil || event.Sync.Op != result.op {
			// Confirmed by a webhook meanwhile, or changed again since the push started.
			continue
		}
		if result.err != nil {
			if errors.Is(result.err, errReviewNotResolvable) {
				event.Sync = nil
			} else {
				event.Sync = &domain.ReviewEventSync{State: "failed", Op: result.op, Error: reviewSyncErrorMessage(provider, result.err), AttemptedAt: now}
			}
			events[index] = event
			continue
		}
		event.Sync = nil
		if result.remote != nil {
			ref := *result.remote
			event.Remote = &ref
			key := reviewRemoteKey(provider, event.Remote)
			for _, other := range events {
				if other.ID != event.ID && reviewRemoteKey(provider, other.Remote) == key {
					drop[other.ID] = event.ID
				}
			}
		}
		if result.threadID != "" && event.Remote != nil {
			ref := *event.Remote
			ref.ThreadID = result.threadID
			event.Remote = &ref
		}
		events[index] = event
	}
	if len(drop) > 0 {
		events = slices.DeleteFunc(events, func(event domain.ReviewEvent) bool { return drop[event.ID] != "" })
		for index := range events {
			if local, ok := drop[events[index].ParentID]; ok {
				events[index].ParentID = local
			}
		}
	}
	review.Events = events
}

// writeBackReviewChange pushes the given local events (just committed with
// Sync pending) and records the outcome; it returns the updated review.
func (s *server) writeBackReviewChange(ctx context.Context, workspace string, remote reviewRemote, reviewID string, eventIDs ...string) (domain.CodeReview, error) {
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		return domain.CodeReview{}, errNotFound
	}
	review, err := reviewByID(data, reviewID)
	if err != nil {
		return review, err
	}
	pending := review
	pending.Events = slices.Clone(review.Events)
	for index := range pending.Events {
		// Only the requested changes; others wait for the next sync.
		if !slices.Contains(eventIDs, pending.Events[index].ID) && pending.Events[index].Sync != nil {
			pending.Events[index].Sync = nil
		}
	}
	pushCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	results := pushPendingReviewChanges(pushCtx, remote, pending)
	var updated domain.CodeReview
	err = s.store.MutateWorkspace(ctx, workspace, "review.written_back", review.ID, map[string]any{"events": eventIDs}, func(data *domain.Bootstrap) error {
		index := reviewIndex(*data, review.ID)
		if index < 0 {
			return errNotFound
		}
		current := data.Reviews[index]
		applyReviewPushResults(&current, review.Provider, results, time.Now().UTC())
		data.Reviews[index], updated = current, current
		return nil
	})
	return updated, err
}
