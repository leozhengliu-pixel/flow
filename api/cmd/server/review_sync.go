package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Review sync: files, inline comment threads, conversation comments and
// reviews of a GitHub pull request / GitLab merge request are mirrored into
// the CodeReview (Files, Events). Every synced event carries its Remote ref,
// so full syncs, webhooks and write-back update one event per code host
// object. Local changes that have not reached the code host carry Sync.

// remoteReviewAuthor is a code host account.
type remoteReviewAuthor struct {
	Login, Name, AvatarURL, Email string
}

// remoteReviewComment is one comment, review or note on the code host.
type remoteReviewComment struct {
	Kind, ID string
	// ThreadID: GitHub review thread node id / GitLab discussion id.
	ThreadID string
	// ParentKind/ParentID: the thread root this comment replies to.
	ParentKind, ParentID string
	Type                 string
	Body, Path           string
	Line, StartLine      int
	Side, StartSide      string
	Outdated             bool
	// Resolved is nil when the source does not say (webhook comment events).
	Resolved                *bool
	CommitID, DiffHunk, URL string
	Author                  remoteReviewAuthor
	CreatedAt, UpdatedAt    time.Time
}

func (c remoteReviewComment) key() string { return c.Kind + ":" + c.ID }

// reviewSnapshot is what one fetch read from the code host.
type reviewSnapshot struct {
	Provider                   string
	HeadSHA, BaseSHA, StartSHA string
	// Pull is the pull/merge request itself (nil: unchanged).
	Pull *remotePullRequest
	// Files replaces the review's files unless nil.
	Files    []domain.ReviewFile
	Comments []remoteReviewComment
	// Complete: Comments is every comment of the request, so synced events
	// missing from it were deleted on the code host.
	Complete bool
}

type remotePullRequest struct {
	Title, Description, BaseBranch, HeadBranch, URL string
	Draft, Merged, Closed                           bool
	Additions, Deletions, Commits                   int
}

const (
	reviewRemoteReviewComment = "review_comment"
	reviewRemoteIssueComment  = "issue_comment"
	reviewRemoteReview        = "review"
	reviewRemoteNote          = "note"
)

func reviewRemoteKey(provider string, remote *domain.ReviewEventRemote) string {
	if remote == nil || remote.Provider != provider || remote.ID == "" {
		return ""
	}
	return remote.Kind + ":" + remote.ID
}

// syncedReviewEventID is the event id of a comment first seen on the code host.
func syncedReviewEventID(provider string, comment remoteReviewComment) string {
	return provider + "-" + comment.Kind + "-" + comment.ID
}

// reviewActorResolver maps code host accounts to Flow users: an
// application's GitHub username, then a member whose username, name or
// email matches the login; anyone else becomes an external actor.
func reviewActorResolver(data *domain.Bootstrap, provider string, appUsers map[string]domain.User) func(remoteReviewAuthor) domain.User {
	return func(author remoteReviewAuthor) domain.User {
		login := strings.TrimSpace(author.Login)
		if login == "" {
			login = "ghost"
		}
		if user, ok := appUsers[strings.ToLower(login)]; ok {
			return user
		}
		for _, user := range data.Users {
			local := strings.SplitN(user.Email, "@", 2)[0]
			if strings.EqualFold(login, user.Username) || strings.EqualFold(login, user.Name) || strings.EqualFold(login, user.DisplayName) || strings.EqualFold(login, local) ||
				(author.Email != "" && strings.EqualFold(author.Email, user.Email)) {
				return user
			}
		}
		name := strings.TrimSpace(author.Name)
		if name == "" {
			name = login
		}
		return domain.User{ID: provider + ":" + login, Name: login, DisplayName: name, Username: login, AvatarURL: author.AvatarURL}
	}
}

