package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type eventAuthority struct {
	version store.EventSourceVersion
	epoch   string
}

type eventAuthorityRead struct {
	done              chan struct{}
	value             eventAuthority
	err               error
	ready             chan struct{}
	arrived, expected int
}

type eventDeliveryKey struct{}
type eventDeliverySuppressKey struct{}
type eventAccessRefreshKey struct{}
type eventSourceEpochKey struct{}
type eventDelivery struct {
	id      string
	viewers int
}

type eventDeliveryCounts struct {
	mu     sync.Mutex
	counts map[string]int
	order  []string
}

func (h *realtimeHub) eventDeliveryViewers(workspace, identity string, resource bool) int {
	cache := &h.rules.deliveries
	key := workspace + "|" + identity
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if viewers, exists := cache.counts[key]; exists {
		return viewers
	}
	h.mu.Lock()
	viewers := 0
	for _, subscriber := range h.subscribers[workspace] {
		subscriber.mu.Lock()
		if !subscriber.closed && (!resource || !subscriber.syncV2) {
			viewers++
		}
		subscriber.mu.Unlock()
	}
	h.mu.Unlock()
	if cache.counts == nil {
		cache.counts = map[string]int{}
	}
	if len(cache.order) == eventSourceCacheEntries {
		delete(cache.counts, cache.order[0])
		cache.order = cache.order[1:]
	}
	cache.counts[key] = viewers
	cache.order = append(cache.order, key)
	return viewers
}

func (s *server) eventDeliveryRequest(r *http.Request, event domain.RealtimeEvent) *http.Request {
	if s.realtime == nil || r.URL.Path != "/api/realtime/events" || event.ID == "" {
		return r
	}
	if suppressed, _ := r.Context().Value(eventDeliverySuppressKey{}).(bool); suppressed {
		return r
	}
	identity := (&EventSource{event: event}).cacheKey()
	viewers := s.realtime.eventDeliveryViewers(workspaceKey(r), identity, syncResourceEvent(event.Type))
	if viewers == 0 {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), eventDeliveryKey{}, eventDelivery{identity, min(viewers, eventAccessCacheEntries)}))
}

func eventAuthorityKey(ctx context.Context, workspace, phase string) string {
	delivery, _ := ctx.Value(eventDeliveryKey{}).(eventDelivery)
	return fmt.Sprintf("%s|%s|%s", workspace, phase, delivery.id)
}

// Seal each cohort before reading authority. A caller arriving after the SQL
// starts must not inherit a proof that could predate its delivery boundary.
type eventAuthorityReads struct {
	mu      sync.Mutex
	pending map[string]*eventAuthorityRead
}

func (s *server) readEventAuthority(ctx context.Context, workspace string) (eventAuthority, error) {
	read := func(readCtx context.Context) (eventAuthority, error) {
		version, err := s.store.RealtimeSourceVersion(readCtx, workspace)
		if err != nil {
			return eventAuthority{}, err
		}
		epoch, err := s.store.AuthzEpoch(readCtx, version.WorkspaceID)
		return eventAuthority{version, epoch}, err
	}
	if s.realtime == nil {
		return read(ctx)
	}
	return s.realtime.rules.authority.load(ctx, eventAuthorityKey(ctx, workspace, "validate"), read)
}

func (s *server) readEventProjectionAuthority(ctx context.Context, workspace string) (eventAuthority, error) {
	read := func(readCtx context.Context) (eventAuthority, error) {
		version, err := s.store.RealtimeSourceVersion(readCtx, workspace)
		if err != nil {
			return eventAuthority{}, err
		}
		epoch, err := s.store.AuthzEpoch(readCtx, version.WorkspaceID)
		return eventAuthority{version, epoch}, err
	}
	if s.realtime == nil {
		return read(ctx)
	}
	return s.realtime.rules.authority.load(ctx, eventAuthorityKey(ctx, workspace, "refresh"), read)
}

func (s *server) readEventSourceVersion(ctx context.Context, workspace string) (eventAuthority, error) {
	read := func(readCtx context.Context) (eventAuthority, error) {
		version, err := s.store.RealtimeSourceVersion(readCtx, workspace)
		return eventAuthority{version: version}, err
	}
	if s.realtime == nil {
		return read(ctx)
	}
	return s.realtime.rules.authority.load(ctx, eventAuthorityKey(ctx, workspace, "source"), read)
}

