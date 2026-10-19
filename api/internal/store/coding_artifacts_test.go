package store

import (
	"errors"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestCodingArtifactsStore(t *testing.T) {
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "artifacts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	ctx := t.Context()
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	first := domain.CodingArtifact{ID: "a1", SessionID: "s1", Kind: "screenshot", Label: "before", Group: "settings", TurnID: "t1", ContentType: "image/png", SizeBytes: 10, CreatedAt: at.Add(time.Minute), URL: "https://transient", ContentURL: "/x"}
	second := domain.CodingArtifact{ID: "a2", SessionID: "s1", Kind: "video", Label: "flow", ContentType: "video/webm", SizeBytes: 20, CreatedAt: at}
	other := domain.CodingArtifact{ID: "a3", SessionID: "s2", Kind: "screenshot", Label: "x", ContentType: "image/png", CreatedAt: at}
	for _, test := range []struct {
		workspace string
		artifact  domain.CodingArtifact
		key       string
	}{{"w", first, "coding-artifact_s1_a1.png"}, {"w", second, "coding-artifact_s1_a2.webm"}, {"w", other, "coding-artifact_s2_a3.png"}} {
		if err := repo.AddCodingArtifact(ctx, test.workspace, test.artifact, test.key); err != nil {
			t.Fatal(err)
		}
	}
	// Idempotent on the id: a retried insert succeeds and keeps the first row.
	retry := first
	retry.Label = "changed"
	if err := repo.AddCodingArtifact(ctx, "w", retry, "coding-artifact_s1_a1.png"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if err := repo.AddCodingArtifact(ctx, "w", domain.CodingArtifact{ID: "", SessionID: "s1"}, "k"); err == nil {
		t.Fatal("missing id must fail")
	}
	// The same id in another workspace is a different, conflicting row.
	if err := repo.AddCodingArtifact(ctx, "other", first, "k"); err == nil {
		t.Fatal("an id owned by another workspace must not be reported as stored")
	}

	list, err := repo.CodingArtifacts(ctx, "w", "s1")
	if err != nil || len(list) != 2 || list[0].ID != "a2" || list[1].ID != "a1" {
		t.Fatalf("list oldest first: %+v %v", list, err)
	}
	if list[1].Label != "before" || list[1].Group != "settings" || list[1].TurnID != "t1" || list[1].URL != "" || list[1].ContentURL != "" {
		t.Fatalf("stored fields (URLs are derived, never stored): %+v", list[1])
	}
	if empty, err := repo.CodingArtifacts(ctx, "other", "s1"); err != nil || len(empty) != 0 {
		t.Fatalf("workspace isolation: %+v %v", empty, err)
	}

	artifact, key, err := repo.CodingArtifact(ctx, "w", "s1", "a1")
	if err != nil || artifact.ID != "a1" || key != "coding-artifact_s1_a1.png" {
		t.Fatalf("get: %+v %q %v", artifact, key, err)
	}
	for _, missing := range [][3]string{{"w", "s2", "a1"}, {"other", "s1", "a1"}, {"w", "s1", "nope"}} {
		if _, _, err := repo.CodingArtifact(ctx, missing[0], missing[1], missing[2]); !errors.Is(err, ErrCodingArtifactNotFound) {
			t.Fatalf("get %v: %v", missing, err)
		}
	}

	if err := repo.SetCodingArtifactActivity(ctx, "w", "s1", "a1", "act-9"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetCodingArtifactActivity(ctx, "w", "s1", "a1", "act-9"); err != nil {
		t.Fatalf("idempotent link: %v", err)
	}
	if artifact, _, _ := repo.CodingArtifact(ctx, "w", "s1", "a1"); artifact.ActivityID != "act-9" {
		t.Fatalf("activity link: %+v", artifact)
	}
	if err := repo.SetCodingArtifactActivity(ctx, "w", "s1", "nope", "x"); !errors.Is(err, ErrCodingArtifactNotFound) {
		t.Fatalf("link missing: %v", err)
	}

	keys, err := repo.CodingArtifactObjectKeys(ctx, "w", "s1")
	if err != nil || len(keys) != 2 || keys["a2"] != "coding-artifact_s1_a2.webm" {
		t.Fatalf("keys: %v %v", keys, err)
	}
	deleted, err := repo.DeleteCodingArtifacts(ctx, "w", "s1")
	sort.Strings(deleted)
	if err != nil || len(deleted) != 2 || deleted[0] != "coding-artifact_s1_a1.png" {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	if list, _ := repo.CodingArtifacts(ctx, "w", "s1"); len(list) != 0 {
		t.Fatalf("after delete: %+v", list)
	}
	if list, _ := repo.CodingArtifacts(ctx, "w", "s2"); len(list) != 1 {
		t.Fatalf("other sessions are untouched: %+v", list)
	}
}

func TestCodingArtifactsOrderWithinOneMillisecond(t *testing.T) {
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "artifacts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	at := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	for _, artifact := range []domain.CodingArtifact{
		{ID: "b-tied", SessionID: "s1", CreatedAt: at.Add(900 * time.Microsecond)},
		{ID: "a-later", SessionID: "s1", CreatedAt: at.Add(900 * time.Microsecond)},
		{ID: "z-earlier", SessionID: "s1", CreatedAt: at.Add(100 * time.Microsecond)},
		{ID: "m-previous-millisecond", SessionID: "s1", CreatedAt: at.Add(-time.Millisecond)},
	} {
		if err := repo.AddCodingArtifact(t.Context(), "w", artifact, "object_"+artifact.ID); err != nil {
			t.Fatal(err)
		}
	}
	artifacts, err := repo.CodingArtifacts(t.Context(), "w", "s1")
	if err != nil || len(artifacts) != 4 {
		t.Fatalf("artifacts: %+v %v", artifacts, err)
	}
	for index, want := range []string{"m-previous-millisecond", "z-earlier", "a-later", "b-tied"} {
		if artifacts[index].ID != want {
			t.Fatalf("artifact %d = %s, want %s (oldest first, ID breaks exact ties)", index, artifacts[index].ID, want)
		}
	}
}

func TestOrphanCodingArtifactSessions(t *testing.T) {
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "artifacts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	ctx := t.Context()
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO coding_sessions(id,workspace_key,agent_session_id,creator_id,source,state,phase,phase_started_at,data,created_at,updated_at) VALUES('live','w','task1','u','issue','active','running',1,'{}',1,1)`); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []domain.CodingArtifact{{ID: "a1", SessionID: "live", Kind: "screenshot"}, {ID: "a2", SessionID: "gone", Kind: "screenshot"}} {
		if err := repo.AddCodingArtifact(ctx, "w", artifact, "coding-artifact_"+artifact.SessionID+"_"+artifact.ID+".png"); err != nil {
			t.Fatal(err)
		}
	}
	owners, err := repo.OrphanCodingArtifactSessions(ctx, 10)
	if err != nil || len(owners) != 1 || owners[0] != (CodingArtifactOwner{WorkspaceKey: "w", SessionID: "gone"}) {
		t.Fatalf("orphans = %+v %v", owners, err)
	}
}
