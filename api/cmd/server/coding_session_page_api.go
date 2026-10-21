package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// The coding session page (the reference app's /diff/<id> page): the full
// diff of the session's branch, "Subscribe to session", and follow-ups with
// composer attachments.

// codingDiffMaxLines caps the diff lines one response carries; files past
// the cap keep their counts and are marked tooLarge.
const codingDiffMaxLines = 20000

// codingDiffCacheTTL keeps a code host compare for a short while: the page
// refetches the diff on every session push.
const codingDiffCacheTTL = 20 * time.Second

// codingDiffFile is one changed file of GET /api/coding/sessions/{id}/diff.
type codingDiffFile struct {
	Path         string              `json:"path"`
	PreviousPath string              `json:"previousPath,omitempty"`
	Status       string              `json:"status"`
	Additions    int                 `json:"additions"`
	Deletions    int                 `json:"deletions"`
	Binary       bool                `json:"binary,omitempty"`
	TooLarge     bool                `json:"tooLarge,omitempty"`
	Hunks        []codehost.DiffHunk `json:"hunks"`
}

// codingDiffView is the session's change set. Source is "codeHost" (the
// branch compared with its base on the code host, with hunks), "summary"
// (the runner's per-file counts only, when the host cannot be asked) or
// "none" (no changes yet).
type codingDiffView struct {
	Source    string           `json:"source"`
	Base      string           `json:"base,omitempty"`
	Head      string           `json:"head,omitempty"`
	HeadSHA   string           `json:"headSha,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`
	ErrorCode string           `json:"errorCode,omitempty"`
	Additions int              `json:"additions"`
	Deletions int              `json:"deletions"`
	Files     []codingDiffFile `json:"files"`
}

type codingDiffCacheEntry struct {
	key  string
	at   time.Time
	view codingDiffView
}

var codingDiffCache = struct {
	sync.Mutex
	entries map[string]codingDiffCacheEntry
}{entries: map[string]codingDiffCacheEntry{}}

// getCodingSessionDiff answers GET /api/coding/sessions/{id}/diff.
func (s *server) getCodingSessionDiff(w http.ResponseWriter, r *http.Request) {
	viewer, session, ok := s.codingSessionAccess(w, r, false)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.codingSessionDiff(r.Context(), viewer.workspace, session))
}

// codingDiffRefs are the refs the session's changes are compared between:
// its (latest) PR/MR, or the branch it works on and the base it started from.
func codingDiffRefs(session domain.CodingSession) (base, head string) {
	base, head = session.BaseRef, session.BranchName
	if pr := session.LatestPullRequest; pr != nil {
		base, head = firstNonEmpty(pr.BaseBranch, base), firstNonEmpty(pr.HeadBranch, head)
	}
	return base, head
}

func (s *server) codingSessionDiff(ctx context.Context, workspace string, session domain.CodingSession) codingDiffView {
	base, head := codingDiffRefs(session)
	view := codingDiffView{Source: "none", Base: base, Head: head, Files: []codingDiffFile{}}
	pushed := session.Pushed || session.ExistingBranch || session.LatestPullRequest != nil || len(session.PullRequests) > 0 || len(session.CommitsByTurnActivityID) > 0
	var host codehost.Host
	if s.coding != nil {
		host = s.coding.hosts[session.CodeHost]
	}
	comparer, canCompare := host.(codehost.Comparer)
	if canCompare && pushed && base != "" && head != "" && session.Repository != "" {
		key := strings.Join([]string{session.ID, base, head, session.HeadSHA}, "\x00")
		if cached, ok := codingDiffCached(session.ID, key); ok {
			return cached
		}
		compareCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		comparison, err := comparer.Compare(compareCtx, workspace, codehost.RepoRef{CodeHost: session.CodeHost, HostName: session.HostName, FullName: session.Repository}, base, head)
		cancel()
		if err == nil {
			view = codingDiffFromComparison(view, comparison)
			codingDiffStore(session.ID, key, view)
			return view
		}
		if !errors.Is(err, codehost.ErrNotFound) {
			slog.WarnContext(ctx, "coding session diff: compare failed", "session", session.ID, "error", err)
			view.ErrorCode = string(codehost.ErrorCode(session.CodeHost, err))
		}
	}
	for _, diff := range session.Diffs {
		view.Source = "summary"
		view.Additions += diff.Additions
		view.Deletions += diff.Deletions
		view.Files = append(view.Files, codingDiffFile{Path: diff.Path, Status: diff.Status, Additions: diff.Additions, Deletions: diff.Deletions, Hunks: []codehost.DiffHunk{}})
	}
	return view
}

