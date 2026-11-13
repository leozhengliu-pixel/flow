package store

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	ErrBoundedMetadataCoverage = errors.New("bounded metadata coverage is incomplete")
	ErrMetadataWorkBudget      = errors.New("metadata process work budget exceeded")
)

const (
	metadataPolicyBytes    = 192 << 20
	metadataBodyCacheBytes = 64 << 20
	metadataWorkBytes      = 256 << 20
)

// Fields are classified explicitly. Adding a Bootstrap field requires a
// residency decision, even if it is derived or stored in a different table.
var metadataResidencyFields = sync.OnceValue(func() map[string]string {
	fields := map[string]string{}
	for mode, names := range map[string]string{
		"core":     `workspace viewer viewerRole`,
		"policy":   `teams teamSettings workspaceSettings teamParents`,
		"records":  `users customers states labels labelGroups cycles cycleSettings issueTemplates projectTemplates documentTemplates documents customerRequests releases releasePipelines customEmojis asks loops loopRuns loopVersions loopAttachments slaRules issueSlas slaEvents drafts favorites favoriteFolders subscriptions threadSubscriptions auditLog trash importJobs exportJobs migrationJobs projectRelations initiativeRelations documentContentDrafts customerStatuses customerTiers releaseNotes releaseHistory teamResourceSections teamPinnedResources agentActivities aiConversations aiPromptProgress issueSuggestions commentActions projects projectStatuses projectDisplayDefault projectUpdates initiatives initiativeUpdates savedViews notificationPreferences pushSubscriptions triageResponsibilities triageRoutingRules triageAssignments workflowDefinitions workflowRuns emailIntakeAddresses emailIntakeMessages userSettings apiKeys passkeys passkeyRegistrationChallenges oauthApplications oauthAuthorizations webhooks webhookFailureEvents integrationConnections jiraLinks identityProviders integrationDeliveries gitAutomationStates targetBranches reviews agentSessions agentSkills settings members departedMembers teamMembers invitations workspaceInviteLink oauthSyncGroupRequests`,
		"external": `issues comments activities notifications notificationDeliveries`,
		"derived":  `resourceDetailsOmitted providerJobs issueCollectionPaged inboxUnreadCount reviewCount trashRetentionDays chatPlatform`,
	} {
		for _, name := range strings.Fields(names) {
			fields[name] = mode
		}
	}
	return fields
})

func boundedMetadataRequested() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_BOUNDED_METADATA"))) {
	case "1", "true", "on":
		return true
	}
	return false
}

func validateBoundedMetadataCoverage() error {
	if _, err := metadataConsumerReviews(); err != nil {
		return fmt.Errorf("%w: %w", ErrBoundedMetadataCoverage, err)
	}
	consumers := metadataFullWorkspaceConsumers()
	if len(consumers) != 0 {
		owners := []string{}
		for _, consumer := range consumers {
			if !slices.Contains(owners, consumer.owner) {
				owners = append(owners, consumer.owner)
			}
		}
		slices.Sort(owners)
		return fmt.Errorf("%w: %d registered full-workspace consumers (%s); keep FLOW_BOUNDED_METADATA off", ErrBoundedMetadataCoverage, len(consumers), strings.Join(owners, ", "))
	}
	if !RecordHydrationEnabled() || recordJournalMode() != "on" {
		return fmt.Errorf("%w: record hydration and the record journal must both be on", ErrBoundedMetadataCoverage)
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("FLOW_RECORD_PEER_SYNC")), "shadow") {
		return fmt.Errorf("%w: complete-snapshot peer shadow requires bounded metadata off", ErrBoundedMetadataCoverage)
	}
	return nil
}

type metadataResidency struct {
	requireCoverage          bool
	mu                       sync.Mutex
	cache                    map[metadataResidentCacheKey]*list.Element
	lru                      list.List
	cacheBytes, cacheLimit   int
	policy                   map[string]int
	policyBytes, policyLimit int
	stamps                   map[string]copyStamp
	applied                  map[string]int64
	work                     *metadataWorkBudget
}

type metadataResidentCacheKey struct {
	workspace, workspaceID string
	revision               int64
	field, key             string
}

type metadataResidentCacheEntry struct {
	key   metadataResidentCacheKey
	row   metadataCacheRow
	bytes int
}

func newMetadataResidency() *metadataResidency {
	return &metadataResidency{requireCoverage: true, cache: map[metadataResidentCacheKey]*list.Element{}, cacheLimit: metadataBodyCacheBytes, policy: map[string]int{}, policyLimit: metadataPolicyBytes, stamps: map[string]copyStamp{}, applied: map[string]int64{}, work: newMetadataWorkBudget(metadataWorkBytes)}
}

