package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Review import: webhooks are not the only way Flow learns about pull and
// merge requests. The import lists the open PRs of every repository the
// workspace's GitHub App installations and classic GitHub connections
// reach, and the open MRs of the projects its GitLab connections reach
// (gitlab.com or self-managed), then creates or refreshes their reviews.
// Files and comments come from the same full sync as "Sync with GitHub"
// (syncReview). It runs from "Sync now" on the Reviews list and in the
// background for workspaces whose webhooks are not known to work.
//
// It is idempotent with webhooks: reviews are matched by code host and
// external id (or repository and number) inside the workspace mutation, so
// a webhook and an import racing for the same request leave one review.

const (
	// reviewImportMaxRepositoryPages bounds repository listings (100 a page).
	reviewImportMaxRepositoryPages = 20
	// reviewImportMaxPullPages bounds the open requests listed per repository.
	reviewImportMaxPullPages = 10
	// reviewImportRateReserve: a run stops when a token has fewer requests
	// left than this, so members' own actions keep working.
	reviewImportRateReserve = 100
	// reviewImportWebhookStale: a webhook that delivered nothing for this
	// long is not counted as working.
	reviewImportWebhookStale    = 7 * 24 * time.Hour
	reviewImportDefaultInterval = 10 * time.Minute
)

// reviewImportSettings is the deployment configuration of the background
// import: FLOW_REVIEW_IMPORT (auto | always | off) and
// FLOW_REVIEW_IMPORT_INTERVAL (a Go duration, 0 turns it off).
type reviewImportSettings struct {
	Mode     string
	Interval time.Duration
}

func reviewImportSettingsFromEnv() reviewImportSettings {
	settings := reviewImportSettings{Mode: "auto", Interval: reviewImportDefaultInterval}
	switch mode := strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_REVIEW_IMPORT"))); mode {
	case "always", "off":
		settings.Mode = mode
	case "", "auto":
	default:
		log.Printf("FLOW_REVIEW_IMPORT=%q is not auto, always or off; using auto", mode)
	}
	if raw := strings.TrimSpace(os.Getenv("FLOW_REVIEW_IMPORT_INTERVAL")); raw != "" {
		interval, err := time.ParseDuration(raw)
		switch {
		case raw == "0" || (err == nil && interval == 0):
			settings.Mode = "off"
		case err != nil || interval < time.Minute:
			log.Printf("FLOW_REVIEW_IMPORT_INTERVAL=%q must be a duration of at least 1m; using %s", raw, reviewImportDefaultInterval)
		default:
			settings.Interval = interval
		}
	}
	return settings
}

// reviewImportResult is what one run did.
type reviewImportResult struct {
	Repositories int      `json:"repositories"`
	Imported     int      `json:"imported"`
	Updated      int      `json:"updated"`
	Closed       int      `json:"closed"`
	Errors       []string `json:"errors"`
	// RateLimitedUntil: the run stopped early for a code host's rate limit.
	RateLimitedUntil *time.Time `json:"rateLimitedUntil,omitempty"`
	StartedAt        time.Time  `json:"startedAt"`
	FinishedAt       time.Time  `json:"finishedAt"`
}

// reviewImportTracker keeps one run at a time per workspace and the last
// run of each.
type reviewImportTracker struct {
	mu          sync.Mutex
	running     map[string]bool
	last        map[string]reviewImportResult
	pausedUntil map[string]time.Time
}

func (t *reviewImportTracker) start(workspace string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running == nil {
		t.running, t.last, t.pausedUntil = map[string]bool{}, map[string]reviewImportResult{}, map[string]time.Time{}
	}
	if t.running[workspace] {
		return false
	}
	t.running[workspace] = true
	return true
}

func (t *reviewImportTracker) finish(workspace string, result reviewImportResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.running, workspace)
	t.last[workspace] = result
	if result.RateLimitedUntil != nil {
		t.pausedUntil[workspace] = *result.RateLimitedUntil
	} else {
		delete(t.pausedUntil, workspace)
	}
}

func (t *reviewImportTracker) status(workspace string) (last *reviewImportResult, running bool, pausedUntil time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if result, ok := t.last[workspace]; ok {
		last = &result
	}
	return last, t.running[workspace], t.pausedUntil[workspace]
}

var errReviewImportRunning = errors.New("an import of pull and merge requests is already running")

