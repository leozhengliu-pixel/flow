package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"time"

	"flow/api/internal/domain"
)

// A chat reply's write call runs once. The same call can reach Flow again: a provider stream that repeats a call in
// its turn, a reply rebuilt from the conversation's stored calls, a second request for the same reply on this server.
// Each of those answers with the first run's result (the same document, issue or project) instead of writing again.
// The conversation's stored calls answer across servers and restarts; agentToolCallLog answers calls still running,
// so a replay that arrives while the first run writes waits for it.

// agentToolCallTTL is how long a write's result answers a replay from memory; the stored conversation keeps it after.
const agentToolCallTTL = 30 * time.Minute

type agentToolCallLog struct {
	mu      sync.Mutex
	entries map[string]*agentToolCallEntry
}

type agentToolCallEntry struct {
	done   chan struct{}
	at     time.Time
	result []byte
	change *domain.AgentChange
	title  string
	failed bool
}

// begin returns the entry of key and whether this caller runs the call (false: it ran, or is running, already).
func (l *agentToolCallLog) begin(key string, now time.Time) (*agentToolCallEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = map[string]*agentToolCallEntry{}
	}
	for other, entry := range l.entries {
		if now.Sub(entry.at) > agentToolCallTTL {
			delete(l.entries, other)
		}
	}
	if entry, ok := l.entries[key]; ok {
		return entry, false
	}
	entry := &agentToolCallEntry{done: make(chan struct{}), at: now}
	l.entries[key] = entry
	return entry, true
}

// forget drops a failed call, so a later retry of it may run.
func (l *agentToolCallLog) forget(key string, entry *agentToolCallEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries[key] == entry {
		delete(l.entries, key)
	}
}

// agentToolCallKey names a call by its id, tool and arguments: only the same call is answered from an earlier run
// (some providers number call ids per response, so an id alone may name a new call).
func agentToolCallKey(call domain.AgentToolCall) string {
	arguments := string(call.Arguments)
	var value any
	if json.Unmarshal(call.Arguments, &value) == nil {
		if raw, err := json.Marshal(value); err == nil {
			arguments = string(raw)
		}
	}
	return call.ID + "\x00" + agentToolBaseName(call.Name) + "\x00" + arguments
}

// storedAgentToolCall is the same call (agentToolCallKey) among session's stored replies, completed without an error.
func storedAgentToolCall(session domain.AgentSession, call domain.AgentToolCall) (domain.AgentToolCall, bool) {
	key := agentToolCallKey(call)
	for _, message := range slices.Backward(session.Messages) {
		for _, part := range message.Parts {
			if stored := part.ToolCall; stored != nil && stored.ID == call.ID && stored.Status == "completed" && stored.Error == "" && len(stored.Result) > 0 && agentToolCallKey(*stored) == key {
				return *stored, true
			}
		}
	}
	return domain.AgentToolCall{}, false
}

// executeAgentToolCallOnce runs call for session's reply like executeAgentToolRecordingChange, except that a write
// call that already ran answers with that run's result (and change) instead of writing again.
func (s *server) executeAgentToolCallOnce(r *http.Request, data domain.Bootstrap, session domain.AgentSession, call *domain.AgentToolCall) ([]byte, error) {
	if _, write := agentLedgerTool(r.Context(), call.Name); !write || call.ID == "" {
		return s.executeAgentToolRecordingChange(r, data, call)
	}
	if stored, ok := storedAgentToolCall(session, *call); ok {
		call.Change, call.Title = stored.Change, stored.Title
		return stored.Result, nil
	}
	key := workspaceKey(r) + "\x00" + session.ID + "\x00" + agentToolCallKey(*call)
	for {
		entry, run := s.agentToolCalls.begin(key, time.Now())
		if run {
			return s.runAgentToolCallEntry(r, data, call, key, entry)
		}
		select {
		case <-entry.done:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		if !entry.failed {
			call.Change, call.Title = entry.change, entry.title
			return entry.result, nil
		}
		// The first run failed (and was forgotten): this one may run the call.
	}
}

func (s *server) runAgentToolCallEntry(r *http.Request, data domain.Bootstrap, call *domain.AgentToolCall, key string, entry *agentToolCallEntry) (result []byte, err error) {
	entry.failed = true
	defer func() {
		if entry.failed {
			s.agentToolCalls.forget(key, entry)
		}
		close(entry.done)
	}()
	result, err = s.executeAgentToolRecordingChange(r, data, call)
	entry.result, entry.change, entry.title, entry.failed = result, call.Change, call.Title, err != nil
	return result, err
}

// uniqueAgentToolCalls drops the repeats of a call id within one turn: a provider stream can deliver a call twice, and
// running it twice would write twice. Calls without an id are kept as they are.
func uniqueAgentToolCalls(calls []domain.AgentToolCall) []domain.AgentToolCall {
	seen := map[string]bool{}
	return slices.DeleteFunc(slices.Clone(calls), func(call domain.AgentToolCall) bool {
		if call.ID == "" {
			return false
		}
		if seen[call.ID] {
			return true
		}
		seen[call.ID] = true
		return false
	})
}
