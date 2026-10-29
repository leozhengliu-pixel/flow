package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// Review import: besides webhooks, Flow can list the open pull and merge
// requests of repositories a workspace admin approved and create or refresh
// their reviews. It is off unless the server allows it (FLOW_REVIEW_IMPORT=on)
// and the workspace turned it on, and it never enumerates what a token can
// see: only approved repositories are read.
//
// Every run is bounded (repositories, requests per repository and per run,
// workspace writes, a delay between writes) and resumes at a cursor; requests
// whose head commit, update time and state did not change are skipped
// without a write; remote failures back off per repository and per request;
// one run per workspace holds a database lease, so replicas never run the
// same workspace's import at once. All changed requests of a run are applied
// in one workspace write ("review.imported"), so browsers see one realtime
// event per run plus one per review whose files and comments were synced.

const (
	reviewImportDefaultInterval = 10 * time.Minute
	// reviewImportRateReserve: a run stops when a token has fewer requests
	// left than this, so members' own actions keep working.
	reviewImportRateReserve = 100
	// reviewImportLease outlives the longest run (scheduled runs time out after 10m).
	reviewImportLease = 15 * time.Minute
	// reviewImportMaxApproved bounds the approved repositories of a workspace.
	reviewImportMaxApproved = 100
	reviewImportMaxErrors   = 20
)

// reviewImportLimits bounds one run.
type reviewImportLimits struct {
	Repositories  int           // repositories read per run
	PerRepository int           // open requests listed per repository (one page, at most 100)
	Total         int           // requests examined per run
	Writes        int           // workspace writes per run: the import's one and each full sync
	WriteDelay    time.Duration // pause between writes
	BackoffBase   time.Duration // first backoff of a failing repository or request, doubled per failure
	BackoffCap    time.Duration
}

var defaultReviewImportLimits = reviewImportLimits{Repositories: 10, PerRepository: 50, Total: 200, Writes: 20, WriteDelay: 2 * time.Second, BackoffBase: 15 * time.Minute, BackoffCap: 6 * time.Hour}

func (s *server) importLimits() reviewImportLimits {
	if s.reviewImportLimits != nil {
		return *s.reviewImportLimits
	}
	return defaultReviewImportLimits
}

// reviewImportSettings is the deployment configuration: FLOW_REVIEW_IMPORT
// (off by default; on lets workspace admins turn the import on) and
// FLOW_REVIEW_IMPORT_INTERVAL (a Go duration, 0 turns it off).
type reviewImportSettings struct {
	Mode     string // on | off
	Interval time.Duration
}

