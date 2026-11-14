package main

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW20PurgeExpiredTeamInputs(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		now := time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC)
		old, boundary, future := now.Add(-teamRestoreWindow-time.Nanosecond), now.Add(-teamRestoreWindow), now.Add(-teamRestoreWindow+time.Nanosecond)
		expired := []string{idgen.New("sw20-z-old-"), idgen.New("sw20-a-boundary-")}
		futureID := idgen.New("sw20-future-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw20.purge_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams,
				domain.Team{ID: expired[0], ArchivedAt: &old, Name: strings.Repeat("unused name ", 10000)},
				domain.Team{ID: futureID, ArchivedAt: &future},
				domain.Team{ID: expired[1], ArchivedAt: &boundary})
			data.TeamSettings[expired[0]] = domain.TeamSettings{AgentGuidance: strings.Repeat("unused guidance ", 10000)}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, failure := range []string{"none", "missing-first", "stop-first"} {
				sentinel := errors.New("sw20-stop")
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, probe} {
					r := f.request(user, key, nil)
					var ids []string
					if source == repo {
						data, _ := repo.WorkspaceMetadataFields(workspace, "teams")
						for _, team := range data.Teams {
							if team.ArchivedAt != nil && !now.Before(team.ArchivedAt.Add(teamRestoreWindow)) {
								ids = append(ids, team.ID)
							}
						}
					} else {
						read, found, err := source.BeginExpiredTeamPolicyRead(r.Context(), workspace, now, teamRestoreWindow)
						if err != nil || !found {
							t.Fatal("bounded candidate snapshot", found, err)
						}
						defer read.Close()
						ids = read.IDs()
					}
					called := []string{}
					err := purgeExpiredTeamIDs(ids, func(id string) error {
						called = append(called, id)
						if id == expired[0] {
							switch failure {
							case "missing-first":
								return errNotFound
							case "stop-first":
								return sentinel
							}
						}
						return nil
					})
					outcomes = append(outcomes, []any{called, errors.Is(err, sentinel)})
					want := expired
					if failure == "stop-first" {
						want = expired[:1]
					}
					if !reflect.DeepEqual(called, want) {
						t.Fatalf("candidate order/boundary/stop changed: %v / %v", called, want)
					}
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("owned purge selection differed: %v", outcomes)
				}
			}
		})
		var events []domain.DomainEvent
		repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { events = append(events, event) })
		if err := (&server{store: repo}).purgeExpiredTeams(t.Context(), workspace, now); err != nil {
			t.Fatal("unchanged off-mode positive purge", err)
		}
		data, _ := repo.WorkspaceMetadata(workspace)
		for _, id := range expired {
			if slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == id }) {
				t.Fatal("off-mode purge retained expired team", id)
			}
		}
		if !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == futureID }) {
			t.Fatal("off-mode purge removed future team")
		}
		purged := []string{}
		for _, event := range events {
			if event.Type == "team.deleted" {
				purged = append(purged, event.AggregateID)
			}
		}
		if !reflect.DeepEqual(purged, expired) {
			t.Fatalf("off-mode positive purge event order: %v / %v", purged, expired)
		}
		empty, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		service := &server{store: empty}
		if err := service.purgeExpiredTeams(t.Context(), workspace, now); err != nil {
			t.Fatal("bounded empty candidate path", err)
		}
		if err := service.purgeExpiredTeams(t.Context(), workspace+"\x00", now); err != nil {
			t.Fatal("bounded exact missing workspace path", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := service.purgeExpiredTeams(ctx, workspace, now); !errors.Is(err, context.Canceled) {
			t.Fatal("bounded cancellation lost", err)
		}
		if probe.WorkspaceMetadataDecodes() != 0 || empty.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("purge candidate selection decoded full metadata")
		}
	}
}
