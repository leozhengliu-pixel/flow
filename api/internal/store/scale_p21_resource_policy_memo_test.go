package store

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21PolicyMemoFixture(t *testing.T) (*SQLiteStore, *sqlTx, context.Context) {
	t.Helper()
	view := scaleP21HotLogoStore(t, nil)
	view.db = &sqlDatabase{}
	view.db.journalReady.Store(true)
	tx := &sqlTx{database: view.db}
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: view.db, workspace: "workspace", reader: tx})
	return view, tx, ctx
}

func TestScaleP21SW0ResourcePolicyMemoBindingAndLifetime(t *testing.T) {
	view, tx, ctx := scaleP21PolicyMemoFixture(t)
	owner := &ResourceRead{store: view}
	ctx, closeMemo := withResourcePolicyMemo(ctx, owner)
	defer owner.Close()
	memo := resourcePolicyMemoFor(ctx, view, "workspace")
	if memo == nil {
		t.Fatal("owned ready-journal snapshot has no canonical memo")
	}
	stamp := copyStamp{workspaceID: "identity", revision: 7}
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "identity", URLKey: "workspace"},
		Teams:     []domain.Team{{ID: "team", Key: "KEY", Private: true}},
		TeamSettings: map[string]domain.TeamSettings{
			"team": {ParentTeamID: "parent", Access: "private", MemberPermission: "admin"},
		},
	}
	if _, found := memo.lookup(stamp, "epoch"); found {
		t.Fatal("empty memo manufactured policy")
	}
	if err := memo.remember(ctx, stamp, "epoch", data); err != nil {
		t.Fatal(err)
	}
	held := view.MetadataWorkUsedForTesting()
	got, found := memo.lookup(stamp, "epoch")
	if !found || !reflect.DeepEqual(got, data) || held == 0 {
		t.Fatal("canonical policy lost its independent ownership", found, held)
	}
	if err := memo.remember(ctx, stamp, "epoch", domain.Bootstrap{}); err != nil ||
		view.MetadataWorkUsedForTesting() != held {
		t.Fatal("repeated remember replaced policy or added a lease", err)
	}
	for _, test := range []struct {
		stamp copyStamp
		epoch string
	}{
		{copyStamp{workspaceID: "another", revision: 7}, "epoch"},
		{copyStamp{workspaceID: "identity", revision: 8}, "epoch"},
		{stamp, "revoked"},
	} {
		if _, found := memo.lookup(test.stamp, test.epoch); found {
			t.Fatal("canonical memo crossed its metadata or authority version")
		}
	}
	for _, invalid := range []context.Context{
		context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: view.db, workspace: "workspace", reader: &sqlTx{database: view.db}}),
		context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: view.db, workspace: "other", reader: tx}),
		context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: view.db, workspace: "workspace"}),
		context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: &sqlDatabase{}, workspace: "workspace", reader: tx}),
	} {
		if resourcePolicyMemoFor(invalid, view, "workspace") != nil {
			t.Fatal("canonical memo crossed its SQL snapshot")
		}
	}
	if resourcePolicyMemoFor(ctx, &SQLiteStore{db: view.db}, "workspace") != nil ||
		resourcePolicyMemoFor(ctx, view, "other") != nil {
		t.Fatal("canonical memo crossed store or workspace")
	}
	view.db.journalReady.Store(false)
	if resourcePolicyMemoFor(ctx, view, "workspace") != nil {
		t.Fatal("canonical memo bypassed the compatibility policy path")
	}
	view.db.journalReady.Store(true)
	closeMemo()
	closeMemo()
	if _, found := memo.lookup(stamp, "epoch"); found || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("completed memo retained policy or allowance")
	}
	owner.Close()
	if resourcePolicyMemoFor(ctx, view, "workspace") != nil {
		t.Fatal("closed output owner retained usable policy")
	}
}

func TestScaleP21SW0ResourcePolicyMemoFailuresAndOwners(t *testing.T) {
	view, _, base := scaleP21PolicyMemoFixture(t)
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "identity"}}
	for _, owner := range []*ResourceRead{nil, {}, {store: &SQLiteStore{}}} {
		ctx, close := withResourcePolicyMemo(base, owner)
		close()
		if ctx != base {
			t.Fatal("unsupported operation acquired a memo")
		}
	}
	closed := &ResourceRead{store: view}
	closed.Close()
	if ctx, close := withResourcePolicyMemo(base, closed); ctx != base {
		close()
		t.Fatal("closed operation acquired a memo")
	}
	owner := &ResourceRead{store: view}
	ctx, close := withResourcePolicyMemo(base, owner)
	memo := resourcePolicyMemoFor(ctx, view, "workspace")
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	if err := memo.remember(ctx, copyStamp{}, "", data); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("canonical retention escaped quota", err)
	}
	view.metadataResidency.work.limit = limit
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := memo.remember(cancelled, copyStamp{}, "", data); !errors.Is(err, context.Canceled) ||
		view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cancelled retention acquired work", err)
	}
	close()
	owner.Close()
	var jobs sync.WaitGroup
	for range 4 {
		jobs.Go(func() {
			for range 3 {
				owner := &ResourceRead{store: view}
				ctx, close := withResourcePolicyMemo(base, owner)
				memo := resourcePolicyMemoFor(ctx, view, "workspace")
				if memo == nil {
					t.Error("independent owner lost its memo")
					return
				}
				if err := memo.remember(ctx, copyStamp{}, "", data); err != nil {
					t.Error(err)
					return
				}
				var closers sync.WaitGroup
				closers.Go(close)
				closers.Go(owner.Close)
				closers.Wait()
				if _, found := memo.lookup(copyStamp{}, ""); found {
					t.Error("closed independent memo retained policy")
				}
			}
		})
	}
	jobs.Wait()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("independent owners leaked canonical policy work")
	}
}
