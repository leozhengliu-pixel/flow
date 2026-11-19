package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"flow/api/internal/domain"
)

// Incremental COLD publication (bounded metadata on).
//
// After a COLD legacy write committed, the parent's installed image of the
// workspace used to be reloaded from SQL in full while the shared write lock
// was held (seconds at scale; HOT writers queued behind it). The image is a
// pure function of two SQL inputs: the workspace_states root (its inline
// fields and the collection manifest) and the workspace's canonical
// workspace_metadata_records rows. The record journal's triggers bump
// record_versions for every row they change, whoever writes it (this server,
// a peer, an untracked writer), and bounded metadata requires the journal.
//
// So the publication compares two states of those inputs, read under the
// shared write lock:
//
//   - before (coldPublishState, taken when the COLD write took the lock): the
//     stored stamp, the journal generation, the root's occurrences of every
//     field and its manifest, and every canonical row's (order, version);
//   - after (the same, read after the write committed, in the snapshot the
//     publication then reads its records from).
//
// before is taken only when the installed generation is exact at the stored
// stamp (revision known, not behind, equal to SQL's), so the installed image
// is the reload of before. A field whose root occurrences, manifest entry and
// rows (keys, orders, versions) are all unchanged has the same raw value in
// after; when none of its normalization dependencies changed either, its
// installed value is the one a reload of after produces, and the publication
// shares it. Every other field is produced from after by the reload's own
// code: the raw recovery of that field (readRawMetadataLogicalSourcePart) and
// the original normalizer (field), with the normalizer's cross-record index
// built from the raw dependency fields it reads. A field whose own rows are
// all that changed is patched: its unchanged items are copied frame for frame
// from the installed field, its changed rows are decoded, staged and
// normalized exactly as the reload does, in the reload's order.
//
// Anything the comparison cannot show exact falls back to the full reload:
// no exact installed generation when the write took the lock (a peer commit
// since the image's revision, a behind marker, an unknown stamp), a journal
// that is not capturing or changed generation, a changed workspaceSettings
// field (the normalizer's base for every field), a changed manifest
// presence, a replaced workspace, a generation the writer installed itself,
// a rename (also keys), and any error. TestColdIncrementalPublication* prove
// the result equals a fresh reload after each of many COLD legacy writers.

// coldPublishRootBytes bounds the root a publication compares (larger roots
// reload in full).
const coldPublishRootBytes = 16 << 20

// coldPublishRecord is one canonical row's state.
type coldPublishRecord struct {
	order    int64
	version  int64 // -1: no record_versions row (never changed while captured)
	physical bool  // SQLite: the key is stored as a BLOB
}

// coldPublishState is one snapshot of a workspace's metadata inputs.
type coldPublishState struct {
	stamp        copyStamp
	generation   string
	records      map[string]map[string]coldPublishRecord
	occurrences  map[string][]byte
	manifest     map[string]string
	manifestRows bool
}

// coldPublishBase is the state the installed generation (source) is exact at.
type coldPublishBase struct {
	workspace string
	source    *metadataLogicalSource
	state     *coldPublishState
}

func (b *coldPublishBase) release() {
	if b != nil && b.source != nil {
		b.source.Release()
		b.source = nil
	}
}

var errColdPublishInexact = errors.New("cold publication cannot be shown exact")

func coldPublishInexact(reason string) error {
	return fmt.Errorf("%w: %s", errColdPublishInexact, reason)
}

// Publication counters (load measurement).
var (
	coldPublishIncrementals, coldPublishIncrementalNanos atomic.Int64
	coldPublishFulls, coldPublishFullNanos               atomic.Int64
	coldPublishUnchanged                                 atomic.Int64
	coldPublishBaseNanos                                 atomic.Int64
	coldPublishFallbackReasons                           sync.Map // string -> *atomic.Int64
)

func noteColdPublishFallback(reason string) {
	slog.Info("bounded metadata: COLD publication reloads in full", "reason", reason)
	if i := strings.IndexByte(reason, ':'); i > 0 {
		reason = reason[:i]
	}
	counter, _ := coldPublishFallbackReasons.LoadOrStore(reason, new(atomic.Int64))
	counter.(*atomic.Int64).Add(1)
}

// coldPublishFieldPlans counts, per field, the incremental publications that
// patched or rebuilt it (tests).
var coldPublishFieldPlans sync.Map // string -> *[2]atomic.Int64

func noteColdPublishPlan(plan *coldPublishPlan) {
	note := func(name string, which int) {
		counters, _ := coldPublishFieldPlans.LoadOrStore(name, new([2]atomic.Int64))
		counters.(*[2]atomic.Int64)[which].Add(1)
	}
	for name := range plan.patch {
		note(name, 0)
	}
	for name := range plan.rebuild {
		note(name, 1)
	}
}

// ColdPublishFieldPlansForTesting reports per field how many incremental
// publications patched ([0]) or rebuilt ([1]) it.
func ColdPublishFieldPlansForTesting() map[string][2]int64 {
	result := map[string][2]int64{}
	coldPublishFieldPlans.Range(func(key, value any) bool {
		counters := value.(*[2]atomic.Int64)
		result[key.(string)] = [2]int64{counters[0].Load(), counters[1].Load()}
		return true
	})
	return result
}

// ColdPublishFallbacksForTesting reports the full-reload fallbacks of COLD
// publications by reason.
func ColdPublishFallbacksForTesting() map[string]int64 {
	result := map[string]int64{}
	coldPublishFallbackReasons.Range(func(key, value any) bool {
		result[key.(string)] = value.(*atomic.Int64).Load()
		return true
	})
	return result
}

// ColdPublishCountsForTesting reports incremental and full COLD publications.
func ColdPublishCountsForTesting() (incremental, full, unchanged int64) {
	return coldPublishIncrementals.Load(), coldPublishFulls.Load(), coldPublishUnchanged.Load()
}

