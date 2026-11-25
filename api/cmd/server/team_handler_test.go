package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

func TestScaleP21SW22TeamHandlerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		prefix := idgen.New("team_sw22_")
		at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		privateID := ""
		policy, ok := repo.IssueAccessMetadata(t.Context(), workspace)
		if !ok {
			t.Fatal("missing policy fixture")
		}
		for _, team := range policy.Teams {
			if team.Private {
				privateID = team.ID
				break
			}
		}
		if privateID == "" {
			t.Fatal("fixture lacks a private team")
		}
		teams := []domain.Team{
			{ID: prefix + "-key-before-id", Key: prefix + "-exact", Name: "Earlier key collision", Color: "red"},
			{ID: prefix + "-exact", Key: "EX22", Name: "Exact identity wins", Color: "green", Icon: "people", ExternalSource: "retained-source", CreatedAt: &at, UpdatedAt: &at},
			{ID: prefix + "-first-duplicate", Key: "DUP22", Name: "First duplicate key"},
			{ID: prefix + "-second-duplicate", Key: "dup22", Name: "Second duplicate key"},
			{ID: prefix + "-visible-key", Key: privateID, Name: "Visible alias of hidden ID"},
			{ID: prefix + "-kelvin", Key: "\u212a", Name: "Kelvin key"},
			{ID: prefix + "-long-s", Key: "\u017f", Name: "Long s key"},
			{ID: prefix + "-retired", Key: "RET22", Name: "Retired directory", RetiredAt: &at},
			{ID: prefix + "-archived", Key: "ARC22", Name: "Archived directory", ArchivedAt: &at},
			{ID: prefix + "-external", Key: "HR22", Name: "Directory leaf", ExternalSource: "hr:org:node:sw22"},
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "teamSettings"), workspace, "scale_p21_sw22.team_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, teams...)
			for _, team := range teams {
				data.TeamSettings[team.ID] = domain.TeamSettings{TeamID: team.ID}
			}
			for index := range 65 {
				team := domain.Team{ID: fmt.Sprintf("%s-retained-%d", prefix, index), Key: fmt.Sprintf("R%04d", index), Name: "Retained"}
				data.Teams = append(data.Teams, team)
				data.TeamSettings[team.ID] = domain.TeamSettings{TeamID: team.ID}
			}
			data.TeamSettings[data.Teams[len(data.Teams)-1].ID] = domain.TeamSettings{AgentGuidance: strings.Repeat("unrelated team guidance ", 8000)}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, teams[7].ID, f.principals[2].user.ID, "member", true); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, teams[9].ID, f.principals[2].user.ID, "member", true); err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "teams")
		refs := []string{
			privateID, f.public, prefix + "-exact", prefix + "-key-before-id", "ex22", "DUP22",
			"k", "S", prefix + "-retired", prefix + "-archived", prefix + "-external",
			prefix + "-missing", " Exact identity wins ", "", strings.Repeat("x", 192), prefix + "-exact\x00",
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, ref := range refs {
				var results []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					r, w := f.request(user, key, nil), httptest.NewRecorder()
					r.Method = http.MethodGet
					r.SetPathValue("id", ref)
					scaleP21M3Call(&server{store: source}, w, r, (*server).getTeamRecord)
					results = append(results, []any{w.Code, scaleP21M3JSON(t, w.Body.Bytes())})
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatalf("team reference %q differs: %s", ref, scaleP21M2Diff(results[0], results[1]))
				}
			}
		})
		t.Run("auth-disabled-private-archived-and-credentials", func(t *testing.T) {
			for _, ref := range append(refs, "") {
				var results []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					r, w := f.request(f.data.Viewer, &domain.APIKey{TeamRestriction: "selected", TeamIDs: []string{}}, nil), httptest.NewRecorder()
					r.SetPathValue("id", ref)
					(&server{store: source, authDisabled: true}).getTeamRecord(w, r)
					results = append(results, []any{w.Code, scaleP21M3JSON(t, w.Body.Bytes())})
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatalf("auth-disabled reference %q differs: %s", ref, scaleP21M2Diff(results[0], results[1]))
				}
			}
		})
		t.Run("legacy-selected-key-inference", func(t *testing.T) {
			key := &domain.APIKey{ID: "sw22-key-legacy", Scopes: []string{"read"}, TeamIDs: []string{f.public}}
			for _, ref := range []string{f.public, privateID, teams[1].ID} {
				var results []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					r, w := f.request(f.data.Viewer, key, nil), httptest.NewRecorder()
					r.SetPathValue("id", ref)
					(&server{store: source}).getTeamRecord(w, r)
					results = append(results, []any{w.Code, scaleP21M3JSON(t, w.Body.Bytes())})
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatal("legacy selected-key inference changed", scaleP21M2Diff(results[0], results[1]))
				}
			}
		})
		t.Run("hidden-large-team-does-not-disclose-budget", func(t *testing.T) {
			setIcon := func(icon string) {
				t.Helper()
				if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams"), workspace, "team.updated", privateID, nil, func(data *domain.Bootstrap) error {
					for index := range data.Teams {
						if data.Teams[index].ID == privateID {
							data.Teams[index].Icon = icon
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			setIcon(strings.Repeat("large private team icon ", 8000))
			defer setIcon("")
			for _, spec := range []struct {
				user domain.User
				key  *domain.APIKey
			}{
				{f.principals[2].user, nil},
				{f.data.Viewer, &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}},
				{f.data.Viewer, &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
			} {
				var results []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					r, w := f.request(spec.user, spec.key, nil), httptest.NewRecorder()
					r.SetPathValue("id", privateID)
					(&server{store: source}).getTeamRecord(w, r)
					if w.Code == http.StatusServiceUnavailable {
						t.Fatal("hidden team body disclosed its budget")
					}
					results = append(results, []any{w.Code, scaleP21M3JSON(t, w.Body.Bytes())})
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatal("hidden exact-ID/visible-key precedence changed", scaleP21M2Diff(results[0], results[1]))
				}
			}
		})
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW22 foreign team", idgen.New("sw22-team-workspace-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.Header.Set("X-Workspace-Key", foreign.Workspace.URLKey)
			r.SetPathValue("workspaceKey", foreign.Workspace.URLKey)
			r.SetPathValue("id", teams[1].ID)
			(&server{store: source}).getTeamRecord(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatal("team escaped its workspace", w.Code, w.Body.String())
			}
			for _, disabled := range []bool{false, true} {
				r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
				r.Header.Set("X-Workspace-Key", workspace+"-missing")
				r.SetPathValue("workspaceKey", workspace+"-missing")
				r.SetPathValue("id", teams[1].ID)
				(&server{store: source, authDisabled: disabled}).getTeamRecord(w, r)
				want := http.StatusForbidden
				if disabled {
					want = http.StatusNotFound
				}
				if w.Code != want {
					t.Fatal("missing workspace error precedence changed", disabled, w.Code, w.Body.String())
				}
			}
		}
		member := f.principals[2].user
		queued := f.request(member, nil, nil)
		queued.SetPathValue("id", f.public)
		retained, err := f.probe.ResolveAccess(queued.Context(), workspace, member.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err := f.probe.TeamRecordForAccess(t.Context(), retained, f.public); !errors.Is(err, store.ErrAuthForbidden) {
				t.Fatal("retained team access survived revocation", err)
			}
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				w := httptest.NewRecorder()
				scaleP21M3Call(&server{store: source}, w, queued, (*server).getTeamRecord)
				if w.Code != http.StatusForbidden {
					t.Fatal("queued or replayed team request survived revocation")
				}
			}
		}
		if before != scaleP21M3Fingerprint(t, repo, workspace, "teams") || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("team read changed canonical teams or decoded full metadata")
		}
	}
}

