package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW19SCIMNativeDirectoryDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		first, second, mapped, unicode := idgen.New("team_sw19_first_"), idgen.New("team_sw19_second_"), idgen.New("team_sw19_mapped_"), idgen.New("team_sw19_unicode_")
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "workspaceSettings"), workspace, "team.updated", "", nil, func(data *domain.Bootstrap) error {
			now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			data.Teams = append(data.Teams,
				domain.Team{ID: first, Key: "SHARED", Name: "First duplicate", Private: true, CreatedAt: &now},
				domain.Team{ID: second, Key: "SHARED", Name: "Second duplicate", ArchivedAt: &now},
				domain.Team{ID: mapped, Key: "EXPLICIT", Name: "Explicit map", RetiredAt: &now, UpdatedAt: &now},
				domain.Team{ID: unicode, Key: "ſcope", Name: "Unicode match"},
			)
			for range 125 {
				data.Teams = append(data.Teams, domain.Team{ID: idgen.New("team_sw19_retained_"), Key: idgen.New("key"), Name: "Retained team"})
			}
			data.Teams[len(data.Teams)-1].Name = strings.Repeat("large legal directory name ", 9000)
			data.WorkspaceSettings.SCIMTeamGroupMapping = map[string]string{
				mapped: "  Custom-linked  ", "not-real-team": "fallback-to-key",
			}
			data.WorkspaceSettings.SCIMRoleGroups = map[string]string{"admin": "Custom-linked"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		f.probe = probe
		groups := []store.SCIMGroup{}
		for _, spec := range []struct{ external, name, role string }{
			{"MAPPED-SQL", "Custom-linked", "admin"},
			{"SHARED", "Duplicate-key", ""},
			{unicode, "Unicode group", ""},
			{"not-a-team", "Name is not a team key", ""},
		} {
			group, err := repo.CreateSCIMGroup(t.Context(), f.data.Workspace.ID, spec.external+"-"+idgen.New("sw19-"), spec.name, spec.role)
			if err != nil {
				t.Fatal(err)
			}
			if spec.name != "Name is not a team key" {
				group, err = repo.UpdateSCIMGroup(t.Context(), f.data.Workspace.ID, group.ID, spec.external, spec.name, spec.role)
				if err != nil {
					t.Fatal(err)
				}
			}
			groups = append(groups, group)
		}
		t.Cleanup(func() {
			for _, group := range groups {
				if err := repo.DeleteSCIMGroup(context.Background(), f.data.Workspace.ID, group.ID); err != nil {
					t.Error(err)
				}
			}
		})
		before := scaleP21SW19Hash(t, repo, workspace, "teams", "")
		direct := []store.SCIMGroup{
			{DisplayName: "CUSTOM-LINKED", ExternalID: "SHARED"},
			{ExternalID: "SHARED"}, {ExternalID: strings.ToUpper(second)}, {ExternalID: "Scope"},
			{DisplayName: "First duplicate"}, {ExternalID: " SHARED "}, {ExternalID: "EXPLICIT"},
			{DisplayName: "fallback-to-key", ExternalID: "SHARED"}, {ExternalID: "missing"},
		}
		expected := []string{mapped, first, second, unicode, "", "", mapped, first, ""}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for index, group := range direct {
				var result []string
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					result = append(result, (&server{store: source}).scimTeamForGroup(workspace, group))
				}
				if result[0] != expected[index] || result[0] != result[1] {
					t.Fatalf("SCIM map/external precedence=%v want=%q", result, expected[index])
				}
			}
			var outputs [][]domain.Team
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				outputs = append(outputs, (&server{store: source}).unlinkedSCIMTeams(workspace, groups))
			}
			if !reflect.DeepEqual(outputs[0], outputs[1]) || len(outputs[1]) < 100 {
				t.Fatal("unlinked team values, linked exclusion or canonical order changed")
			}
		})
		token, err := repo.CreateSCIMToken(t.Context(), f.data.Workspace.ID, "SW19 native directory")
		if err != nil {
			t.Fatal(err)
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, query := range []string{
				"attributes=id,displayName&count=20",
				"excludedAttributes=members&sortBy=displayName&sortOrder=descending&startIndex=3&count=7",
				"attributes=id,displayName,members&filter=" + url.QueryEscape(`displayName sw "Retained"`),
				"attributes=id,displayName,members&filter=" + url.QueryEscape(`members.value eq "`+f.principals[1].user.ID+`"`),
				"attributes=id&count=0",
			} {
				var outputs []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					r, w := f.request(user, key, nil), httptest.NewRecorder()
					r.Method = http.MethodGet
					r.URL.RawQuery = query
					r.Header.Set("Authorization", "Bearer "+token.Secret)
					(&server{store: source}).scimGroups(w, r)
					if w.Code != http.StatusOK {
						t.Fatalf("SCIM directory=%d %s", w.Code, w.Body.String())
					}
					outputs = append(outputs, []any{w.Code, scaleP21M3JSON(t, w.Body.Bytes())})
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatalf("SCIM HTTP directory differential: %s", scaleP21M2Diff(outputs[0], outputs[1]))
				}
			}
		})
		queued := f.request(f.data.Viewer, nil, nil)
		queued.Method = http.MethodGet
		queued.Header.Set("Authorization", "Bearer "+token.Secret)
		if err := repo.RevokeSCIMToken(t.Context(), f.data.Workspace.ID, token.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replayed"} {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				w := httptest.NewRecorder()
				(&server{store: source}).scimGroups(w, queued)
				if w.Code != http.StatusUnauthorized {
					t.Fatalf("%s directory reused revoked token: %d", phase, w.Code)
				}
			}
		}
		filter, err := parseSCIMFilter("")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := (&server{store: f.probe}).scimTeamForGroupContext(ctx, workspace, direct[0]); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled mapping context=%v", err)
		}
		cancelled := f.request(f.data.Viewer, nil, nil).WithContext(ctx)
		if err := (&server{store: f.probe}).syncSCIMGroupTeamIfLinked(cancelled, workspace, f.data.Workspace.ID, groups[0]); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled linked-team synchronization=%v", err)
		}
		if _, err := (&server{store: f.probe}).listSCIMGroupResources(ctx, workspace, f.data.Workspace.ID, "https://flow.example.test/scim", filter, "", scimProjection{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled directory=%v", err)
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW19 native other", idgen.New("sw19-other-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		freshToken, err := repo.CreateSCIMToken(t.Context(), f.data.Workspace.ID, "SW19 workspace binding")
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			if got := (&server{store: source}).scimTeamForGroup(foreign.Workspace.URLKey, store.SCIMGroup{ExternalID: first}); got != "" {
				t.Fatal("team escaped workspace binding")
			}
			if got := (&server{store: source}).unlinkedSCIMTeams(workspace+"-missing", groups); got != nil {
				t.Fatal("missing workspace lost nil shape")
			}
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.Method = http.MethodGet
			r.SetPathValue("workspace", foreign.Workspace.URLKey)
			r.Header.Set("Authorization", "Bearer "+freshToken.Secret)
			(&server{store: source}).scimGroups(w, r)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "SCIM token is not valid for this workspace") {
				t.Fatalf("SCIM directory bearer escaped workspace binding: %d %s", w.Code, w.Body.String())
			}
		}
		// Team values are caller-owned and the native return never exposes
		// future canonical JSON fields. SQL records and unrelated bodies stay put.
		got := (&server{store: f.probe}).unlinkedSCIMTeams(workspace, nil)
		original := got[0].Name
		got[0].Name = "caller-owned"
		again := (&server{store: f.probe}).unlinkedSCIMTeams(workspace, nil)
		if again[0].Name != original || scaleP21SW19Hash(t, repo, workspace, "teams", "") != before || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("native directory changed canonical state, aliases results or decoded full metadata")
		}
		raw, _ := json.Marshal(again)
		if strings.Contains(string(raw), "m2-unrelated") || slices.ContainsFunc(again, func(team domain.Team) bool { return team.ID == "" }) {
			t.Fatal("native output leaked unrelated metadata")
		}
	}
}
