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

func TestScaleP21SW214InstalledMoveInputs(t *testing.T) {
	at := time.Date(2026, 11, 16, 3, 4, 5, 6, time.UTC)
	last := at.Add(time.Hour)
	members := []domain.WorkspaceMember{
		{User: domain.User{ID: "irrelevant", Name: strings.Repeat("large profile body ", 150000)}, Role: "member", Status: "active", JoinedAt: at},
		{User: domain.User{ID: "selected\x00id", Name: "Unused display snapshot"}, Role: "member\x00role", Status: "suspended\x00", JoinedAt: at, LastSeenAt: &last, ManagedBy: "native\x00manager"},
		{User: domain.User{ID: "selected\x00id"}, Role: "admin", Status: "active", JoinedAt: last},
		{User: domain.User{ID: ""}, Role: "guest", Status: "", JoinedAt: at},
	}
	teams := []domain.TeamMember{
		{UserID: "irrelevant", TeamID: "private-unrelated", Role: "member", JoinedAt: at},
		{UserID: "selected\x00id", TeamID: "native\x00team", Role: "lead\x00", JoinedAt: at, Managed: true, ManagedSource: "native\x00source", Origin: "import"},
		{UserID: "selected\x00id", TeamID: "native\x00team", Role: "member", JoinedAt: last},
		{UserID: "", TeamID: "empty-user", JoinedAt: at},
	}
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "identity"}, NextIssueNumber: 9147, Members: members, TeamMembers: teams}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	for _, test := range []struct {
		name  string
		users []string
		want  []int
		team  []int
	}{
		{"duplicates-and-native-values", []string{"selected\x00id"}, []int{1, 2}, []int{1, 2}},
		{"empty-is-an-exact-id", []string{""}, []int{3}, []int{3}},
		{"nonempty-directory-witness", []string{"not-a-member"}, []int{0}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, err := view.retainMetadataLogicalSource(t.Context(), "physical")
			if err != nil {
				t.Fatal(err)
			}
			read := &issueHotInputs{store: view, source: source}
			defer read.close()
			queries := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
			number, err := read.issueMoveSnapshotNumber(ctx)
			if err != nil || number != data.NextIssueNumber {
				t.Fatal("unlocked move replaced its installed issue number", number, err)
			}
			got, gotTeams, err := read.issueMoveSnapshotAudience(ctx, test.users)
			if err != nil {
				t.Fatal(err)
			}
			want := []domain.WorkspaceMember{}
			for _, index := range test.want {
				member := members[index]
				member.User = domain.User{ID: member.User.ID}
				want = append(want, member)
			}
			wantTeams := []domain.TeamMember{}
			for _, index := range test.team {
				wantTeams = append(wantTeams, teams[index])
			}
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotTeams, wantTeams) {
				t.Fatal("installed member snapshots lost duplicates, native values or consumed fields", got, gotTeams)
			}
			if queries != 0 || view.MetadataWorkUsedForTesting() == 0 {
				t.Fatal("snapshot move loaded SQL or released its owned inputs", queries)
			}
			read.close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("snapshot move retained admission")
			}
		})
	}
	for _, mode := range []string{"quota", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			source, err := view.retainMetadataLogicalSource(t.Context(), "physical")
			if err != nil {
				t.Fatal(err)
			}
			read := &issueHotInputs{store: view, source: source}
			defer read.close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			limit := view.metadataResidency.work.limit
			if mode == "quota" {
				view.metadataResidency.work.limit = 0
			} else {
				cancel()
			}
			_, _, err = read.issueMoveSnapshotAudience(ctx, []string{"selected\x00id"})
			view.metadataResidency.work.limit = limit
			expected := ErrMetadataWorkBudget
			if mode == "cancel" {
				expected = context.Canceled
			}
			if !errors.Is(err, expected) {
				t.Fatal("snapshot audience admission changed", err)
			}
			read.close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("failed snapshot audience retained admission")
			}
		})
	}
}