// coldIncrementalDisabled turns the incremental path off (tests compare it
// with the full reload).
var coldIncrementalDisabled atomic.Bool

// SetColdIncrementalPublicationForTesting enables (the default) or disables
// incremental COLD publication.
func SetColdIncrementalPublicationForTesting(enabled bool) {
	coldIncrementalDisabled.Store(!enabled)
}

var coldPublishFieldNames = sync.OnceValue(func() []string {
	t := reflect.TypeFor[domain.Bootstrap]()
	names := []string{}
	for i := 0; i < t.NumField(); i++ {
		if name := jsonFieldName(t.Field(i)); name != "-" {
			names = append(names, name)
		}
	}
	return names
})

// coldPublishBaseFields are the normalizer's base (newMetadataLogicalNormalizer),
// always recovered. Of them only workspaceSettings shapes other fields
// (normalizeStoredMetadata of the base gives every collection's empty
// default): a change of it reloads in full. The viewer is read only for
// initiatives (normalize defaults an initiative's creator to it, and the
// normalizer's index looks the viewer up among users), a dependency below;
// workspace and viewerRole are read by no other field's normalization.
var coldPublishBaseFields = []string{"workspace", "viewer", "viewerRole", "workspaceSettings"}

// coldPublishDependencies are the raw fields the normalizer's index reads for
// a field's records (metadataLogicalNormalizer.record and missing). "projects"
// is read only for label counts there.
var coldPublishDependencies = map[string][]string{
	"members":                 {"users"},
	"initiatives":             {"users", "viewer"},
	"projectUpdates":          {"users"},
	"initiativeUpdates":       {"users"},
	"comments":                {"users"},
	"activities":              {"users"},
	"notifications":           {"users"},
	"userSettings":            {"users"},
	"notificationPreferences": {"users"},
	"projects":                {"users", "projectStatuses", "issues"},
	"teamSettings":            {"teams", "states"},
	"cycleSettings":           {"teams", "states"},
	"issues":                  {"issues", "labels", "projects", "initiatives"},
	"labels":                  {"projects", "initiatives", "issues"},
}

// coldPublishUnpatchable normalize with the item's ordinal, synthesize missing
// items or keep a stored aggregate: they are rebuilt, never patched.
var coldPublishUnpatchable = map[string]bool{
	"issues": true, "projectStatuses": true, "releasePipelines": true,
	"userSettings": true, "notificationPreferences": true, "teamSettings": true,
	"integrationDeliveries": true, "integrationConnections": true,
}

// captureColdPublishBase reads the state the installed generation of
// workspace is exact at, when it is. The caller holds the write lock. It
// returns nil (and the reason) when the publication must reload in full.
func (s *SQLiteStore) captureColdPublishBase(ctx context.Context, workspace string) (*coldPublishBase, string) {
	if coldIncrementalDisabled.Load() || s.metadataResidency == nil {
		return nil, "disabled"
	}
	started := time.Now()
	defer func() { coldPublishBaseNanos.Add(int64(time.Since(started))) }()
	r := s.metadataResidency
	r.mu.Lock()
	source := r.logical[workspace]
	var err error
	if source == nil {
		err = coldPublishInexact("no installed generation")
	} else {
		err = source.Retain()
	}
	r.mu.Unlock()
	if err != nil {
		return nil, err.Error()
	}
	key := coldLabelCacheKey{r, workspace}
	known, haveKnown := knownColdPublication(key)
	fromKnown := func() *coldPublishBase {
		// A publication's own generation (retained) and the state it was
		// produced from: the next comparison includes every commit since,
		// whatever generations HOT writers installed in between.
		coldPublishBaseKnown.Add(1)
		return &coldPublishBase{workspace: workspace, source: known.source, state: known.state}
	}
	if haveKnown && known.source == source {
		source.Release()
		return fromKnown(), ""
	}
	base := &coldPublishBase{workspace: workspace, source: source}
	stamp := source.Stamp()
	reason := ""
	if stamp.revision < 0 || source.behind.Load() {
		reason = "installed generation not exact"
	} else {
		var tx *sqlTx
		ctx = context.WithoutCancel(ctx)
		tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err == nil {
			defer tx.Rollback()
			base.state, err = s.readColdPublishState(ctx, tx, workspace)
		}
		switch {
		case err != nil:
			reason = "base read: " + err.Error()
		case base.state.stamp != stamp:
			// A peer (or an untracked writer) committed since the image's revision.
			reason = "installed generation behind"
		}
	}
	if reason == "" {
		if haveKnown {
			known.source.Release()
		}
		return base, ""
	}
	base.release()
	if haveKnown {
		return fromKnown(), ""
	}
	return nil, reason
}

// rememberReloadedColdPublication keeps the generation a full reload just
// installed (exact at the stored stamp) as the known publication, with its
// state read at that stamp, so the next publication need not reload in full
// when a HOT writer installs a generation that is not exact meanwhile. The
// caller holds the write lock.
func (s *SQLiteStore) rememberReloadedColdPublication(ctx context.Context, workspace string) {
	if coldIncrementalDisabled.Load() || s.metadataResidency == nil {
		return
	}
	base, reason := s.captureColdPublishBase(ctx, workspace)
	if base == nil {
		slog.Info("bounded metadata: reloaded generation not kept as the COLD publication base", "workspace", workspace, "reason", reason)
		return
	}
	defer base.release()
	r := s.metadataResidency
	r.mu.Lock()
	current := r.logical[workspace]
	r.mu.Unlock()
	if base.source == current && base.state.stamp == current.Stamp() {
		rememberColdPublication(coldLabelCacheKey{r, workspace}, current, base.state)
		return
	}
	slog.Info("bounded metadata: reloaded generation not kept as the COLD publication base", "workspace", workspace, "reason", "base of another generation")
}

// coldPublishKnown is the generation (source, retained) an incremental
// publication installed and the state it is exact at.
type coldPublishKnown struct {
	source *metadataLogicalSource
	state  *coldPublishState
}

