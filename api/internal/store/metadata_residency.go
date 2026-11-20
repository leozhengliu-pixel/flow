package store

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"flow/api/internal/boundedcrdt"
)

var (
	ErrBoundedMetadataCoverage = errors.New("bounded metadata coverage is incomplete")
	ErrMetadataWorkBudget      = errors.New("metadata process work budget exceeded")
)

const (
	// metadataPolicyBytes is the default resident policy budget of all
	// workspaces together (FLOW_METADATA_POLICY_BYTES) and the most one
	// workspace's policy image may take; whole-policy operations admit that
	// much work.
	metadataPolicyBytes    = 192 << 20
	metadataBodyCacheBytes = 64 << 20
	metadataWorkBytes      = 256 << 20
	// metadataSourceReloadHeadroom is the share of the installed source
	// budget a bounded start keeps free for the work that runs beside the
	// installed sources: requests' and publications' scratch spools (a
	// group each) and the write buffers of storages being written, and
	// workspaces created at runtime.
	metadataSourceReloadHeadroom = 16 << 20
)

// metadataBudgetBytes reads a byte budget from the environment: a positive
// whole number of bytes. Unset, invalid, zero or negative values use the
// default. The budgets grow with the number of workspaces a replica holds
// (docs/bounded-metadata.md, Tuning). The disk budget is read on every spool
// append, so an unset variable returns before parsing (a failed parse
// allocates its error).
func metadataBudgetBytes(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// metadataLogicalDescriptorCap is the process-wide budget of installed
// logical sources: every resident workspace holds one spool group charged
// metadataLogicalDescriptorBytes (its read cache and write buffer), shared by
// every generation of its source (scale_p21_logical_spool_group.go).
func metadataLogicalDescriptorCap() int64 {
	return metadataBudgetBytes("FLOW_METADATA_SOURCE_BYTES", metadataLogicalDescriptorLimit)
}

// metadataLogicalDiskCap is the process-wide spool disk budget of logical
// sources, installed ones included.
func metadataLogicalDiskCap() int64 {
	return metadataBudgetBytes("FLOW_METADATA_SOURCE_DISK_BYTES", metadataLogicalDiskBytes)
}

// metadataWorkspacePolicyLimit is the most one workspace's resident policy
// may take: the total budget, but never more than the whole-policy work an
// operation admits.
func (r *metadataResidency) metadataWorkspacePolicyLimit() int {
	return min(r.policyLimit, metadataPolicyBytes)
}

const metadataMiB = 1 << 20

func ceilMiB(bytes int64) int64 { return (bytes + metadataMiB - 1) / metadataMiB }

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
	// Activation needs every HOT consumer migrated. An unmigrated consumer
	// reviewed as COLD (with a reason) keeps its legacy full-workspace
	// behaviour: its routes run on the COLD scope (default-COLD routing,
	// SW530/SW550) and its background work under WithColdWork, one COLD slot
	// per replica, and the store refuses a whole-workspace read outside a
	// COLD facade (SW531).
	consumers := registeredHotMetadataConsumers()
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
	if err := boundedcrdt.CheckRuntime(); err != nil {
		return fmt.Errorf("%w: %w", ErrBoundedMetadataCoverage, err)
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
	logical                  map[string]*metadataLogicalSource
	coldOnce                 sync.Once
	coldSlot                 chan struct{}
	// coldImages: decoded images of installed generations reused by COLD
	// facades and legacy views (scale_p21_cold_decode_cache.go).
	coldImages coldDecodeCache
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
	return &metadataResidency{requireCoverage: true, cache: map[metadataResidentCacheKey]*list.Element{}, cacheLimit: metadataBodyCacheBytes, policy: map[string]int{}, policyLimit: int(min(metadataBudgetBytes("FLOW_METADATA_POLICY_BYTES", metadataPolicyBytes), 1<<46)), stamps: map[string]copyStamp{}, applied: map[string]int64{}, work: newMetadataWorkBudget(metadataWorkBytes)}
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
	// peak is the highest used so far (MetadataWorkPeakForTesting).
	peak    int
	changed chan struct{}
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
			noteMetadataWorkDemand(ctx, bytes, 0)
			b.used += bytes
			b.peak = max(b.peak, b.used)
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
			noteMetadataWorkDemand(ctx, 0, bytes)
			return ctx, nil, errMetadataWorkContended
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