func TestScaleP21SW22TeamLargeBodyAndCurrentKey(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		id := f.public
		for _, icon := range []string{strings.Repeat("legal icon ", 10000), "recovered-icon"} {
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams"), workspace, "team.updated", id, nil, func(data *domain.Bootstrap) error {
				for index := range data.Teams {
					if data.Teams[index].ID == id {
						data.Teams[index].Icon, data.Teams[index].Key = icon, "CUR22"
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before := scaleP21M3Fingerprint(t, repo, workspace, "teams")
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", "cur22")
			(&server{store: f.probe}).getTeamRecord(w, r)
			if w.Code != http.StatusOK {
				t.Fatal("legal full team body was omitted", w.Code, w.Body.String())
			}
			var got domain.Team
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Icon != icon || got.ID != id {
				t.Fatal("team detail body changed", err)
			}
			if before != scaleP21M3Fingerprint(t, repo, workspace, "teams") {
				t.Fatal("team budget path changed canonical rows")
			}
		}
		old := "CUR22"
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams"), workspace, "team.updated", id, nil, func(data *domain.Bootstrap) error {
			for index := range data.Teams {
				if data.Teams[index].ID == id {
					data.Teams[index].Key = "NEW22"
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", old)
			(&server{store: source}).getTeamRecord(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatal("team reader introduced historical key aliases", w.Code, w.Body.String())
			}
		}
		access, err := f.probe.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := f.probe.TeamRecordForAccess(ctx, access, id); !errors.Is(err, context.Canceled) {
			t.Fatal("team read ignored cancellation", err)
		}
		if _, err := f.probe.TeamRecordForAccess(t.Context(), access, id); err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("team read did not recover after cancellation", err)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("team detail decoded full metadata")
		}
	}
}