var (
	coldPublishKnownMu     sync.Mutex
	coldPublishKnownStates = map[coldLabelCacheKey]coldPublishKnown{}
)

// rememberColdPublication keeps source (retained) and the state it is exact
// at as workspace's known publication, replacing the previous one.
func rememberColdPublication(key coldLabelCacheKey, source *metadataLogicalSource, state *coldPublishState) {
	if source.Retain() != nil {
		forgetColdPublications(key.residency, key.workspace)
		return
	}
	coldPublishKnownMu.Lock()
	previous := coldPublishKnownStates[key]
	coldPublishKnownStates[key] = coldPublishKnown{source: source, state: state}
	coldPublishKnownMu.Unlock()
	if previous.source != nil {
		previous.source.Release()
	}
}

// knownColdPublication returns workspace's known publication with its
// source retained for the caller, unless that generation is marked behind.
func knownColdPublication(key coldLabelCacheKey) (coldPublishKnown, bool) {
	coldPublishKnownMu.Lock()
	defer coldPublishKnownMu.Unlock()
	known, ok := coldPublishKnownStates[key]
	if !ok || known.source.behind.Load() || known.source.Retain() != nil {
		return coldPublishKnown{}, false
	}
	return known, true
}

// forgetColdPublications drops the known publications of residency (of one
// workspace, or all when workspace is empty).
func forgetColdPublications(residency *metadataResidency, workspace string) {
	coldPublishKnownMu.Lock()
	var released []*metadataLogicalSource
	for key, known := range coldPublishKnownStates {
		if key.residency == residency && (workspace == "" || key.workspace == workspace) {
			released = append(released, known.source)
			delete(coldPublishKnownStates, key)
		}
	}
	coldPublishKnownMu.Unlock()
	for _, source := range released {
		source.Release()
	}
}

// coldPublishBaseKnown counts bases taken from a publication's own state.
var coldPublishBaseKnown atomic.Int64

