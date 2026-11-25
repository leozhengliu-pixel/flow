package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW20ExpiredTeamPolicySnapshot(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	window := 30 * 24 * time.Hour
	old, boundary, future := now.Add(-window-time.Nanosecond), now.Add(-window), now.Add(-window+time.Nanosecond)
	source := domain.Bootstrap{
		Teams: []domain.Team{
			{ID: "live"},
			{ID: "z-old", ArchivedAt: &old, Name: strings.Repeat("unrelated name ", 30000)},
			{ID: "future", ArchivedAt: &future},
			{ID: "a-boundary", ArchivedAt: &boundary},
			{ID: "NUL\x00ID", ArchivedAt: &old},
			{ID: "", ArchivedAt: &old},
			{ID: "z-old", ArchivedAt: &old},
		},
		TeamSettings: map[string]domain.TeamSettings{"unused": {AgentGuidance: strings.Repeat("unrelated guidance ", 30000)}},
	}
	repo := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"workspace": source}, lastWorkspaceKey: "workspace", metadataResidency: newMetadataResidency()}
	want := []string{"z-old", "a-boundary", "NUL\x00ID", "", "z-old"}
	read, found, err := repo.BeginExpiredTeamPolicyRead(t.Context(), "", now, window)
	if err != nil || !found || !reflect.DeepEqual(read.IDs(), want) {
		t.Fatalf("exact resident selection: found=%v err=%v", found, err)
	}
	if used := repo.metadataResidency.work.used; used == 0 || used >= 3<<20 {
		t.Fatalf("unrelated policy entered candidate allowance: %d", used)
	}
	data := repo.workspaces["workspace"]
	data.Teams[1].ID = "source replacement"
	repo.workspaces["workspace"] = data
	if !reflect.DeepEqual(read.IDs(), want) {
		t.Fatal("source mutation changed owned candidate IDs")
	}
	read.IDs()[0] = "caller replacement"
	if repo.workspaces["workspace"].Teams[1].ID != "source replacement" {
		t.Fatal("caller mutated resident policy")
	}
	_, release, err := repo.admitMetadataWork(read.Context(), 2<<20)
	if err != nil {
		t.Fatal("sequential downstream work did not inherit live admission", err)
	}
	release()
	read.Close()
	read.Close()
	if repo.metadataResidency.work.used != 0 {
		t.Fatal("candidate work leaked after close")
	}
	for _, workspace := range []string{"missing", "workspace\x00"} {
		if read, found, err := repo.BeginExpiredTeamPolicyRead(t.Context(), workspace, now, window); read != nil || found || err != nil {
			t.Fatalf("exact missing workspace %q: %v %v", workspace, found, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if read, _, err := repo.BeginExpiredTeamPolicyRead(ctx, "workspace", now, window); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled policy selection did not fail before allocation", err)
	}
	repo.metadataResidency.work.used = repo.metadataResidency.work.limit
	short, stop := context.WithTimeout(t.Context(), time.Millisecond)
	defer stop()
	if read, _, err := repo.BeginExpiredTeamPolicyRead(short, "workspace", now, window); read != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("saturated admission did not honor cancellation", err)
	}
	repo.metadataResidency.work.used = 0
	if repo.metadataResidency.work.waiting != 0 {
		t.Fatal("cancelled policy waiter leaked")
	}
}

func TestScaleP21SW20ExpiredTeamPolicySelectedBudget(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	old := now.Add(-31 * 24 * time.Hour)
	huge := strings.Repeat("x", scaleP21SW20TeamPurgeMaxWork)
	repo := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"workspace": {Teams: []domain.Team{{ID: huge, ArchivedAt: &old}}}}, metadataResidency: newMetadataResidency()}
	if read, found, err := repo.BeginExpiredTeamPolicyRead(t.Context(), "workspace", now, 30*24*time.Hour); read != nil || !found || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("selected candidate IDs bypassed their work bound", found, err)
	}
	if repo.metadataResidency.work.used != 0 {
		t.Fatal("rejected candidate allocation retained admission")
	}
	repo.workspaces["workspace"] = domain.Bootstrap{Teams: []domain.Team{{ID: huge}}}
	read, found, err := repo.BeginExpiredTeamPolicyRead(t.Context(), "workspace", now, 30*24*time.Hour)
	if err != nil || !found || len(read.IDs()) != 0 {
		t.Fatal("unused huge ID entered selected candidate budget", found, err)
	}
	read.Close()
}

func TestScaleP21SW20ExpiredTeamPolicyParentLease(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	old := now.Add(-31 * 24 * time.Hour)
	repo := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"workspace": {Teams: []domain.Team{{ID: "expired", ArchivedAt: &old}}}}, metadataResidency: newMetadataResidency()}
	const parentBytes = 8 << 20
	parent, releaseParent, err := repo.admitMetadataWork(t.Context(), parentBytes)
	if err != nil {
		t.Fatal(err)
	}
	read, found, err := repo.BeginExpiredTeamPolicyRead(parent, "workspace", now, 30*24*time.Hour)
	if err != nil || !found {
		releaseParent()
		t.Fatal("nested snapshot admission", found, err)
	}
	owned := repo.metadataResidency.work.used - parentBytes
	if owned <= 0 || repo.metadataResidency.work.waiting != 0 {
		t.Fatal("owned handle reused parent allowance or queued while holding it")
	}
	releaseParent()
	if repo.metadataResidency.work.used != owned || !reflect.DeepEqual(read.IDs(), []string{"expired"}) {
		t.Fatal("parent release uncharged the still-live handle")
	}
	_, releaseNested, err := repo.admitMetadataWork(read.Context(), 2<<20)
	if err != nil {
		t.Fatal("independent handle context expired with its parent", err)
	}
	releaseNested()
	read.Close()
	if repo.metadataResidency.work.used != 0 {
		t.Fatal("owned allowance leaked after Close")
	}
	if stale, _, err := repo.BeginExpiredTeamPolicyRead(parent, "workspace", now, 30*24*time.Hour); stale != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("released parent lease was revived", err)
	}
	saturated, releaseSaturated, err := repo.admitMetadataWork(t.Context(), repo.metadataResidency.work.limit)
	if err != nil {
		t.Fatal(err)
	}
	blocked, _, err := repo.BeginExpiredTeamPolicyRead(saturated, "workspace", now, 30*24*time.Hour)
	if blocked != nil || !errors.Is(err, ErrMetadataWorkBudget) || repo.metadataResidency.work.waiting != 0 {
		t.Fatal("nested owned admission waited behind its own parent", err)
	}
	releaseSaturated()
	if repo.metadataResidency.work.used != 0 {
		t.Fatal("nested rejection leaked admission")
	}
}
