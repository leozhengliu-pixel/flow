package store

import (
	"errors"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestChatReplyJobRenewalThreadExclusionAndTakeover(t *testing.T) {
	repo, record := chatStoreFixture(t)
	ctx, now := t.Context(), time.Now()
	for _, id := range []string{"reply_a", "reply_b"} {
		if _, err := repo.EnqueueChatJob(ctx, domain.ChatJob{ID: id, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Kind: "dm_reply", DedupKey: id, Payload: []byte(`{}`), RunAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := repo.ClaimChatJobs(ctx, record.WorkspaceKey, "dm_reply", 2, now)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("claim: %+v %v", jobs, err)
	}
	a, b := jobs[0], jobs[1]
	if ok, err := repo.CheckpointChatReplyJob(ctx, &a, []byte(`{"started":true}`), "thread", now); err != nil || !ok {
		t.Fatalf("first thread claim: %v %v", ok, err)
	}
	if ok, err := repo.CheckpointChatReplyJob(ctx, &b, b.Payload, "thread", now); err != nil || ok {
		t.Fatalf("overlapping thread claim: %v %v", ok, err)
	}
	stale := a
	if ok, err := repo.CheckpointChatReplyJob(ctx, &a, a.Payload, "", now.Add(40*time.Second)); err != nil || !ok {
		t.Fatalf("heartbeat: %v %v", ok, err)
	}
	// B expired, but A's heartbeat keeps its reply owned beyond the original lease.
	due, err := repo.ClaimChatJobs(ctx, record.WorkspaceKey, "dm_reply", 2, now.Add(70*time.Second))
	if err != nil || len(due) != 1 || due[0].ID != b.ID {
		t.Fatalf("renewed job was stolen: %+v %v", due, err)
	}
	if ok, err := repo.CheckpointChatReplyJob(ctx, &stale, stale.Payload, "thread", now.Add(70*time.Second)); !errors.Is(err, ErrChatJobLeaseLost) || ok {
		t.Fatalf("stale checkpoint accepted: %v %v", ok, err)
	}
	recovered, err := repo.ClaimChatJobs(ctx, record.WorkspaceKey, "dm_reply", 2, now.Add(110*time.Second))
	if err != nil || len(recovered) != 1 || recovered[0].ID != a.ID {
		t.Fatalf("expired reply not recovered: %+v %v", recovered, err)
	}
	next := recovered[0]
	if ok, err := repo.CheckpointChatReplyJob(ctx, &next, next.Payload, "thread", now.Add(110*time.Second)); err != nil || !ok {
		t.Fatalf("successor thread claim: %v %v", ok, err)
	}
	if err := repo.ReleaseChatReplyLeases(ctx, a); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := repo.db.QueryRowContext(ctx, `SELECT job_id FROM chat_reply_leases WHERE thread_key='thread'`).Scan(&owner); err != nil || owner != a.ID {
		t.Fatalf("stale release cleared successor: %q %v", owner, err)
	}
	if err := repo.FinishChatJob(ctx, a, "completed", time.Time{}, ""); err == nil {
		t.Fatal("stale completion accepted")
	}
	if err := repo.FinishChatJob(ctx, next, "completed", time.Time{}, ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReleaseChatReplyLeases(ctx, next); err != nil {
		t.Fatal(err)
	}
}

func TestChatReplyJobRecoveryDoesNotExhaustDeliveryAttempts(t *testing.T) {
	repo, record := chatStoreFixture(t)
	ctx, now := t.Context(), time.Now()
	job := domain.ChatJob{ID: "reply", WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Kind: "dm_reply", DedupKey: "reply", Payload: []byte(`{"started":true}`), RunAt: now}
	if _, err := repo.EnqueueChatJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	for attempt := range 10 {
		jobs, err := repo.ClaimChatJobs(ctx, record.WorkspaceKey, job.Kind, 1, now.Add(time.Duration(attempt)*2*time.Minute))
		if err != nil || len(jobs) != 1 || jobs[0].Attempts != attempt+1 {
			t.Fatalf("attempt %d: %+v %v", attempt+1, jobs, err)
		}
	}
}