// readColdPublishState reads workspace's metadata inputs in tx's snapshot.
func (s *SQLiteStore) readColdPublishState(ctx context.Context, tx *sqlTx, workspace string) (*coldPublishState, error) {
	state := &coldPublishState{records: map[string]map[string]coldPublishRecord{}, occurrences: map[string][]byte{}, manifest: map[string]string{}}
	var err error
	if state.stamp, err = readMetadataStamp(ctx, tx, workspace); err != nil {
		return nil, err
	}
	var enabled, capturing int
	if err := tx.QueryRowContext(ctx, `SELECT enabled,capturing,generation FROM record_journal_mode WHERE id=1`).Scan(&enabled, &capturing, &state.generation); err != nil {
		return nil, err
	}
	if enabled != 1 || capturing != 1 {
		return nil, coldPublishInexact("record journal not capturing")
	}
	rootLength := "LENGTH(data)"
	if s.dialect == "sqlite" {
		rootLength = "LENGTH(CAST(data AS BLOB))"
	}
	var size int64
	var root []byte
	if err := tx.QueryRowContext(ctx, `SELECT `+rootLength+`,CASE WHEN `+rootLength+`<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?`, coldPublishRootBytes, workspace).Scan(&size, &root); err != nil {
		return nil, err
	}
	if size > coldPublishRootBytes || root == nil {
		return nil, coldPublishInexact("root size")
	}
	if err := state.readRoot(root); err != nil {
		return nil, coldPublishInexact("root: " + err.Error())
	}
	physical := "0"
	if s.dialect == "sqlite" {
		physical = "CASE WHEN typeof(r.record_key)='blob' THEN 1 ELSE 0 END"
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.field,r.record_key,r.collection_order,COALESCE(v.version,-1),`+physical+` FROM workspace_metadata_records r LEFT JOIN record_versions v ON v.workspace_key=r.workspace_key AND v.field=r.field AND v.record_key=r.record_key WHERE r.workspace_key=?`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var field string
		var key []byte
		var record coldPublishRecord
		var blob int
		if err := rows.Scan(&field, &key, &record.order, &record.version, &blob); err != nil {
			return nil, err
		}
		record.physical = blob == 1
		records := state.records[field]
		if records == nil {
			records = map[string]coldPublishRecord{}
			state.records[field] = records
		}
		if _, duplicate := records[string(key)]; duplicate {
			// A text and a BLOB key with the same bytes: their order is the
			// database's, not the comparison's.
			return nil, coldPublishInexact("ambiguous record key")
		}
		records[string(key)] = record
	}
	return state, rows.Err()
}

// readRoot records the root's occurrences of every field (in root order, as
// recovery reads them) and its manifest (readManifest's semantics).
func (state *coldPublishState) readRoot(root []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(root))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return nil
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return errors.New("root is not an object")
	}
	names := coldPublishFieldNames()
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("root key")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		if strings.EqualFold(key, metadataCollectionsKey) {
			if err := state.readManifest(raw); err != nil {
				return err
			}
		}
		for _, name := range names {
			if strings.EqualFold(key, name) {
				var size [8]byte
				binary.LittleEndian.PutUint64(size[:], uint64(len(raw)))
				state.occurrences[name] = append(append(state.occurrences[name], size[:]...), raw...)
			}
		}
	}
	_, err = decoder.Token()
	return err
}

func (state *coldPublishState) readManifest(raw json.RawMessage) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		clear(state.manifest)
		state.manifestRows = false
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return errors.New("manifest is not an object")
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok {
			return errors.New("manifest key")
		}
		var shape string
		if err := decoder.Decode(&shape); err != nil {
			return err
		}
		state.manifestRows = true
		if _, _, recognized := metadataLogicalField(name); recognized {
			state.manifest[name] = shape
		}
	}
	_, err = decoder.Token()
	return err
}

// coldPublishChange is how one field's inputs changed between two states.
type coldPublishChange struct {
	root, manifest bool
	keys           []string // changed, inserted or removed rows
}

// diffColdPublishStates returns the changed fields.
func diffColdPublishStates(before, after *coldPublishState) map[string]*coldPublishChange {
	changes := map[string]*coldPublishChange{}
	get := func(name string) *coldPublishChange {
		if changes[name] == nil {
			changes[name] = &coldPublishChange{}
		}
		return changes[name]
	}
	for _, name := range coldPublishFieldNames() {
		if !bytes.Equal(before.occurrences[name], after.occurrences[name]) {
			get(name).root = true
		}
		shapeBefore, inBefore := before.manifest[name]
		shapeAfter, inAfter := after.manifest[name]
		if inBefore != inAfter || shapeBefore != shapeAfter {
			get(name).manifest = true
		}
		if !after.manifestRows || !inBefore && !inAfter {
			// Recovery reads no canonical row of this field.
			continue
		}
		old, current := before.records[name], after.records[name]
		for key, record := range current {
			if previous, ok := old[key]; !ok || previous != record {
				get(name).keys = append(get(name).keys, key)
			}
		}
		for key := range old {
			if _, ok := current[key]; !ok {
				get(name).keys = append(get(name).keys, key)
			}
		}
	}
	for _, change := range changes {
		slices.Sort(change.keys)
	}
	return changes
}

// publishColdIncrementalLocked installs the image of workspace after a COLD
// legacy write from base and the records the write changed. The caller holds
// the write lock. It returns errColdPublishInexact (or another error) when
// the caller must reload in full; nothing was installed then.
func (s *SQLiteStore) publishColdIncrementalLocked(ctx context.Context, base *coldPublishBase) (unchanged bool, err error) {
	workspace := base.workspace
	r := s.metadataResidency
	ctx = metadataLogicalUnpinnedContext(ctx)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	ctx, releaseParser, err := r.work.acquire(ctx, 2<<20)
	if err != nil {
		return false, err
	}
	defer releaseParser()
	owner := &ResourceRead{store: s}
	defer owner.Close()
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return false, err
	}
	r.mu.Lock()
	current := r.logical[workspace]
	r.mu.Unlock()
	if current != nil && current.Stamp() == stamp {
		// The installed generation is exact at the stored stamp: the write
		// committed no metadata change, or published an exact image itself
		// (reloadResidentWorkspace keeps it the same way).
		return true, tx.Commit()
	}
	// Under the write lock only this write installs: a generation other than
	// base's is its own publication with an unknown stamp, which this one
	// replaces. base's generation (retained) is exact at before.
	installed := base.source
	after, err := s.readColdPublishState(ctx, tx, workspace)
	if err != nil {
		return false, err
	}
	before := base.state
	if after.generation != before.generation {
		return false, coldPublishInexact("journal generation changed")
	}
	if after.stamp.workspaceID != before.stamp.workspaceID {
		return false, coldPublishInexact("workspace replaced")
	}
	if after.manifestRows != before.manifestRows {
		return false, coldPublishInexact("manifest presence changed")
	}
	changes := diffColdPublishStates(before, after)
	if changes["workspaceSettings"] != nil {
		return false, coldPublishInexact("base field workspaceSettings changed")
	}
	plan, err := s.planColdPublish(ctx, tx, workspace, installed, before, after, changes)
	if err != nil {
		return false, err
	}
	defer plan.close()
	source, err := s.buildColdPublication(ctx, tx, workspace, installed, after, plan)
	if err != nil {
		return false, err
	}
	defer func() {
		if source != nil {
			source.Release()
		}
	}()
	head, err := residentJournalHead(ctx, tx, after.stamp.workspaceID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	data, cost, err := s.metadataLogicalPolicyAdmitted(ctx, source, func(size uint64) error {
		return owner.reserve(ctx, size)
	})
	if err != nil {
		return false, err
	}
	if err := s.installResidentPolicy(workspace, data, after.stamp, head, cost, source); err != nil {
		return false, err
	}
	rememberColdPublication(coldLabelCacheKey{r, workspace}, source, after)
	source = nil
	noteColdPublishPlan(plan)
	slog.Info("bounded metadata: COLD publication", "workspace", workspace, "patched", slices.Sorted(maps.Keys(plan.patch)),
		"rebuilt", slices.Sorted(maps.Keys(plan.rebuild)), "recovered", slices.Sorted(maps.Keys(plan.part)), "labelCounts", len(plan.supplied) != 0)
	return false, nil
}

// coldPublishPlan says how each field of the publication is produced.
type coldPublishPlan struct {
	changes map[string]*coldPublishChange
	// rebuild: recovered and normalized in full; patch: installed items
	// copied, changed rows normalized.
	rebuild, patch map[string]bool
	// part: the raw fields recovered (rebuild fields and the normalizer's
	// base and dependencies); supplied: raw fields given instead (label
	// uses of every project).
	part     map[string]bool
	supplied map[string]reflect.Value
	// rows: the decoded changed rows of patched fields, by key.
	rows map[string]map[string]reflect.Value
	// order, before: the rows of patched fields in recovery order, after
	// and before the write.
	order, before map[string][]string
}

func (p *coldPublishPlan) close() {}

// planColdPublish decides each field's production. Fields whose inputs and
// dependencies are unchanged keep their installed node.
func (s *SQLiteStore) planColdPublish(ctx context.Context, tx *sqlTx, workspace string, installed *metadataLogicalSource, before, after *coldPublishState, changes map[string]*coldPublishChange) (*coldPublishPlan, error) {
	plan := &coldPublishPlan{changes: changes, rebuild: map[string]bool{}, patch: map[string]bool{}, part: map[string]bool{},
		supplied: map[string]reflect.Value{}, rows: map[string]map[string]reflect.Value{}, order: map[string][]string{}, before: map[string][]string{}}
	labels := newColdLabelUses(s, workspace, after)
	// A project change reaches labels and issues only through label counts:
	// when every changed project uses the same labels as before, it does not.
	countNeutral := false
	if change := changes["projects"]; change != nil && !change.root && !change.manifest {
		neutral, err := labels.unchanged(ctx, tx, before, after, change.keys)
		if err != nil {
			return nil, err
		}
		countNeutral = neutral
	}
	affected := map[string]bool{}
	for name := range changes {
		affected[name] = true
	}
	for _, name := range coldPublishFieldNames() {
		for _, dependency := range coldPublishDependencies[name] {
			if changes[dependency] == nil || dependency == name {
				continue
			}
			if dependency == "projects" && countNeutral {
				continue
			}
			affected[name] = true
		}
	}
	for name := range affected {
		if s.coldPublishPatchable(name, installed, before, after, changes) {
			plan.patch[name] = true
		} else {
			plan.rebuild[name] = true
		}
	}
	for _, name := range coldPublishBaseFields {
		plan.part[name] = true
	}
	needsLabelCounts := false
	for name := range affected {
		if plan.rebuild[name] {
			plan.part[name] = true
		}
		for _, dependency := range coldPublishDependencies[name] {
			if dependency == "projects" {
				needsLabelCounts = true
				continue
			}
			plan.part[dependency] = true
		}
	}
	if needsLabelCounts && !plan.part["projects"] {
		if !coldPublishRootEmpty(after.occurrences["projects"], "[]") {
			// Inline root projects are recovered with their rows.
			plan.part["projects"] = true
		} else {
			projects, err := labels.all(ctx, tx, after)
			if err != nil {
				return nil, err
			}
			plan.supplied["projects"] = projects
		}
	}
	for name := range plan.patch {
		if err := s.readColdPublishRows(ctx, tx, workspace, name, before, after, changes[name].keys, plan); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

// coldPublishPatchable reports whether field name can be patched: only its
// own canonical rows changed (no root occurrence, the same manifest shape, no
// dependency change), it is a collection of structured items normalized one
// by one, and the installed field holds exactly the before rows.
func (s *SQLiteStore) coldPublishPatchable(name string, installed *metadataLogicalSource, before, after *coldPublishState, changes map[string]*coldPublishChange) bool {
	change := changes[name]
	if change == nil || change.root || change.manifest || coldPublishUnpatchable[name] {
		return false
	}
	for _, dependency := range coldPublishDependencies[name] {
		if dependency != name && changes[dependency] != nil {
			return false
		}
	}
	index, field, ok := metadataLogicalField(name)
	if !ok {
		return false
	}
	shape := before.manifest[name]
	switch {
	case field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.Struct:
		if shape != "array" || !coldPublishRootEmpty(before.occurrences[name], "[]") {
			return false
		}
		if id, ok := field.Type.Elem().FieldByName("ID"); !ok || id.Type.Kind() != reflect.String {
			return false
		}
	case field.Type.Kind() == reflect.Map && field.Type.Key().Kind() == reflect.String && field.Type.Elem().Kind() != reflect.Slice:
		if shape != "map" || !coldPublishRootEmpty(before.occurrences[name], "{}") {
			return false
		}
	default:
		return false
	}
	rows, current := len(before.records[name]), len(after.records[name])
	if rows == 0 || current == 0 || len(change.keys)*2 > current+1 {
		// Empty collections take the normalizer's default branch; a field
		// mostly rewritten is rebuilt.
		return false
	}
	node := installed.fields[index]
	if node.typ != field.Type || node.blocked || node.generic {
		return false
	}
	return true
}

// coldPublishRootEmpty reports whether every root occurrence of a split
// collection (the length-prefixed list readRoot records) is null or empty
// (empty: "[]" or "{}", whitespace aside), so the collection's items are its
// canonical rows only. (The occurrences are unchanged by the caller's check.)
func coldPublishRootEmpty(occurrences []byte, empty string) bool {
	for len(occurrences) > 0 {
		if len(occurrences) < 8 {
			return false
		}
		size := binary.LittleEndian.Uint64(occurrences[:8])
		occurrences = occurrences[8:]
		if uint64(len(occurrences)) < size {
			return false
		}
		raw := bytes.TrimSpace(occurrences[:size])
		occurrences = occurrences[size:]
		if bytes.Equal(raw, []byte("null")) {
			continue
		}
		if len(raw) < 2 || raw[0] != empty[0] || raw[len(raw)-1] != empty[1] || len(bytes.TrimSpace(raw[1:len(raw)-1])) != 0 {
			return false
		}
	}
	return true
}

// readColdPublishRows decodes the changed rows of patched field name as
// recovery decodes them, and records the rows' recovery order.
func (s *SQLiteStore) readColdPublishRows(ctx context.Context, tx *sqlTx, workspace, name string, before, after *coldPublishState, keys []string, plan *coldPublishPlan) error {
	_, field, _ := metadataLogicalField(name)
	plan.order[name] = coldPublishRecoveryOrder(after.records[name], field.Type.Kind() == reflect.Map)
	plan.before[name] = coldPublishRecoveryOrder(before.records[name], field.Type.Kind() == reflect.Map)
	records := after.records[name]
	decoded := map[string]reflect.Value{}
	for _, key := range keys {
		record, present := records[key]
		if !present {
			continue
		}
		raw, err := s.readColdPublishRow(ctx, tx, workspace, name, key, record)
		if err != nil {
			return err
		}
		value, err := decodeColdPublishRow(raw, field.Type.Elem())
		if err != nil {
			return err
		}
		decoded[key] = value
	}
	plan.rows[name] = decoded
	return nil
}

// coldPublishRecoveryOrder orders a field's rows as recovery appends them:
// arrays by collection order, then key bytes; maps by key bytes.
func coldPublishRecoveryOrder(records map[string]coldPublishRecord, isMap bool) []string {
	order := make([]string, 0, len(records))
	for key := range records {
		order = append(order, key)
	}
	slices.SortFunc(order, func(a, b string) int {
		if !isMap && records[a].order != records[b].order {
			if records[a].order < records[b].order {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	return order
}

// readColdPublishRow reads one canonical row's body as recovery does
// (readCanonical): its stored bytes with offloaded markers resolved.
func (s *SQLiteStore) readColdPublishRow(ctx context.Context, tx *sqlTx, workspace, name, key string, record coldPublishRecord) ([]byte, error) {
	data, length := "data", "LENGTH(data)"
	if s.dialect == "sqlite" {
		data, length = "CAST(data AS BLOB)", "LENGTH(CAST(data AS BLOB))"
	}
	var recordKey any = key
	if record.physical || s.dialect == "mysql" {
		recordKey = []byte(key)
	}
	var size int64
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT `+length+`,CASE WHEN `+length+`<=? THEN `+data+` ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`,
		metadataLogicalRecoveryRecordBytes, workspace, name, recordKey).Scan(&size, &raw); err != nil {
		return nil, err
	}
	if size > metadataLogicalRecoveryRecordBytes || int64(len(raw)) != size {
		return nil, ErrMetadataWorkBudget
	}
	row := []metadataCacheRow{{Field: name, Key: key, Data: raw}}
	if err := s.inlineOffloadedRows(ctx, workspace, tx, row); err != nil {
		return nil, err
	}
	if len(row[0].Data) > metadataLogicalRecoveryRecordBytes {
		return nil, ErrMetadataWorkBudget
	}
	return row[0].Data, nil
}

