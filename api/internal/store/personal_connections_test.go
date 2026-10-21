package store

import (
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestPersonalConnectionsLifecycle(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)

	saved, err := repository.UpsertPersonalConnection(ctx, domain.PersonalConnection{ID: "pcn_a", UserID: "u1", Provider: domain.PersonalConnectionGoogleCalendar, Account: "a@example.com", Scopes: []string{"openid", "email"}})
	if err != nil || saved.Status != domain.PersonalConnectionActive {
		t.Fatalf("upsert %+v %v", saved, err)
	}
	if _, err := repository.UpsertPersonalConnection(ctx, domain.PersonalConnection{ID: "x", UserID: "u1", Provider: "slack"}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	// One connection per member and provider: a second upsert keeps the id and clears the window.
	until := now.Add(time.Hour)
	if _, err := repository.RecordPersonalConnectionSyncSuccess(ctx, saved, 0, &now, &until); err != nil {
		t.Fatal(err)
	}
	again, err := repository.UpsertPersonalConnection(ctx, domain.PersonalConnection{ID: "pcn_b", UserID: "u1", Provider: domain.PersonalConnectionGoogleCalendar, Account: "b@example.com"})
	if err != nil || again.ID != "pcn_a" {
		t.Fatalf("second upsert %+v %v", again, err)
	}
	if current, _ := repository.PersonalConnectionFor(ctx, "u1", domain.PersonalConnectionGoogleCalendar); current.Account != "b@example.com" || current.AwayUntil != nil {
		t.Fatalf("after reconnect %+v", current)
	}

	// A window is stored once; the same window is not written again.
	current, _ := repository.PersonalConnectionFor(ctx, "u1", domain.PersonalConnectionGoogleCalendar)
	if wrote, err := repository.RecordPersonalConnectionSyncSuccess(ctx, current, 0, &now, &until); err != nil || !wrote {
		t.Fatalf("first window wrote=%v %v", wrote, err)
	}
	current, _ = repository.PersonalConnectionFor(ctx, "u1", domain.PersonalConnectionGoogleCalendar)
	if wrote, err := repository.RecordPersonalConnectionSyncSuccess(ctx, current, 0, &now, &until); err != nil || wrote {
		t.Fatalf("unchanged window wrote=%v %v", wrote, err)
	}
	windows, err := repository.ActiveAwayWindows(ctx, []string{"u1", "u2"}, now)
	if err != nil || len(windows) != 1 || windows["u1"].AwayUntil == nil {
		t.Fatalf("windows %+v %v", windows, err)
	}
	if windows, _ = repository.ActiveAwayWindows(ctx, []string{"u1"}, until.Add(time.Minute)); len(windows) != 0 {
		t.Fatalf("ended window listed: %+v", windows)
	}

	// The sync claim is a compare-and-swap: one winner per interval.
	due, err := repository.DuePersonalConnections(ctx, domain.PersonalConnectionGoogleCalendar, now, 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("due %+v %v", due, err)
	}
	if won, _ := repository.ClaimPersonalConnectionSync(ctx, "pcn_a", now, now.Add(15*time.Minute)); !won {
		t.Fatal("first claim lost")
	}
	if won, _ := repository.ClaimPersonalConnectionSync(ctx, "pcn_a", now, now.Add(15*time.Minute)); won {
		t.Fatal("second claim in the same interval won")
	}
	if due, _ = repository.DuePersonalConnections(ctx, domain.PersonalConnectionGoogleCalendar, now.Add(time.Minute), 10); len(due) != 0 {
		t.Fatalf("claimed connection still due: %+v", due)
	}
	if err := repository.RecordPersonalConnectionSyncFailure(ctx, "pcn_a", 2, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if failures, _ := repository.PersonalConnectionSyncFailures(ctx, "pcn_a"); failures != 2 {
		t.Fatalf("failures %d", failures)
	}

	// A connection that needs a reconnect is neither due nor shown as away.
	if err := repository.SetPersonalConnectionStatus(ctx, "pcn_a", domain.PersonalConnectionReconnect, "revoked", nil); err != nil {
		t.Fatal(err)
	}
	if due, _ = repository.DuePersonalConnections(ctx, domain.PersonalConnectionGoogleCalendar, now.Add(24*time.Hour), 10); len(due) != 0 {
		t.Fatalf("reconnect connection due: %+v", due)
	}
	if windows, _ = repository.ActiveAwayWindows(ctx, []string{"u1"}, now); len(windows) != 0 {
		t.Fatalf("reconnect connection away: %+v", windows)
	}

	// Another member cannot delete it; the owner can.
	if removed, _ := repository.DeletePersonalConnection(ctx, "u2", "pcn_a"); removed {
		t.Fatal("deleted another member's connection")
	}
	if removed, err := repository.DeletePersonalConnection(ctx, "u1", "pcn_a"); err != nil || !removed {
		t.Fatalf("delete removed=%v %v", removed, err)
	}
	if _, err := repository.PersonalConnectionFor(ctx, "u1", domain.PersonalConnectionGoogleCalendar); err != ErrPersonalConnectionNotFound {
		t.Fatalf("after delete: %v", err)
	}
}