// reviewImportRateLimit stops a run: a code host's rate limit is (nearly) spent.
type reviewImportRateLimit struct{ until time.Time }

func (e *reviewImportRateLimit) Error() string { return "rate limited" }

// importedPull is one pull/merge request as a list or get answer has it.
type importedPull struct {
	Number                  int
	ExternalID              string
	Title, Description, URL string
	State                   string // open | closed | merged
	Draft                   bool
	BaseBranch, HeadBranch  string
	HeadSHA                 string
	Author                  remoteReviewAuthor
	Reviewers               []string
	CreatedAt, UpdatedAt    time.Time
	MergedAt                *time.Time
}

type githubImportPull struct {
	ID       int64      `json:"id"`
	Number   int        `json:"number"`
	Title    string     `json:"title"`
	Body     *string    `json:"body"`
	HTMLURL  string     `json:"html_url"`
	State    string     `json:"state"`
	Draft    bool       `json:"draft"`
	Merged   bool       `json:"merged"`
	MergedAt *time.Time `json:"merged_at"`
	User     struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	} `json:"user"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	RequestedReviewers []struct {
		Login string `json:"login"`
	} `json:"requested_reviewers"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (p githubImportPull) pull() importedPull {
	state := "open"
	switch {
	case p.Merged || p.MergedAt != nil:
		state = "merged"
	case p.State == "closed":
		state = "closed"
	}
	pull := importedPull{Number: p.Number, ExternalID: strconv.FormatInt(p.ID, 10), Title: p.Title, URL: p.HTMLURL, State: state, Draft: p.Draft,
		BaseBranch: p.Base.Ref, HeadBranch: p.Head.Ref, HeadSHA: p.Head.SHA, Author: remoteReviewAuthor{Login: p.User.Login, AvatarURL: p.User.AvatarURL},
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, MergedAt: p.MergedAt}
	if p.Body != nil {
		pull.Description = *p.Body
	}
	for _, reviewer := range p.RequestedReviewers {
		pull.Reviewers = append(pull.Reviewers, reviewer.Login)
	}
	return pull
}