func (s *SQLiteStore) residentStamp(workspace string) (copyStamp, int64, bool) {
	s.metadataResidency.mu.Lock()
	defer s.metadataResidency.mu.Unlock()
	stamp, ok := s.metadataResidency.stamps[workspace]
	return stamp, s.metadataResidency.applied[workspace], ok
}

func (s *SQLiteStore) admitMetadataWork(ctx context.Context, bytes int) (context.Context, func(), error) {
	if s.metadataResidency == nil {
		return ctx, func() {}, nil
	}
	return s.metadataResidency.work.acquire(ctx, bytes)
}

func (r *metadataResidency) cached(key metadataResidentCacheKey) (metadataCacheRow, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	element, ok := r.cache[key]
	if !ok {
		return metadataCacheRow{}, false
	}
	r.lru.MoveToFront(element)
	row := element.Value.(metadataResidentCacheEntry).row
	row.Data = slices.Clone(row.Data)
	return row, true
}

func (r *metadataResidency) cacheRow(key metadataResidentCacheKey, row metadataCacheRow) {
	// Charge key, map/list nodes and allocator overhead as well as body bytes.
	cost := len(row.Data) + len(key.workspace) + len(key.workspaceID) + len(key.field) + len(key.key) + 512
	if cost > r.cacheLimit {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if element, ok := r.cache[key]; ok {
		r.removeCacheEntry(element)
	}
	for r.cacheBytes+cost > r.cacheLimit {
		r.removeCacheEntry(r.lru.Back())
	}
	row.Data = slices.Clone(row.Data)
	element := r.lru.PushFront(metadataResidentCacheEntry{key, row, cost})
	r.cache[key] = element
	r.cacheBytes += cost
}

func (r *metadataResidency) removeCacheEntry(element *list.Element) {
	entry := element.Value.(metadataResidentCacheEntry)
	delete(r.cache, entry.key)
	r.cacheBytes -= entry.bytes
	r.lru.Remove(element)
}

func (r *metadataResidency) replacePolicyCosts(costs map[string]int) error {
	total := 0
	for _, cost := range costs {
		if cost < 0 || cost > r.policyLimit-total {
			return ErrMetadataWorkBudget
		}
		total += cost
	}
	r.mu.Lock()
	r.policy, r.policyBytes = costs, total
	r.mu.Unlock()
	return nil
}

// Admission covers internal callers too, and happens before a SQL snapshot
// or workspace write lock is held. A nested operation reuses its live lease.
type metadataWorkBudget struct {
	mu                   sync.Mutex
	limit, used, waiting int
	changed              chan struct{}
}

type metadataWorkLease struct {
	budget *metadataWorkBudget
	bytes  int
	live   bool
}

type metadataWorkLeaseKey struct{}

func newMetadataWorkBudget(limit int) *metadataWorkBudget {
	return &metadataWorkBudget{limit: limit, changed: make(chan struct{})}
}

func (b *metadataWorkBudget) acquire(ctx context.Context, bytes int) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	b.mu.Lock()
	if lease, ok := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease); ok && lease.budget == b && lease.live {
		b.mu.Unlock()
		if bytes > lease.bytes {
			return ctx, nil, ErrMetadataWorkBudget
		}
		return ctx, func() {}, nil
	}
	if bytes <= 0 || bytes > b.limit || b.waiting >= 128 {
		b.mu.Unlock()
		return ctx, nil, ErrMetadataWorkBudget
	}
	b.waiting++
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.waiting--
		b.mu.Unlock()
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		b.mu.Lock()
		if err := ctx.Err(); err != nil {
			b.mu.Unlock()
			return ctx, nil, err
		}
		if bytes <= b.limit-b.used {
			b.used += bytes
			lease := &metadataWorkLease{b, bytes, true}
			b.mu.Unlock()
			var once sync.Once
			release := func() {
				once.Do(func() {
					b.mu.Lock()
					lease.live = false
					b.used -= bytes
					close(b.changed)
					b.changed = make(chan struct{})
					b.mu.Unlock()
				})
			}
			return context.WithValue(ctx, metadataWorkLeaseKey{}, lease), release, nil
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx, nil, ctx.Err()
		case <-timer.C:
			return ctx, nil, ErrMetadataWorkBudget
		case <-changed:
		}
	}
}

// Use the hydration parser's conservative object accounting for retained
// policy too. Each page is independently bounded before it is decoded.
func metadataResidentCost(raw json.RawMessage) (int, error) {
	budget := newMetadataHydrationBudget()
	before := budget.work
	if err := budget.consume(raw); err != nil {
		return 0, err
	}
	return before - budget.work + 512, nil
}
