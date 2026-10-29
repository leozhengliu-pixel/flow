package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func newPeerChatDMFixture(t *testing.T, handler http.HandlerFunc) (*chatDMFixture, peerServers) {
	t.Helper()
	peers := newPeerServers(t)
	f := newChatDMFixture(t, nil)
	provider := httptest.NewServer(handler)
	t.Cleanup(provider.Close)
	for _, s := range peers.servers {
		if err := s.store.PutChatIntegration(t.Context(), f.record); err != nil {
			t.Fatal(err)
		}
		if err := s.store.PutChatIdentity(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, f.identity); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(f.credentials)
		if err := s.store.PutConnectorSecret(t.Context(), chatCredentialID(f.record), raw, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		s.agent = appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256}
		s.agentClient = provider.Client()
	}
	f.s = peers.servers[0]
	t.Cleanup(func() {
		peers.deafen[0]()
		peers.deafen[1]()
		chatDMReplies.Wait()
	})
	return f, peers
}

// Advance only the queue's clock to exercise the production lease-expiry path
// without sleeping a minute. The old process is canceled before reclaiming.
func recoverPeerChatDMJob(t *testing.T, s *server, workspace string) domain.ChatJob {
	t.Helper()
	jobs, err := s.store.ClaimChatJobs(t.Context(), workspace, chatDMReplyJobKind, 1, time.Now().Add(2*time.Minute))
	if err != nil || len(jobs) != 1 {
		t.Fatalf("expired reply claim: %+v %v", jobs, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	l := &chatDMReplyLease{s: s, ctx: ctx, cancel: cancel, job: jobs[0]}
	if err := json.Unmarshal(jobs[0].Payload, &l.payload); err != nil {
		t.Fatal(err)
	}
	if err := s.processChatDMReplyJob(l); err != nil {
		t.Fatal(err)
	}
	// Re-delivery after a lost acknowledgement uses the checkpointed final
	// card and action, without another generation or another send.
	if err := s.processChatDMReplyJob(l); err != nil {
		t.Fatal(err)
	}
	if err := s.store.FinishChatJob(t.Context(), l.job, "completed", time.Time{}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.store.ReleaseChatReplyLeases(t.Context(), l.job); err != nil {
		t.Fatal(err)
	}
	return l.job
}

func TestPeerChatDMReplyTakeoverInterruptsAndRetryIsIdempotent(t *testing.T) {
	var calls atomic.Int32
	f, peers := newPeerChatDMFixture(t, func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if call > 1 {
			_, _ = w.Write([]byte(chatDMTextTurn("Recovered answer.")))
			return
		}
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Partial answer\"}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	f.text(t, "om_peer_takeover", "Answer slowly")
	f.waitFor(t, "partial reply", func() bool {
		messages := f.sent()
		return len(messages) == 1 && strings.Contains(f.lastUpdate(messages[0].ID), "Partial answer")
	})
	card := f.sent()[0]
	if jobs, err := peers.stores[1].ClaimChatJobs(t.Context(), f.record.WorkspaceKey, chatDMReplyJobKind, 1, time.Now()); err != nil || len(jobs) != 0 {
		t.Fatalf("live reply stolen: %+v %v", jobs, err)
	}
	peers.deafen[0]()
	chatDMReplies.Wait()
	job := recoverPeerChatDMJob(t, peers.servers[1], f.record.WorkspaceKey)
	if job.Attempts != 2 || calls.Load() != 1 || len(f.sent()) != 1 {
		t.Fatalf("takeover replayed generation/card: attempts=%d calls=%d cards=%d", job.Attempts, calls.Load(), len(f.sent()))
	}
	final := f.lastUpdate(card.ID)
	if !strings.Contains(final, "interrupted") || !strings.Contains(final, "Retry") || strings.Contains(final, "Thinking") {
		t.Fatalf("hanging takeover card: %s", final)
	}
	stale := job
	stale.Attempts--
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	old := &chatDMReplyLease{s: peers.servers[0], ctx: ctx, cancel: cancel, job: stale}
	called := false
	if err := old.update(nil, "", func(ctx context.Context) error { called = true; return nil }); !errors.Is(err, store.ErrChatJobLeaseLost) || called {
		t.Fatalf("stale writer reached the platform: called=%v err=%v", called, err)
	}
	// A repeated recovery delivery is the same PATCH and same action, never
	// another card or another agent run.
	if err := peers.servers[0].store.FinishChatJob(t.Context(), domain.ChatJob{ID: job.ID, WorkspaceKey: job.WorkspaceKey, ConnectionID: job.ConnectionID, LeaseUntil: time.Now(), Attempts: 1}, "completed", time.Time{}, ""); err == nil {
		t.Fatal("old server acknowledged successor job")
	}
	// Click the actual replacement card twice with distinct platform events.
	card.Raw = final
	_ = json.Unmarshal([]byte(final), &card.Card)
	f.s = peers.servers[1]
	f.click(t, card, "Retry", nil)
	chatDMReplies.Wait()
	f.click(t, card, "Retry", nil)
	chatDMReplies.Wait()
	if calls.Load() != 2 || len(f.sent()) != 2 || !strings.Contains(f.lastUpdate(f.sent()[1].ID), "Recovered answer.") {
		t.Fatalf("retry not deduplicated: calls=%d cards=%+v", calls.Load(), f.sent())
	}
}

func TestPeerChatDMConcurrentThreadMessagesHaveOneAnswer(t *testing.T) {
	var calls, active, maximum atomic.Int32
	release := make(chan struct{})
	var once sync.Once
	f, peers := newPeerChatDMFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Working\"}\n\n"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = w.Write([]byte(chatDMTextTurn("Done.")))
		case <-r.Context().Done():
		}
	})
	defer once.Do(func() { close(release) })
	// Seed one existing Agent thread without starting a reply.
	sessionID := "agent_peer_dm_thread"
	err := peers.stores[0].MutateWorkspace(t.Context(), f.record.WorkspaceKey, "agent.session_created", sessionID, nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = append(data.AgentSessions, domain.AgentSession{ID: sessionID, SlugID: "peer-dm", UserID: f.identity.UserID, Origin: "feishu", Location: "page", Title: "Peer DM", Messages: []domain.AgentMessage{}, CreatedAt: time.Now(), UpdatedAt: time.Now()})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	thread := chatDMThread{Kind: "session", OpenID: f.identity.OpenID, WorkspaceKey: f.record.WorkspaceKey, SessionID: sessionID}
	f.s.chatDMRememberThread(t.Context(), f.record, f.identity.UserID, "om_thread", thread)
	var start sync.WaitGroup
	start.Add(2)
	errs := make(chan error, 2)
	for index, s := range peers.servers {
		go func() {
			start.Done()
			start.Wait()
			content, _ := json.Marshal(map[string]string{"text": "Answer this thread"})
			values := map[string]json.RawMessage{"messageType": json.RawMessage(`"text"`), "content": nil, "parentId": json.RawMessage(`"om_thread"`)}
			values["content"], _ = json.Marshal(string(content))
			command := chatInboundCommand{Kind: "dm_message", OpenID: f.identity.OpenID, ChatID: chatDMTestChat, MessageID: []string{"om_peer_a", "om_peer_b"}[index], Values: values}
			job := domain.ChatJob{DedupKey: command.MessageID}
			errs <- s.chatDMMessage(t.Context(), f.record, job, command)
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	// Race the two independent queue consumers, not a process-local guard.
	for _, s := range peers.servers {
		go func() { errs <- s.startChatDMReplyJobs(t.Context(), f.record.WorkspaceKey) }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	f.waitFor(t, "busy reply on the losing server", func() bool {
		for _, card := range f.sent() {
			if strings.Contains(f.lastUpdate(card.ID), "still answering") {
				return true
			}
		}
		return false
	})
	once.Do(func() { close(release) })
	chatDMReplies.Wait()
	if calls.Load() != 1 || maximum.Load() != 1 {
		t.Fatalf("overlapping answers: calls=%d maximum=%d", calls.Load(), maximum.Load())
	}
	// Once the winner completes, another replica can answer this thread.
	f.s = peers.servers[1]
	f.reply(t, "om_after_completion", "Continue", "om_thread", "om_thread")
	chatDMReplies.Wait()
	if calls.Load() != 2 || maximum.Load() != 1 {
		t.Fatalf("thread lease not released: calls=%d maximum=%d", calls.Load(), maximum.Load())
	}
}

func TestPeerChatDMWorkerReplyTakeoverFollowsTheExistingRun(t *testing.T) {
	var providerCalls atomic.Int32
	f, peers := newPeerChatDMFixture(t, func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		_, _ = w.Write([]byte(chatDMTextTurn("unexpected")))
	})
	for _, s := range peers.servers {
		s.agent.WorkerSecret = testWorkerSecret
		s.agent.Runtime = appconfig.AgentRuntimeWorker
	}
	handler := newHandler(peers.servers[1])
	t.Cleanup(func() { peers.servers[1].stopDeliveryScheduler(t.Context()) })
	f.text(t, "om_peer_worker", "Answer on the worker")
	claimed := runtimeJSON[struct {
		Runs []store.AgentRun `json:"runs"`
	}](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/claim", worker: "w1", body: map[string]any{"free": map[string]int{"chat": 1}, "waitMs": 5000}}, http.StatusOK)
	if len(claimed.Runs) != 1 {
		t.Fatal("no worker run queued")
	}
	run := claimed.Runs[0]
	f.waitFor(t, "worker session checkpoint", func() bool {
		cards := f.sent()
		if len(cards) != 1 {
			return false
		}
		_, ok := f.s.chatDMThreadFor(t.Context(), f.record, f.identity, cards[0].ID)
		return ok
	})
	card := f.sent()[0]
	peers.deafen[0]()
	chatDMReplies.Wait()
	result := make(chan domain.ChatJob, 1)
	go func() { result <- recoverPeerChatDMJob(t, peers.servers[1], f.record.WorkspaceKey) }()
	post := func(activity map[string]any) {
		runtimeJSON[map[string]any](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/activities", worker: "w1", body: activity}, http.StatusOK)
	}
	post(map[string]any{"type": "action", "activityId": "c1:call", "callId": "c1", "content": map[string]any{"action": "list_issues", "parameter": map[string]any{"limit": 5}}})
	f.waitFor(t, "takeover streaming the existing worker step", func() bool {
		return strings.Contains(f.lastUpdate(card.ID), "1 running")
	})
	post(map[string]any{"type": "action.result", "activityId": "c1:result", "callId": "c1", "content": map[string]any{"action": "list_issues", "result": `{"items":[]}`}})
	post(map[string]any{"type": "response", "activityId": "end", "body": "Worker survived the API restart."})
	runtimeJSON[map[string]any](t, handler, runtimeCall{method: http.MethodPost, path: "/internal/agent-runtime/runs/" + run.ID + "/complete", worker: "w1", body: map[string]any{"status": "done"}}, http.StatusOK)
	select {
	case <-result:
	case <-time.After(15 * time.Second):
		t.Fatal("takeover did not finish")
	}
	runs, err := peers.stores[1].SessionAgentRuns(t.Context(), run.WorkspaceKey, run.SessionID, 10)
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID || runs[0].CancelRequested || providerCalls.Load() != 0 || len(f.sent()) != 1 {
		t.Fatalf("worker was restarted or canceled: %+v %v calls=%d cards=%d", runs, err, providerCalls.Load(), len(f.sent()))
	}
	if final := f.lastUpdate(card.ID); !strings.Contains(final, "Worker survived the API restart.") || !strings.Contains(final, `"template":"green"`) {
		t.Fatalf("worker final card: %s", final)
	}
}