// mergeReviewSnapshot applies a snapshot to a review. It is idempotent:
// events are matched by their Remote ref (or, for a local comment whose
// write-back has not recorded its ref yet, by content), replies are linked
// to their root, thread state is mirrored on replies and, for a complete
// snapshot, synced events deleted on the code host are dropped.
func mergeReviewSnapshot(review *domain.CodeReview, snapshot reviewSnapshot, actor func(remoteReviewAuthor) domain.User, now time.Time) {
	provider := snapshot.Provider
	if snapshot.HeadSHA != "" {
		review.HeadSHA = snapshot.HeadSHA
	}
	if snapshot.BaseSHA != "" {
		review.BaseSHA = snapshot.BaseSHA
	}
	if snapshot.StartSHA != "" {
		review.StartSHA = snapshot.StartSHA
	}
	if pull := snapshot.Pull; pull != nil {
		if pull.Title != "" {
			review.Title = pull.Title
		}
		review.Description = pull.Description
		if pull.BaseBranch != "" {
			review.BaseBranch, review.HeadBranch = pull.BaseBranch, pull.HeadBranch
		}
		if pull.URL != "" {
			review.URL = pull.URL
		}
		review.Draft = pull.Draft
		if pull.Commits > 0 {
			review.CommitCount = pull.Commits
		}
		switch {
		case pull.Merged && review.Status != "merged":
			review.Status, review.MergedAt, review.ClosedAt = "merged", &now, nil
		case pull.Closed && !pull.Merged && review.Status != "closed":
			review.Status, review.ClosedAt, review.MergedAt = "closed", &now, nil
		case !pull.Merged && !pull.Closed && (review.Status == "merged" || review.Status == "closed"):
			review.Status, review.ClosedAt, review.MergedAt = "open", nil, nil
		}
		if pull.Additions > 0 || pull.Deletions > 0 {
			review.Additions, review.Deletions = pull.Additions, pull.Deletions
		}
	}
	if snapshot.Files != nil {
		review.Files = snapshot.Files
		if snapshot.Pull == nil || (snapshot.Pull.Additions == 0 && snapshot.Pull.Deletions == 0) {
			review.Additions, review.Deletions = 0, 0
			for _, file := range snapshot.Files {
				review.Additions += file.Additions
				review.Deletions += file.Deletions
			}
		}
	}
	events := slices.Clone(review.Events)
	byKey := map[string]int{}
	for index, event := range events {
		if key := reviewRemoteKey(provider, event.Remote); key != "" {
			byKey[key] = index
		}
	}
	comments := slices.Clone(snapshot.Comments)
	// Roots before replies, oldest first.
	slices.SortStableFunc(comments, func(a, b remoteReviewComment) int {
		if (a.ParentID == "") != (b.ParentID == "") {
			if a.ParentID == "" {
				return -1
			}
			return 1
		}
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	seen := map[string]bool{}
	for _, comment := range comments {
		key := comment.key()
		seen[key] = true
		parentID := ""
		if comment.ParentID != "" {
			if index, ok := byKey[comment.ParentKind+":"+comment.ParentID]; ok {
				parentID = events[index].ID
			}
		}
		index, ok := byKey[key]
		if !ok {
			// A comment Flow just wrote whose remote ref is not recorded yet.
			index = slices.IndexFunc(events, func(event domain.ReviewEvent) bool {
				return event.Remote == nil && event.Sync != nil && event.Sync.Op == "create" &&
					strings.TrimSpace(event.Body) == strings.TrimSpace(comment.Body) && event.Path == comment.Path && (comment.Path == "" || event.Line == comment.Line)
			})
			ok = index >= 0
		}
		if ok {
			events[index] = applyRemoteReviewComment(events[index], provider, comment, parentID, actor)
			byKey[key] = index
			continue
		}
		event := applyRemoteReviewComment(domain.ReviewEvent{ID: syncedReviewEventID(provider, comment), CreatedAt: comment.CreatedAt}, provider, comment, parentID, actor)
		if event.CreatedAt.IsZero() {
			event.CreatedAt = now
		}
		events = append(events, event)
		byKey[key] = len(events) - 1
	}
	if snapshot.Complete {
		removed := map[string]bool{}
		for _, event := range events {
			if key := reviewRemoteKey(provider, event.Remote); key != "" && !seen[key] {
				removed[event.ID] = true
			}
		}
		events = slices.DeleteFunc(events, func(event domain.ReviewEvent) bool {
			// Replies of a deleted root go with it unless they still exist remotely.
			return removed[event.ID] || (removed[event.ParentID] && reviewRemoteKey(provider, event.Remote) == "")
		})
	}
	propagateReviewThreadState(events)
	slices.SortStableFunc(events, func(a, b domain.ReviewEvent) int { return a.CreatedAt.Compare(b.CreatedAt) })
	review.Events = events
}

func applyRemoteReviewComment(event domain.ReviewEvent, provider string, comment remoteReviewComment, parentID string, actor func(remoteReviewAuthor) domain.User) domain.ReviewEvent {
	synced := strings.HasPrefix(event.ID, provider+"-")
	if synced || event.Actor.ID == "" {
		// Comments written in Flow keep their Flow author (the code host shows
		// the App or token owner).
		event.Actor = actor(comment.Author)
	}
	if synced || event.Type == "" {
		event.Type = comment.Type
	}
	if (synced || event.CreatedAt.IsZero()) && !comment.CreatedAt.IsZero() {
		event.CreatedAt = comment.CreatedAt
	}
	event.Body = comment.Body
	if comment.Path != "" || synced {
		event.Path, event.Line, event.Side = comment.Path, comment.Line, comment.Side
		event.StartLine, event.StartSide = comment.StartLine, comment.StartSide
		event.Outdated, event.CommitID, event.DiffHunk = comment.Outdated, comment.CommitID, comment.DiffHunk
	}
	if comment.ParentID != "" && parentID != "" {
		event.ParentID = parentID
	}
	pendingResolution := event.Sync != nil && (event.Sync.Op == "resolve" || event.Sync.Op == "unresolve")
	if comment.Resolved != nil && !pendingResolution {
		event.Resolved = *comment.Resolved
	}
	threadID := comment.ThreadID
	if threadID == "" && event.Remote != nil {
		threadID = event.Remote.ThreadID
	}
	event.Remote = &domain.ReviewEventRemote{Provider: provider, Kind: comment.Kind, ID: comment.ID, ThreadID: threadID, URL: comment.URL}
	if event.Sync != nil && event.Sync.Op == "create" {
		event.Sync = nil
	}
	if !comment.UpdatedAt.IsZero() && comment.UpdatedAt.Sub(event.CreatedAt) > time.Second {
		updated := comment.UpdatedAt
		event.UpdatedAt = &updated
	}
	return event
}

// propagateReviewThreadState mirrors a thread root's resolved state on its replies.
func propagateReviewThreadState(events []domain.ReviewEvent) {
	roots := map[string]bool{}
	for _, event := range events {
		if event.ParentID == "" {
			roots[event.ID] = event.Resolved
		}
	}
	for index := range events {
		if resolved, ok := roots[events[index].ParentID]; ok && events[index].ParentID != "" {
			events[index].Resolved = resolved
		}
	}
}

// removeRemoteReviewEvents drops the events of deleted code host comments.
func removeRemoteReviewEvents(review *domain.CodeReview, provider string, keys ...string) bool {
	drop := map[string]bool{}
	for _, key := range keys {
		drop[key] = true
	}
	before := len(review.Events)
	review.Events = slices.DeleteFunc(slices.Clone(review.Events), func(event domain.ReviewEvent) bool {
		return drop[reviewRemoteKey(provider, event.Remote)]
	})
	return len(review.Events) != before
}

// trimReviewDeliveryEvents bounds the webhook delivery trail of a review
// without dropping comments, reviews or changes awaiting the code host.
func trimReviewDeliveryEvents(events []domain.ReviewEvent, limit int) []domain.ReviewEvent {
	delivery := func(event domain.ReviewEvent) bool {
		return event.Remote == nil && event.Sync == nil && event.ParentID == "" && !reviewCommentEvent(event)
	}
	count := 0
	for _, event := range events {
		if delivery(event) {
			count++
		}
	}
	if count <= limit {
		return events
	}
	extra := count - limit
	return slices.DeleteFunc(events, func(event domain.ReviewEvent) bool {
		if extra > 0 && delivery(event) {
			extra--
			return true
		}
		return false
	})
}

// fetchReviewSnapshot reads everything the review mirrors from its code host.
func fetchReviewSnapshot(ctx context.Context, remote reviewRemote, review domain.CodeReview) (reviewSnapshot, error) {
	if remote.Provider() == domain.CodeHostGitLab {
		return fetchGitLabReviewSnapshot(ctx, remote, review)
	}
	return fetchGitHubReviewSnapshot(ctx, remote, review)
}

// reviewDefaultPageSize is the page size of every paginated read, except
// those that name their own (see fetchPagesSized).
const reviewDefaultPageSize = 100

// fetchPages reads a paginated list endpoint (per_page=100) until a short
// page. Hitting maxPages stops silently; use fetchPagesSized when a
// truncated list must be told apart.
func fetchPages[T any](ctx context.Context, remote reviewRemote, path string, maxPages int) ([]T, error) {
	all, _, err := fetchPagesSized[T](ctx, remote, path, reviewDefaultPageSize, maxPages)
	return all, err
}

// reviewPagePath sets page and per_page on path's query, keeping every other
// parameter and replacing any page or per_page the path already carried.
func reviewPagePath(path string, pageSize, page int) (string, error) {
	target, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("invalid paginated path: %w", err)
	}
	query := target.Query()
	query.Set("per_page", strconv.Itoa(pageSize))
	query.Set("page", strconv.Itoa(page))
	target.RawQuery = query.Encode()
	return target.String(), nil
}

// fetchPagesSized reads a paginated list endpoint with an explicit page size
// until a short page. truncated is true when maxPages was used up on a full
// page, so more items may remain.
func fetchPagesSized[T any](ctx context.Context, remote reviewRemote, path string, pageSize, maxPages int) (all []T, truncated bool, err error) {
	all = []T{}
	for page := 1; page <= maxPages; page++ {
		target, err := reviewPagePath(path, pageSize, page)
		if err != nil {
			return nil, false, err
		}
		var items []T
		if err := remote.REST(ctx, http.MethodGet, target, nil, &items); err != nil {
			return nil, false, err
		}
		all = append(all, items...)
		if len(items) < pageSize {
			return all, false, nil
		}
	}
	return all, true, nil
}

// reviewSyncFreshness: a non-forced sync (opening the review) is skipped
// when the review was synced this recently.
const reviewSyncFreshness = 30 * time.Second

var errReviewNotSynced = errors.New("review has no reachable code host")

// syncReview pushes local changes still waiting for the code host, then
// fetches the pull/merge request and merges it into the review.
func (s *server) syncReview(ctx context.Context, workspace, reviewID string, force bool) (domain.CodeReview, error) {
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		return domain.CodeReview{}, errNotFound
	}
	review, err := reviewByID(data, reviewID)
	if err != nil {
		return review, err
	}
	if !force && review.SyncedAt != nil && time.Since(*review.SyncedAt) < reviewSyncFreshness {
		return review, nil
	}
	// Reads use the workspace credentials; each pending change is retried as
	// its member (their linked account first), with the same rules as when
	// it was made, so a member's change can be retried even when the
	// workspace itself cannot reach the code host.
	remote, err := s.reviewRemoteFor(ctx, data, review, true)
	pending := slices.ContainsFunc(review.Events, func(event domain.ReviewEvent) bool { return event.Sync != nil })
	if remote == nil && err == nil && !pending {
		return review, errReviewNotSynced
	}
	var snapshot reviewSnapshot
	pushed := pushPendingReviewChanges(ctx, s.memberReviewWriter(ctx, data, review), review)
	fetched := false
	if remote != nil && err == nil {
		snapshot, err = fetchReviewSnapshot(ctx, remote, review)
		fetched = err == nil
	}
	appUsers := map[string]domain.User{}
	if review.Provider == domain.CodeHostGitHub {
		appUsers = s.githubAppUsers(ctx, workspace)
	}
	now := time.Now().UTC()
	var updated domain.CodeReview
	scope := store.WithMutationScope(ctx, store.MutationScope{Resources: []string{review.ID}})
	mutateErr := s.store.MutateWorkspace(scope, workspace, "review.synced", review.ID, map[string]any{"provider": review.Provider, "force": force}, func(data *domain.Bootstrap) error {
		index := reviewIndex(*data, review.ID)
		if index < 0 {
			return errNotFound
		}
		current := data.Reviews[index]
		applyReviewPushResults(&current, review.Provider, pushed, now)
		switch {
		case err != nil:
			current.SyncError = reviewSyncErrorMessage(review.Provider, err)
		case fetched:
			mergeReviewSnapshot(&current, snapshot, reviewActorResolver(data, review.Provider, appUsers), now)
			current.SyncError = ""
		}
		if fetched || err != nil {
			current.SyncedAt = &now
		}
		data.Reviews[index], updated = current, current
		return nil
	})
	if mutateErr != nil {
		return updated, mutateErr
	}
	return updated, err
}

