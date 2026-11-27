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
	"sync/atomic"
	"time"

	"flow/api/internal/boundedcrdt"
)

var (
	ErrBoundedMetadataCoverage = errors.New("bounded metadata coverage is incomplete")
	ErrMetadataWorkBudget      = errors.New("metadata process work budget exceeded")
)

const (
	// Whole-policy operations admit up to this much work per workspace.
	metadataPolicyBytes      = 192 << 20
	metadataWorkBytesDefault = 256 << 20
	// metadataSourceReloadHeadroom is the share of the installed source
	// budget a bounded start keeps free for the work that runs beside the
	// installed sources: requests' and publications' scratch spools (a
	// group each) and the write buffers of storages being written, and
	// workspaces created at runtime.
	metadataSourceReloadHeadroom = 16 << 20
)

// metadataOperationWorkBytes is the operation work limit set by the
// bounded residency of the process (zero: unset).
var metadataOperationWorkBytes atomic.Int64

// metadataWorkBytes is the most work one metadata operation, or one record
// of it, may admit: metadataWorkBytesDefault (256 MiB), or the process work
// budget (FLOW_METADATA_WORK_BYTES) when that is larger. Every operation
// still reserves its work from the process budget, so a larger limit lets a
// record that bounded admission accepted (logical_capacity.go) be read and
// written by requests within the same budget, and memory stays within it.
func metadataWorkBytes() int {
	if limit := metadataOperationWorkBytes.Load(); limit > 0 {
		return int(limit)
	}
	return metadataWorkBytesDefault
}

// setMetadataOperationWork sets the operation work limit for a process work
// budget of budget bytes. A bounded start sets it; zero (store closed or
// fallen back to legacy reads) restores the default.
func setMetadataOperationWork(budget int) {
	metadataOperationWorkBytes.Store(int64(max(budget, metadataWorkBytesDefault)))
}

// boundedMetadataSize sums nonnegative sizes or counts without exceeding limit.
func boundedMetadataSize(limit int, parts ...int) (int, error) {
	if limit < 0 {
		return 0, ErrMetadataWorkBudget
	}
	total := 0
	for _, part := range parts {
		if part < 0 || part > limit-total {
			return 0, ErrMetadataWorkBudget
		}
		total += part
	}
	return total, nil
}

// metadataBudgetBytes reads a byte budget from the environment: a positive
// whole number of bytes. Unset, invalid, zero or negative values use the
// default. The budgets grow with the number of workspaces a replica holds
// (docs/bounded-metadata.md, Tuning).
func metadataBudgetBytes(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, strconv.IntSize)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// metadataLogicalDescriptorCap is the process-wide budget of installed
// logical sources: every resident workspace holds one spool group charged
// metadataLogicalDescriptorBytes (its read cache and write buffer), shared by
// every generation of its source (logical_spool_group.go).
func metadataLogicalDescriptorCap() int64 {
	return metadataBudgetBytes("FLOW_METADATA_SOURCE_BYTES", metadataMemoryShare(1, 32))
}

// metadataLogicalDiskCap is the process-wide spool disk budget of logical
// sources, installed ones included. It defaults to the memory ceiling.
func metadataLogicalDiskCap() int64 {
	return metadataBudgetBytes("FLOW_METADATA_SOURCE_DISK_BYTES", metadataMemoryShare(1, 1))
}

// metadataWorkspacePolicyLimit is the most one workspace's resident policy
// may take: the total budget, but never more than the whole-policy work an
// operation admits.
func (r *metadataResidency) metadataWorkspacePolicyLimit() int {
	return min(r.policyLimit, metadataPolicyBytes)
}

const metadataMiB = 1 << 20

func ceilMiB(bytes int64) int64 {
	whole := bytes / metadataMiB
	if bytes%metadataMiB > 0 {
		whole++
	}
	return whole
}

// Fields are classified explicitly. Adding a Bootstrap field requires a
// residency decision, even if it is derived or stored in a different table.
var metadataResidencyFields = sync.OnceValue(func() map[string]string {
	fields := map[string]string{}
	for mode, names := range map[string]string{
		"core":     `workspace viewer viewerRole`,
		"policy":   `teams teamSettings workspaceSettings teamParents`,
		"records":  `users customers states labels labelGroups cycles cycleSettings issueTemplates projectTemplates documentTemplates documents customerRequests releases releasePipelines customEmojis asks loops loopRuns loopVersions loopAttachments slaRules issueSlas slaEvents drafts favorites favoriteFolders subscriptions threadSubscriptions auditLog trash importJobs exportJobs migrationJobs projectRelations initiativeRelations documentContentDrafts customerStatuses customerTiers releaseNotes releaseHistory teamResourceSections teamPinnedResources agentActivities aiConversations aiPromptProgress issueSuggestions commentActions projects projectStatuses projectDisplayDefault projectUpdates initiatives initiativeUpdates savedViews notificationPreferences pushSubscriptions triageResponsibilities triageRoutingRules triageAssignments workflowDefinitions workflowRuns emailIntakeAddresses emailIntakeMessages userSettings apiKeys passkeys passkeyRegistrationChallenges oauthApplications oauthAuthorizations webhooks webhookFailureEvents integrationConnections jiraLinks identityProviders integrationDeliveries gitAutomationStates targetBranches reviews agentSessions agentSkills settings members departedMembers teamMembers invitations workspaceInviteLink oauthSyncGroupRequests`,
		"external": `issues comments activities notifications notificationDeliveries`,
		"derived":  `resourceDetailsOmitted providerJobs issueCollectionPaged inboxUnreadCount reviewCount trashRetentionDays chatPlatform uploadMaxBytes`,
	} {
		for _, name := range strings.Fields(names) {
			fields[name] = mode
		}
	}
	return fields
})

