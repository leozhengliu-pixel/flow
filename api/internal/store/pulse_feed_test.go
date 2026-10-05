package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestPulseFeedIndexOrdersRecentAndPopularAndRebuildsAfterWrites(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	member := domain.User{ID: "usr_member", Name: "member", DisplayName: "Test member"}
	ctx := WithMetadataFields(context.Background(), "projectUpdates", "initiativeUpdates")
	err = repo.MutateWorkspace(ctx, "test-workspace", "project.update_updated", "project_aut", nil, func(data *domain.Bootstrap) error {
		data.ProjectUpdates["project_aut"] = []domain.ProjectUpdate{
			// Newest, no engagement.
			{ID: "u_new", ProjectID: "project_aut", Body: "Shipped @TestMember's change", CreatedAt: base.Add(48 * time.Hour), User: member},
			// A day older with enough discussion to outrank it: ln(20) ≈ 3 days.
			{ID: "u_hot", ProjectID: "project_aut", Body: "Hot", CreatedAt: base.Add(24 * time.Hour), User: member, Reactions: map[string][]string{"👍": {"a", "b", "c", "d", "e", "f", "g", "h", "i"}}, Comments: make([]domain.Comment, 10)},
			{ID: "u_old", ProjectID: "project_aut", Body: "Old", CreatedAt: base, User: member},
		}
		data.InitiativeUpdates["initiative_x"] = []domain.InitiativeUpdate{{ID: "i_mid", InitiativeID: "initiative_x", Body: "Mid", CreatedAt: base.Add(36 * time.Hour), User: member}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	feed, ok := repo.PulseFeed("test-workspace")
	if !ok {
		t.Fatal("no feed")
	}
	ids := func(entries []*PulseFeedEntry) []string {
		result := []string{}
		for _, entry := range entries {
			result = append(result, entry.UpdateID)
		}
		return result
	}
	if got := ids(feed.Recent); !slices.Equal(got, []string{"u_new", "i_mid", "u_hot", "u_old"}) {
		t.Fatalf("recent order = %v", got)
	}
	if got := ids(feed.Popular); !slices.Equal(got, []string{"u_hot", "u_new", "i_mid", "u_old"}) {
		t.Fatalf("popular order = %v", got)
	}
	hot := feed.Popular[0]
	if want := PulsePopularScore(10, 9, hot.CreatedAt); hot.Score != want || hot.Comments != 10 || hot.Reactions != 9 {
		t.Fatalf("hot entry = %#v, want score %v", hot, want)
	}
	if !slices.Contains(feed.Recent[0].Mentions, "usr_member") {
		t.Fatalf("mention by @name not indexed: %#v", feed.Recent[0].Mentions)
	}
	// The cached index is reused until the update collection is replaced.
	again, _ := repo.PulseFeed("test-workspace")
	if &again.Recent[0] != &feed.Recent[0] {
		t.Fatal("index rebuilt without a write")
	}
	err = repo.MutateWorkspace(ctx, "test-workspace", "project.update_updated", "project_aut", nil, func(data *domain.Bootstrap) error {
		updates := data.ProjectUpdates["project_aut"]
		updates[2].Comments = make([]domain.Comment, 400)
		data.ProjectUpdates["project_aut"] = updates
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := repo.PulseFeed("test-workspace")
	if after.Popular[0].UpdateID != "u_old" {
		t.Fatalf("popular order after write = %v", ids(after.Popular))
	}
}

func TestPulsePopularScoreMatchesLinearFormula(t *testing.T) {
	at := time.Date(2024, 9, 8, 0, 0, 0, 0, time.UTC)
	if got := PulsePopularScore(0, 0, at); got != 2 {
		t.Fatalf("score with no engagement two days after epoch = %v, want 2", got)
	}
	if PulsePopularScore(3, 4, at) <= PulsePopularScore(0, 0, at) {
		t.Fatal("engagement must raise the score")
	}
}

func TestNormalizeMapsLegacyEmptyPulseScheduleToDefault(t *testing.T) {
	data := EmptyWorkspace("Legacy", "legacy", "us", domain.User{ID: "usr_legacy"})
	data.UserSettings = map[string]domain.UserSettings{"usr_legacy": {UserID: "usr_legacy"}, "usr_never": {UserID: "usr_never", PulseSchedule: "never"}}
	normalize(&data)
	if got := data.UserSettings["usr_legacy"].PulseSchedule; got != "default" {
		t.Fatalf("legacy empty schedule = %q, want default (follow the workspace)", got)
	}
	if got := data.UserSettings["usr_never"].PulseSchedule; got != "never" {
		t.Fatalf("explicit never = %q", got)
	}
	if got := defaultUserSettings("usr_new").PulseSchedule; got != "default" {
		t.Fatalf("new member schedule = %q", got)
	}
}

func TestVisibleTeamIDsForHidesPrivateTeamsFromNonMembers(t *testing.T) {
	teams := []domain.Team{{ID: "public"}, {ID: "private", Private: true}}
	member := VisibleTeamIDsFor(teams, map[string]domain.TeamSettings{}, []domain.TeamMember{{TeamID: "public", UserID: "u"}}, "u", "member")
	if !slices.Equal(member, []string{"public"}) {
		t.Fatalf("member teams = %v", member)
	}
	admin := VisibleTeamIDsFor(teams, map[string]domain.TeamSettings{}, nil, "a", "admin")
	if len(admin) != 2 {
		t.Fatalf("admin teams = %v", admin)
	}
}