func codingDiffFromComparison(view codingDiffView, comparison codehost.Comparison) codingDiffView {
	view.Source, view.HeadSHA, view.Truncated = "codeHost", comparison.HeadSHA, comparison.Truncated
	lines := 0
	for _, file := range comparison.Files {
		item := codingDiffFile{Path: file.Path, PreviousPath: file.PreviousPath, Status: file.Status, Additions: file.Additions, Deletions: file.Deletions, Binary: file.Binary, TooLarge: file.TooLarge, Hunks: []codehost.DiffHunk{}}
		if hunks := codehost.ParsePatch(file.Patch); len(hunks) > 0 {
			count := 0
			for _, hunk := range hunks {
				count += len(hunk.Lines)
			}
			if lines+count > codingDiffMaxLines {
				item.TooLarge = true
			} else {
				lines += count
				item.Hunks = hunks
			}
		}
		view.Additions += item.Additions
		view.Deletions += item.Deletions
		view.Files = append(view.Files, item)
	}
	return view
}

func codingDiffCached(sessionID, key string) (codingDiffView, bool) {
	codingDiffCache.Lock()
	defer codingDiffCache.Unlock()
	entry, ok := codingDiffCache.entries[sessionID]
	if !ok || entry.key != key || time.Since(entry.at) > codingDiffCacheTTL {
		return codingDiffView{}, false
	}
	return entry.view, true
}

func codingDiffStore(sessionID, key string, view codingDiffView) {
	codingDiffCache.Lock()
	defer codingDiffCache.Unlock()
	if len(codingDiffCache.entries) >= 256 {
		for id, entry := range codingDiffCache.entries {
			if time.Since(entry.at) > codingDiffCacheTTL {
				delete(codingDiffCache.entries, id)
			}
		}
		if len(codingDiffCache.entries) >= 256 {
			codingDiffCache.entries = map[string]codingDiffCacheEntry{}
		}
	}
	codingDiffCache.entries[sessionID] = codingDiffCacheEntry{key: key, at: time.Now(), view: view}
}

// setCodingSessionSubscription answers POST /api/coding/sessions/{id}/subscription
// ({"subscribed": bool}): "Subscribe to session" in the Session details menu.
func (s *server) setCodingSessionSubscription(w http.ResponseWriter, r *http.Request) {
	viewer, session, ok := s.codingSessionAccess(w, r, false)
	if !ok {
		return
	}
	var input struct {
		Subscribed *bool `json:"subscribed"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Subscribed == nil {
		writeCodingError(w, fmt.Errorf("%w: subscribed is required", coding.ErrInvalidInput))
		return
	}
	updated, err := s.coding.service.SetSubscription(r.Context(), viewer.workspace, session.ID, viewer.user.ID, *input.Subscribed)
	s.respondCodingSession(w, r.Context(), viewer, updated, err)
}

// codingPromptFileLimit is the most composer files a follow-up carries
// (the agent composer's limit).
const codingPromptFileLimit = 8

// codingPromptFileLifetime is how long the runner's signed attachment URLs work.
const codingPromptFileLifetime = 7 * 24 * time.Hour

// codingPromptFiles checks the follow-up's composer files (the viewer's own
// agent uploads) and returns them with the signed URLs the runner can fetch.
func (s *server) codingPromptFiles(r *http.Request, viewer codingViewer, files []domain.AgentMessageAttachment) ([]domain.AgentMessageAttachment, []string, error) {
	if len(files) == 0 {
		return nil, nil, nil
	}
	if len(files) > codingPromptFileLimit {
		return nil, nil, fmt.Errorf("%w: at most %d files", coding.ErrInvalidInput, codingPromptFileLimit)
	}
	key, err := s.fileSigningKey(r.Context())
	if err != nil {
		return nil, nil, err
	}
	expires := time.Now().Add(codingPromptFileLifetime).Truncate(time.Second)
	kept := make([]domain.AgentMessageAttachment, 0, len(files))
	signed := make([]string, 0, len(files))
	for _, file := range files {
		name := uploadNameFromURL(file.URL)
		if name == "" || (!s.authDisabled && !agentUploadVisible(viewer.user.ID, "/uploads/"+name)) {
			return nil, nil, fmt.Errorf("%w: attach files uploaded from the composer", coding.ErrInvalidInput)
		}
		file.URL = "/uploads/" + name
		file.Name = firstNonEmpty(strings.TrimSpace(file.Name), name)
		kept = append(kept, file)
		signed = append(signed, signedFileURL(key, externalBaseURL(r), name, expires))
	}
	return kept, signed, nil
}
