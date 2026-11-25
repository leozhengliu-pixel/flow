package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

// One normalizer operation owns its base, current record and every dependency
// retained while that record is normalized/encoded. Parent credit applies to
// their sum once; independent leases cover only the excess above that credit.
type metadataLogicalNormalizeWork struct {
	ctx          context.Context
	store        *SQLiteStore
	total        int
	credit       int
	extra        int
	children     []func()
	retained     []func()
	parent       *metadataWorkLease
	closed       bool
	dependencies int
}

func startMetadataLogicalNormalizeWork(ctx context.Context, store *SQLiteStore, baseWork, currentWork int) (*metadataLogicalNormalizeWork, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store == nil || baseWork < 0 || currentWork < 0 || baseWork > metadataWorkBytes-currentWork {
		return nil, ErrMetadataWorkBudget
	}
	work := &metadataLogicalNormalizeWork{ctx: ctx, store: store}
	if store.metadataResidency != nil {
		if lease, ok := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease); ok && lease.budget == store.metadataResidency.work {
			lease.budget.mu.Lock()
			if lease.live {
				work.credit, work.parent = lease.bytes, lease
			}
			lease.budget.mu.Unlock()
			if work.parent == nil {
				return nil, ErrMetadataWorkBudget
			}
		}
	}
	if err := work.setTotal(baseWork + currentWork); err != nil {
		work.Close()
		return nil, err
	}
	return work, nil
}

func (w *metadataLogicalNormalizeWork) live() error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if w.closed {
		return ErrMetadataWorkBudget
	}
	if w.parent != nil {
		w.parent.budget.mu.Lock()
		live := w.parent.live
		w.parent.budget.mu.Unlock()
		if !live {
			return ErrMetadataWorkBudget
		}
	}
	return nil
}

func (w *metadataLogicalNormalizeWork) setTotal(total int) error {
	if err := w.live(); err != nil {
		return err
	}
	if total < 0 || total > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	required := max(0, total-w.credit)
	if required > w.extra {
		_, release, err := w.store.tryAdmitAdditionalMetadataWork(w.ctx, required-w.extra)
		if err != nil {
			return err
		}
		w.children = append(w.children, release)
		w.extra = required
	}
	w.total = total
	return nil
}

type metadataLogicalNormalizeDependency struct {
	work     *metadataLogicalNormalizeWork
	bytes    int
	released bool
}

func (w *metadataLogicalNormalizeWork) dependency(bytes int) (*metadataLogicalNormalizeDependency, error) {
	if bytes < 0 || bytes > metadataWorkBytes-w.total {
		return nil, ErrMetadataWorkBudget
	}
	if err := w.setTotal(w.total + bytes); err != nil {
		return nil, err
	}
	w.dependencies++
	return &metadataLogicalNormalizeDependency{work: w, bytes: bytes}, nil
}

func (d *metadataLogicalNormalizeDependency) resize(bytes int) error {
	if d.released || bytes < 0 || bytes > metadataWorkBytes || d.work.total-d.bytes > metadataWorkBytes-bytes {
		return ErrMetadataWorkBudget
	}
	if err := d.work.setTotal(d.work.total - d.bytes + bytes); err != nil {
		return err
	}
	d.bytes = bytes
	return nil
}

func (d *metadataLogicalNormalizeDependency) release() {
	if d.released {
		return
	}
	d.released = true
	if !d.work.closed {
		d.work.total -= d.bytes
		d.work.dependencies--
	}
}

func (w *metadataLogicalNormalizeWork) keep(release func()) {
	if release == nil {
		return
	}
	if w.closed {
		release()
		return
	}
	w.retained = append(w.retained, release)
}

func (w *metadataLogicalNormalizeWork) Close() {
	if w == nil || w.closed {
		return
	}
	for _, release := range w.retained {
		release()
	}
	w.closed = true
	for i := len(w.children) - 1; i >= 0; i-- {
		w.children[i]()
	}
	w.retained, w.children = nil, nil
	w.total, w.credit, w.extra, w.dependencies = 0, 0, 0, 0
}

func metadataLogicalDependencyType(kind string) reflect.Type {
	switch kind {
	case "users":
		return reflect.TypeFor[domain.User]()
	case "teams":
		return reflect.TypeFor[domain.Team]()
	case "states":
		return reflect.TypeFor[domain.WorkflowState]()
	case "labels":
		return reflect.TypeFor[domain.IssueLabel]()
	case "projectStatuses":
		return reflect.TypeFor[domain.ProjectStatus]()
	}
	return nil
}

// Typed dependency frames preserve native timestamps, nil/empty containers
// and concrete interface values. Their source's receipts carry the original
// JSON decoding provenance; no second JSON marshal/unmarshal is involved.
func (n *metadataLogicalNormalizer) putTyped(ctx context.Context, kind, key string, ordinal int, value reflect.Value, origin *metadataLogicalStorage) error {
	if n.values == nil || value.Type() != metadataLogicalDependencyType(kind) {
		return ErrMetadataRecordQuery
	}
	if origin != nil {
		if err := n.values.inheritLocations(origin); err != nil {
			return err
		}
	}
	node, err := n.values.writeValue(value, 0, false)
	if err != nil {
		return err
	}
	unavailable, blocked := 0, 0
	if node.unavailable {
		unavailable = 1
	}
	if node.blocked {
		blocked = 1
	}
	_, err = n.stage.exec(ctx, int64(len(kind)+len(key)+512),
		`INSERT INTO logical_values(kind,key,ordinal,value,frame_offset,frame_size,frame_cost,frame_unavailable,frame_blocked) VALUES(?,?,?,NULL,?,?,?,?,?) ON CONFLICT(kind,key) DO UPDATE SET ordinal=excluded.ordinal,value=NULL,frame_offset=excluded.frame_offset,frame_size=excluded.frame_size,frame_cost=excluded.frame_cost,frame_unavailable=excluded.frame_unavailable,frame_blocked=excluded.frame_blocked`,
		kind, key, ordinal, node.offset, node.size, node.cost, unavailable, blocked)
	return err
}