func (cache *eventAuthorityReads) load(ctx context.Context, workspace string, read func(context.Context) (eventAuthority, error)) (eventAuthority, error) {
	if err := ctx.Err(); err != nil {
		return eventAuthority{}, err
	}
	cache.mu.Lock()
	if pending := cache.pending[workspace]; pending != nil {
		pending.arrived++
		if pending.arrived == pending.expected {
			close(pending.ready)
		}
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			return eventAuthority{}, ctx.Err()
		case <-pending.done:
			if err := ctx.Err(); err != nil {
				return eventAuthority{}, err
			}
			return pending.value, pending.err
		}
	}
	if cache.pending == nil {
		cache.pending = map[string]*eventAuthorityRead{}
	}
	if len(cache.pending) >= eventSourceCacheEntries*2 {
		cache.mu.Unlock()
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return read(readCtx)
	}
	delivery, _ := ctx.Value(eventDeliveryKey{}).(eventDelivery)
	pending := &eventAuthorityRead{done: make(chan struct{}), ready: make(chan struct{}), arrived: 1, expected: max(1, delivery.viewers)}
	cache.pending[workspace] = pending
	cache.mu.Unlock()
	if pending.expected > 1 {
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-pending.ready:
		case <-timer.C:
		}
		timer.Stop()
	} else {
		runtime.Gosched()
	}
	cache.mu.Lock()
	delete(cache.pending, workspace)
	shared := pending.arrived > 1
	cache.mu.Unlock()
	readCtx := ctx
	if shared {
		readCtx = context.WithoutCancel(ctx)
	}
	readCtx, cancel := context.WithTimeout(readCtx, 2*time.Second)
	pending.value, pending.err = read(readCtx)
	cancel()
	close(pending.done)
	if err := ctx.Err(); err != nil {
		return eventAuthority{}, err
	}
	return pending.value, pending.err
}

const (
	eventAccessCacheEntries = 4096
	eventAccessCacheBytes   = 32 << 20
)

type eventAccessKey struct {
	workspace, principal string
	credential           [32]byte
}

type eventAccessEntry struct {
	access store.EventAccess
	bytes  int
	source *store.EventPolicySource
}

type eventAccessCache struct {
	mu      sync.Mutex
	entries map[eventAccessKey]eventAccessEntry
	order   []eventAccessKey
	bytes   int
	sources map[*store.EventPolicySource]int
}

func (cache *eventAccessCache) release(entry eventAccessEntry) {
	cache.bytes -= entry.bytes
	cache.sources[entry.source]--
	if cache.sources[entry.source] == 0 {
		cache.bytes -= entry.source.Bytes()
		delete(cache.sources, entry.source)
	}
}

func (cache *eventAccessCache) retain(entry eventAccessEntry) {
	cache.bytes += entry.bytes
	if cache.sources[entry.source] == 0 {
		cache.bytes += entry.source.Bytes()
	}
	cache.sources[entry.source]++
}

func eventAccessCacheKey(workspace, principal string, key *domain.APIKey) eventAccessKey {
	var credential any
	if key != nil {
		credential = struct {
			ID, Actor, Restriction string
			Scopes, Teams          []string
		}{key.ID, key.Actor, key.TeamRestriction, key.Scopes, key.TeamIDs}
	}
	raw, _ := json.Marshal(credential)
	return eventAccessKey{workspace, principal, sha256.Sum256(raw)}
}

func (s *server) cachedEventAccess(ctx context.Context, source *store.EventPolicySource, authority eventAuthority, principal string, key *domain.APIKey) (store.EventAccess, error) {
	if s.realtime == nil {
		return s.store.ResolveEventAccessFromSource(ctx, source, principal, key)
	}
	cache := &s.realtime.rules.accesses
	identity := eventAccessCacheKey(authority.version.WorkspaceID, principal, key)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entry, ok := cache.entries[identity]; ok && entry.access.MatchesCredential(key) {
		epoch := authority.epoch
		if epoch == "" {
			epoch = entry.access.AuthzEpoch
		}
		if access, err := entry.access.ForSource(source, epoch); err == nil {
			return access, nil
		}
	}
	access, err := s.store.ResolveEventAccessFromSource(ctx, source, principal, key)
	if err != nil {
		return store.EventAccess{}, err
	}
	if authority.epoch != "" && access.AuthzEpoch != authority.epoch || access.SourceVersion() != authority.version {
		return store.EventAccess{}, store.ErrAuthForbidden
	}
	if cache.entries == nil {
		cache.entries = map[eventAccessKey]eventAccessEntry{}
		cache.sources = map[*store.EventPolicySource]int{}
	}
	cost := access.CacheBytes()
	if cost+source.Bytes() > eventAccessCacheBytes {
		return access, nil
	}
	if previous, exists := cache.entries[identity]; exists {
		cache.release(previous)
	} else {
		cache.order = append(cache.order, identity)
	}
	entry := eventAccessEntry{access, cost, source}
	cache.entries[identity] = entry
	cache.retain(entry)
	for len(cache.order) > eventAccessCacheEntries || cache.bytes > eventAccessCacheBytes {
		oldest := cache.order[0]
		cache.order[0] = eventAccessKey{}
		cache.order = cache.order[1:]
		cache.release(cache.entries[oldest])
		delete(cache.entries, oldest)
	}
	return access, nil
}