// decodeColdPublishRow decodes one row into a fresh item (recovery's
// decodeBytes).
func decodeColdPublishRow(raw []byte, typ reflect.Type) (reflect.Value, error) {
	budget := metadataHydrationBudget{bytes: metadataLogicalRecoveryRecordBytes, work: metadataWorkBytes - 128<<10}
	if err := budget.consume(raw); err != nil {
		return reflect.Value{}, err
	}
	value := reflect.New(typ).Elem()
	if err := json.Unmarshal(raw, value.Addr().Interface()); err != nil {
		return reflect.Value{}, err
	}
	return value, nil
}

// buildColdPublication produces the new generation: unchanged fields share
// the installed nodes, rebuilt fields are recovered and normalized from tx,
// patched fields copy their unchanged items.
func (s *SQLiteStore) buildColdPublication(ctx context.Context, tx *sqlTx, workspace string, installed *metadataLogicalSource, after *coldPublishState, plan *coldPublishPlan) (_ *metadataLogicalSource, err error) {
	if err := installed.Retain(); err != nil {
		return nil, err
	}
	defer installed.Release()
	scratch := metadataLogicalSourceScratchContext(ctx, installed)
	raw, err := s.readRawMetadataLogicalSourcePart(scratch, tx, workspace, true, plan.part, plan.supplied)
	if err != nil {
		return nil, err
	}
	defer raw.Release()
	if raw.Stamp() != after.stamp {
		return nil, coldPublishInexact("stamp moved")
	}
	if err := validateMetadataLogicalRootIdentity(scratch, raw); err != nil {
		return nil, err
	}
	normalizeCtx := context.WithValue(scratch, metadataWorkLeaseKey{}, nil)
	n, normalizeCtx, done, err := s.newMetadataLogicalNormalizer(normalizeCtx, raw)
	if err != nil {
		return nil, err
	}
	defer done()
	patched := map[string]map[string]reflect.Value{}
	for name := range plan.patch {
		values, err := s.normalizeColdPublishRows(normalizeCtx, n, name, plan)
		if err != nil {
			return nil, err
		}
		patched[name] = values
	}
	b, err := newMetadataLogicalBuilder(normalizeCtx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			b.source.Release()
		}
	}()
	err = installed.VisitFields(normalizeCtx, func(name string, node metadataLogicalNode) error {
		index, _, ok := metadataLogicalField(name)
		if !ok || name == "-" {
			return nil
		}
		if !plan.rebuild[name] && !plan.patch[name] {
			if !slices.Contains(b.source.stores, node.source) {
				if err := node.source.Retain(); err != nil {
					return err
				}
				b.source.stores = append(b.source.stores, node.source)
			}
			b.source.fields[index] = node
			return nil
		}
		// Each rewritten field has its own registry (as Fork): copied frames
		// keep the installed storage's dynamic type and time IDs.
		part, err := newMetadataLogicalBuilder(normalizeCtx)
		if err != nil {
			return err
		}
		b.source.stores = append(b.source.stores, part.storage)
		b.storage, b.offset = part.storage, 0
		part.source.stores = nil
		part.source.Release()
		b.storage.types, b.storage.times = slices.Clone(node.source.types), slices.Clone(node.source.times)
		if err := b.inheritLocations(node.source); err != nil {
			return err
		}
		if err := b.inheritTimestampInputs(normalizeCtx); err != nil {
			return err
		}
		if plan.rebuild[name] {
			return n.field(normalizeCtx, b, name, raw.fields[index])
		}
		return s.patchColdPublicationField(normalizeCtx, b, name, node, plan, patched[name])
	})
	if err != nil {
		return nil, err
	}
	return b.Finish(after.stamp)
}

