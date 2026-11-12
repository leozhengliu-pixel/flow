package main

import (
	"encoding/json"
	"sync"
	"unsafe"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Per-event work every stream of a workspace would otherwise repeat: each
// open stream authorizes and projects every project and initiative event, at
// about the same moment. Building the project/initiative index (pulseRules,
// O(projects)) once per stream per event made hundreds of identical copies
// live at once in a large workspace.

// pulseRulesKey identifies the installed collections a pulseRules index was
// built from. An installed workspace copy is never changed in place (a write
// installs a new copy), so the same backing arrays mean the same contents;
// the cached index keeps them alive, so their addresses cannot be reused for
// other contents while it is cached.
type pulseRulesKey struct {
	projects, initiatives, relations, teams                 unsafe.Pointer
	projectCount, initiativeCount, relationCount, teamCount int
}

func pulseRulesKeyOf(snapshot *store.PulseFeedSnapshot) pulseRulesKey {
	return pulseRulesKey{
		projects: unsafe.Pointer(unsafe.SliceData(snapshot.Projects)), projectCount: len(snapshot.Projects),
		initiatives: unsafe.Pointer(unsafe.SliceData(snapshot.Initiatives)), initiativeCount: len(snapshot.Initiatives),
		relations: unsafe.Pointer(unsafe.SliceData(snapshot.InitiativeRelations)), relationCount: len(snapshot.InitiativeRelations),
		teams: unsafe.Pointer(unsafe.SliceData(snapshot.Teams)), teamCount: len(snapshot.Teams),
	}
}

// pulseRulesCache holds the latest index per workspace; concurrent streams
// share one build.
type pulseRulesCache struct {
	mu       sync.Mutex
	entries  map[string]*pulseRulesEntry
	sources  eventSourceCache
	policies eventSourceCache
}

type pulseRulesEntry struct {
	key   pulseRulesKey
	ready chan struct{}
	rules *pulseRules
}

// pulseRulesFor returns the visibility index of the snapshot's projects and
// initiatives, built once per installed copy. Only the realtime visibility
// rules (projects, initiatives, their links and parents) may be read from it:
// its snapshot field is cleared, the stream's own snapshot has the rest.
func (h *realtimeHub) pulseRulesFor(workspace string, snapshot *store.PulseFeedSnapshot) *pulseRules {
	if h == nil {
		return newPulseRules(snapshot)
	}
	key := pulseRulesKeyOf(snapshot)
	cache := &h.rules
	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = map[string]*pulseRulesEntry{}
	}
	entry := cache.entries[workspace]
	build := entry == nil || entry.key != key
	if build {
		entry = &pulseRulesEntry{key: key, ready: make(chan struct{})}
		cache.entries[workspace] = entry
	}
	cache.mu.Unlock()
	if !build {
		<-entry.ready
		return entry.rules
	}
	defer close(entry.ready)
	rules := newPulseRules(snapshot)
	// The feed fields (updates, subscriptions, settings) of this copy are not
	// part of the key: nothing may read them through the shared index.
	rules.snapshot = nil
	entry.rules = rules
	return rules
}

// presenceRosterCache keeps the latest presence event's decoded roster: every
// stream filters the same roster at about the same time. Readers must not
// change it.
type presenceRosterCache struct {
	mu     sync.Mutex
	id     string
	roster []domain.Presence
}

func (h *realtimeHub) presenceRoster(event domain.RealtimeEvent) ([]domain.Presence, error) {
	decode := func() ([]domain.Presence, error) {
		var payload struct {
			Presence []domain.Presence `json:"presence"`
		}
		err := json.Unmarshal(event.Payload, &payload)
		return payload.Presence, err
	}
	if h == nil {
		return decode()
	}
	cache := &h.rosters
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.id == event.ID && event.ID != "" {
		return cache.roster, nil
	}
	roster, err := decode()
	if err != nil {
		return nil, err
	}
	cache.id, cache.roster = event.ID, roster
	return roster, nil
}

// scopeChanged is realtimeScopeChanged once per event (it decodes the whole
// payload), shared by every stream.
func (h *realtimeHub) scopeChanged(event domain.RealtimeEvent) bool {
	if realtimeScopeFields[event.Type] == nil {
		return false
	}
	return string(h.cachedPayload(event.ID+"|scope", func() json.RawMessage {
		if realtimeScopeChanged(event) {
			return json.RawMessage("true")
		}
		return json.RawMessage("false")
	})) == "true"
}
