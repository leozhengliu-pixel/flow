package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"flow/api/internal/domain"
)

type legacyRecordBaseKey struct{}

// legacyRecordBase keeps the full writer's physical fingerprints and order
// state on the existing bounded migration stage. It borrows SQL transactions.
type legacyRecordBase struct {
	store           *SQLiteStore
	stage           *sw26MigrationStage
	owner           *ResourceRead
	loaded          map[string]bool
	lockedTx        *sqlTx
	lockedWorkspace string
	lockedTarget    *metadataLogicalSource
	closed          bool
}

func contextWithLegacyRecordBase(ctx context.Context, base *legacyRecordBase) context.Context {
	return context.WithValue(ctx, legacyRecordBaseKey{}, base)
}

func legacyRecordBaseCapture(ctx context.Context) *legacyRecordBase {
	base, _ := ctx.Value(legacyRecordBaseKey{}).(*legacyRecordBase)
	return base
}

func (s *SQLiteStore) newLegacyRecordBase(ctx context.Context, installed *metadataLogicalSource) (_ *legacyRecordBase, err error) {
	if s == nil || !s.BoundedMetadataEnabled() || installed == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = metadataLogicalSourceScratchContext(metadataLogicalUnpinnedContext(ctx), installed)
	base := &legacyRecordBase{store: s, owner: &ResourceRead{store: s}, loaded: map[string]bool{}}
	defer func() {
		if err != nil {
			base.Close()
		}
	}()
	if err = base.owner.reserve(ctx, 256<<10); err != nil {
		return nil, err
	}
	base.stage, err = sw26NewMigrationStage(ctx)
	if err != nil {
		return nil, err
	}
	for _, pair := range [][2]string{{"issue", "issues"}, {"activity", "activities"}, {"comment", "comments"}, {"notification", "notifications"}, {"delivery", "notificationDeliveries"}} {
		index, _, _ := metadataLogicalField(pair[1])
		base.loaded[pair[0]], err = installed.fields[index].isNil(ctx)
		if err != nil {
			return nil, err
		}
	}
	for _, query := range []string{
		`CREATE TABLE legacy_base(kind TEXT NOT NULL,resource BLOB NOT NULL,id BLOB NOT NULL,stored BLOB NOT NULL,loaded BLOB NOT NULL,PRIMARY KEY(kind,resource,id)) WITHOUT ROWID`,
		`CREATE TABLE legacy_current(kind TEXT NOT NULL,resource BLOB NOT NULL,id BLOB NOT NULL,hash BLOB,position INTEGER NOT NULL,seen INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(kind,resource,id)) WITHOUT ROWID`,
		`CREATE TABLE legacy_issue_order(seq INTEGER PRIMARY KEY,id BLOB NOT NULL,number INTEGER NOT NULL,position INTEGER,frame_offset INTEGER NOT NULL,frame_size INTEGER NOT NULL,frame_cost INTEGER NOT NULL)`,
		`CREATE INDEX legacy_issue_order_id ON legacy_issue_order(id)`,
		`CREATE TABLE legacy_issue_payload(id BLOB PRIMARY KEY,data BLOB NOT NULL) WITHOUT ROWID`,
		`CREATE TABLE legacy_issue_history(seq INTEGER PRIMARY KEY,selected BLOB NOT NULL,resource BLOB NOT NULL,data BLOB NOT NULL)`,
		`CREATE INDEX legacy_issue_history_selected ON legacy_issue_history(selected,seq)`,
		`CREATE TABLE legacy_issue_positions(id BLOB PRIMARY KEY,position INTEGER NOT NULL) WITHOUT ROWID`,
		`CREATE TABLE legacy_issue_stats(scope TEXT NOT NULL,id BLOB NOT NULL,state BLOB NOT NULL,type BLOB NOT NULL,priority INTEGER NOT NULL,archived INTEGER NOT NULL,total INTEGER NOT NULL,PRIMARY KEY(scope,id,state,type,priority,archived)) WITHOUT ROWID`,
	} {
		if _, err = base.stage.exec(ctx, 4096, query); err != nil {
			return nil, err
		}
	}
	return base, nil
}

func (b *legacyRecordBase) Close() {
	if b == nil || b.closed {
		return
	}
	b.closed = true
	if b.stage != nil {
		_ = b.stage.Close()
		b.stage = nil
	}
	if b.owner != nil {
		b.owner.Close()
		b.owner = nil
	}
	b.loaded = nil
	b.lockedTx, b.lockedTarget, b.lockedWorkspace = nil, nil, ""
}

func (b *legacyRecordBase) check(ctx context.Context) error {
	if b == nil || b.closed || b.stage == nil || b.owner == nil {
		return errMetadataLogicalClosed
	}
	return ctx.Err()
}

// Hydration calls this with the raw row it actually decoded. A second SQL
// scan could observe a different READ COMMITTED generation and is not a base.
func (b *legacyRecordBase) RememberStored(ctx context.Context, kind, resource, id string, raw []byte) error {
	if err := b.check(ctx); err != nil {
		return err
	}
	if !b.loaded[kind] {
		return nil
	}
	hash := sha256.Sum256(raw)
	var empty [32]byte
	_, err := b.stage.exec(ctx, int64(len(resource)+len(id)+256),
		`INSERT INTO legacy_base(kind,resource,id,stored,loaded) VALUES(?,?,?,?,?) ON CONFLICT(kind,resource,id) DO UPDATE SET stored=excluded.stored,loaded=excluded.loaded`,
		kind, []byte(resource), []byte(id), hash[:], empty[:])
	return err
}