func reviewImportSettingsFromEnv() reviewImportSettings {
	settings := reviewImportSettings{Mode: "off", Interval: reviewImportDefaultInterval}
	switch mode := strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_REVIEW_IMPORT"))); mode {
	case "on", "true", "1", "enabled":
		settings.Mode = "on"
	case "auto", "always":
		// Earlier values: they now only allow workspaces to turn it on.
		settings.Mode = "on"
	case "", "off", "false", "0", "disabled":
	default:
		log.Printf("FLOW_REVIEW_IMPORT=%q is not on or off; using off", mode)
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

// reviewImportRepository is one approved repository (GitLab project).
type reviewImportRepository struct {
	Provider string `json:"provider"`
	FullName string `json:"repository"`
}

func (r reviewImportRepository) key() string {
	return r.Provider + ":" + strings.ToLower(r.FullName)
}

func (r reviewImportRepository) pullKey(number int) string {
	return fmt.Sprintf("%s#%d", r.key(), number)
}

// placeholder is a review of the repository, for the remote lookups.
func (r reviewImportRepository) placeholder() domain.CodeReview {
	owner, name, _ := strings.Cut(r.FullName, "/")
	return domain.CodeReview{Provider: r.Provider, RepositoryOwner: owner, RepositoryName: name, Number: 1}
}

// normalizeImportRepository accepts owner/name (a GitLab group path may
// nest) or the repository's web URL.
func normalizeImportRepository(input reviewImportRepository) (reviewImportRepository, error) {
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	if provider != domain.CodeHostGitHub && provider != domain.CodeHostGitLab {
		return input, &reviewImportRequestError{code: reviewImportCodeProviderInvalid, message: "provider must be github or gitlab"}
	}
	name := strings.TrimSpace(input.FullName)
	isURL := strings.Contains(name, "://")
	if isURL {
		if parsed, err := url.Parse(name); err == nil {
			name = parsed.Path
		}
	}
	name = strings.TrimSuffix(strings.Trim(name, "/"), ".git")
	if before, _, found := strings.Cut(name, "/-/"); found {
		name = before
	}
	parts := strings.Split(name, "/")
	// A GitHub URL may point inside the repository (…/owner/name/pull/3).
	if isURL && provider == domain.CodeHostGitHub && len(parts) > 2 {
		parts = parts[:2]
		name = strings.Join(parts, "/")
	}
	valid := len(parts) >= 2 && len(name) <= 200 && !strings.ContainsAny(name, " \t?#%")
	for _, part := range parts {
		valid = valid && part != "" && part != "." && part != ".."
	}
	if provider == domain.CodeHostGitHub && len(parts) != 2 {
		valid = false
	}
	if !valid {
		return input, &reviewImportRequestError{code: reviewImportCodeRepositoryInvalid, message: fmt.Sprintf("%q is not a repository (owner/name)", input.FullName), params: map[string]string{"repository": input.FullName}}
	}
	return reviewImportRepository{Provider: provider, FullName: name}, nil
}

// reviewImportConfig is the workspace's setting.
type reviewImportConfig struct {
	Enabled      bool                     `json:"enabled"`
	Repositories []reviewImportRepository `json:"repositories"`
	UpdatedBy    string                   `json:"updatedBy,omitempty"`
	UpdatedAt    *time.Time               `json:"updatedAt,omitempty"`
}

// reviewImportFailure is a repository or request whose remote call failed.
type reviewImportFailure struct {
	Target string `json:"target"`
	// Code names the kind of failure for clients to translate (see
	// reviewImportClassify); Params are its values (status, provider).
	// Message stays English diagnostic text.
	Code         string            `json:"code,omitempty"`
	Params       map[string]string `json:"params,omitempty"`
	Message      string            `json:"message"`
	Failures     int               `json:"failures,omitempty"`
	BackoffUntil *time.Time        `json:"backoffUntil,omitempty"`
}

// reviewImportResult is what one run did.
type reviewImportResult struct {
	Trigger      string `json:"trigger"` // manual | schedule
	Repositories int    `json:"repositories"`
	// Scanned requests; SkippedUnchanged of them had nothing new.
	Scanned          int `json:"scanned"`
	Imported         int `json:"imported"`
	Updated          int `json:"updated"`
	Closed           int `json:"closed"`
	SkippedUnchanged int `json:"skippedUnchanged"`
	Synced           int `json:"synced"`
	Failed           int `json:"failed"`
	// BackedOff repositories and requests were skipped: a recent failure.
	BackedOff int `json:"backedOff"`
	Writes    int `json:"writes"`
	// Partial: a per-run limit stopped the run; the next continues at the cursor.
	Partial  bool                  `json:"partial"`
	Errors   []string              `json:"errors"`
	Failures []reviewImportFailure `json:"failures"`
	// RateLimitedUntil: the run stopped early for a code host's rate limit.
	RateLimitedUntil *time.Time `json:"rateLimitedUntil,omitempty"`
	StartedAt        time.Time  `json:"startedAt"`
	FinishedAt       time.Time  `json:"finishedAt"`
}

var (
	errReviewImportRunning  = errors.New("an import of pull and merge requests is already running")
	errReviewImportOff      = errors.New("pull and merge request import is turned off on this server")
	errReviewImportDisabled = errors.New("pull and merge request import is not turned on for this workspace")
)

// reviewImportRequestError is a rejected PATCH input with its machine code.
type reviewImportRequestError struct {
	code, message string
	params        map[string]string
}

func (e *reviewImportRequestError) Error() string { return e.message }

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

// fingerprint is what decides whether a request changed since the last import.
func (p importedPull) fingerprint() string {
	updated := ""
	if !p.UpdatedAt.IsZero() {
		updated = p.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return p.HeadSHA + "|" + updated + "|" + p.State
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

// reviewImportStop turns a rate limited answer into a stop of the run.
func reviewImportStop(err error) error {
	if limited, until := reviewRemoteRateLimited(err); limited {
		return &reviewImportRateLimit{until: until}
	}
	return err
}

// reviewImportCheckBudget stops the run when the remote's token is nearly spent.
func reviewImportCheckBudget(remote reviewRemote) error {
	remaining, reset, known := reviewRemoteBudget(remote)
	if known && remaining < reviewImportRateReserve {
		return &reviewImportRateLimit{until: reset}
	}
	return nil
}

func reviewImportOwner() string {
	host, _ := os.Hostname()
	if len(host) > 40 {
		host = host[:40]
	}
	buffer := make([]byte, 8)
	_, _ = rand.Read(buffer)
	return "import-" + host + "-" + hex.EncodeToString(buffer)
}

// loadReviewImport reads the workspace's setting and last run.
func (s *server) loadReviewImport(ctx context.Context, workspace string) (store.ReviewImportRecord, reviewImportConfig, *reviewImportResult, error) {
	record, err := s.store.ReviewImportState(ctx, workspace)
	config := reviewImportConfig{Repositories: []reviewImportRepository{}}
	if err != nil {
		return record, config, nil, err
	}
	if record.Settings != "" {
		_ = json.Unmarshal([]byte(record.Settings), &config)
	}
	if config.Repositories == nil {
		config.Repositories = []reviewImportRepository{}
	}
	config.Enabled = record.Enabled
	var last *reviewImportResult
	if record.Status != "" && record.Status != "{}" {
		var result reviewImportResult
		if json.Unmarshal([]byte(record.Status), &result) == nil && !result.StartedAt.IsZero() {
			last = &result
		}
	}
	return record, config, last, nil
}

// runReviewImport runs one import for a workspace under its lease.
func (s *server) runReviewImport(ctx context.Context, workspace, trigger string) (reviewImportResult, error) {
	settings := reviewImportSettingsFromEnv()
	if settings.Mode != "on" || s.store == nil {
		return reviewImportResult{}, errReviewImportOff
	}
	record, config, _, err := s.loadReviewImport(ctx, workspace)
	if err != nil {
		return reviewImportResult{}, err
	}
	if !config.Enabled {
		return reviewImportResult{}, errReviewImportDisabled
	}
	owner := reviewImportOwner()
	claimed, err := s.store.ClaimReviewImport(ctx, workspace, owner, reviewImportLease)
	if err != nil {
		return reviewImportResult{}, err
	}
	if !claimed {
		return reviewImportResult{}, errReviewImportRunning
	}
	result := reviewImportResult{Trigger: trigger, Errors: []string{}, Failures: []reviewImportFailure{}, StartedAt: time.Now().UTC()}
	run := &reviewImportRun{s: s, workspace: workspace, limits: s.importLimits(), result: &result, items: map[string]store.ReviewImportItem{}}
	cursor := run.execute(ctx, record.Cursor, config.Repositories)
	result.FinishedAt = time.Now().UTC()
	next := result.FinishedAt.Add(settings.Interval)
	if result.RateLimitedUntil != nil && result.RateLimitedUntil.After(next) {
		next = *result.RateLimitedUntil
	}
	raw, _ := json.Marshal(result)
	// A cancelled request still records the run and releases the lease.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.store.FinishReviewImport(finishCtx, workspace, owner, string(raw), cursor, next); err != nil {
		log.Printf("review import workspace=%s: record run: %v", workspace, err)
	}
	return result, nil
}

// reviewImportRun is one run's progress.
type reviewImportRun struct {
	s         *server
	workspace string
	limits    reviewImportLimits
	result    *reviewImportResult
	items     map[string]store.ReviewImportItem // by bounded key
	examined  int
}

// reviewImportBatch is a repository's new and changed requests.
type reviewImportBatch struct {
	repository   reviewImportRepository
	pulls        []importedPull
	linked       map[string]domain.User
	fingerprints map[int]string
}

// reviewImportSync is a review whose files and comments need a full sync.
type reviewImportSync struct {
	repository reviewImportRepository
	number     int
	head       string
	reviewID   string // "" for a review the run creates
}

// item is the import's memory of a repository or request key (stored under
// the bounded key).
func (run *reviewImportRun) item(repository reviewImportRepository, key string) store.ReviewImportItem {
	key = store.ReviewImportKey(key)
	if item, ok := run.items[key]; ok {
		return item
	}
	return store.ReviewImportItem{Key: key, Scope: repository.key()}
}

func (run *reviewImportRun) save(ctx context.Context, item store.ReviewImportItem) {
	run.items[item.Key] = item
	if err := run.s.store.SaveReviewImportItem(ctx, run.workspace, item); err != nil {
		log.Printf("review import workspace=%s: save %s: %v", run.workspace, item.Key, err)
	}
}

func (run *reviewImportRun) addError(failure reviewImportFailure) {
	if len(run.result.Failures) >= reviewImportMaxErrors {
		return
	}
	run.result.Failures = append(run.result.Failures, failure)
	run.result.Errors = append(run.result.Errors, failure.Target+": "+failure.Message)
}

// fail records a failed remote call and backs the item off exponentially.
func (run *reviewImportRun) fail(ctx context.Context, item store.ReviewImportItem, target string, err error) {
	item.Failures++
	backoff := run.limits.BackoffBase
	for i := 1; i < item.Failures && backoff < run.limits.BackoffCap; i++ {
		backoff *= 2
	}
	backoff = min(backoff, run.limits.BackoffCap)
	item.BackoffUntil = time.Now().Add(backoff).UTC()
	message := err.Error()
	if len(message) > 300 {
		message = message[:300] + "…"
	}
	code, params := reviewImportClassify(err)
	item.LastError = reviewImportEncodeError(code, params, message)
	run.save(ctx, item)
	run.result.Failed++
	until := item.BackoffUntil
	run.addError(reviewImportFailure{Target: target, Code: code, Params: params, Message: message, Failures: item.Failures, BackoffUntil: &until})
}

// succeed clears an item's backoff.
func (run *reviewImportRun) succeed(ctx context.Context, item store.ReviewImportItem) {
	if item.Failures == 0 && item.LastError == "" && item.BackoffUntil.IsZero() {
		return
	}
	item.Failures, item.LastError, item.BackoffUntil = 0, "", time.Time{}
	run.save(ctx, item)
}

// stopped reports a rate limit stop and records when to resume.
func (run *reviewImportRun) stopped(err error) bool {
	var limit *reviewImportRateLimit
	if !errors.As(err, &limit) {
		return false
	}
	until := limit.until
	if until.IsZero() || until.Before(time.Now()) {
		until = time.Now().Add(reviewImportDefaultInterval).UTC()
	}
	run.result.RateLimitedUntil = &until
	return true
}

// execute reads the approved repositories from cursor on, applies the
// changed requests in one workspace write and runs the full syncs it needs;
// it returns the cursor of the next run.
func (run *reviewImportRun) execute(ctx context.Context, cursor int, repositories []reviewImportRepository) int {
	if len(repositories) == 0 {
		return 0
	}
	data, ok := run.s.store.WorkspaceMetadata(run.workspace)
	if !ok {
		run.addError(reviewImportFailure{Target: run.workspace, Code: reviewImportCodeWorkspaceMissing, Message: "workspace not found"})
		return cursor
	}
	start := max(cursor, 0) % len(repositories)
	next := start
	batches, syncs := []reviewImportBatch{}, []reviewImportSync{}
	for offset := range repositories {
		if offset >= run.limits.Repositories || run.examined >= run.limits.Total {
			run.result.Partial = true
			break
		}
		if ctx.Err() != nil {
			break
		}
		index := (start + offset) % len(repositories)
		batch, needSync, complete, stop := run.scanRepository(ctx, data, repositories[index])
		if len(batch.pulls) > 0 {
			batches = append(batches, batch)
		}
		syncs = append(syncs, needSync...)
		if stop || !complete {
			run.result.Partial = run.result.Partial || !complete
			next = index
			break
		}
		next = (index + 1) % len(repositories)
	}
	created := run.apply(ctx, batches)
	// A spent rate limit leaves the full syncs to the next run.
	if run.result.RateLimitedUntil == nil {
		run.sync(ctx, syncs, created)
	}
	return next
}

// scanRepository lists one repository's open requests (and reads the state
// of its reviews that are open in Flow but no longer listed), and returns
// the new and changed ones and those needing a full sync. complete is false
// when the run's request budget ended inside the repository; stop when a
// rate limit ends the run.
func (run *reviewImportRun) scanRepository(ctx context.Context, data domain.Bootstrap, repository reviewImportRepository) (batch reviewImportBatch, syncs []reviewImportSync, complete, stop bool) {
	batch = reviewImportBatch{repository: repository, fingerprints: map[int]string{}}
	label := reviewHostLabel(repository.Provider) + " " + repository.FullName
	stored, err := run.s.store.ReviewImportItems(ctx, run.workspace, repository.key())
	if err != nil {
		run.addError(reviewImportFailure{Target: label, Code: reviewImportCodeUnknown, Message: err.Error()})
		return batch, nil, true, false
	}
	for _, item := range stored {
		if _, known := run.items[item.Key]; !known {
			run.items[item.Key] = item
		}
	}
	repositoryItem := run.item(repository, repository.key())
	if time.Now().Before(repositoryItem.BackoffUntil) {
		run.result.BackedOff++
		return batch, nil, true, false
	}
	remote, err := run.s.reviewRemoteFor(ctx, data, repository.placeholder(), true)
	if err == nil && remote == nil {
		err = &reviewImportNoSourceError{provider: repository.Provider}
	}
	if err != nil {
		run.fail(ctx, repositoryItem, label, err)
		return batch, nil, true, false
	}
	run.result.Repositories++
	perPage := min(run.limits.PerRepository, 100, run.limits.Total-run.examined)
	if perPage <= 0 {
		return batch, nil, false, false
	}
	pulls := []importedPull{}
	if repository.Provider == domain.CodeHostGitLab {
		var items []gitlabImportMergeRequest
		err = remote.REST(ctx, http.MethodGet, fmt.Sprintf("/merge_requests?state=opened&order_by=updated_at&sort=desc&per_page=%d&page=1", perPage), nil, &items)
		for _, item := range items {
			pulls = append(pulls, item.pull())
		}
	} else {
		var items []githubImportPull
		err = remote.REST(ctx, http.MethodGet, fmt.Sprintf("/pulls?state=open&sort=updated&direction=desc&per_page=%d&page=1", perPage), nil, &items)
		for _, item := range items {
			pulls = append(pulls, item.pull())
		}
	}
	if err != nil {
		if run.stopped(reviewImportStop(err)) {
			return batch, nil, false, true
		}
		run.fail(ctx, repositoryItem, label, err)
		return batch, nil, true, false
	}
	run.succeed(ctx, repositoryItem)
	run.examined += len(pulls)
	complete = true
	// Reviews open in Flow that the list no longer has: read their state
	// (only when the list was complete, so they are really not open).
	if len(pulls) < perPage {
		listed := map[int]bool{}
		for _, pull := range pulls {
			listed[pull.Number] = true
		}
		for _, review := range data.Reviews {
			if review.Provider != repository.Provider || review.Status != "open" || review.Number <= 0 || listed[review.Number] ||
				!strings.EqualFold(review.RepositoryOwner+"/"+review.RepositoryName, repository.FullName) {
				continue
			}
			if run.examined >= run.limits.Total {
				complete = false
				break
			}
			item := run.item(repository, repository.pullKey(review.Number))
			if time.Now().Before(item.BackoffUntil) {
				run.result.BackedOff++
				continue
			}
			run.examined++
			pull, err := fetchImportedPull(ctx, remote, repository.Provider, review.Number)
			if err != nil {
				if run.stopped(reviewImportStop(err)) {
					return batch, syncs, false, true
				}
				run.fail(ctx, item, fmt.Sprintf("%s#%d", label, review.Number), err)
				continue
			}
			run.succeed(ctx, item)
			pulls = append(pulls, pull)
		}
	}
	// Unchanged requests are skipped without a write.
	candidates := []importedPull{}
	for _, pull := range pulls {
		run.result.Scanned++
		key := repository.pullKey(pull.Number)
		item := run.item(repository, key)
		existing, found := importedReview(data, repository, pull)
		if !found && pull.State != "open" {
			continue
		}
		if found && item.Fingerprint == pull.fingerprint() {
			run.result.SkippedUnchanged++
		} else {
			candidates = append(candidates, pull)
		}
		// Files and comments: for a new review or a new head commit.
		if pull.State == "open" {
			synced := item.SyncedHead
			if synced == "" && found && existing.SyncedAt != nil && existing.SyncError == "" {
				synced = existing.HeadSHA
			}
			if !found || (pull.HeadSHA != "" && synced != pull.HeadSHA) {
				if time.Now().Before(item.BackoffUntil) {
					run.result.BackedOff++
				} else {
					syncs = append(syncs, reviewImportSync{repository: repository, number: pull.Number, head: pull.HeadSHA, reviewID: existing.ID})
				}
			}
		}
	}
	if len(candidates) > 0 {
		batch.linked = run.s.importLinkedMembers(ctx, run.workspace, repository, candidates)
		now := time.Now().UTC()
		for _, pull := range candidates {
			key := repository.pullKey(pull.Number)
			existing, found := importedReview(data, repository, pull)
			if found {
				review := existing
				changed := applyImportedPull(&review, pull, now)
				if pull.State == "open" && len(pull.Reviewers) > 0 && len(normalizedStrings(append(slices.Clone(review.ReviewerIDs), importedReviewerIDs(&data, batch.linked, pull.Reviewers)...))) != len(review.ReviewerIDs) {
					changed = true
				}
				if !changed {
					// Only the fingerprint moved (a comment, a label): remember it.
					item := run.item(repository, key)
					item.Fingerprint = pull.fingerprint()
					run.save(ctx, item)
					run.result.SkippedUnchanged++
					continue
				}
			}
			batch.pulls = append(batch.pulls, pull)
			batch.fingerprints[pull.Number] = pull.fingerprint()
		}
	}
	if err := reviewImportCheckBudget(remote); err != nil && run.stopped(err) {
		return batch, syncs, complete, true
	}
	return batch, syncs, complete, false
}

// importedReview is the review of a request: by external id, then by
// repository and number.
func importedReview(data domain.Bootstrap, repository reviewImportRepository, pull importedPull) (domain.CodeReview, bool) {
	if pull.ExternalID != "" {
		if index := slices.IndexFunc(data.Reviews, func(review domain.CodeReview) bool {
			return review.Provider == repository.Provider && review.ExternalID == pull.ExternalID
		}); index >= 0 {
			return data.Reviews[index], true
		}
	}
	return findSyncedReview(data, repository.Provider, repository.FullName, pull.Number)
}

// importLinkedMembers maps the logins of the requests' authors and reviewers
// to members who linked their account on the request's host.
func (s *server) importLinkedMembers(ctx context.Context, workspace string, repository reviewImportRepository, pulls []importedPull) map[string]domain.User {
	members, ok := s.store.WorkspaceMetadataFields(workspace, "users", "members")
	if !ok {
		return map[string]domain.User{}
	}
	logins, hostName := []string{}, ""
	for _, pull := range pulls {
		logins = append(append(logins, pull.Author.Login), pull.Reviewers...)
		if parsed, err := url.Parse(pull.URL); err == nil && parsed.Host != "" && hostName == "" {
			hostName = strings.ToLower(parsed.Host)
		}
	}
	return s.codeHostMemberLogins(ctx, &members, repository.Provider, hostName, logins...)
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
func reviewImportScope(ctx context.Context, batches []reviewImportBatch) context.Context {
	haystack, repositories := []string{}, map[string]bool{}
	for _, batch := range batches {
		repositories[batch.repository.key()] = true
		for _, pull := range batch.pulls {
			haystack = append(haystack, pull.Title, pull.Description, pull.HeadBranch)
		}
	}
	return store.WithMutationScope(ctx, store.MutationScope{IssueIdentifiers: identifierCandidates(strings.Join(haystack, " ")), StatusAutomation: true,
		Resolve: func(data domain.Bootstrap) store.MutationScope {
			scope := store.MutationScope{}
			for _, review := range data.Reviews {
				if repositories[reviewImportRepository{Provider: review.Provider, FullName: review.RepositoryOwner + "/" + review.RepositoryName}.key()] {
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

// apply creates the reviews of new open requests and updates the changed
// ones in one workspace write for the whole run, then remembers their
// fingerprints. It returns the ids of the reviews it created, by request key.
func (run *reviewImportRun) apply(ctx context.Context, batches []reviewImportBatch) map[string]string {
	created := map[string]string{}
	count, names := 0, []string{}
	for _, batch := range batches {
		count += len(batch.pulls)
		names = append(names, batch.repository.Provider+":"+batch.repository.FullName)
	}
	if count == 0 {
		return created
	}
	if run.result.Writes >= run.limits.Writes {
		run.result.Partial = true
		return created
	}
	appUsers := map[string]domain.User{}
	if slices.ContainsFunc(batches, func(batch reviewImportBatch) bool { return batch.repository.Provider == domain.CodeHostGitHub }) {
		appUsers = run.s.githubAppUsers(ctx, run.workspace)
	}
	var (
		imported, updated, closed int
		mergedIssueIDs            []string
	)
	err := run.s.store.MutateWorkspace(reviewImportScope(ctx, batches), run.workspace, "review.imported", "review-import", map[string]any{"count": count, "repositories": names}, func(data *domain.Bootstrap) error {
		imported, updated, closed, mergedIssueIDs, created = 0, 0, 0, nil, map[string]string{}
		now := time.Now().UTC()
		position := 0
		for _, batch := range batches {
			repository := batch.repository
			owner, name, _ := strings.Cut(repository.FullName, "/")
			resolve := reviewActorResolver(data, repository.Provider, appUsers)
			actor := func(author remoteReviewAuthor) domain.User {
				if user, ok := batch.linked[strings.ToLower(strings.TrimSpace(author.Login))]; ok && author.Login != "" {
					return user
				}
				return resolve(author)
			}
			for _, pull := range batch.pulls {
				position++
				existing, found := importedReview(*data, repository, pull)
				if !found {
					if pull.State != "open" {
						continue
					}
					author := actor(pull.Author)
					createdAt := pull.CreatedAt
					if createdAt.IsZero() {
						createdAt = now
					}
					review := domain.CodeReview{ID: idgen.New("review_"), SlugID: fmt.Sprintf("%s-%d", repository.Provider, pull.Number), Provider: repository.Provider,
						ExternalID: pull.ExternalID, Number: pull.Number, Status: "open", RepositoryOwner: owner, RepositoryName: name, Author: author,
						ReviewerIDs: importedReviewerIDs(data, batch.linked, pull.Reviewers), TeamReviewers: []string{}, IssueIDs: []string{}, BranchState: "upToDate",
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
					created[repository.pullKey(pull.Number)] = review.ID
					continue
				}
				index := reviewIndex(*data, existing.ID)
				review := data.Reviews[index]
				before := review.Status
				changed := applyImportedPull(&review, pull, now)
				if pull.State == "open" && len(pull.Reviewers) > 0 {
					if reviewers := normalizedStrings(append(slices.Clone(review.ReviewerIDs), importedReviewerIDs(data, batch.linked, pull.Reviewers)...)); len(reviewers) != len(review.ReviewerIDs) {
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
			}
		}
		return nil
	})
	run.result.Writes++
	if err != nil {
		run.addError(reviewImportFailure{Target: strings.Join(names, ", "), Code: reviewImportCodeUnknown, Message: err.Error()})
		return map[string]string{}
	}
	run.result.Imported += imported
	run.result.Updated += updated
	run.result.Closed += closed
	for _, batch := range batches {
		for number, fingerprint := range batch.fingerprints {
			key := batch.repository.pullKey(number)
			item := run.item(batch.repository, key)
			item.Fingerprint = fingerprint
			run.save(ctx, item)
		}
	}
	if len(mergedIssueIDs) > 0 {
		if err := run.s.store.MarkAgentSessionsMerged(ctx, run.workspace, mergedIssueIDs, time.Now()); err != nil {
			log.Printf("review import workspace=%s: merged sessions: %v", run.workspace, err)
		}
	}
	return created
}

// sync runs the full syncs one after the other within the write budget,
// pausing between writes; a failing request backs off and the others go on.
func (run *reviewImportRun) sync(ctx context.Context, syncs []reviewImportSync, created map[string]string) {
	for _, candidate := range syncs {
		key := candidate.repository.pullKey(candidate.number)
		reviewID := candidate.reviewID
		if reviewID == "" {
			if reviewID = created[key]; reviewID == "" {
				continue
			}
		}
		if run.result.Writes >= run.limits.Writes {
			run.result.Partial = true
			return
		}
		if run.result.Writes > 0 && run.limits.WriteDelay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(run.limits.WriteDelay):
			}
		}
		if ctx.Err() != nil {
			return
		}
		inFlight := run.workspace + "/" + reviewID
		if _, busy := reviewSyncInFlight.LoadOrStore(inFlight, true); busy {
			continue
		}
		_, err := run.s.syncReview(ctx, run.workspace, reviewID, true)
		reviewSyncInFlight.Delete(inFlight)
		if errors.Is(err, errReviewNotSynced) {
			continue
		}
		run.result.Writes++
		item := run.item(candidate.repository, key)
		if err != nil {
			run.fail(ctx, item, fmt.Sprintf("%s %s#%d", reviewHostLabel(candidate.repository.Provider), candidate.repository.FullName, candidate.number), err)
			if run.stopped(reviewImportStop(err)) {
				return
			}
			continue
		}
		run.result.Synced++
		item.SyncedHead, item.Failures, item.LastError, item.BackoffUntil = candidate.head, 0, "", time.Time{}
		run.save(ctx, item)
	}
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

// runReviewImportScheduler runs the due imports of enabled workspaces. With
// FLOW_REVIEW_IMPORT=off it does not even query.
func (s *server) runReviewImportScheduler(ctx context.Context) {
	if reviewImportSettingsFromEnv().Mode != "on" || s.store == nil {
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
			s.runReviewImportTick(ctx, time.Now())
		}
	}
}

func (s *server) runReviewImportTick(ctx context.Context, now time.Time) {
	if reviewImportSettingsFromEnv().Mode != "on" {
		return
	}
	workspaces, err := s.store.DueReviewImports(ctx, now, 20)
	if err != nil {
		log.Printf("review import: due workspaces: %v", err)
		return
	}
	for _, workspace := range workspaces {
		if ctx.Err() != nil {
			return
		}
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		result, err := s.runReviewImport(runCtx, workspace, "schedule")
		cancel()
		switch {
		case errors.Is(err, errReviewImportRunning), errors.Is(err, errReviewImportDisabled), errors.Is(err, errReviewImportOff):
		case err != nil:
			log.Printf("review import workspace=%s: %v", workspace, err)
		case len(result.Errors) > 0:
			log.Printf("review import workspace=%s: %d failed: %s", workspace, result.Failed, strings.Join(result.Errors, "; "))
		}
	}
}

// reviewImportStatus is GET /api/reviews/import.
type reviewImportStatus struct {
	// Available: the server allows imports (FLOW_REVIEW_IMPORT=on); Enabled:
	// the workspace turned them on; Active: both.
	Available    bool                     `json:"available"`
	Enabled      bool                     `json:"enabled"`
	Active       bool                     `json:"active"`
	Interval     int                      `json:"interval"` // seconds
	Repositories []reviewImportRepository `json:"repositories"`
	Limits       reviewImportLimitsView   `json:"limits"`
	Running      bool                     `json:"running"`
	Last         *reviewImportResult      `json:"last,omitempty"`
	NextRunAt    *time.Time               `json:"nextRunAt,omitempty"`
	// RateLimitedUntil: Sync now waits until then.
	RateLimitedUntil *time.Time            `json:"rateLimitedUntil,omitempty"`
	Backoffs         []reviewImportFailure `json:"backoffs"`
	UpdatedAt        *time.Time            `json:"updatedAt,omitempty"`
}

type reviewImportLimitsView struct {
	RepositoriesPerRun    int `json:"repositoriesPerRun"`
	RequestsPerRepository int `json:"requestsPerRepository"`
	RequestsPerRun        int `json:"requestsPerRun"`
	WritesPerRun          int `json:"writesPerRun"`
	WriteDelaySeconds     int `json:"writeDelaySeconds"`
	MaxRepositories       int `json:"maxRepositories"`
}

func (s *server) reviewImportStatus(ctx context.Context, workspace string) (reviewImportStatus, error) {
	settings := reviewImportSettingsFromEnv()
	record, config, last, err := s.loadReviewImport(ctx, workspace)
	if err != nil {
		return reviewImportStatus{}, err
	}
	limits := s.importLimits()
	now := time.Now()
	status := reviewImportStatus{Available: settings.Mode == "on", Enabled: config.Enabled, Active: settings.Mode == "on" && config.Enabled, Interval: int(settings.Interval.Seconds()),
		Repositories: config.Repositories, Running: now.Before(record.LeaseUntil), Last: last, Backoffs: []reviewImportFailure{}, UpdatedAt: config.UpdatedAt,
		Limits: reviewImportLimitsView{RepositoriesPerRun: limits.Repositories, RequestsPerRepository: min(limits.PerRepository, 100), RequestsPerRun: limits.Total, WritesPerRun: limits.Writes,
			WriteDelaySeconds: int(limits.WriteDelay.Seconds()), MaxRepositories: reviewImportMaxApproved}}
	if status.Active && !record.NextRunAt.IsZero() {
		next := record.NextRunAt
		status.NextRunAt = &next
	}
	if last != nil && last.RateLimitedUntil != nil && now.Before(*last.RateLimitedUntil) {
		status.RateLimitedUntil = last.RateLimitedUntil
	}
	backoffs, err := s.store.ReviewImportBackoffs(ctx, workspace, now, reviewImportMaxErrors)
	if err != nil {
		return status, err
	}
	for _, item := range backoffs {
		until := item.BackoffUntil
		code, params, message := reviewImportDecodeError(item.LastError)
		status.Backoffs = append(status.Backoffs, reviewImportFailure{Target: item.Key, Code: code, Params: params, Message: message, Failures: item.Failures, BackoffUntil: &until})
	}
	return status, nil
}

// reviewImportWorkspace is the request's workspace key (the default
// workspace in local development without one).
func (s *server) reviewImportWorkspace(r *http.Request) string {
	if data, ok := s.store.WorkspaceSettingsMetadata(workspaceKey(r)); ok && data.Workspace.URLKey != "" {
		return data.Workspace.URLKey
	}
	return workspaceKey(r)
}

func (s *server) getReviewImport(w http.ResponseWriter, r *http.Request) {
	status, err := s.reviewImportStatus(r.Context(), s.reviewImportWorkspace(r))
	if err != nil {
		writeReviewImportError(w, http.StatusInternalServerError, reviewImportCodeReadFailed, "Could not read the pull request import", nil)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// updateReviewImport — PATCH /api/reviews/import (workspace admins): turns
// the import on or off and replaces the approved repositories.
func (s *server) updateReviewImport(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled      *bool                     `json:"enabled"`
		Repositories *[]reviewImportRepository `json:"repositories"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	workspace := s.reviewImportWorkspace(r)
	record, config, _, err := s.loadReviewImport(r.Context(), workspace)
	if err != nil {
		writeReviewImportError(w, http.StatusInternalServerError, reviewImportCodeReadFailed, "Could not read the pull request import", nil)
		return
	}
	changed := false
	if input.Repositories != nil {
		data, _ := s.store.WorkspaceMetadata(workspace)
		repositories, seen := []reviewImportRepository{}, map[string]bool{}
		for _, item := range *input.Repositories {
			repository, err := normalizeImportRepository(item)
			if err != nil {
				var invalid *reviewImportRequestError
				if errors.As(err, &invalid) {
					writeReviewImportError(w, http.StatusBadRequest, invalid.code, invalid.message, invalid.params)
				} else {
					writeError(w, http.StatusBadRequest, err.Error())
				}
				return
			}
			if !s.reviewImportSourceConnected(r.Context(), data, repository.Provider) {
				writeReviewImportError(w, http.StatusBadRequest, reviewImportCodeSourceNotConnected, "Connect "+reviewHostLabel(repository.Provider)+" before approving its repositories", map[string]string{"provider": reviewHostLabel(repository.Provider)})
				return
			}
			if !seen[repository.key()] {
				seen[repository.key()] = true
				repositories = append(repositories, repository)
			}
		}
		if len(repositories) > reviewImportMaxApproved {
			writeReviewImportError(w, http.StatusBadRequest, reviewImportCodeTooManyRepos, fmt.Sprintf("Approve at most %d repositories", reviewImportMaxApproved), map[string]string{"max": strconv.Itoa(reviewImportMaxApproved)})
			return
		}
		changed = !slices.Equal(repositories, config.Repositories)
		config.Repositories = repositories
	}
	if input.Enabled != nil {
		changed = changed || *input.Enabled != config.Enabled
		config.Enabled = *input.Enabled
	}
	if !changed {
		// The current settings again: nothing is saved and nothing becomes due.
		s.getReviewImport(w, r)
		return
	}
	now := time.Now().UTC()
	config.UpdatedBy, config.UpdatedAt = authUser(r).ID, &now
	raw, _ := json.Marshal(config)
	// Turning it on or changing what is approved makes the workspace due now.
	next := time.Time{}
	if config.Enabled && (changed || !record.Enabled) {
		next = now
	}
	if err := s.store.SaveReviewImportSettings(r.Context(), workspace, string(raw), config.Enabled, next); err != nil {
		writeReviewImportError(w, http.StatusInternalServerError, reviewImportCodeSaveFailed, "Could not save the pull request import", nil)
		return
	}
	s.getReviewImport(w, r)
}

// reviewImportSourceConnected reports a connected source of the code host:
// a classic connection, or for GitHub an App installation.
func (s *server) reviewImportSourceConnected(ctx context.Context, data domain.Bootstrap, provider string) bool {
	if slices.ContainsFunc(data.IntegrationConnections, func(connection domain.IntegrationConnection) bool {
		return connection.Provider == provider && connection.Status != "disconnected"
	}) {
		return true
	}
	if provider == domain.CodeHostGitHub {
		if host := s.gitHubAppHost(); host != nil && host.Configured() {
			installations, err := s.store.CodeHostInstallations(ctx, data.Workspace.URLKey, domain.CodeHostGitHub)
			return err == nil && len(installations) > 0
		}
	}
	return false
}

// importReviewsFromCodeHosts — POST /api/reviews/import ("Sync now",
// workspace admins): runs the import of the approved repositories at once.
func (s *server) importReviewsFromCodeHosts(w http.ResponseWriter, r *http.Request) {
	workspace := s.reviewImportWorkspace(r)
	if _, _, last, err := s.loadReviewImport(r.Context(), workspace); err == nil && last != nil && last.RateLimitedUntil != nil && time.Now().Before(*last.RateLimitedUntil) {
		until := *last.RateLimitedUntil
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "The code host's rate limit is spent; try again after " + until.Format(time.RFC3339), "code": reviewImportCodeRateLimited, "rateLimitedUntil": until})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	result, err := s.runReviewImport(ctx, workspace, "manual")
	switch {
	case errors.Is(err, errReviewImportRunning):
		writeReviewImportError(w, http.StatusConflict, reviewImportCodeAlreadyRunning, "A sync with the code hosts is already running", nil)
	case errors.Is(err, errReviewImportOff):
		writeReviewImportError(w, http.StatusConflict, reviewImportCodeServerOff, "Pull and merge request import is turned off on this server", nil)
	case errors.Is(err, errReviewImportDisabled):
		writeReviewImportError(w, http.StatusConflict, reviewImportCodeWorkspaceOff, "Turn on pull request import in the GitHub or GitLab settings first", nil)
	case err != nil:
		log.Printf("review import workspace=%s: %v", workspace, err)
		writeReviewImportError(w, http.StatusInternalServerError, reviewImportCodeSyncFailed, "Could not sync with the code hosts", nil)
	default:
		writeJSON(w, http.StatusOK, result)
	}
}