func boundedMetadataRequested(driver string) bool {
	const setting = "FLOW_BOUNDED_METADATA"
	if !metadataFeatureEnabled(setting, driver) {
		return false
	}
	// A manual opt-in still goes through the startup guard. Implicit
	// activation leaves explicitly disabled dependencies and shadows alone.
	return strings.TrimSpace(os.Getenv(setting)) != "" ||
		recordHydrationEnabled(driver) && recordJournalMode(driver) == "on" && !boundedMetadataShadowIncompatible()
}

func boundedMetadataShadowIncompatible() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("FLOW_RECORD_PEER_SYNC")), "shadow") || metadataCatalogShadowEnabled()
}

func validateBoundedMetadataCoverage(driver string) error {
	if _, err := metadataConsumerReviews(); err != nil {
		return fmt.Errorf("%w: %w", ErrBoundedMetadataCoverage, err)
	}
	// Activation needs every HOT consumer migrated. An unmigrated consumer
	// reviewed as COLD (with a reason) keeps its legacy full-workspace
	// behaviour: its routes run on the COLD scope (default-COLD routing,
	// flag-on route sweep/route temperature guard) and its background work under WithColdWork, one COLD slot
	// per replica, and the store refuses a whole-workspace read outside a
	// COLD facade.
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
	if !recordHydrationEnabled(driver) || recordJournalMode(driver) != "on" {
		return fmt.Errorf("%w: record hydration and the record journal must both be on", ErrBoundedMetadataCoverage)
	}
	if boundedMetadataShadowIncompatible() {
		return fmt.Errorf("%w: peer sync and catalog shadow modes require bounded metadata off", ErrBoundedMetadataCoverage)
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
	// facades and legacy views (cold_decode_cache.go).
	coldImages coldDecodeCache
	// residentBuilds serializes the image builds of shared resident readers
	// per workspace (resident_generation.go).
	residentBuilds residentImageBuilds
	reloadBuilds   residentImageBuilds
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
	// Budgets derive from the memory ceiling (metadata_memory.go).
	return &metadataResidency{requireCoverage: true, cache: map[metadataResidentCacheKey]*list.Element{}, cacheLimit: metadataBodyCacheBudgetBytes(), policy: map[string]int{}, policyLimit: metadataPolicyBudgetBytes(), stamps: map[string]copyStamp{}, applied: map[string]int64{}, work: newMetadataWorkBudget(metadataWorkBudgetBytes())}
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
	demand := metadataWorkDemandFrom(ctx)
	b.mu.Lock()
	if lease, ok := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease); ok && lease.budget == b && lease.live {
		b.mu.Unlock()
		if bytes > lease.bytes {
			return ctx, nil, ErrMetadataWorkBudget
		}
		return ctx, func() {}, nil
	}
	if bytes <= 0 || !demand.fits(bytes, b.limit) {
		b.mu.Unlock()
		return ctx, nil, ErrMetadataWorkBudget
	}
	if b.waiting >= 128 {
		demand.refuse(bytes)
		b.mu.Unlock()
		return ctx, nil, errMetadataWorkContended
	}
	b.waiting++
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.waiting--
		b.mu.Unlock()
	}()
	timer := time.NewTimer(time.Duration(metadataWorkRetryWait.Load()))
	defer timer.Stop()
	waited, timedOut := false, false
	for {
		b.mu.Lock()
		if err := ctx.Err(); err != nil {
			if waited && errors.Is(err, context.DeadlineExceeded) && demand.fits(bytes, b.limit) {
				demand.refuse(bytes)
				err = errors.Join(errMetadataWorkContended, err)
			}
			b.mu.Unlock()
			return ctx, nil, err
		}
		if !demand.fits(bytes, b.limit) {
			b.mu.Unlock()
			return ctx, nil, ErrMetadataWorkBudget
		}
		if bytes <= b.limit-b.used {
			demand.charge(bytes)
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
					demand.charge(-bytes)
					close(b.changed)
					b.changed = make(chan struct{})
					b.mu.Unlock()
				})
			}
			return context.WithValue(ctx, metadataWorkLeaseKey{}, lease), release, nil
		}
		if timedOut {
			demand.refuse(bytes)
			b.mu.Unlock()
			return ctx, nil, errMetadataWorkContended
		}
		changed := b.changed
		b.mu.Unlock()
		waited = true
		select {
		case <-ctx.Done():
		case <-timer.C:
			timedOut = true
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