func (b *legacyRecordBase) FingerprintLoaded(ctx context.Context, source *metadataLogicalSource) error {
	if err := b.check(ctx); err != nil {
		return err
	}
	if source == nil {
		return ErrMetadataRecordQuery
	}
	if err := source.Retain(); err != nil {
		return err
	}
	defer source.Release()
	visit := func(kind, resource string, _ int, value reflect.Value, _ *ResourceRead) error {
		if !b.loaded[kind] {
			return nil
		}
		var id string
		var raw []byte
		var err error
		if kind == "issue" {
			item := value.Interface().(domain.Issue)
			id = item.ID
			values, err := issueRecordValues("", item)
			if err != nil {
				return err
			}
			raw = values[len(values)-1].([]byte)
		} else {
			raw, err = json.Marshal(value.Interface())
			if err != nil {
				return err
			}
			var identity struct {
				ID string `json:"id"`
			}
			if err = json.Unmarshal(raw, &identity); err != nil {
				return err
			}
			id = identity.ID
		}
		hash := sha256.Sum256(raw)
		_, err = b.stage.exec(ctx, int64(len(id)+len(resource)+256),
			`UPDATE legacy_base SET loaded=? WHERE kind=? AND resource=? AND id=?`, hash[:], kind, []byte(resource), []byte(id))
		return err
	}
	for _, kind := range []string{"issue", "activity", "comment", "notification", "delivery"} {
		if b.loaded[kind] {
			if err := b.visit(ctx, source, kind, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// visit follows the literal target arrays. Notification grouping changes
// only the physical resource key; order within each group remains unchanged.
func (b *legacyRecordBase) visit(ctx context.Context, source *metadataLogicalSource, selected string, visit func(string, string, int, reflect.Value, *ResourceRead) error) error {
	ordinal := map[string]int{}
	return source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		kind := ""
		switch name {
		case "issues":
			kind = "issue"
		case "activities":
			kind = "activity"
		case "comments":
			kind = "comment"
		case "notifications":
			kind = "notification"
		case "notificationDeliveries":
			kind = "delivery"
		default:
			return nil
		}
		if selected != "" && selected != kind {
			return nil
		}
		item := func(resource string, value metadataLogicalNode) error {
			work := &ResourceRead{store: b.store}
			defer work.Close()
			if value.cost > (metadataWorkBytes-(64<<10))/4 {
				return ErrMetadataWorkBudget
			}
			if err := work.reserve(ctx, value.cost*4+64<<10); err != nil {
				return err
			}
			native, err := value.Value(ctx, metadataWorkBytes)
			if err != nil {
				return err
			}
			defer native.SetZero()
			if kind == "notification" {
				notification := native.Interface().(domain.Notification)
				resource = notification.IssueID
				if resource == "" {
					resource = notification.SourceID
				}
			}
			at := ordinal[kind]
			ordinal[kind] = at + 1
			return visit(kind, resource, at, native, work)
		}
		if kind == "activity" || kind == "comment" {
			return node.MapEntries(ctx, func(key, list metadataLogicalNode) error {
				keyWork := &ResourceRead{store: b.store}
				defer keyWork.Close()
				if key.cost > (metadataWorkBytes-4096)/8 {
					return ErrMetadataWorkBudget
				}
				if err := keyWork.reserve(ctx, key.cost*8+4096); err != nil {
					return err
				}
				value, err := key.Value(ctx, metadataWorkBytes)
				if err != nil {
					return err
				}
				return list.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error { return item(value.String(), record) })
			})
		}
		return node.Children(ctx, func(_ reflect.Value, value metadataLogicalNode) error { return item("", value) })
	})
}

func (b *legacyRecordBase) fingerprint(ctx context.Context, kind, resource, id string) (recordFingerprint, bool, error) {
	var stored, loaded []byte
	err := b.stage.db.QueryRowContext(ctx, `SELECT stored,loaded FROM legacy_base WHERE kind=? AND resource=? AND id=?`, kind, []byte(resource), []byte(id)).Scan(&stored, &loaded)
	if errors.Is(err, sql.ErrNoRows) {
		return recordFingerprint{}, false, nil
	}
	if err != nil {
		return recordFingerprint{}, false, err
	}
	if len(stored) != 32 || len(loaded) != 32 {
		return recordFingerprint{}, false, ErrMetadataRecordQuery
	}
	var result recordFingerprint
	copy(result.stored[:], stored)
	copy(result.loaded[:], loaded)
	return result, true, nil
}

func (b *legacyRecordBase) previous(ctx context.Context, kind, resource, id string) ([32]byte, int, bool, error) {
	var raw []byte
	var position int
	query := `SELECT hash,position FROM legacy_current WHERE kind=? AND resource=? AND id=?`
	if kind != "issue" {
		query += ` AND seen=0`
	}
	err := b.stage.db.QueryRowContext(ctx, query, kind, []byte(resource), []byte(id)).Scan(&raw, &position)
	if errors.Is(err, sql.ErrNoRows) {
		return [32]byte{}, 0, false, nil
	}
	if err != nil {
		return [32]byte{}, 0, false, err
	}
	var hash [32]byte
	if kind != "issue" && len(raw) != 32 {
		return hash, 0, false, ErrMetadataRecordQuery
	}
	copy(hash[:], raw)
	return hash, position, true, nil
}

func (b *legacyRecordBase) seen(ctx context.Context, kind, resource, id string) error {
	_, err := b.stage.exec(ctx, int64(len(resource)+len(id)+256),
		`UPDATE legacy_current SET seen=1 WHERE kind=? AND resource=? AND id=?`, kind, []byte(resource), []byte(id))
	return err
}

// scanCurrent replaces only the population-sized maps in the original
// replace/sync functions. CASE guards transfer before any driver allocation.
func (b *legacyRecordBase) scanCurrent(ctx context.Context, tx *sqlTx, workspace, kind string) error {
	wire := &ResourceRead{store: b.store}
	defer wire.Close()
	table, where, args := "workspace_content_records", "workspace_key=? AND kind=?", []any{workspace, kind}
	if kind == "issue" {
		table, where, args = "issue_records", "workspace_key=?", []any{workspace}
	}
	length := wire.byteLength("id")
	maxQuery := "SELECT COALESCE(MAX(" + length + "),0)"
	if kind != "issue" {
		maxQuery += ",COALESCE(MAX(" + wire.byteLength("resource_id") + "),0),COALESCE(MAX(" + wire.byteLength("data") + "),0)"
	}
	maxQuery += " FROM " + table + " WHERE " + where
	var maxID, maxResource, maxBody int64
	var err error
	if kind == "issue" {
		err = tx.QueryRowContext(ctx, maxQuery, args...).Scan(&maxID)
	} else {
		err = tx.QueryRowContext(ctx, maxQuery, args...).Scan(&maxID, &maxResource, &maxBody)
	}
	if err != nil {
		return err
	}
	if maxID < 0 || maxResource < 0 || maxBody < 0 || maxID > metadataWorkBytes/16 || maxResource > metadataWorkBytes/16 || maxBody > metadataWorkBytes/16 {
		return ErrMetadataWorkBudget
	}
	if err := wire.reserve(ctx, uint64(maxID+maxResource)*8+uint64(maxBody)*3+128<<10); err != nil {
		return err
	}
	query := "SELECT CASE WHEN " + length + "<=? THEN id ELSE NULL END,"
	values := []any{maxID}
	if kind == "issue" {
		query += "collection_order FROM " + table + " WHERE " + where
	} else {
		query += "CASE WHEN " + wire.byteLength("resource_id") + "<=? THEN resource_id ELSE NULL END,CASE WHEN " + wire.byteLength("data") + "<=? THEN data ELSE NULL END," + wire.byteLength("data") + " FROM " + table + " WHERE " + where
		values = append(values, maxResource, maxBody)
	}
	values = append(values, args...)
	rows, err := tx.QueryContext(ctx, query, values...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, resource sql.NullString
		var position int
		var raw []byte
		var actual sql.NullInt64
		if kind == "issue" {
			err = rows.Scan(&id, &position)
		} else {
			err = rows.Scan(&id, &resource, &raw, &actual)
		}
		if err != nil {
			return err
		}
		if !id.Valid || kind != "issue" && (!resource.Valid || !actual.Valid || actual.Int64 < 0 || actual.Int64 > maxBody || int64(len(raw)) != actual.Int64) {
			return ErrConcurrentWrite
		}
		var hash []byte
		if kind != "issue" {
			value := sha256.Sum256(raw)
			hash = value[:]
		}
		if _, err := b.stage.exec(ctx, int64(len(id.String)+len(resource.String)+256),
			`INSERT INTO legacy_current(kind,resource,id,hash,position,seen) VALUES(?,?,?,?,?,0) ON CONFLICT(kind,resource,id) DO UPDATE SET hash=excluded.hash,position=excluded.position,seen=0`,
			kind, []byte(resource.String), []byte(id.String), hash, position); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (b *legacyRecordBase) currentBody(ctx context.Context, tx *sqlTx, workspace, kind, resource, id string, work *ResourceRead) ([]byte, bool, error) {
	table, where, args := "workspace_content_records", "workspace_key=? AND kind=? AND resource_id=? AND id=?", []any{workspace, kind, resource, id}
	if kind == "issue" {
		table, where, args = "issue_records", "workspace_key=? AND id=?", []any{workspace, id}
	}
	length := work.byteLength("data")
	var size int64
	if err := tx.QueryRowContext(ctx, "SELECT "+length+" FROM "+table+" WHERE "+where, args...).Scan(&size); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if size < 0 || size > metadataWorkBytes/16 {
		return nil, false, ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(size)*4+64<<10); err != nil {
		return nil, false, err
	}
	var raw []byte
	var physical sql.NullString
	query := "SELECT id,CASE WHEN " + length + "<=? THEN data ELSE NULL END FROM " + table + " WHERE " + where
	if err := tx.QueryRowContext(ctx, query, append([]any{size}, args...)...).Scan(&physical, &raw); err != nil {
		return nil, false, err
	}
	if !physical.Valid || int64(len(raw)) != size {
		return nil, false, ErrConcurrentWrite
	}
	// The original issue payload batch indexes results by exact Go physical
	// ID, even when SQL collation matched a differently-cased candidate.
	if kind == "issue" && physical.String != id {
		return nil, false, nil
	}
	return raw, true, nil
}

func (b *legacyRecordBase) lock(ctx context.Context, tx *sqlTx, workspace string, active map[string]bool) error {
	if tx.dialect == "sqlite" {
		return nil
	}
	if active["issue"] && b.loaded["issue"] {
		var last int
		if err := tx.QueryRowContext(ctx, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=? FOR UPDATE`, workspace).Scan(&last); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := lockRows(ctx, tx, `SELECT 1 FROM issue_records WHERE workspace_key=? FOR UPDATE`, workspace); err != nil {
			return err
		}
	}
	var kinds []string
	for _, kind := range []string{"activity", "comment", "delivery", "notification"} {
		if active[kind] && b.loaded[kind] {
			kinds = append(kinds, kind)
		}
	}
	if len(kinds) == 0 {
		return nil
	}
	clause, args := bindList("kind", kinds)
	return lockRows(ctx, tx, `SELECT 1 FROM workspace_content_records WHERE workspace_key=? AND `+clause+` FOR UPDATE`, append([]any{workspace}, args...)...)
}

func (b *legacyRecordBase) active(ctx context.Context, source *metadataLogicalSource) (map[string]bool, error) {
	result := map[string]bool{}
	for _, pair := range [][2]string{{"issue", "issues"}, {"activity", "activities"}, {"comment", "comments"}, {"notification", "notifications"}, {"delivery", "notificationDeliveries"}} {
		index, _, _ := metadataLogicalField(pair[1])
		nilValue, err := source.fields[index].isNil(ctx)
		if err != nil {
			return nil, err
		}
		result[pair[0]] = !nilValue
	}
	return result, nil
}

// LockForPersistence lets the full writer keep its literal lock-before-import
// order. The transaction, workspace and immutable target must match Persist.
func (b *legacyRecordBase) LockForPersistence(ctx context.Context, tx *sqlTx, workspace string, target *metadataLogicalSource) error {
	if err := b.check(ctx); err != nil {
		return err
	}
	if target == nil || tx == nil || tx.database != b.store.db {
		return ErrMetadataRecordQuery
	}
	if b.lockedTx != nil {
		if b.lockedTx != tx || b.lockedWorkspace != workspace || b.lockedTarget != target {
			return ErrMetadataRecordQuery
		}
		return nil
	}
	if err := target.Retain(); err != nil {
		return err
	}
	defer target.Release()
	active, err := b.active(ctx, target)
	if err != nil {
		return err
	}
	if err := b.lock(ctx, tx, workspace, active); err != nil {
		return err
	}
	b.lockedTx, b.lockedWorkspace, b.lockedTarget = tx, workspace, target
	return nil
}

// Persist retains the original content family order and the original issue
// target batch boundary. Close must run after the caller commits or rolls back.
func (b *legacyRecordBase) Persist(ctx context.Context, tx *sqlTx, workspace string, target *metadataLogicalSource) error {
	if err := b.check(ctx); err != nil {
		return err
	}
	if target == nil || tx == nil || tx.database != b.store.db {
		return ErrMetadataRecordQuery
	}
	if b.lockedTx != nil && (b.lockedTx != tx || b.lockedWorkspace != workspace || b.lockedTarget != target) {
		return ErrMetadataRecordQuery
	}
	if err := target.Retain(); err != nil {
		return err
	}
	defer target.Release()
	active, err := b.active(ctx, target)
	if err != nil {
		return err
	}
	if b.lockedTx == nil {
		if err := b.lock(ctx, tx, workspace, active); err != nil {
			return err
		}
	}
	for _, kind := range []string{"delivery", "notification", "activity", "comment", "issue"} {
		if !active[kind] {
			continue
		}
		if err := b.scanCurrent(ctx, tx, workspace, kind); err != nil {
			return err
		}
		if kind == "issue" {
			if err := b.persistIssues(ctx, tx, workspace, target); err != nil {
				return err
			}
		} else if err := b.persistContent(ctx, tx, workspace, target, kind); err != nil {
			return err
		}
	}
	return nil
}

func (b *legacyRecordBase) persistContent(ctx context.Context, tx *sqlTx, workspace string, target *metadataLogicalSource, kind string) error {
	err := b.visit(ctx, target, kind, func(recordKind, resource string, _ int, value reflect.Value, work *ResourceRead) error {
		if recordKind != kind {
			return nil
		}
		raw, err := json.Marshal(value.Interface())
		if err != nil {
			return err
		}
		id, _, err := contentRecordIdentity(value.Interface())
		if err != nil {
			return err
		}
		hash := sha256.Sum256(raw)
		current, _, stored, err := b.previous(ctx, kind, resource, id)
		if err != nil {
			return err
		}
		if b.loaded[kind] {
			base, loaded, err := b.fingerprint(ctx, kind, resource, id)
			if err != nil {
				return err
			}
			if loaded && (!stored || current != base.stored) || !loaded && stored {
				if loaded && base.loaded == hash {
					return b.seen(ctx, kind, resource, id)
				}
				return fmt.Errorf("%s %s: %w", kind, id, ErrConcurrentWrite)
			}
		}
		if !stored || current != hash {
			var previous []byte
			if stored {
				var exists bool
				previous, exists, err = b.currentBody(ctx, tx, workspace, kind, resource, id, work)
				if err != nil {
					return err
				}
				if !exists {
					return sql.ErrNoRows
				}
			}
			refs, err := b.recordReferences(ctx, target, work, previous, raw)
			if err != nil {
				return err
			}
			if !refs.equalDisplayData(previous, raw) {
				if err := b.writeContent(ctx, tx, workspace, kind, resource, value); err != nil {
					return err
				}
			}
		}
		return b.seen(ctx, kind, resource, id)
	})
	if err != nil {
		return err
	}
	return b.unseen(ctx, kind, func(resource, id string, current [32]byte) error {
		if b.loaded[kind] {
			fingerprint, loaded, err := b.fingerprint(ctx, kind, resource, id)
			if err != nil {
				return err
			}
			if !loaded {
				return nil
			}
			if current != fingerprint.stored {
				return fmt.Errorf("%s %s: %w", kind, id, ErrConcurrentWrite)
			}
		}
		if kind == "activity" {
			return deleteIssueActivity(ctx, tx, workspace, resource, id)
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind=? AND resource_id=? AND id=?`, workspace, kind, resource, id)
		return err
	})
}

func (b *legacyRecordBase) unseen(ctx context.Context, kind string, visit func(string, string, [32]byte) error) error {
	// Stage rows may be modified by visit through another stage operation.
	// A one-row cursor releases each result before entering the callback.
	wire := &ResourceRead{store: b.store}
	defer wire.Close()
	var maxResource, maxID int64
	if err := b.stage.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(LENGTH(resource)),0),COALESCE(MAX(LENGTH(id)),0) FROM legacy_current WHERE kind=? AND seen=0`, kind).Scan(&maxResource, &maxID); err != nil {
		return err
	}
	if maxResource < 0 || maxID < 0 || uint64(maxResource+maxID) > (metadataWorkBytes-(128<<10))/8 {
		return ErrMetadataWorkBudget
	}
	if err := wire.reserve(ctx, uint64(maxResource+maxID)*8+128<<10); err != nil {
		return err
	}
	var resource, id []byte
	started := false
	for {
		query := `SELECT resource,id,hash FROM legacy_current WHERE kind=? AND seen=0`
		args := []any{kind}
		if started {
			query += ` AND (resource,id)>(?,?)`
			args = append(args, resource, id)
		}
		query += ` ORDER BY resource,id LIMIT 1`
		var nextResource, nextID, raw []byte
		err := b.stage.db.QueryRowContext(ctx, query, args...).Scan(&nextResource, &nextID, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		work := &ResourceRead{store: b.store}
		if err := work.reserve(ctx, uint64(len(nextResource)+len(nextID))*8+4096); err != nil {
			work.Close()
			return err
		}
		var hash [32]byte
		copy(hash[:], raw)
		err = visit(string(nextResource), string(nextID), hash)
		work.Close()
		if err != nil {
			return err
		}
		resource, id, started = nextResource, nextID, true
	}
}

func (b *legacyRecordBase) recordReferences(ctx context.Context, target *metadataLogicalSource, work *ResourceRead, raws ...[]byte) (issueReferences, error) {
	// Raw size alone does not bound maps with many small or unknown fields.
	// Admit the reference and canonical-display JSON work before either decode.
	for _, raw := range raws {
		if len(raw) == 0 {
			continue
		}
		cost, err := metadataAggregatePayloadJSONWork(ctx, raw)
		if err != nil {
			return issueReferences{}, err
		}
		if err := work.reserve(ctx, cost); err != nil {
			return issueReferences{}, err
		}
	}
	plan := &legacyMetadataWritePlan{store: b.store}
	return plan.references(ctx, target, work, nil, nil, raws...)
}

// Issue comparisons have their own encoding/decode owner after this call.
// Only one reference map is live here; returned IDs/users stay owned by work.
func (b *legacyRecordBase) recordIssueRowReferences(ctx context.Context, target *metadataLogicalSource, work *ResourceRead, raws ...[]byte) (issueReferences, error) {
	var maximum uint64
	for _, raw := range raws {
		if len(raw) == 0 {
			continue
		}
		cost, err := metadataAggregatePayloadJSONWork(ctx, raw)
		if err != nil {
			return issueReferences{}, err
		}
		maximum = max(maximum, cost)
	}
	scratch := &ResourceRead{store: b.store}
	defer scratch.Close()
	if maximum != 0 {
		if err := scratch.reserve(ctx, maximum); err != nil {
			return issueReferences{}, err
		}
	}
	plan := &legacyMetadataWritePlan{store: b.store}
	return plan.references(ctx, target, work, nil, nil, raws...)
}

func (b *legacyRecordBase) writeContent(ctx context.Context, tx *sqlTx, workspace, kind, resource string, value reflect.Value) error {
	// Supplied nonnil collections do not take loaded-family row locks. The
	// opt-in original-writer hook independently guards its second previous read.
	ctx = context.WithValue(ctx, legacyContentWriteBudgetKey{}, b.store)
	start := len(tx.signals)
	var release func()
	if kind == "notification" {
		notification := value.Interface().(domain.Notification)
		size := uint64(len(workspace)) + uint64(len(notification.ID)) + uint64(len(notification.RecipientID)) + uint64(len(notification.Actor.ID))
		if size > (metadataWorkBytes-1024)/8 {
			return ErrMetadataWorkBudget
		}
		var err error
		_, release, err = b.store.tryAdmitAdditionalMetadataWork(ctx, int(size*8+1024))
		if err != nil {
			return err
		}
	}
	err := writeContentRecord(ctx, tx, workspace, kind, resource, value.Interface())
	if release != nil {
		if len(tx.signals) > start {
			// Keep every appended occurrence charged, including duplicate IDs:
			// the original transaction retains a slice, not a distinct-key map.
			b.owner.release = append(b.owner.release, release)
		} else {
			release()
		}
	}
	return err
}

func (b *legacyRecordBase) prepareIssueOrder(ctx context.Context, target *metadataLogicalSource) error {
	if _, err := b.stage.exec(ctx, 4096, `DELETE FROM legacy_issue_order`); err != nil {
		return err
	}
	if _, err := b.stage.exec(ctx, 4096, `DELETE FROM legacy_issue_positions`); err != nil {
		return err
	}
	index, _, _ := metadataLogicalField("issues")
	seq := 0
	err := target.fields[index].Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		work := &ResourceRead{store: b.store}
		defer work.Close()
		idNode, err := issueHotField(ctx, node, "ID")
		if err != nil {
			return err
		}
		numberNode, err := issueHotField(ctx, node, "Number")
		if err != nil {
			return err
		}
		if idNode.cost > (metadataWorkBytes-4096)/8 {
			return ErrMetadataWorkBudget
		}
		if err := work.reserve(ctx, idNode.cost*8+4096); err != nil {
			return err
		}
		id, err := idNode.Value(ctx, metadataWorkBytes)
		if err != nil {
			return err
		}
		number, err := numberNode.Value(ctx, 4096)
		if err != nil {
			return err
		}
		_, position, known, err := b.previous(ctx, "issue", "", id.String())
		if err != nil {
			return err
		}
		var stored any
		if known {
			stored = position
		}
		if _, err := b.stage.exec(ctx, int64(len(id.String())+256), `INSERT INTO legacy_issue_order(seq,id,number,position,frame_offset,frame_size,frame_cost) VALUES(?,?,?,?,?,?,?)`, seq, []byte(id.String()), number.Int(), stored, node.offset, node.size, int64(node.cost)); err != nil {
			return err
		}
		seq++
		return nil
	})
	if err != nil {
		return err
	}
	var first, last sql.NullInt64
	if err := b.stage.db.QueryRowContext(ctx, `SELECT MIN(seq),MAX(seq) FROM legacy_issue_order WHERE position IS NOT NULL`).Scan(&first, &last); err != nil {
		return err
	}
	stable := first.Valid
	if stable {
		var gaps int
		if err := b.stage.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_issue_order WHERE seq>=? AND seq<=? AND position IS NULL`, first.Int64, last.Int64).Scan(&gaps); err != nil {
			return err
		}
		stable = gaps == 0
		var disorder int
		if err := b.stage.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_issue_order a JOIN legacy_issue_order z ON z.seq=a.seq-1 WHERE a.seq>? AND a.seq<=? AND (a.position<z.position OR (a.position=z.position AND a.id<z.id))`, first.Int64, last.Int64).Scan(&disorder); err != nil {
			return err
		}
		stable = stable && disorder == 0
	}
	var firstOrder, lastOrder int
	if stable {
		if err := b.stage.db.QueryRowContext(ctx, `SELECT position FROM legacy_issue_order WHERE seq=?`, first.Int64).Scan(&firstOrder); err != nil {
			return err
		}
		if err := b.stage.db.QueryRowContext(ctx, `SELECT position FROM legacy_issue_order WHERE seq=?`, last.Int64).Scan(&lastOrder); err != nil {
			return err
		}
	}
	wire := &ResourceRead{store: b.store}
	defer wire.Close()
	var maxID int64
	if err := b.stage.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(LENGTH(id)),0) FROM legacy_issue_order`).Scan(&maxID); err != nil {
		return err
	}
	if maxID < 0 || uint64(maxID) > (metadataWorkBytes-(128<<10))/8 {
		return ErrMetadataWorkBudget
	}
	if err := wire.reserve(ctx, uint64(maxID)*8+128<<10); err != nil {
		return err
	}
	for at := 0; at < seq; at++ {
		var id []byte
		var position sql.NullInt64
		if err := b.stage.db.QueryRowContext(ctx, `SELECT id,position FROM legacy_issue_order WHERE seq=?`, at).Scan(&id, &position); err != nil {
			return err
		}
		order := at
		if stable {
			switch {
			case int64(at) < first.Int64:
				order = firstOrder - int(first.Int64) + at
			case int64(at) > last.Int64:
				order = lastOrder + at - int(last.Int64)
			default:
				order = int(position.Int64)
			}
		}
		if _, err := b.stage.exec(ctx, int64(len(id)+256), `INSERT INTO legacy_issue_positions(id,position) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET position=excluded.position`, id, order); err != nil {
			return err
		}
	}
	return nil
}

