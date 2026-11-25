package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW19SCIMGroupOrTeamDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		targetID := idgen.New("team_sw19_")
		stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		target := domain.Team{ID: targetID, Key: "SW19", Name: "SW19 linked team", Private: true, CreatedAt: &stamp, UpdatedAt: &stamp}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "workspaceSettings"), workspace, "team.updated", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, target)
			for range 75 {
				data.Teams = append(data.Teams, domain.Team{ID: idgen.New("team_sw19_other_"), Key: "OTHER", Name: "Retained", CreatedAt: &stamp})
			}
			data.WorkspaceSettings.SCIMRoleGroups = map[string]string{"admin": "SW19 linked team"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		existing, err := repo.CreateSCIMGroup(t.Context(), f.data.Workspace.ID, idgen.New("sw19-group-"), "SW19 existing group", "member")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := repo.DeleteSCIMGroup(context.Background(), f.data.Workspace.ID, existing.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
				t.Error(err)
			}
		})
		before := scaleP21SW19Hash(t, repo, workspace, "teams", "")
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, spec := range []struct {
				name, id string
				link     bool
				missing  bool
			}{
				{name: "team", id: targetID},
				{name: "link-team", id: targetID, link: true},
				{name: "existing-group", id: existing.ID},
				{name: "existing-group-link", id: existing.ID, link: true},
				{name: "missing", id: targetID + "-missing", missing: true},
				{name: "missing-link", id: targetID + "-missing", link: true, missing: true},
				{name: "case", id: strings.ToUpper(targetID), missing: true},
				{name: "long", id: strings.Repeat("x", 192), missing: true},
				{name: "nul", id: "missing\x00", missing: true},
				{name: "key-is-not-id", id: target.Key, missing: true},
			} {
				t.Run(spec.name, func(t *testing.T) {
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						count := 0
						source.SetWebhookSink(func(string, domain.DomainEvent) { count++ })
						source.SetRealtimeSink(func(string, domain.RealtimeEvent) { count++ })
						ctx := f.request(user, key, nil).Context()
						started := time.Now().UTC()
						group, team, err := (&server{store: source}).scimGroupOrTeam(ctx, workspace, f.data.Workspace.ID, spec.id, spec.link)
						if spec.missing && !errors.Is(err, sql.ErrNoRows) || !spec.missing && err != nil {
							t.Fatalf("SCIM group/team error=%v", err)
						}
						var clocks []time.Time
						var stored any
						if spec.id == targetID && !spec.link {
							if team == nil || !reflect.DeepEqual(*team, target) || group.ID != "" {
								t.Fatalf("target team fields changed: %+v", team)
							}
						}
						if spec.id == targetID && spec.link {
							if group.ID == "" || team != nil || group.ExternalID != targetID || group.DisplayName != target.Name || group.Role != "admin" ||
								group.CreatedAt.Before(started) || group.CreatedAt.After(time.Now().UTC()) {
								t.Fatalf("linked group lost role/name/identity or generated clock: %+v", group)
							}
							persisted, err := repo.SCIMGroup(t.Context(), f.data.Workspace.ID, group.ID)
							if err != nil || !reflect.DeepEqual(group, persisted) {
								t.Fatal("linked group was not stored identically", err)
							}
							if err := repo.DeleteSCIMGroup(t.Context(), f.data.Workspace.ID, group.ID); err != nil {
								t.Fatal(err)
							}
							clocks = []time.Time{group.CreatedAt, group.UpdatedAt}
							group.ID, persisted.ID = "<generated-group>", "<generated-group>"
							stored = persisted
						}
						if spec.id == existing.ID && (!reflect.DeepEqual(group, existing) || team != nil) {
							t.Fatal("existing SQL group lookup changed")
						}
						outcomes = append(outcomes, scaleP21SW19JSON(t, []any{errors.Is(err, sql.ErrNoRows), group, team, stored, count}, clocks...))
						if team != nil {
							team.Name = "owned return"
							if scaleP21SW19Hash(t, repo, workspace, "teams", "") != before {
								t.Fatal("returned team aliases canonical state")
							}
						}
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("SCIM group/team differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			if _, _, err := (&server{store: source}).scimGroupOrTeam(ctx, workspace, f.data.Workspace.ID, targetID, true); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled SCIM lookup: %v", err)
			}
		}
		if _, _, err := (&server{store: f.probe}).scimGroupOrTeam(t.Context(), workspace+"-missing", f.data.Workspace.ID, targetID, false); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("missing workspace: %v", err)
		}
		token, err := repo.CreateSCIMToken(t.Context(), f.data.Workspace.ID, "SW19 boundary")
		if err != nil {
			t.Fatal(err)
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			var responses []any
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r, w := f.request(user, key, nil), httptest.NewRecorder()
				r.Method = http.MethodGet
				r.SetPathValue("id", targetID)
				r.Header.Set("Authorization", "Bearer "+token.Secret)
				(&server{store: source}).scimGroup(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("SCIM-token boundary status=%d", w.Code)
				}
				responses = append(responses, []any{w.Code, w.Header().Get("ETag"), w.Body.String()})
			}
			if !reflect.DeepEqual(responses[0], responses[1]) {
				t.Fatal("SCIM team resource wire shape differs")
			}
		})
		queued := f.request(f.data.Viewer, nil, nil)
		queued.Method = http.MethodGet
		queued.SetPathValue("id", targetID)
		queued.Header.Set("Authorization", "Bearer "+token.Secret)
		if err := repo.RevokeSCIMToken(t.Context(), f.data.Workspace.ID, token.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replayed"} {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				w := httptest.NewRecorder()
				(&server{store: source}).scimGroup(w, queued)
				if w.Code != http.StatusUnauthorized {
					t.Fatalf("%s SCIM request reused revoked authority: %d", phase, w.Code)
				}
			}
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW19 SCIM foreign", idgen.New("sw19-scim-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		f.probe, err = repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			if _, _, err := (&server{store: source}).scimGroupOrTeam(t.Context(), foreign.Workspace.URLKey, foreign.Workspace.ID, targetID, true); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("team escaped workspace binding: %v", err)
			}
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams"), workspace, "team.updated", targetID, nil, func(data *domain.Bootstrap) error {
			for i := range data.Teams {
				if data.Teams[i].ID == targetID {
					data.Teams[i].Name = strings.Repeat("selected team ", 24000)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		if _, _, err := (&server{store: f.probe}).scimGroupOrTeam(t.Context(), workspace, f.data.Workspace.ID, targetID, false); !errors.Is(err, store.ErrMetadataPageBudget) {
			t.Fatalf("oversized selected team was silently omitted: %v", err)
		}
		// The primary SQL-group branch must not read any team metadata.
		if group, team, err := (&server{store: f.probe}).scimGroupOrTeam(t.Context(), workspace, f.data.Workspace.ID, existing.ID, true); err != nil || team != nil || group.ID != existing.ID {
			t.Fatalf("existing group lost precedence over metadata: %v", err)
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams"), workspace, "team.updated", targetID, nil, func(data *domain.Bootstrap) error {
			for i := range data.Teams {
				if data.Teams[i].ID == targetID {
					data.Teams[i] = target
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		if _, team, err := (&server{store: f.probe}).scimGroupOrTeam(t.Context(), workspace, f.data.Workspace.ID, targetID, false); err != nil || team == nil || team.ID != targetID {
			t.Fatalf("team read after budget failure did not recover: %v", err)
		}
		if scaleP21SW19Hash(t, repo, workspace, "teams", "") != before || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("SCIM lookup changed team rows or decoded full metadata")
		}
	}
}
