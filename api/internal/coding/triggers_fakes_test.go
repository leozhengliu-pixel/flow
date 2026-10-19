package coding

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

// fakeTriggerStore implements every store subset the trigger code uses.
type fakeTriggerStore struct {
	mu           sync.Mutex
	sessions     map[string]domain.CodingSession
	anchors      map[string]domain.AgentTask
	keys         map[string]string
	inbox        map[string][]InboxItem
	environments []domain.CodingEnvironment
	pullRequests map[string]domain.SessionPullRequest
	spend        int64
	spendQueries []SpendQuery
	nextID       int
}

func newFakeTriggerStore(environments ...domain.CodingEnvironment) *fakeTriggerStore {
	return &fakeTriggerStore{
		sessions: map[string]domain.CodingSession{}, anchors: map[string]domain.AgentTask{}, keys: map[string]string{},
		inbox: map[string][]InboxItem{}, environments: environments, pullRequests: map[string]domain.SessionPullRequest{},
	}
}

func (f *fakeTriggerStore) newID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	return fmt.Sprintf("s%02d", f.nextID)
}

func (f *fakeTriggerStore) CreateCodingSession(_ context.Context, session domain.CodingSession, anchor domain.AgentTask, key string) (domain.CodingSession, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.keys[session.WorkspaceKey+"\x00"+key]; ok {
		return f.sessions[id], false, nil
	}
	session.Version = 1
	f.sessions[session.ID] = session
	f.anchors[anchor.ID] = anchor
	f.keys[session.WorkspaceKey+"\x00"+key] = session.ID
	return session, true, nil
}

func (f *fakeTriggerStore) CodingSession(_ context.Context, workspaceKey, id string) (domain.CodingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[id]
	if !ok || session.WorkspaceKey != workspaceKey {
		return domain.CodingSession{}, ErrNotFound
	}
	return session, nil
}

func (f *fakeTriggerStore) ListCodingSessions(_ context.Context, workspaceKey string, filter SessionFilter) ([]domain.CodingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []domain.CodingSession
	for _, session := range f.sessions {
		if session.WorkspaceKey != workspaceKey || (filter.IssueID != "" && session.IssueID != filter.IssueID) {
			continue
		}
		if len(filter.States) > 0 {
			match := false
			for _, state := range filter.States {
				match = match || state == session.State
			}
			if !match {
				continue
			}
		}
		result = append(result, session)
	}
	return result, nil
}

func (f *fakeTriggerStore) UpdateCodingSession(_ context.Context, workspaceKey, id string, mutate func(*domain.CodingSession) error) (domain.CodingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[id]
	if !ok || session.WorkspaceKey != workspaceKey {
		return domain.CodingSession{}, ErrNotFound
	}
	copyOf := session
	if err := mutate(&copyOf); err != nil {
		return domain.CodingSession{}, err
	}
	if from, to := session.Phase, copyOf.Phase; from != to && !CanTransition(from, to) {
		return domain.CodingSession{}, fmt.Errorf("illegal transition %s → %s", from, to)
	}
	copyOf.Version++
	f.sessions[id] = copyOf
	return copyOf, nil
}

func (f *fakeTriggerStore) EnqueueCodingInbox(_ context.Context, _ string, sessionID string, item InboxItem) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.inbox[sessionID] {
		if existing.Turn != nil && item.Turn != nil && existing.Turn.ID == item.Turn.ID {
			return existing.Seq, nil
		}
	}
	item.Seq = int64(len(f.inbox[sessionID]) + 1)
	f.inbox[sessionID] = append(f.inbox[sessionID], item)
	return item.Seq, nil
}

func (f *fakeTriggerStore) ListCodingEnvironments(_ context.Context, _ string, includeArchived bool) ([]domain.CodingEnvironment, error) {
	if includeArchived {
		return f.environments, nil
	}
	return activeEnvironments(f.environments), nil
}

func (f *fakeTriggerStore) CodingEnvironmentForRepository(_ context.Context, _ string, codeHost, hostName, repository string) (domain.CodingEnvironment, error) {
	for _, environment := range activeEnvironments(f.environments) {
		if environment.CodeHost == codeHost && environment.HostName == hostName && strings.EqualFold(environment.Repository, repository) {
			return environment, nil
		}
	}
	return domain.CodingEnvironment{}, ErrNotFound
}