type legacyRecordIssueBatch struct {
	issues   []domain.Issue
	orders   map[string]int
	releases []func()
	targets  int
}

func (batch *legacyRecordIssueBatch) close() {
	clear(batch.issues)
	batch.issues = nil
	batch.orders = nil
	for _, release := range batch.releases {
		release()
	}
	batch.releases = nil
	batch.targets = 0
}

type legacyRecordIssueHeader struct {
	id   string
	node metadataLogicalNode
}

func (b *legacyRecordBase) issueBatch(ctx context.Context, source *metadataLogicalSource, start int, work *ResourceRead) ([]legacyRecordIssueHeader, error) {
	var bytes int64
	if err := b.stage.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(LENGTH(id)),0) FROM legacy_issue_order WHERE seq>=? AND seq<?`, start, start+64).Scan(&bytes); err != nil {
		return nil, err
	}
	if bytes < 0 || uint64(bytes) > (metadataWorkBytes-(128<<10))/8 {
		return nil, ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(bytes)*8+128<<10); err != nil {
		return nil, err
	}
	rows, err := b.stage.db.QueryContext(ctx, `SELECT id,frame_offset,frame_size,frame_cost FROM legacy_issue_order WHERE seq>=? AND seq<? ORDER BY seq`, start, start+64)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	index, _, _ := metadataLogicalField("issues")
	field := source.fields[index]
	headers := make([]legacyRecordIssueHeader, 0, 64)
	for rows.Next() {
		var header legacyRecordIssueHeader
		var cost int64
		if err := rows.Scan(&header.id, &header.node.offset, &header.node.size, &cost); err != nil {
			return nil, err
		}
		if cost < 0 || cost > metadataWorkBytes || header.node.offset < 0 || header.node.size < 16 || header.node.offset > field.source.bytes-header.node.size {
			return nil, ErrMetadataRecordQuery
		}
		header.node.source, header.node.typ, header.node.cost = field.source, reflect.TypeFor[domain.Issue](), uint64(cost)
		headers = append(headers, header)
	}
	return headers, rows.Err()
}

// The literal writer snapshots payloads once for each group of 64 TARGET
// occurrences, including duplicate IDs. Stage that exact batch, not a later
// point read after an earlier duplicate has written.
func (b *legacyRecordBase) issuePayloadBatch(ctx context.Context, tx *sqlTx, workspace string, headers []legacyRecordIssueHeader) error {
	if _, err := b.stage.exec(ctx, 4096, `DELETE FROM legacy_issue_payload`); err != nil {
		return err
	}
	ids := make([]string, len(headers))
	for i, header := range headers {
		ids[i] = header.id
	}
	clause, args := bindList("id", ids)
	values := append([]any{workspace}, args...)
	wire := &ResourceRead{store: b.store}
	defer wire.Close()
	length := wire.byteLength("data")
	var maximum int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX("+length+"),0) FROM issue_records WHERE workspace_key=? AND "+clause, values...).Scan(&maximum); err != nil {
		return err
	}
	if maximum < 0 || maximum > metadataWorkBytes/16 {
		return ErrMetadataWorkBudget
	}
	if err := wire.reserve(ctx, uint64(maximum)*3+64<<10); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,CASE WHEN "+length+"<=? THEN data ELSE NULL END,"+length+" FROM issue_records WHERE workspace_key=? AND "+clause, append([]any{maximum}, values...)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		var actual sql.NullInt64
		if err := rows.Scan(&id, &raw, &actual); err != nil {
			return err
		}
		if !actual.Valid || actual.Int64 < 0 || actual.Int64 > maximum || int64(len(raw)) != actual.Int64 {
			return ErrConcurrentWrite
		}
		if raw == nil {
			raw = []byte{}
		}
		if _, err := b.stage.exec(ctx, int64(len(id)+len(raw)+256), `INSERT INTO legacy_issue_payload(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, []byte(id), raw); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (b *legacyRecordBase) issuePayload(ctx context.Context, id string, work *ResourceRead) ([]byte, bool, error) {
	var bytes int64
	err := b.stage.db.QueryRowContext(ctx, `SELECT LENGTH(data) FROM legacy_issue_payload WHERE id=?`, []byte(id)).Scan(&bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if bytes < 0 || uint64(bytes) > (metadataWorkBytes-(64<<10))/4 {
		return nil, false, ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(bytes)*4+64<<10); err != nil {
		return nil, false, err
	}
	var raw []byte
	if err := b.stage.db.QueryRowContext(ctx, `SELECT data FROM legacy_issue_payload WHERE id=?`, []byte(id)).Scan(&raw); err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func (b *legacyRecordBase) persistIssues(ctx context.Context, tx *sqlTx, workspace string, target *metadataLogicalSource) error {
	if err := b.prepareIssueOrder(ctx, target); err != nil {
		return err
	}
	batch := &legacyRecordIssueBatch{orders: map[string]int{}}
	defer batch.close()
	var history *legacyRecordIssueHistory
	defer func() { history.Close() }()
	flush := func() error {
		writeCtx := withIssueRecordWriteAdmission(ctx, b.store)
		if history != nil {
			if err := history.flush(ctx, tx); err != nil {
				return err
			}
			writeCtx = context.WithValue(writeCtx, legacyIssueHistoryPreparedKey{}, true)
		}
		if err := b.store.importIssueRecordBatch(writeCtx, tx, workspace, batch.issues, batch.orders); err != nil {
			return err
		}
		batch.close()
		batch.orders = map[string]int{}
		return nil
	}
	maxNumber, total := 0, 0
	retryAdmission := errors.New("retry legacy issue row admission")
	admissionError := func(err error) error {
		if errors.Is(err, ErrMetadataWorkBudget) {
			return retryAdmission
		}
		return err
	}
	process := func(value reflect.Value, native *ResourceRead) error {
		work := &ResourceRead{store: b.store}
		defer work.Close()
		issue := value.Interface().(domain.Issue)
		var position int
		if err := b.stage.db.QueryRowContext(ctx, `SELECT position FROM legacy_issue_positions WHERE id=?`, []byte(issue.ID)).Scan(&position); err != nil {
			return err
		}
		previous, stored, err := b.issuePayload(ctx, issue.ID, work)
		if err != nil {
			return admissionError(err)
		}
		columnsRelease, err := reserveIssueRecordEncoding(ctx, b.store, issue, nil)
		if err != nil {
			return admissionError(err)
		}
		defer columnsRelease()
		values, err := b.store.issueRecordColumns(workspace, issue)
		if err != nil {
			return err
		}
		raw := values[len(values)-1].([]byte)
		skip := false
		if b.loaded["issue"] {
			fingerprint, loaded, err := b.fingerprint(ctx, "issue", "", issue.ID)
			if err != nil {
				return err
			}
			if loaded && (!stored || sha256.Sum256(previous) != fingerprint.stored) || !loaded && stored {
				if loaded && fingerprint.loaded == sha256.Sum256(raw) {
					skip = true
				} else {
					return fmt.Errorf("issue %s: %w", issue.ID, ErrConcurrentWrite)
				}
			}
		}
		if !skip {
			refs, err := b.recordIssueRowReferences(ctx, target, work, previous, raw)
			if err != nil {
				return admissionError(err)
			}
			if len(previous) > 0 {
				release, err := reserveIssueRecordComparisonEncoding(ctx, b.store, issue, previous)
				if err != nil {
					return admissionError(err)
				}
				defer release()
				// Initial columns plus this delta own the full equality
				// envelope without a duplicate reservation during transfer.
			}
			// equalOwned also resolves indexed team/state/label dimensions.
			// This decodes the old issue under the equality owner's lease.
			refs, err = b.issueReferences(ctx, target, work, refs, issue, previous)
			if err != nil {
				return admissionError(err)
			}
			if !equalIssueRecordData(previous, raw) && !refs.equalOwned(previous, issue) {
				if history == nil {
					history, err = b.newIssueHistory(ctx)
					if err != nil {
						return admissionError(err)
					}
				}
				if err := history.append(ctx, workspace, issue, previous); err != nil {
					return admissionError(err)
				}
				if len(previous) > 0 {
					var old domain.Issue
					if err := json.Unmarshal(previous, &old); err != nil {
						return err
					}
					if err := b.addStats(ctx, issueStatsOf(old), -1); err != nil {
						return err
					}
				}
				if err := b.addStats(ctx, issueStatsOf(issue), 1); err != nil {
					return err
				}
				// The batch retains a shallow copy of this native row.
				// Transfer its existing owner instead of reserving the
				// same live allocations again during the handoff.
				batch.releases = append(batch.releases, native.Close)
				batch.issues = append(batch.issues, issue)
				batch.orders[issue.ID] = position
			} else {
				_, old, known, err := b.previous(ctx, "issue", "", issue.ID)
				if err != nil {
					return err
				}
				if !known || old != position {
					if _, err := tx.ExecContext(ctx, `UPDATE issue_records SET collection_order=? WHERE workspace_key=? AND id=?`, position, workspace, issue.ID); err != nil {
						return err
					}
				}
			}
		}
		if err := b.seen(ctx, "issue", "", issue.ID); err != nil {
			return err
		}
		batch.targets++
		total++
		maxNumber = max(maxNumber, issue.Number)
		return nil
	}
	for start := 0; ; start += 64 {
		headerWork := &ResourceRead{store: b.store}
		headers, err := b.issueBatch(ctx, target, start, headerWork)
		if err != nil {
			headerWork.Close()
			return err
		}
		if len(headers) == 0 {
			headerWork.Close()
			break
		}
		if err := b.issuePayloadBatch(ctx, tx, workspace, headers); err != nil {
			headerWork.Close()
			return err
		}
		if err := b.issueHistoryPayloadBatch(ctx, tx, workspace, headers); err != nil {
			headerWork.Close()
			return err
		}
		for _, header := range headers {
			for {
				work := &ResourceRead{store: b.store}
				err := func() error {
					// A changed row transfers this owner into batch. An
					// unchanged or failed row releases it locally.
					transferred := false
					defer func() {
						if !transferred {
							work.Close()
						}
					}()
					decode := &ResourceRead{store: b.store}
					defer decode.Close()
					if header.node.cost > (metadataWorkBytes-(64<<10))/4 {
						return retryAdmission
					}
					if err := decode.reserve(ctx, header.node.cost*4+64<<10); err != nil {
						return admissionError(err)
					}
					value, err := header.node.Value(ctx, metadataWorkBytes)
					if err != nil {
						return err
					}
					defer value.SetZero()
					// The frame decoder's temporary buffers are gone.
					// Transfer ownership to the actual native row before
					// entering independently admitted encoding phases.
					cost, err := metadataAggregateNativeCost(ctx, value, 0)
					if err != nil {
						return err
					}
					if cost > (metadataWorkBytes-(64<<10))/4 {
						return retryAdmission
					}
					if err := work.reserve(ctx, cost*4+64<<10); err != nil {
						return admissionError(err)
					}
					decode.Close()
					before := len(batch.issues)
					err = process(value, work)
					transferred = len(batch.issues) > before
					return err
				}()
				if err == retryAdmission && len(batch.issues) > 0 {
					if err := flush(); err != nil {
						headerWork.Close()
						return err
					}
					continue
				}
				if err != nil {
					headerWork.Close()
					if err == retryAdmission {
						return ErrMetadataWorkBudget
					}
					return err
				}
				break
			}
		}
		if err := flush(); err != nil {
			headerWork.Close()
			return err
		}
		history.Close()
		history = nil
		headerWork.Close()
	}
	if err := b.unseen(ctx, "issue", func(_, id string, _ [32]byte) error {
		work := &ResourceRead{store: b.store}
		defer work.Close()
		if b.loaded["issue"] {
			fingerprint, loaded, err := b.fingerprint(ctx, "issue", "", id)
			if err != nil {
				return err
			}
			if !loaded {
				return nil
			}
			current, stored, err := b.currentBody(ctx, tx, workspace, "issue", "", id, work)
			if err != nil {
				return err
			}
			if !stored {
				return sql.ErrNoRows
			}
			if sha256.Sum256(current) != fingerprint.stored {
				return fmt.Errorf("issue %s: %w", id, ErrConcurrentWrite)
			}
		}
		for _, table := range []string{"issue_subscriber_records", "issue_actor_records", "issue_attribute_records", "issue_attachment_records", "issue_search_documents", "issue_identifier_aliases"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE workspace_key=? AND issue_id=?", workspace, id); err != nil {
				return err
			}
		}
		old, err := readIssueStats(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		if err := b.addStats(ctx, old, -1); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM issue_permission_records WHERE workspace_key=? AND issue_id=?`, workspace, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM issue_label_records WHERE workspace_key=? AND issue_id=?`, workspace, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM issue_records WHERE workspace_key=? AND id=?`, workspace, id)
		return err
	}); err != nil {
		return err
	}
	if err := b.flushStats(ctx, tx, workspace); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO issue_records_migrations(workspace_key,migrated_at) VALUES(?,?) ON CONFLICT DO NOTHING`, workspace, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if b.loaded["issue"] {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM issue_records WHERE workspace_key=?`, workspace).Scan(&total); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO issue_collection_counts(workspace_key,total) VALUES(?,?) ON CONFLICT(workspace_key) DO UPDATE SET total=excluded.total`, workspace, total); err != nil {
		return err
	}
	return writeIssueNumber(ctx, tx, workspace, maxNumber)
}

func (b *legacyRecordBase) addStats(ctx context.Context, value *issueStatsDimensions, delta int64) error {
	deltas := map[issueStatsKey]int64{}
	addIssueStats(deltas, value, delta)
	for key, total := range deltas {
		if _, err := b.stage.exec(ctx, int64(len(key.Scope)+len(key.ID)+len(key.State)+len(key.Type)+512),
			`INSERT INTO legacy_issue_stats(scope,id,state,type,priority,archived,total) VALUES(?,?,?,?,?,?,?) ON CONFLICT(scope,id,state,type,priority,archived) DO UPDATE SET total=total+excluded.total`,
			key.Scope, []byte(key.ID), []byte(key.State), []byte(key.Type), key.Priority, key.Archived, total); err != nil {
			return err
		}
	}
	return nil
}

func (b *legacyRecordBase) flushStats(ctx context.Context, tx *sqlTx, workspace string) error {
	if err := writeIssueStats(ctx, tx, workspace, nil); err != nil {
		return err
	}
	rows, err := b.stage.db.QueryContext(ctx, `SELECT scope,id,state,type,priority,archived,total FROM legacy_issue_stats WHERE total<>0 ORDER BY scope,id,state,type,priority,archived`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key issueStatsKey
		var total int64
		if err := rows.Scan(&key.Scope, &key.ID, &key.State, &key.Type, &key.Priority, &key.Archived, &total); err != nil {
			return err
		}
		if err := b.owner.reserve(ctx, uint64(len(key.Scope)+len(key.ID)+len(key.State)+len(key.Type))*8+1024); err != nil {
			return err
		}
		if err := writeIssueStats(ctx, tx, workspace, map[issueStatsKey]int64{key: total}); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (b *legacyRecordBase) issueReferences(ctx context.Context, target *metadataLogicalSource, work *ResourceRead, refs issueReferences, issue domain.Issue, previous []byte) (issueReferences, error) {
	issues := []domain.Issue{issue}
	if len(previous) > 0 {
		var old domain.Issue
		if err := json.Unmarshal(previous, &old); err != nil {
			return refs, err
		}
		issues = append(issues, old)
	}
	needed := map[string]map[string]bool{"teams": {}, "states": {}, "users": {}, "labels": {}, "projects": {}}
	for _, item := range issues {
		needed["teams"][item.Team.ID] = true
		needed["states"][item.State.ID] = true
		needed["users"][item.Creator.ID] = true
		for _, user := range []*domain.User{item.Assignee, item.Delegate} {
			if user != nil {
				needed["users"][user.ID] = true
			}
		}
		if item.Project != nil {
			needed["projects"][item.Project.ID] = true
		}
		for _, label := range item.Labels {
			needed["labels"][label.ID] = true
		}
	}
	err := target.VisitFields(ctx, func(name string, collection metadataLogicalNode) error {
		ids, ok := needed[name]
		if !ok {
			return nil
		}
		return collection.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
			id, err := issueHotField(ctx, node, "ID")
			if err != nil {
				return err
			}
			var matched string
			found := false
			for key := range ids {
				same, err := id.StringEqual(ctx, key)
				if err != nil {
					return err
				}
				if same {
					matched, found = key, true
					break
				}
			}
			if !found {
				return nil
			}
			if name == "projects" {
				value, err := work.projectNode(ctx, node, "ID", "Name", "Icon", "Color")
				if err != nil {
					return err
				}
				project := value.Interface().(domain.Project)
				refs.projects[matched] = domain.ProjectSummary{ID: project.ID, Name: project.Name, Icon: project.Icon, Color: project.Color}
				return nil
			}
			if node.cost > (metadataWorkBytes-(64<<10))/4 {
				return ErrMetadataWorkBudget
			}
			if err := work.reserve(ctx, node.cost*4+64<<10); err != nil {
				return err
			}
			value, err := node.Value(ctx, metadataWorkBytes)
			if err != nil {
				return err
			}
			switch name {
			case "teams":
				refs.teams[matched] = value.Interface().(domain.Team)
			case "states":
				refs.states[matched] = value.Interface().(domain.WorkflowState)
			case "users":
				refs.users[matched] = value.Interface().(domain.User)
			case "labels":
				refs.labels[matched] = value.Interface().(domain.IssueLabel)
			}
			return nil
		})
	})
	return refs, err
}
