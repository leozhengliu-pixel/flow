package store

import (
	"context"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

type resourcePolicyMemoKey struct{}

// Only canonical team policy is shared. Principal, credential, installed-source
// precedence and live epoch checks still run for every ResolveAccess call.
type resourcePolicyMemo struct {
	mu        sync.Mutex
	owner     *ResourceRead
	store     *SQLiteStore
	workspace string
	tx        *sqlTx
	stamp     copyStamp
	epoch     string
	data      domain.Bootstrap
	release   func()
	present   bool
	closed    bool
}

func withResourcePolicyMemo(ctx context.Context, owner *ResourceRead) (context.Context, func()) {
	if owner == nil || owner.store == nil || owner.store.db == nil || !owner.store.BoundedMetadataEnabled() || !owner.store.db.journalReady.Load() {
		return ctx, func() {}
	}
	snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead)
	if !ok || snapshot.database != owner.store.db || snapshot.reader == nil ||
		snapshot.reader.database != owner.store.db || snapshot.workspace == "" {
		return ctx, func() {}
	}
	memo := &resourcePolicyMemo{owner: owner, store: owner.store, workspace: snapshot.workspace, tx: snapshot.reader}
	owner.mu.Lock()
	if owner.closed {
		owner.mu.Unlock()
		return ctx, func() {}
	}
	owner.release = append(owner.release, memo.close)
	owner.mu.Unlock()
	return context.WithValue(ctx, resourcePolicyMemoKey{}, memo), memo.close
}

func (m *resourcePolicyMemo) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	m.data = domain.Bootstrap{}
	if m.release != nil {
		m.release()
		m.release = nil
	}
}

func resourcePolicyMemoFor(ctx context.Context, s *SQLiteStore, workspace string) *resourcePolicyMemo {
	memo, ok := ctx.Value(resourcePolicyMemoKey{}).(*resourcePolicyMemo)
	if !ok || memo == nil || memo.store != s || memo.workspace != workspace || !s.db.journalReady.Load() {
		return nil
	}
	snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead)
	if !ok || snapshot.database != s.db || snapshot.workspace != workspace || snapshot.reader != memo.tx ||
		snapshot.reader == nil || snapshot.reader.database != s.db {
		return nil
	}
	memo.owner.mu.Lock()
	closed := memo.owner.closed
	memo.owner.mu.Unlock()
	if closed {
		return nil
	}
	return memo
}

func (m *resourcePolicyMemo) lookup(stamp copyStamp, epoch string) (domain.Bootstrap, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !m.present || m.stamp != stamp || m.epoch != epoch {
		return domain.Bootstrap{}, false
	}
	return m.data, true
}

func (m *resourcePolicyMemo) remember(ctx context.Context, stamp copyStamp, epoch string, data domain.Bootstrap) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.present {
		return nil
	}
	cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(data), 0)
	if err != nil {
		return err
	}
	if cost > metadataWorkBytes-(64<<10) {
		return ErrMetadataWorkBudget
	}
	_, release, err := m.store.tryAdmitAdditionalMetadataWork(ctx, int(cost)+(64<<10))
	if err != nil {
		return err
	}
	m.stamp, m.epoch, m.data, m.release, m.present = stamp, epoch, data, release, true
	return nil
}