func prKey(codeHost, hostName, repository string, number int) string {
	return fmt.Sprintf("%s|%s|%s|%d", codeHost, hostName, strings.ToLower(repository), number)
}

func (f *fakeTriggerStore) UpsertSessionPullRequest(_ context.Context, _ string, pr domain.SessionPullRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pullRequests[pr.SessionID+"|"+prKey(pr.CodeHost, pr.HostName, pr.Repository, pr.Number)] = pr
	return nil
}

func (f *fakeTriggerStore) SessionsForChangeRequest(_ context.Context, codeHost, hostName, repository string, number int) ([]domain.CodingSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []domain.CodingSession
	for key, pr := range f.pullRequests {
		if strings.HasSuffix(key, "|"+prKey(codeHost, hostName, repository, number)) {
			if session, ok := f.sessions[pr.SessionID]; ok {
				result = append(result, session)
			}
		}
	}
	return result, nil
}

func (f *fakeTriggerStore) CodingSpendMicros(_ context.Context, query SpendQuery) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spendQueries = append(f.spendQueries, query)
	return f.spend, nil
}

func (f *fakeTriggerStore) turns(sessionID string) []Turn {
	f.mu.Lock()
	defer f.mu.Unlock()
	var turns []Turn
	for _, item := range f.inbox[sessionID] {
		if item.Turn != nil {
			turns = append(turns, *item.Turn)
		}
	}
	return turns
}

// recordingNotifier records notifications.
type recordingNotifier struct {
	mu         sync.Mutex
	approvals  []string
	selections []string
	resolved   []string
	checks     []string
}

func (n *recordingNotifier) ApprovalRequested(_ context.Context, session domain.CodingSession, _ TriggerStartRequest) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.approvals = append(n.approvals, session.ID)
	return nil
}

func (n *recordingNotifier) EnvironmentSelectionRequested(_ context.Context, session domain.CodingSession, _ []EnvironmentSuggestion) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.selections = append(n.selections, session.ID)
	return nil
}

func (n *recordingNotifier) ApprovalResolved(_ context.Context, session domain.CodingSession, decision, _ string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.resolved = append(n.resolved, session.ID+":"+decision)
	return nil
}

func (n *recordingNotifier) ChecksFailed(_ context.Context, session domain.CodingSession, _ domain.SessionPullRequest, status codehost.CIStatus) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.checks = append(n.checks, session.ID+":"+status.State)
	return nil
}

// fakeHost is a code host whose ParseWebhook returns a canned event after
// checking a shared-secret header, and that records comments.
type fakeHost struct {
	codehost.Stub
	secret   string
	event    codehost.WebhookEvent
	comments []string
}

func (h *fakeHost) ParseWebhook(_ context.Context, header http.Header, _ []byte) (codehost.WebhookEvent, error) {
	if header.Get("X-Fake-Signature") != h.secret {
		return codehost.WebhookEvent{}, codehost.ErrBadSignature
	}
	return h.event, nil
}

func (h *fakeHost) Comment(_ context.Context, _ string, _ codehost.RepoRef, number int, body string) error {
	h.comments = append(h.comments, fmt.Sprintf("#%d %s", number, body))
	return nil
}

type fakeCommenters map[string]Commenter

func (f fakeCommenters) ResolveCommenter(_ context.Context, _, _, _, login, _ string) (Commenter, error) {
	return f[strings.ToLower(login)], nil
}

func env(id, name, repository string) domain.CodingEnvironment {
	return domain.CodingEnvironment{ID: id, Name: name, CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: repository}
}

var fixedNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func newTestTriggers(store *fakeTriggerStore, notifier *recordingNotifier) *Triggers {
	starter := &StoreStarter{Sessions: store, PullRequests: store, Now: func() time.Time { return fixedNow }, NewID: store.newID}
	return &Triggers{Starter: starter, Sessions: store, Environments: store, Spend: store, Notifier: notifier, Now: func() time.Time { return fixedNow }}
}