// normalizeColdPublishRows stages the decoded changed rows of field name as
// recovery stages them (a typed frame, read back, written into the raw
// field) and normalizes each from its raw frame as the normalizer does.
func (s *SQLiteStore) normalizeColdPublishRows(ctx context.Context, n *metadataLogicalNormalizer, name string, plan *coldPublishPlan) (map[string]reflect.Value, error) {
	result := map[string]reflect.Value{}
	rows := plan.rows[name]
	if len(rows) == 0 {
		return result, nil
	}
	_, field, _ := metadataLogicalField(name)
	keys := make([]string, 0, len(rows))
	for _, key := range plan.order[name] {
		if _, ok := rows[key]; ok {
			keys = append(keys, key)
		}
	}
	isMap := field.Type.Kind() == reflect.Map
	index, _, _ := metadataLogicalField(name)
	// stage writes values as one collection of a scratch builder and returns
	// its node; close abandons the builder (the node is read before).
	stage := func(values func(func(string, reflect.Value) error) error) (metadataLogicalNode, func(), error) {
		builder, err := newMetadataLogicalBuilder(ctx)
		if err != nil {
			return metadataLogicalNode{}, nil, err
		}
		if err := builder.BeginCollection(name, false); err != nil {
			builder.Close()
			return metadataLogicalNode{}, nil, err
		}
		if err := values(func(key string, value reflect.Value) error {
			if err := builder.auditJSONLocations(value); err != nil {
				return err
			}
			k := reflect.Value{}
			if isMap {
				k = reflect.ValueOf(key).Convert(field.Type.Key())
			}
			return builder.Append(k, value)
		}); err != nil {
			builder.Close()
			return metadataLogicalNode{}, nil, err
		}
		if err := builder.EndCollection(); err != nil {
			builder.Close()
			return metadataLogicalNode{}, nil, err
		}
		return builder.source.fields[index], builder.Close, nil
	}
	// recovery's typed stage (put): a fresh decode, audited.
	typed, closeTyped, err := stage(func(appendValue func(string, reflect.Value) error) error {
		for _, key := range keys {
			if err := appendValue(key, rows[key]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	defer closeTyped()
	// recovery's builder (get, audit, Append): the staged values read back.
	stored, closeStored, err := stage(func(appendValue func(string, reflect.Value) error) error {
		position := 0
		return typed.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error {
			value, err := item.Value(ctx, int(item.cost))
			if err != nil {
				return err
			}
			key := keys[position]
			position++
			return appendValue(key, value)
		})
	})
	if err != nil {
		return nil, err
	}
	defer closeStored()
	position := 0
	ordinals := map[string]int{}
	for index, key := range plan.order[name] {
		ordinals[key] = index
	}
	err = stored.Children(ctx, func(key reflect.Value, item metadataLogicalNode) error {
		id := keys[position]
		position++
		if !isMap {
			key = reflect.Value{}
		}
		normalized, done, err := n.item(ctx, name, key, item, ordinals[id])
		if err != nil {
			return err
		}
		// The value is the caller's own memory; only its work allowance ends.
		done()
		result[id] = normalized
		return nil
	})
	if err == nil && position != len(keys) {
		err = coldPublishInexact("staged rows")
	}
	return result, err
}

// patchColdPublicationField writes field name in recovery order: unchanged
// rows copy the installed item's frames, changed rows their normalized value.
func (s *SQLiteStore) patchColdPublicationField(ctx context.Context, b *metadataLogicalBuilder, name string, node metadataLogicalNode, plan *coldPublishPlan, normalized map[string]reflect.Value) error {
	changed := map[string]bool{}
	for _, key := range plan.changes[name].keys {
		changed[key] = true
	}
	if err := b.BeginCollection(name, false); err != nil {
		return err
	}
	before := plan.before[name]
	if node.typ.Kind() == reflect.Slice {
		// The installed array is the before rows in recovery order (the
		// installed generation is exact at before). Each copied item's ID
		// must be its row's key.
		items := []metadataLogicalNode{}
		if err := node.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error {
			items = append(items, item)
			return nil
		}); err != nil {
			return err
		}
		if len(items) != len(before) {
			return coldPublishInexact("installed items")
		}
		position := make(map[string]int, len(before))
		for index, key := range before {
			position[key] = index
		}
		for _, key := range plan.order[name] {
			if changed[key] {
				value, ok := normalized[key]
				if !ok {
					return coldPublishInexact("missing normalized row")
				}
				if err := b.auditJSONLocations(value); err != nil {
					return err
				}
				if err := b.Append(reflect.Value{}, value); err != nil {
					return err
				}
				continue
			}
			index, ok := position[key]
			if !ok {
				return coldPublishInexact("installed items")
			}
			item := items[index]
			if match, err := coldPublishItemID(ctx, item, key); err != nil {
				return err
			} else if !match {
				return coldPublishInexact("installed item key")
			}
			copied, err := b.copyNode(item)
			if err != nil {
				return err
			}
			b.cost += copied.cost + uint64(item.typ.Size())*2
			b.unavailable = b.unavailable || copied.unavailable
			b.count++
		}
		return b.EndCollection()
	}
	type entry struct{ key, value metadataLogicalNode }
	entries := map[string]entry{}
	if err := node.MapEntries(ctx, func(key, item metadataLogicalNode) error {
		decoded, err := key.Value(ctx, 4096)
		if err != nil {
			return err
		}
		if _, duplicate := entries[decoded.String()]; duplicate {
			return coldPublishInexact("duplicate installed key")
		}
		entries[decoded.String()] = entry{key, item}
		return nil
	}); err != nil {
		return err
	}
	if len(entries) != len(before) {
		return coldPublishInexact("installed entries")
	}
	for _, key := range before {
		if _, ok := entries[key]; !ok {
			return coldPublishInexact("installed entries")
		}
	}
	for _, key := range plan.order[name] {
		if changed[key] {
			value, ok := normalized[key]
			if !ok {
				return coldPublishInexact("missing normalized row")
			}
			if err := b.auditJSONLocations(value); err != nil {
				return err
			}
			if err := b.Append(reflect.ValueOf(key).Convert(node.typ.Key()), value); err != nil {
				return err
			}
			continue
		}
		installed, ok := entries[key]
		if !ok {
			return coldPublishInexact("installed entries")
		}
		copiedKey, err := b.copyNode(installed.key)
		if err != nil {
			return err
		}
		copiedValue, err := b.copyNode(installed.value)
		if err != nil {
			return err
		}
		b.cost += copiedKey.cost + copiedValue.cost + uint64(installed.value.typ.Size())*2 + 128
		b.unavailable = b.unavailable || copiedKey.unavailable || copiedValue.unavailable
		b.count++
	}
	return b.EndCollection()
}

// coldPublishItemID reports whether a struct item's ID is id.
func coldPublishItemID(ctx context.Context, item metadataLogicalNode, id string) (bool, error) {
	match := false
	err := item.Children(ctx, func(field reflect.Value, child metadataLogicalNode) error {
		if field.String() != "ID" {
			return nil
		}
		equal, err := child.StringEqual(ctx, id)
		match = equal
		return err
	})
	return match, err
}

// Label uses of projects, by row version: the normalizer's label counts read
// each project's labels (unless archived). A project row with the same
// version (in the same journal generation) has the same body, so its use is
// cached across publications.
type coldLabelUse struct {
	version int64
	project domain.Project // ID, LabelIDs and ArchivedAt only
}

type coldLabelCache struct {
	mu         sync.Mutex
	generation string
	identity   string
	uses       map[string]coldLabelUse
}

type coldLabelCacheKey struct {
	residency *metadataResidency
	workspace string
}

var coldLabelCaches sync.Map // coldLabelCacheKey -> *coldLabelCache

type coldLabelUses struct {
	store     *SQLiteStore
	workspace string
	cache     *coldLabelCache
}

func newColdLabelUses(s *SQLiteStore, workspace string, after *coldPublishState) *coldLabelUses {
	value, _ := coldLabelCaches.LoadOrStore(coldLabelCacheKey{s.metadataResidency, workspace}, &coldLabelCache{})
	cache := value.(*coldLabelCache)
	cache.mu.Lock()
	if cache.generation != after.generation || cache.identity != after.stamp.workspaceID {
		cache.generation, cache.identity, cache.uses = after.generation, after.stamp.workspaceID, map[string]coldLabelUse{}
	}
	cache.mu.Unlock()
	return &coldLabelUses{store: s, workspace: workspace, cache: cache}
}

func coldLabelUseOf(project domain.Project) []string {
	if project.ArchivedAt != nil {
		return nil
	}
	ids := []string{}
	for _, id := range project.LabelIDs {
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// use returns the label use of project row key at its after version.
func (u *coldLabelUses) use(ctx context.Context, tx *sqlTx, after *coldPublishState, key string) (domain.Project, error) {
	record := after.records["projects"][key]
	u.cache.mu.Lock()
	cached, ok := u.cache.uses[key]
	u.cache.mu.Unlock()
	if ok && cached.version == record.version {
		return cached.project, nil
	}
	raw, err := u.store.readColdPublishRow(ctx, tx, u.workspace, "projects", key, record)
	if err != nil {
		return domain.Project{}, err
	}
	value, err := decodeColdPublishRow(raw, reflect.TypeFor[domain.Project]())
	if err != nil {
		return domain.Project{}, err
	}
	project := value.Interface().(domain.Project)
	project = domain.Project{ID: project.ID, LabelIDs: project.LabelIDs, ArchivedAt: project.ArchivedAt}
	u.cache.mu.Lock()
	u.cache.uses[key] = coldLabelUse{version: record.version, project: project}
	u.cache.mu.Unlock()
	return project, nil
}

// unchanged reports whether the changed project rows keys use the same labels
// before and after (a row's before use is known only from the cache).
func (u *coldLabelUses) unchanged(ctx context.Context, tx *sqlTx, before, after *coldPublishState, keys []string) (bool, error) {
	for _, key := range keys {
		var previous, current []string
		if record, ok := before.records["projects"][key]; ok {
			u.cache.mu.Lock()
			cached, known := u.cache.uses[key]
			u.cache.mu.Unlock()
			if !known || cached.version != record.version {
				return false, nil
			}
			previous = coldLabelUseOf(cached.project)
		}
		if _, ok := after.records["projects"][key]; ok {
			project, err := u.use(ctx, tx, after, key)
			if err != nil {
				return false, err
			}
			current = coldLabelUseOf(project)
		}
		if !slices.Equal(previous, current) {
			return false, nil
		}
	}
	return true, nil
}

// all returns the label uses of every project row after, as the raw projects
// field the normalizer's index counts labels from.
func (u *coldLabelUses) all(ctx context.Context, tx *sqlTx, after *coldPublishState) (reflect.Value, error) {
	keys := make([]string, 0, len(after.records["projects"]))
	for key := range after.records["projects"] {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	projects := make([]domain.Project, 0, len(keys))
	for _, key := range keys {
		project, err := u.use(ctx, tx, after, key)
		if err != nil {
			return reflect.Value{}, err
		}
		projects = append(projects, project)
	}
	u.cache.mu.Lock()
	for key := range u.cache.uses {
		if _, ok := after.records["projects"][key]; !ok {
			delete(u.cache.uses, key)
		}
	}
	u.cache.mu.Unlock()
	return reflect.ValueOf(projects), nil
}

// publishColdLegacyWriteIncremental tries the incremental publication of
// base; it reports whether it installed the image (or found it exact).
func (s *SQLiteStore) publishColdLegacyWriteIncremental(ctx context.Context, base *coldPublishBase) bool {
	started := time.Now()
	unchanged, err := s.publishColdIncrementalLocked(ctx, base)
	if err != nil {
		reason := err.Error()
		if !errors.Is(err, errColdPublishInexact) {
			slog.Warn("bounded metadata: incremental COLD publication failed; reloading", "workspace", base.workspace, "error", err)
			reason = "error"
		} else {
			reason = strings.TrimPrefix(reason, errColdPublishInexact.Error()+": ")
		}
		noteColdPublishFallback(reason)
		return false
	}
	if unchanged {
		coldPublishUnchanged.Add(1)
		return true
	}
	coldPublishIncrementals.Add(1)
	coldPublishIncrementalNanos.Add(int64(time.Since(started)))
	return true
}