type gitlabImportMergeRequest struct {
	ID           int64      `json:"id"`
	IID          int        `json:"iid"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	WebURL       string     `json:"web_url"`
	State        string     `json:"state"`
	Draft        bool       `json:"draft"`
	WorkInProg   bool       `json:"work_in_progress"`
	SourceBranch string     `json:"source_branch"`
	TargetBranch string     `json:"target_branch"`
	SHA          string     `json:"sha"`
	MergedAt     *time.Time `json:"merged_at"`
	Author       struct {
		Username  string `json:"username"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	} `json:"author"`
	Reviewers []struct {
		Username string `json:"username"`
	} `json:"reviewers"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (m gitlabImportMergeRequest) pull() importedPull {
	state := "open"
	switch m.State {
	case "merged":
		state = "merged"
	case "closed":
		state = "closed"
	}
	pull := importedPull{Number: m.IID, ExternalID: strconv.FormatInt(m.ID, 10), Title: m.Title, Description: m.Description, URL: m.WebURL, State: state, Draft: m.Draft || m.WorkInProg,
		BaseBranch: m.TargetBranch, HeadBranch: m.SourceBranch, HeadSHA: m.SHA, Author: remoteReviewAuthor{Login: m.Author.Username, Name: m.Author.Name, AvatarURL: m.Author.AvatarURL},
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, MergedAt: m.MergedAt}
	for _, reviewer := range m.Reviewers {
		pull.Reviewers = append(pull.Reviewers, reviewer.Username)
	}
	return pull
}

// reviewImportRepository is one repository (GitLab project) to list.
type reviewImportRepository struct {
	Provider, FullName string
}

// placeholder is a review of the repository, for the remote lookups.
func (r reviewImportRepository) placeholder() domain.CodeReview {
	owner, name, _ := strings.Cut(r.FullName, "/")
	return domain.CodeReview{Provider: r.Provider, RepositoryOwner: owner, RepositoryName: name, Number: 1}
}

// listImportPages reads pages of 100 until a short page or maxPages;
// complete is false when the last page was full.
func listImportPages[T any](maxPages int, fetch func(page int) ([]T, error)) (all []T, complete bool, err error) {
	for page := 1; page <= maxPages; page++ {
		items, err := fetch(page)
		if err != nil {
			return nil, false, err
		}
		all = append(all, items...)
		if len(items) < 100 {
			return all, true, nil
		}
	}
	return all, false, nil
}

// reviewImportCheckBudget stops the run when the remote's token is nearly spent.
func reviewImportCheckBudget(remote reviewRemote) error {
	remaining, reset, known := reviewRemoteBudget(remote)
	if known && remaining < reviewImportRateReserve {
		return &reviewImportRateLimit{until: reset}
	}
	return nil
}

// reviewImportStop turns a rate limited answer into a stop of the run.
func reviewImportStop(err error) error {
	if limited, until := reviewRemoteRateLimited(err); limited {
		return &reviewImportRateLimit{until: until}
	}
	return err
}

// reviewImportRepositories lists the repositories the workspace reaches:
// the GitHub App installations' repositories, then each classic connection's
// (limited to its organization when one is set). errs are per source.
func (s *server) reviewImportRepositories(ctx context.Context, data domain.Bootstrap) ([]reviewImportRepository, []string, error) {
	repositories, errs := []reviewImportRepository{}, []string{}
	seen := map[string]bool{}
	add := func(provider, fullName string) {
		key := provider + ":" + strings.ToLower(fullName)
		if fullName != "" && strings.Contains(fullName, "/") && !seen[key] {
			seen[key] = true
			repositories = append(repositories, reviewImportRepository{Provider: provider, FullName: fullName})
		}
	}
	appCovers := false
	if host := s.gitHubAppHost(); host != nil && host.Configured() && s.store != nil {
		if installations, err := s.store.CodeHostInstallations(ctx, data.Workspace.URLKey, domain.CodeHostGitHub); err == nil && len(installations) > 0 {
			appCovers = true
			cursor := ""
			for range reviewImportMaxRepositoryPages {
				page, err := host.ListRepositories(ctx, data.Workspace.URLKey, codehost.ListOptions{Cursor: cursor})
				if err != nil {
					if stop := reviewImportStop(err); stop != err {
						return repositories, errs, stop
					}
					if !errors.Is(err, codehost.ErrNotConnected) {
						errs = append(errs, "GitHub App: "+err.Error())
					}
					break
				}
				for _, repository := range page.Repositories {
					if !repository.Archived {
						add(domain.CodeHostGitHub, repository.FullName)
					}
				}
				if cursor = page.NextCursor; cursor == "" {
					break
				}
			}
		}
	}
	for _, connection := range data.IntegrationConnections {
		if (connection.Provider != domain.CodeHostGitHub && connection.Provider != domain.CodeHostGitLab) || connection.Status == "disconnected" {
			continue
		}
		organization := strings.TrimSpace(connection.Config["organization"])
		provider, err := s.workspaceReviewProvider(ctx, data, domain.CodeReview{Provider: connection.Provider, RepositoryOwner: organization}, true)
		if err != nil || provider == nil {
			if err != nil && !(connection.Provider == domain.CodeHostGitHub && appCovers) {
				errs = append(errs, reviewHostLabel(connection.Provider)+": "+err.Error())
			}
			continue
		}
		inOrganization := func(fullName string) bool {
			return organization == "" || strings.HasPrefix(strings.ToLower(fullName), strings.ToLower(organization)+"/")
		}
		var listErr error
		if connection.Provider == domain.CodeHostGitHub {
			type repository struct {
				FullName string `json:"full_name"`
				Archived bool   `json:"archived"`
			}
			var items []repository
			items, _, listErr = listImportPages(reviewImportMaxRepositoryPages, func(page int) ([]repository, error) {
				var out []repository
				err := provider.call(ctx, http.MethodGet, fmt.Sprintf("%s/user/repos?affiliation=owner,collaborator,organization_member&sort=pushed&per_page=100&page=%d", provider.base, page), nil, &out)
				return out, err
			})
			for _, item := range items {
				if !item.Archived && inOrganization(item.FullName) {
					add(domain.CodeHostGitHub, item.FullName)
				}
			}
		} else {
			type project struct {
				PathWithNamespace string `json:"path_with_namespace"`
				Archived          bool   `json:"archived"`
			}
			var items []project
			// Reporter access reads merge requests.
			items, _, listErr = listImportPages(reviewImportMaxRepositoryPages, func(page int) ([]project, error) {
				var out []project
				err := provider.call(ctx, http.MethodGet, fmt.Sprintf("%s/projects?membership=true&archived=false&min_access_level=20&simple=true&order_by=last_activity_at&per_page=100&page=%d", provider.base, page), nil, &out)
				return out, err
			})
			for _, item := range items {
				if !item.Archived && inOrganization(item.PathWithNamespace) {
					add(domain.CodeHostGitLab, item.PathWithNamespace)
				}
			}
		}
		if listErr != nil {
			if stop := reviewImportStop(listErr); stop != listErr {
				return repositories, errs, stop
			}
			errs = append(errs, reviewHostLabel(connection.Provider)+": "+listErr.Error())
			continue
		}
		if remaining, reset, known := provider.rate.budget(); known && remaining < reviewImportRateReserve {
			return repositories, errs, &reviewImportRateLimit{until: reset}
		}
	}
	return repositories, errs, nil
}

// importOpenReviews runs one import for a workspace.
func (s *server) importOpenReviews(ctx context.Context, workspace string) (reviewImportResult, error) {
	if !s.reviewImports.start(workspace) {
		return reviewImportResult{}, errReviewImportRunning
	}
	result := reviewImportResult{Errors: []string{}, StartedAt: time.Now().UTC()}
	defer func() {
		result.FinishedAt = time.Now().UTC()
		s.reviewImports.finish(workspace, result)
	}()
	stopped := func(err error) bool {
		var limit *reviewImportRateLimit
		if !errors.As(err, &limit) {
			return false
		}
		until := limit.until
		if until.IsZero() || until.Before(time.Now()) {
			until = time.Now().Add(reviewImportDefaultInterval).UTC()
		}
		result.RateLimitedUntil = &until
		return true
	}
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		return result, errNotFound
	}
	repositories, errs, err := s.reviewImportRepositories(ctx, data)
	result.Errors = append(result.Errors, errs...)
	if err != nil && !stopped(err) {
		return result, err
	}
	if result.RateLimitedUntil != nil {
		return result, nil
	}
	appUsers := s.githubAppUsers(ctx, workspace)
	for _, repository := range repositories {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		err := s.importRepositoryReviews(ctx, workspace, data, repository, appUsers, &result)
		if stopped(err) {
			break
		}
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s %s: %v", reviewHostLabel(repository.Provider), repository.FullName, err))
		}
	}
	return result, nil
}

// importRepositoryReviews lists a repository's open requests, reads the
// state of its reviews that are open in Flow but no longer listed, and
// applies both.
func (s *server) importRepositoryReviews(ctx context.Context, workspace string, data domain.Bootstrap, repository reviewImportRepository, appUsers map[string]domain.User, result *reviewImportResult) error {
	remote, err := s.reviewRemoteFor(ctx, data, repository.placeholder(), true)
	if err != nil || remote == nil {
		return err
	}
	result.Repositories++
	listPath := "/pulls?state=open&sort=updated&direction=desc"
	if repository.Provider == domain.CodeHostGitLab {
		listPath = "/merge_requests?state=opened&order_by=updated_at&sort=desc"
	}
	pulls := []importedPull{}
	var complete bool
	if repository.Provider == domain.CodeHostGitLab {
		var items []gitlabImportMergeRequest
		items, complete, err = listImportPages(reviewImportMaxPullPages, func(page int) ([]gitlabImportMergeRequest, error) {
			var out []gitlabImportMergeRequest
			err := remote.REST(ctx, http.MethodGet, fmt.Sprintf("%s&per_page=100&page=%d", listPath, page), nil, &out)
			return out, err
		})
		for _, item := range items {
			pulls = append(pulls, item.pull())
		}
	} else {
		var items []githubImportPull
		items, complete, err = listImportPages(reviewImportMaxPullPages, func(page int) ([]githubImportPull, error) {
			var out []githubImportPull
			err := remote.REST(ctx, http.MethodGet, fmt.Sprintf("%s&per_page=100&page=%d", listPath, page), nil, &out)
			return out, err
		})
		for _, item := range items {
			pulls = append(pulls, item.pull())
		}
	}
	if err != nil {
		return reviewImportStop(err)
	}
	if complete {
		listed := map[int]bool{}
		for _, pull := range pulls {
			listed[pull.Number] = true
		}
		for _, review := range data.Reviews {
			if review.Provider != repository.Provider || review.Status != "open" || review.Number <= 0 || listed[review.Number] ||
				!strings.EqualFold(review.RepositoryOwner+"/"+review.RepositoryName, repository.FullName) {
				continue
			}
			pull, err := fetchImportedPull(ctx, remote, repository.Provider, review.Number)
			if err != nil {
				if stop := reviewImportStop(err); stop != err {
					return stop
				}
				result.Errors = append(result.Errors, fmt.Sprintf("%s %s#%d: %v", reviewHostLabel(repository.Provider), repository.FullName, review.Number, err))
				continue
			}
			pulls = append(pulls, pull)
		}
	}
	if err := s.applyImportedPulls(ctx, workspace, repository, pulls, appUsers, result); err != nil {
		return err
	}
	return reviewImportCheckBudget(remote)
}

// fetchImportedPull reads one request's state. One the code host no longer
// has (a deleted GitLab merge request) reads as closed.
func fetchImportedPull(ctx context.Context, remote reviewRemote, provider string, number int) (importedPull, error) {
	var (
		pull importedPull
		err  error
	)
	if provider == domain.CodeHostGitLab {
		var item gitlabImportMergeRequest
		err = remote.REST(ctx, http.MethodGet, fmt.Sprintf("/merge_requests/%d", number), nil, &item)
		pull = item.pull()
	} else {
		var item githubImportPull
		err = remote.REST(ctx, http.MethodGet, fmt.Sprintf("/pulls/%d", number), nil, &item)
		pull = item.pull()
	}
	if reviewRemoteStatus(err) == http.StatusNotFound {
		return importedPull{Number: number, State: "closed"}, nil
	}
	pull.Number = number
	return pull, err
}

// reviewImportScope loads the issues the imported requests can link or move.
func reviewImportScope(ctx context.Context, repository reviewImportRepository, pulls []importedPull) context.Context {
	haystack := []string{}
	for _, pull := range pulls {
		haystack = append(haystack, pull.Title, pull.Description, pull.HeadBranch)
	}
	return store.WithMutationScope(ctx, store.MutationScope{IssueIdentifiers: identifierCandidates(strings.Join(haystack, " ")), StatusAutomation: true,
		Resolve: func(data domain.Bootstrap) store.MutationScope {
			scope := store.MutationScope{}
			for _, review := range data.Reviews {
				if review.Provider == repository.Provider && strings.EqualFold(review.RepositoryOwner+"/"+review.RepositoryName, repository.FullName) {
					scope.IssueIDs = append(scope.IssueIDs, review.IssueIDs...)
					scope.Resources = append(scope.Resources, review.IssueIDs...)
				}
			}
			return scope
		},
		Expand: func(issues []domain.Issue) store.MutationScope {
			scope := store.MutationScope{}
			for _, issue := range issues {
				scope.Resources = append(scope.Resources, issue.ID)
			}
			return scope
		}})
}

// applyImportedPulls creates the reviews of new open requests and updates
// the others in one workspace mutation, then queues the full sync of the
// reviews whose files or comments may have changed.
func (s *server) applyImportedPulls(ctx context.Context, workspace string, repository reviewImportRepository, pulls []importedPull, appUsers map[string]domain.User, result *reviewImportResult) error {
	if len(pulls) == 0 {
		return nil
	}
	owner, name, _ := strings.Cut(repository.FullName, "/")
	// Members who linked their account on the request's host are attributed exactly.
	var linked map[string]domain.User
	if members, ok := s.store.WorkspaceMetadataFields(workspace, "users", "members"); ok {
		logins, hostName := []string{}, ""
		for _, pull := range pulls {
			logins = append(append(logins, pull.Author.Login), pull.Reviewers...)
			if parsed, err := url.Parse(pull.URL); err == nil && parsed.Host != "" && hostName == "" {
				hostName = strings.ToLower(parsed.Host)
			}
		}
		linked = s.codeHostMemberLogins(ctx, &members, repository.Provider, hostName, logins...)
	}
	var (
		imported, updated, closed int
		syncIDs, mergedIssueIDs   []string
	)
	err := s.store.MutateWorkspace(reviewImportScope(ctx, repository, pulls), workspace, "review.imported", repository.Provider+":"+repository.FullName, map[string]any{"provider": repository.Provider, "repository": repository.FullName, "count": len(pulls)}, func(data *domain.Bootstrap) error {
		imported, updated, closed, syncIDs, mergedIssueIDs = 0, 0, 0, nil, nil
		now := time.Now().UTC()
		resolve := reviewActorResolver(data, repository.Provider, appUsers)
		actor := func(author remoteReviewAuthor) domain.User {
			if user, ok := linked[strings.ToLower(strings.TrimSpace(author.Login))]; ok && author.Login != "" {
				return user
			}
			return resolve(author)
		}
		for position, pull := range pulls {
			index := slices.IndexFunc(data.Reviews, func(review domain.CodeReview) bool {
				return review.Provider == repository.Provider && pull.ExternalID != "" && review.ExternalID == pull.ExternalID
			})
			if index < 0 {
				if existing, found := findSyncedReview(*data, repository.Provider, repository.FullName, pull.Number); found {
					index = reviewIndex(*data, existing.ID)
				}
			}
			if index < 0 {
				if pull.State != "open" {
					continue
				}
				author := actor(pull.Author)
				createdAt := pull.CreatedAt
				if createdAt.IsZero() {
					createdAt = now
				}
				review := domain.CodeReview{ID: fmt.Sprintf("review_%d", now.UnixNano()+int64(position)), SlugID: fmt.Sprintf("%s-%d", repository.Provider, pull.Number), Provider: repository.Provider,
					ExternalID: pull.ExternalID, Number: pull.Number, Status: "open", RepositoryOwner: owner, RepositoryName: name, Author: author,
					ReviewerIDs: importedReviewerIDs(data, linked, pull.Reviewers), TeamReviewers: []string{}, IssueIDs: []string{}, BranchState: "upToDate",
					Checks: []domain.ReviewCheck{}, Files: []domain.ReviewFile{}, Events: []domain.ReviewEvent{}, CreatedAt: createdAt, UpdatedAt: now}
				applyImportedPull(&review, pull, now)
				review.IssueIDs = normalizedStrings(inferredReviewIssueIDs(data, review))
				for _, issueID := range review.IssueIDs {
					appendActivity(data, issueID, "issue.review_linked", author, map[string]string{"reviewId": review.ID, "reviewTitle": review.Title, "reviewNumber": fmt.Sprint(review.Number), "repository": repository.FullName, "url": review.URL, "source": "import"})
				}
				data.Reviews = append([]domain.CodeReview{review}, data.Reviews...)
				appendReviewLinkbacks(data, repository.Provider, review, author, now)
				if err := applyGitSettingAutomations(data, review, reviewAutomationEvent(review, "opened"), now); err != nil {
					return err
				}
				imported++
				syncIDs = append(syncIDs, review.ID)
				continue
			}
			review := data.Reviews[index]
			before := review.Status
			changed := applyImportedPull(&review, pull, now)
			if pull.State == "open" && len(pull.Reviewers) > 0 {
				if reviewers := normalizedStrings(append(slices.Clone(review.ReviewerIDs), importedReviewerIDs(data, linked, pull.Reviewers)...)); len(reviewers) != len(review.ReviewerIDs) {
					review.ReviewerIDs, changed = reviewers, true
				}
			}
			if changed {
				review.UpdatedAt = now
				updated++
			}
			if before == "open" && review.Status != "open" {
				closed++
			}
			data.Reviews[index] = review
			if before != "merged" && review.Status == "merged" {
				mergedIssueIDs = append(mergedIssueIDs, review.IssueIDs...)
				if err := applyGitSettingAutomations(data, review, "merged", now); err != nil {
					return err
				}
			}
			// Files and comments: when the head moved, the request changed
			// since the last full sync, or it never had one.
			if pull.State == "open" && (review.SyncedAt == nil || (pull.HeadSHA != "" && pull.HeadSHA != review.HeadSHA) || (!pull.UpdatedAt.IsZero() && pull.UpdatedAt.After(*review.SyncedAt))) {
				syncIDs = append(syncIDs, review.ID)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	result.Imported += imported
	result.Updated += updated
	result.Closed += closed
	if len(mergedIssueIDs) > 0 {
		if err := s.store.MarkAgentSessionsMerged(ctx, workspace, mergedIssueIDs, time.Now()); err != nil {
			return err
		}
	}
	s.queueReviewSyncs(workspace, syncIDs)
	return nil
}

// applyImportedPull copies a request's metadata and state onto its review
// and reports whether anything changed.
func applyImportedPull(review *domain.CodeReview, pull importedPull, now time.Time) bool {
	before := fmt.Sprint(review.Title, review.Description, review.URL, review.Draft, review.BaseBranch, review.HeadBranch, review.Status)
	if pull.Title != "" {
		review.Title, review.Description = pull.Title, pull.Description
	}
	if pull.URL != "" {
		review.URL = pull.URL
	}
	if pull.BaseBranch != "" {
		review.BaseBranch, review.HeadBranch, review.Draft = pull.BaseBranch, pull.HeadBranch, pull.Draft
	}
	switch {
	case pull.State == "merged" && review.Status != "merged":
		mergedAt := now
		if pull.MergedAt != nil {
			mergedAt = pull.MergedAt.UTC()
		}
		review.Status, review.MergedAt, review.ClosedAt = "merged", &mergedAt, nil
	case pull.State == "closed" && review.Status != "closed":
		review.Status, review.ClosedAt, review.MergedAt = "closed", &now, nil
	case pull.State == "open" && (review.Status == "merged" || review.Status == "closed"):
		review.Status, review.ClosedAt, review.MergedAt = "open", nil, nil
	}
	return before != fmt.Sprint(review.Title, review.Description, review.URL, review.Draft, review.BaseBranch, review.HeadBranch, review.Status)
}

// importedReviewerIDs maps requested reviewers: linked members exactly,
// then members whose name or email matches the login.
func importedReviewerIDs(data *domain.Bootstrap, linked map[string]domain.User, logins []string) []string {
	result := []string{}
	for _, login := range logins {
		if user, ok := linked[strings.ToLower(login)]; ok {
			result = append(result, user.ID)
			continue
		}
		for _, user := range data.Users {
			local := strings.SplitN(user.Email, "@", 2)[0]
			if strings.EqualFold(login, user.Name) || strings.EqualFold(login, user.DisplayName) || strings.EqualFold(login, local) {
				result = append(result, user.ID)
			}
		}
	}
	return uniqueStrings(result)
}

// queueReviewSyncs runs the full sync of imported reviews one after the
// other in the background (not one goroutine each, which would spend the
// code host's rate limit at once); it stops at a rate limit.
func (s *server) queueReviewSyncs(workspace string, reviewIDs []string) {
	if len(reviewIDs) == 0 {
		return
	}
	reviewSyncRunner(func() {
		for _, reviewID := range reviewIDs {
			key := workspace + "/" + reviewID
			if _, busy := reviewSyncInFlight.LoadOrStore(key, true); busy {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := s.syncReview(ctx, workspace, reviewID, true)
			cancel()
			reviewSyncInFlight.Delete(key)
			if limited, _ := reviewRemoteRateLimited(err); limited {
				log.Printf("review import sync %s: rate limited; the rest syncs on the next import", workspace)
				return
			}
			if err != nil && !errors.Is(err, errReviewNotSynced) {
				log.Printf("review import sync %s: %v", key, err)
			}
		}
	})
}

// reviewImportNeeded reports why the workspace needs background imports
// ("" when it does not): a code host source without a working webhook, or
// FLOW_REVIEW_IMPORT=always with any source.
func (s *server) reviewImportNeeded(ctx context.Context, data domain.Bootstrap, mode string) string {
	if mode == "off" {
		return ""
	}
	stale := func(at *time.Time) bool { return at == nil || time.Since(*at) > reviewImportWebhookStale }
	reasons := []string{}
	sources := 0
	if host := s.gitHubAppHost(); host != nil && host.Configured() && s.store != nil {
		if installations, err := s.store.CodeHostInstallations(ctx, data.Workspace.URLKey, domain.CodeHostGitHub); err == nil && len(installations) > 0 {
			sources++
			last, err := s.store.LastCodeHostWebhookDelivery(ctx, domain.CodeHostGitHub, data.Workspace.URLKey)
			if err != nil || last.IsZero() || stale(&last) {
				reasons = append(reasons, "the GitHub App has delivered no webhook recently")
			}
		}
	}
	for _, connection := range data.IntegrationConnections {
		if (connection.Provider != domain.CodeHostGitHub && connection.Provider != domain.CodeHostGitLab) || connection.Status == "disconnected" {
			continue
		}
		sources++
		secret := strings.TrimSpace(connection.Config["webhookSecret"]) != "" || strings.TrimSpace(connection.Config["webhookSecretEnv"]) != ""
		switch {
		case !secret:
			reasons = append(reasons, reviewHostLabel(connection.Provider)+" has no webhook")
		case stale(connection.LastWebhookAt):
			reasons = append(reasons, reviewHostLabel(connection.Provider)+" has delivered no webhook recently")
		}
	}
	switch {
	case sources == 0:
		return ""
	case len(reasons) > 0:
		return strings.Join(uniqueStrings(reasons), "; ")
	case mode == "always":
		return "FLOW_REVIEW_IMPORT=always"
	}
	return ""
}

// runReviewImportScheduler imports open requests in the background for the
// workspaces that need it, every FLOW_REVIEW_IMPORT_INTERVAL.
func (s *server) runReviewImportScheduler(ctx context.Context) {
	settings := reviewImportSettingsFromEnv()
	if settings.Mode == "off" || s.store == nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.store.WorkerContext().Done():
			return
		case <-ticker.C:
			run := func() error { s.runReviewImportTick(ctx, settings, time.Now()); return nil }
			if s.coordinator == nil {
				_ = run()
			} else if _, err := s.coordinator.WithLeaderLock(ctx, "review-import", run); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("review import: %v", err)
			}
		}
	}
}

func (s *server) runReviewImportTick(ctx context.Context, settings reviewImportSettings, now time.Time) {
	for _, workspace := range s.store.WorkspaceKeys() {
		if ctx.Err() != nil {
			return
		}
		last, running, pausedUntil := s.reviewImports.status(workspace)
		if running || now.Before(pausedUntil) || (last != nil && now.Sub(last.StartedAt) < settings.Interval) {
			continue
		}
		data, ok := s.store.WorkspaceMetadata(workspace)
		if !ok || s.reviewImportNeeded(ctx, data, settings.Mode) == "" {
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		result, err := s.importOpenReviews(runCtx, workspace)
		cancel()
		switch {
		case err != nil && !errors.Is(err, errReviewImportRunning):
			log.Printf("review import workspace=%s: %v", workspace, err)
		case len(result.Errors) > 0:
			log.Printf("review import workspace=%s: %s", workspace, strings.Join(result.Errors, "; "))
		}
	}
}

// reviewImportStatus is GET /api/reviews/import.
type reviewImportStatus struct {
	Running bool `json:"running"`
	// Background is why background imports run for the workspace ("" when
	// they do not); Interval is their period in seconds.
	Background       string              `json:"background"`
	Interval         int                 `json:"interval"`
	Last             *reviewImportResult `json:"last,omitempty"`
	RateLimitedUntil *time.Time          `json:"rateLimitedUntil,omitempty"`
}

func (s *server) getReviewImport(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	settings := reviewImportSettingsFromEnv()
	last, running, pausedUntil := s.reviewImports.status(data.Workspace.URLKey)
	status := reviewImportStatus{Running: running, Background: s.reviewImportNeeded(r.Context(), data, settings.Mode), Interval: int(settings.Interval.Seconds()), Last: last}
	if time.Now().Before(pausedUntil) {
		status.RateLimitedUntil = &pausedUntil
	}
	writeJSON(w, http.StatusOK, status)
}

// importReviewsFromCodeHosts — POST /api/reviews/import ("Sync now"):
// imports the open pull and merge requests of every connected repository.
func (s *server) importReviewsFromCodeHosts(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	_, _, pausedUntil := s.reviewImports.status(data.Workspace.URLKey)
	if time.Now().Before(pausedUntil) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "The code host's rate limit is spent; try again after " + pausedUntil.Format(time.RFC3339), "rateLimitedUntil": pausedUntil})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	result, err := s.importOpenReviews(ctx, data.Workspace.URLKey)
	switch {
	case errors.Is(err, errReviewImportRunning):
		writeError(w, http.StatusConflict, "A sync with the code hosts is already running")
	case err != nil && result.Repositories == 0:
		respondMutation(w, err, http.StatusOK, nil)
	default:
		// Partial failures (one repository, one connection) are in errors.
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		}
		writeJSON(w, http.StatusOK, result)
	}
}