// syncReviewFromCodeHost — POST /api/reviews/{id}/sync: syncs files and
// comments from the code host and retries local changes that did not reach
// it. {"force": false} skips a review synced in the last 30 seconds.
func (s *server) syncReviewFromCodeHost(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Force *bool `json:"force,omitempty"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	force := input.Force == nil || *input.Force
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	data := s.workspaceData(r)
	review, err := reviewByID(data, r.PathValue("id"))
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	updated, err := s.syncReview(ctx, data.Workspace.URLKey, review.ID, force)
	switch {
	case errors.Is(err, errReviewNotSynced):
		writeJSON(w, http.StatusOK, review)
	case errors.Is(err, errNotFound):
		respondMutation(w, err, http.StatusOK, nil)
	case updated.ID == "":
		writeError(w, http.StatusInternalServerError, "could not sync the review")
	default:
		// A code host failure is recorded on the review (syncError).
		writeJSON(w, http.StatusOK, updated)
	}
}

// reviewSyncRunner runs background syncs (tests replace it).
var reviewSyncRunner = func(job func()) { go job() }

var reviewSyncInFlight sync.Map

// queueReviewSync syncs a review in the background (webhooks), once at a time per review.
func (s *server) queueReviewSync(workspace, reviewID string) {
	if workspace == "" || reviewID == "" {
		return
	}
	key := workspace + "/" + reviewID
	if _, busy := reviewSyncInFlight.LoadOrStore(key, true); busy {
		return
	}
	reviewSyncRunner(func() {
		defer reviewSyncInFlight.Delete(key)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := s.syncReview(ctx, workspace, reviewID, true); err != nil && !errors.Is(err, errReviewNotSynced) {
			log.Printf("review sync %s: %v", key, err)
		}
	})
}

// findSyncedReview finds a workspace review by code host, repository and number.
func findSyncedReview(data domain.Bootstrap, provider, repository string, number int) (domain.CodeReview, bool) {
	for _, review := range data.Reviews {
		if review.Provider == provider && review.Number == number && strings.EqualFold(review.RepositoryOwner+"/"+review.RepositoryName, repository) {
			return review, true
		}
	}
	return domain.CodeReview{}, false
}