// getAdmitted checks byte length before SQL transfers a body, then charges the
// actual JSON token work before Unmarshal can allocate its object graph. The
// returned allowance stays live until the caller releases the decoded output.
func (n *metadataLogicalNormalizer) getAdmitted(ctx context.Context, kind, key string, output any) (bool, func(), error) {
	if err := ctx.Err(); err != nil {
		return false, nil, err
	}
	t := reflect.TypeOf(output)
	if t == nil || t.Kind() != reflect.Pointer || reflect.ValueOf(output).IsNil() {
		return false, nil, ErrMetadataRecordQuery
	}
	var rawBytes, offset, size, frameCost, unavailable, blocked sql.NullInt64
	err := n.stage.db.QueryRowContext(ctx,
		`SELECT LENGTH(CAST(value AS BLOB)),frame_offset,frame_size,frame_cost,frame_unavailable,frame_blocked FROM logical_values WHERE kind=? AND key=?`,
		kind, key).Scan(&rawBytes, &offset, &size, &frameCost, &unavailable, &blocked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, func() {}, nil
	}
	if err != nil {
		return false, nil, err
	}
	if offset.Valid {
		return n.getTypedAdmitted(ctx, kind, t.Elem(), output, offset, size, frameCost, unavailable, blocked)
	}
	if !rawBytes.Valid {
		return false, nil, ErrMetadataRecordQuery
	}
	bytes := rawBytes.Int64
	if bytes < 0 || bytes > metadataWorkBytes/2 {
		return false, nil, ErrMetadataWorkBudget
	}
	work := n.work
	standalone := work == nil
	if standalone {
		// Outside a current-record lifetime only presence booleans are used.
		// Retained directory records require an explicit surrounding operation.
		if t.Elem().Kind() != reflect.Bool {
			return false, nil, fmt.Errorf("%w: normalizer dependency needs a record work scope", ErrBoundedMetadataCoverage)
		}
		work, err = startMetadataLogicalNormalizeWork(ctx, n.store, n.baseWork, 0)
		if err != nil {
			return false, nil, err
		}
	}
	token, err := work.dependency(int(bytes)*2 + 64<<10)
	if err != nil {
		if standalone {
			work.Close()
		}
		return false, nil, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			token.release()
			if standalone {
				work.Close()
			}
		})
	}
	var raw []byte
	if err := n.stage.db.QueryRowContext(ctx,
		`SELECT CASE WHEN LENGTH(CAST(value AS BLOB))=? THEN value ELSE NULL END FROM logical_values WHERE kind=? AND key=?`,
		bytes, kind, key).Scan(&raw); err != nil {
		release()
		return false, nil, err
	}
	if raw == nil || int64(len(raw)) != bytes {
		release()
		return false, nil, ErrConcurrentWrite
	}
	budget := metadataHydrationBudget{bytes: len(raw), work: metadataWorkBytes}
	if err := budget.consume(raw); err != nil {
		release()
		return false, nil, ErrMetadataWorkBudget
	}
	cost := metadataWorkBytes - budget.work
	if uint64(t.Elem().Size()) > uint64(metadataWorkBytes/2) {
		release()
		return false, nil, ErrMetadataWorkBudget
	}
	cost += int(t.Elem().Size())*2 + 64<<10
	if err := token.resize(max(token.bytes, cost)); err != nil {
		release()
		return false, nil, err
	}
	if err := ctx.Err(); err != nil {
		release()
		return false, nil, err
	}
	if err := json.Unmarshal(raw, output); err != nil {
		release()
		return false, nil, err
	}
	if err := ctx.Err(); err != nil {
		release()
		return false, nil, err
	}
	return true, release, nil
}

func (n *metadataLogicalNormalizer) getTypedAdmitted(ctx context.Context, kind string, typ reflect.Type, output any, offset, size, cost, unavailable, blocked sql.NullInt64) (bool, func(), error) {
	if n.values == nil || n.work == nil || typ != metadataLogicalDependencyType(kind) ||
		!size.Valid || !cost.Valid || !unavailable.Valid || !blocked.Valid ||
		offset.Int64 < 0 || size.Int64 < 16 || cost.Int64 < 0 ||
		size.Int64 > n.values.offset || offset.Int64 > n.values.offset-size.Int64 ||
		(unavailable.Int64 != 0 && unavailable.Int64 != 1) ||
		(blocked.Int64 != 0 && blocked.Int64 != 1) {
		return false, nil, ErrMetadataRecordQuery
	}
	if cost.Int64 > metadataWorkBytes/4 {
		return false, nil, ErrMetadataWorkBudget
	}
	token, err := n.work.dependency(int(cost.Int64)*4 + 64<<10)
	if err != nil {
		return false, nil, err
	}
	var once sync.Once
	release := func() { once.Do(token.release) }
	node := metadataLogicalNode{
		source: n.values.storage, offset: offset.Int64, size: size.Int64, cost: uint64(cost.Int64), typ: typ,
		unavailable: unavailable.Int64 != 0, blocked: blocked.Int64 != 0,
	}
	value, err := node.Value(ctx, int(cost.Int64))
	if err != nil {
		release()
		return false, nil, err
	}
	if err := ctx.Err(); err != nil {
		release()
		return false, nil, err
	}
	reflect.ValueOf(output).Elem().Set(value)
	return true, release, nil
}
