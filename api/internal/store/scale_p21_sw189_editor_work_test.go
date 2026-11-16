package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW189EditorWorkLifetime(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
	view := scaleP21IssuePersistenceView(t, f, true, false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	read, tx := scaleP21IssueEditorRead(t, ctx, view, f.data.Workspace.URLKey, f.data.Workspace.ID, issue)
	var data domain.Bootstrap
	read.install(&data)
	if data.FormerMembers == nil || data.CollaborationUpdates == nil || data.CollaborationWork == nil {
		t.Fatal("callback services incomplete")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var encoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"CollaborationWork", "collaborationWork", "CollaborationUpdates", "FormerMembers"} {
		if _, ok := encoded[name]; ok {
			t.Fatalf("callback service serialized: %s", name)
		}
	}
	work := data.CollaborationWork()
	if work.Context != ctx {
		t.Fatal("editor owner replaced original context")
	}
	baseline := view.MetadataWorkUsedForTesting()
	for range 1000 {
		release, err := work.Admit(4096)
		if err != nil || view.MetadataWorkUsedForTesting() != baseline+4096 {
			t.Fatalf("independent document lease: %v used=%d", err, view.MetadataWorkUsedForTesting())
		}
		release()
		if view.MetadataWorkUsedForTesting() != baseline {
			t.Fatal("released document kept accumulating admission")
		}
	}
	cancel()
	if release, err := work.Admit(1); !errors.Is(err, context.Canceled) || release != nil {
		t.Fatalf("cancellation lost: %v", err)
	}
	read.close()
	tx.Rollback()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("editor owner retained allocation leases")
	}
	if release, err := work.Admit(1); !errors.Is(err, ErrMetadataRecordQuery) || release != nil {
		t.Fatalf("closed owner admitted work: %v", err)
	}
}

func TestScaleP21SW189EditorWorkBudget(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
	view := scaleP21IssuePersistenceView(t, f, true, false)
	read, tx := scaleP21IssueEditorRead(t, t.Context(), view, f.data.Workspace.URLKey, f.data.Workspace.ID, issue)
	work := read.collaborationWork()
	baseline := view.MetadataWorkUsedForTesting()
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = baseline + 4096
	t.Cleanup(func() { view.metadataResidency.work.limit = limit })
	release, err := work.Admit(4096)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if finish, err := work.Admit(1); !errors.Is(err, ErrMetadataWorkBudget) || finish != nil {
		t.Fatalf("competing allocation exceeded process budget: %v", err)
	}
	if read.err != nil {
		t.Fatal("a failed rounded reservation poisoned a retry", read.err)
	}
	release()
	read.close()
	tx.Rollback()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("failed document leaked work")
	}
}
