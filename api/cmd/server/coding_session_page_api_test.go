package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// compareHost is a code host fake with the optional Comparer.
type compareHost struct {
	codehost.Stub
	calls  atomic.Int32
	result codehost.Comparison
	err    error
}

func (h *compareHost) Compare(_ context.Context, _ string, repo codehost.RepoRef, base, head string) (codehost.Comparison, error) {
	h.calls.Add(1)
	if repo.FullName != "acme/app" || base != "main" || head != "flow/fix" {
		return codehost.Comparison{}, codehost.ErrNotFound
	}
	return h.result, h.err
}

func codingPageSession(t *testing.T, s *server, handler http.Handler) domain.CodingSession {
	t.Helper()
	requestJSON[codingSettingsView](t, handler, http.MethodPatch, "/api/coding/settings", map[string]any{"enabled": true}, http.StatusOK)
	metadata, _ := s.store.WorkspaceMetadata("")
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Flaky test", "teamId": metadata.Teams[0].ID}, http.StatusCreated)
	return requestJSON[domain.CodingSession](t, handler, http.MethodPost, "/api/coding/sessions", map[string]any{"prompt": "Fix the flaky test", "source": "issue", "issueId": issue.ID}, http.StatusCreated)
}

func TestCodingSessionDiff(t *testing.T) {
	s, handler := codingTestServer(t, "")
	session := codingPageSession(t, s, handler)
	metadata, _ := s.store.WorkspaceMetadata("")
	ws := metadata.Workspace.URLKey
	path := "/api/coding/sessions/" + session.ID + "/diff"

	// Nothing pushed, no runner summary: no changes yet.
	if view := requestJSON[codingDiffView](t, handler, http.MethodGet, path, nil, http.StatusOK); view.Source != "none" || len(view.Files) != 0 {
		t.Fatalf("empty diff: %+v", view)
	}
	requestJSON[map[string]any](t, handler, http.MethodGet, "/api/coding/sessions/missing/diff", nil, http.StatusNotFound)

	// The runner's per-file summary before the branch is on the host.
	if _, err := s.store.UpdateCodingSession(t.Context(), ws, session.ID, func(current *domain.CodingSession) error {
		current.CodeHost, current.Repository, current.BranchName, current.BaseRef = domain.CodeHostGitHub, "acme/app", "flow/fix", "main"
		current.Diffs = []domain.CodingDiffSummary{{Path: "a.go", Additions: 2, Deletions: 1, Status: "modified"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	host := &compareHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitHub}, result: codehost.Comparison{HeadSHA: "head", Files: []codehost.ComparedFile{
		{Path: "a.go", Status: "modified", Additions: 2, Deletions: 1, Patch: "@@ -1,2 +1,3 @@\n a\n-b\n+c\n+d\n"},
		{Path: "logo.png", Status: "added", Binary: true},
	}}}
	s.coding.hosts[domain.CodeHostGitHub] = host
	view := requestJSON[codingDiffView](t, handler, http.MethodGet, path, nil, http.StatusOK)
	if view.Source != "summary" || len(view.Files) != 1 || view.Files[0].Additions != 2 || len(view.Files[0].Hunks) != 0 || host.calls.Load() != 0 {
		t.Fatalf("summary diff (unpushed branch must not call the host): %+v", view)
	}

	// A head SHA alone (the clone baseline of a branch that never reached the host) is no evidence of a push.
	if _, err := s.store.UpdateCodingSession(t.Context(), ws, session.ID, func(current *domain.CodingSession) error {
		current.HeadSHA = "head"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if view = requestJSON[codingDiffView](t, handler, http.MethodGet, path, nil, http.StatusOK); view.Source != "summary" || host.calls.Load() != 0 {
		t.Fatalf("a head SHA is not proof of a push: %+v", view)
	}

	// Pushed: the branch compared with its base on the code host, with hunks and line numbers.
	if _, err := s.store.UpdateCodingSession(t.Context(), ws, session.ID, func(current *domain.CodingSession) error {
		current.ExistingBranch = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view = requestJSON[codingDiffView](t, handler, http.MethodGet, path, nil, http.StatusOK)
	if view.Source != "codeHost" || view.Base != "main" || view.Head != "flow/fix" || view.HeadSHA != "head" || view.Additions != 2 || view.Deletions != 1 || len(view.Files) != 2 {
		t.Fatalf("host diff: %+v", view)
	}
	hunk := view.Files[0].Hunks
	if len(hunk) != 1 || len(hunk[0].Lines) != 4 || hunk[0].Lines[2] != (codehost.DiffLine{Kind: "add", NewLine: 2, Text: "c"}) || !view.Files[1].Binary || view.Files[1].Hunks == nil {
		t.Fatalf("hunks: %+v", view.Files)
	}
	requestJSON[codingDiffView](t, handler, http.MethodGet, path, nil, http.StatusOK)
	if host.calls.Load() != 1 {
		t.Fatalf("the compare must be cached while the head is unchanged: %d calls", host.calls.Load())
	}

	// A host failure falls back to the summary with the error code.
	codingDiffCache.Lock()
	codingDiffCache.entries = map[string]codingDiffCacheEntry{}
	codingDiffCache.Unlock()
	host.err = codehost.ErrTransient
	view = requestJSON[codingDiffView](t, handler, http.MethodGet, path, nil, http.StatusOK)
	if view.Source != "summary" || view.ErrorCode != string(domain.CodingErrGitHubTransientError) || len(view.Files) != 1 {
		t.Fatalf("fallback: %+v", view)
	}
}

func TestCodingDiffLineCap(t *testing.T) {
	big := strings.Repeat("+x\n", codingDiffMaxLines)
	view := codingDiffFromComparison(codingDiffView{Files: []codingDiffFile{}}, codehost.Comparison{Files: []codehost.ComparedFile{
		{Path: "small.go", Status: "modified", Additions: 1, Patch: "@@ -1 +1 @@\n+y\n"},
		{Path: "big.go", Status: "added", Additions: codingDiffMaxLines, Patch: "@@ -0,0 +1," + "20000 @@\n" + big},
	}})
	if len(view.Files) != 2 || len(view.Files[0].Hunks) != 1 || !view.Files[1].TooLarge || len(view.Files[1].Hunks) != 0 || view.Additions != codingDiffMaxLines+1 {
		t.Fatalf("cap: %+v", view.Files[1].TooLarge)
	}
}

// "Subscribe to session": the creator is subscribed until they opt out; subscribers get the session's Inbox
// notification on completion, questions and errors, and members who unsubscribed do not.
func TestCodingSessionSubscription(t *testing.T) {
	s, handler := codingTestServer(t, "")
	s.store.SetAgentReplyListener(s.notifyAgentReply)
	t.Cleanup(func() { s.store.SetAgentReplyListener(nil) })
	session := codingPageSession(t, s, handler)
	metadata, _ := s.store.WorkspaceMetadata("")
	ws := metadata.Workspace.URLKey
	creator := session.CreatorID
	other := ""
	for _, user := range metadata.Users {
		if user.ID != creator && !user.App {
			other = user.ID
		}
	}
	if other == "" {
		t.Fatal("fixture has no second member")
	}
	if !domain.CodingSessionSubscribed(session, creator) || domain.CodingSessionSubscribed(session, other) {
		t.Fatalf("default subscription: %+v", session)
	}
	path := "/api/coding/sessions/" + session.ID + "/subscription"
	requestJSON[map[string]any](t, handler, http.MethodPost, path, map[string]any{}, http.StatusBadRequest)
	got := requestJSON[domain.CodingSession](t, handler, http.MethodPost, path, map[string]any{"subscribed": false}, http.StatusOK)
	if domain.CodingSessionSubscribed(got, creator) || !slices.Contains(got.UnsubscribedIDs, creator) {
		t.Fatalf("unsubscribe: %+v", got.UnsubscribedIDs)
	}
	again := requestJSON[domain.CodingSession](t, handler, http.MethodPost, path, map[string]any{"subscribed": false}, http.StatusOK)
	if again.Version != got.Version {
		t.Fatal("an unchanged subscription must not write")
	}
	if _, err := s.coding.service.SetSubscription(t.Context(), ws, session.ID, other, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.coding.service.FailSession(t.Context(), ws, session.ID, domain.CodingErrHarnessFailed, "The agent crashed."); err != nil {
		t.Fatal(err)
	}
	notifications := func(userID string) []domain.Notification {
		return agentThreadNotifications(t, s, metadata.Workspace.URLKey, userID)
	}
	if items := notifications(other); len(items) != 1 || items[0].Payload == nil || items[0].Payload.AgentActivity != "error" || items[0].IssueID != session.IssueID {
		t.Fatalf("subscriber: %+v", items)
	}
	if items := notifications(creator); len(items) != 0 {
		t.Fatalf("the unsubscribed creator was notified: %+v", items)
	}

	// Subscribing again brings the creator back; the other member unsubscribes.
	got = requestJSON[domain.CodingSession](t, handler, http.MethodPost, path, map[string]any{"subscribed": true}, http.StatusOK)
	if !domain.CodingSessionSubscribed(got, creator) || slices.Contains(got.SubscriberIDs, creator) || slices.Contains(got.UnsubscribedIDs, creator) {
		t.Fatalf("resubscribe: %+v %+v", got.SubscriberIDs, got.UnsubscribedIDs)
	}
	if _, err := s.coding.service.SetSubscription(t.Context(), ws, session.ID, other, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.coding.service.Prompt(t.Context(), ws, session.ID, coding.PromptInput{Prompt: "Try again", AuthorID: creator}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.coding.service.FailSession(t.Context(), ws, session.ID, domain.CodingErrHarnessFailed, "Crashed again."); err != nil {
		t.Fatal(err)
	}
	if items := notifications(creator); len(items) != 1 || items[0].Payload.AgentActivity != "error" {
		t.Fatalf("resubscribed creator: %+v", items)
	}
	if items := notifications(other); len(items) != 1 || items[0].OccurrenceCount != 1 {
		t.Fatalf("the unsubscribed member was notified again: %+v", items)
	}
}

// Follow-ups carry composer files: the prompt activity shows them and the runner gets signed URLs.
func TestCodingPromptFiles(t *testing.T) {
	s, handler := codingTestServer(t, "")
	session := codingPageSession(t, s, handler)
	path := "/api/coding/sessions/" + session.ID + "/prompt"
	requestJSON[map[string]any](t, handler, http.MethodPost, path, map[string]any{"prompt": "x", "files": []map[string]any{{"name": "a", "url": "https://evil.test/a.png"}}}, http.StatusBadRequest)
	tooMany := make([]map[string]any, codingPromptFileLimit+1)
	for index := range tooMany {
		tooMany[index] = map[string]any{"url": "/uploads/agent_upload_0123456789abcdef_1_a.png"}
	}
	requestJSON[map[string]any](t, handler, http.MethodPost, path, map[string]any{"prompt": "x", "files": tooMany}, http.StatusBadRequest)
	file := map[string]any{"name": "screen.png", "contentType": "image/png", "size": 12, "url": "/uploads/agent_upload_0123456789abcdef_1_screen.png"}
	requestJSON[domain.CodingSession](t, handler, http.MethodPost, path, map[string]any{"prompt": "", "files": []any{file}}, http.StatusOK)

	items, err := s.store.CodingInbox(t.Context(), session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var turn *coding.Turn
	for _, item := range items {
		var decoded coding.InboxItem
		if json.Unmarshal(item.Payload, &decoded) == nil && decoded.Turn != nil && decoded.Turn.Kind == coding.TurnFollowUp {
			turn = decoded.Turn
		}
	}
	if turn == nil || turn.Prompt != "See the attached files." || len(turn.Attachments) != 1 || !strings.Contains(turn.Attachments[0], "/api/files/signed/agent_upload_0123456789abcdef_1_screen.png?expires=") {
		t.Fatalf("turn: %+v", turn)
	}
	metadata, _ := s.store.WorkspaceSettingsMetadata("")
	activities, err := s.store.AgentActivities(t.Context(), metadata.Workspace.URLKey, session.AgentSessionID, "")
	if err != nil {
		t.Fatal(err)
	}
	var prompt *domain.AgentActivity
	for index := range activities {
		if activities[index].Type == "prompt" && activities[index].Body == "See the attached files." {
			prompt = &activities[index]
		}
	}
	if prompt == nil {
		t.Fatalf("no prompt activity: %+v", activities)
	}
	raw, _ := json.Marshal(prompt.Metadata["attachments"])
	var files []domain.AgentMessageAttachment
	if err := json.Unmarshal(raw, &files); err != nil || len(files) != 1 || files[0].URL != "/uploads/agent_upload_0123456789abcdef_1_screen.png" || files[0].Name != "screen.png" || files[0].ContentType != "image/png" {
		t.Fatalf("activity attachments: %s %v", raw, err)
	}
}
