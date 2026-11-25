package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW22TeamReferenceOrder(t *testing.T) {
	teams := []domain.Team{
		{ID: "hidden", Key: "hidden-key"},
		{ID: "first", Key: "duplicate"},
		{ID: "second", Key: "DUPLICATE"},
		{ID: "key-collision", Key: "exact"},
		{ID: "exact", Key: "other"},
		{ID: "hidden-alias", Key: "hidden"},
		{ID: "unicode-kelvin", Key: "\u212a"},
		{ID: "unicode-long-s", Key: "\u017f"},
		{ID: "empty-key", Key: ""},
	}
	for _, spec := range []struct {
		ref, want string
	}{
		{"duplicate", "first"}, {"DuPliCaTe", "first"}, {"exact", "exact"},
		{"hidden", "hidden-alias"}, {"k", "unicode-kelvin"}, {"S", "unicode-long-s"},
		{"", "empty-key"}, {" duplicate ", ""}, {"first", "first"},
		{"FIRST", ""}, {"missing", ""}, {"first\x00", ""},
	} {
		if got := scaleP21SW22TeamReference(teams, spec.ref, func(team domain.Team) bool { return team.ID != "hidden" }); got != spec.want {
			t.Fatalf("ref=%q got=%q want=%q", spec.ref, got, spec.want)
		}
	}
}

func TestScaleP21SW22TeamNativeReads(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, owner := f.data.Workspace.URLKey, f.data.Viewer.ID
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	target := domain.Team{
		ID: idgen.New("team_sw22_"), Name: "Complete Team", Key: "SW22T", Color: "retain-color",
		Icon: "retain-icon", ExternalSource: "retain-source", Private: true,
		CreatedAt: &stamp, UpdatedAt: &stamp,
	}
	existing := []domain.Team{target}
	for index := range 75 {
		existing = append(existing, domain.Team{ID: fmt.Sprintf("team_sw22_unused_%d", index), Key: fmt.Sprintf("U%04d", index), Name: "Unrelated"})
	}
	other := existing[len(existing)-1].ID
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "teamSettings", "projects"), workspace, "scale_p21_sw22.team_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, existing...)
		data.TeamSettings[target.ID] = domain.TeamSettings{TeamID: target.ID, Access: "private"}
		data.TeamSettings[other] = domain.TeamSettings{TeamID: other, AgentGuidance: strings.Repeat("unrelated guidance ", 9000)}
		data.Projects = append(data.Projects, domain.Project{ID: idgen.New("project_sw22_"), Description: strings.Repeat("unrelated body ", 30000)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	access, err := view.ResolveAccess(t.Context(), workspace, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The admin may inspect the private row without reading its content.
	if !access.policy.teams.Teams[target.ID] || access.policy.teams.Content[target.ID] {
		t.Fatal("fixture must distinguish private settings visibility from content")
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
		current, err := source.ResolveAccess(t.Context(), workspace, owner, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, ref := range []string{target.ID, target.Key, strings.ToLower(target.Key)} {
			got, err := source.TeamRecordForAccess(t.Context(), current, ref)
			if err != nil || !reflect.DeepEqual(got, target) {
				t.Fatalf("complete team differs ref=%q got=%+v err=%v", ref, got, err)
			}
			*got.CreatedAt, *got.UpdatedAt = stamp.Add(time.Hour), stamp.Add(time.Hour)
			again, err := source.TeamRecordForAccess(t.Context(), current, ref)
			if err != nil || !reflect.DeepEqual(again, target) {
				t.Fatal("returned timestamp pointer aliases stored metadata", err)
			}
			trusted, workspaceExists, err := source.TeamRecordForWorkspace(t.Context(), workspace, ref)
			if err != nil || !workspaceExists || !reflect.DeepEqual(trusted, target) {
				t.Fatal("trusted team identity/key/detail differs", trusted, workspaceExists, err)
			}
		}
		for _, ref := range []string{target.Name, target.Key + " ", target.ID + "\x00", strings.Repeat("x", 192)} {
			if _, err := source.TeamRecordForAccess(t.Context(), current, ref); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("missing or malformed team ref=%q err=%v", ref, err)
			}
		}
		for _, selected := range [][]string{nil, {}, {target.ID}, {other}} {
			key := &domain.APIKey{ID: "sw22-team-selected", TeamRestriction: "selected", TeamIDs: selected}
			scoped, err := source.ResolveAccess(t.Context(), workspace, owner, key)
			if err != nil {
				t.Fatal(err)
			}
			got, err := source.TeamRecordForAccess(t.Context(), scoped, target.ID)
			if len(selected) == 1 && selected[0] == target.ID {
				if err != nil || got.ID != target.ID {
					t.Fatal("selected private settings row disappeared", err)
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("selected-empty/outside credential widened team visibility", err)
			}
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := source.TeamRecordForAccess(cancelled, current, target.ID); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled team read: %v", err)
		}
		if _, workspaceExists, err := source.TeamRecordForWorkspace(t.Context(), workspace+"-missing", target.ID); err != nil || workspaceExists {
			t.Fatal("trusted missing workspace did not remain distinct", workspaceExists, err)
		}
		for _, alias := range []string{workspace + "\x00", strings.ToUpper(workspace)} {
			if _, workspaceExists, err := source.TeamRecordForWorkspace(t.Context(), alias, target.ID); err != nil || workspaceExists {
				t.Fatalf("trusted workspace alias matched canonical key %q: exists=%t err=%v", alias, workspaceExists, err)
			}
			if aliased, err := source.ResolveAccess(t.Context(), alias, owner, nil); err == nil {
				if _, err := source.TeamRecordForAccess(t.Context(), aliased, target.ID); !errors.Is(err, ErrAuthForbidden) {
					t.Fatalf("new team reader accepted an aliased access workspace %q: %v", alias, err)
				}
			}
		}
		if _, workspaceExists, err := source.TeamRecordForWorkspace(t.Context(), workspace, target.ID+"\x00"); !workspaceExists || !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("trusted raw NUL ID matched a stripped canonical key", workspaceExists, err)
		}
		if aliased, err := source.ResolveAccess(t.Context(), workspace, owner+"\x00", nil); err == nil {
			if _, err := source.TeamRecordForAccess(t.Context(), aliased, target.ID); !errors.Is(err, ErrAuthForbidden) {
				t.Fatal("new team reader accepted a stripped principal", err)
			}
		}
	}
	before := view.WorkspaceMetadataDecodes()
	retained, err := view.ResolveAccess(t.Context(), workspace, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "customEmojis"), workspace, "custom_emoji.updated", "", nil, func(data *domain.Bootstrap) error {
		data.CustomEmojis = append(data.CustomEmojis, domain.CustomEmoji{ID: idgen.New("sw22_unrelated_"), Name: "Unrelated"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := view.TeamRecordForAccess(t.Context(), retained, target.ID); err != nil || got.ID != target.ID {
		t.Fatal("unrelated metadata revision invalidated unchanged team authority", err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams"), workspace, "team.updated", target.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Teams {
			if data.Teams[index].ID == target.ID {
				data.Teams[index].Key = "NEW22"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := view.TeamRecordForAccess(t.Context(), retained, target.Key); err == nil {
		t.Fatal("retained access served a renamed key")
	}
	fresh, err := view.ResolveAccess(t.Context(), workspace, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := view.TeamRecordForAccess(t.Context(), fresh, target.Key); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("team reader invented a historical key alias", err)
	}
	if got, err := view.TeamRecordForAccess(t.Context(), fresh, "new22"); err != nil || got.ID != target.ID {
		t.Fatal("new current key did not resolve", err)
	}
	budgetTx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer budgetTx.Rollback()
		large := target
		large.Icon = strings.Repeat("oversized selected icon ", 15000)
		raw, err := json.Marshal(large)
		if err != nil {
			t.Fatal(err)
		}
		// Keep the oversized policy row private to this transaction: another
		// package may be loading policy from the shared external database.
		if _, err := budgetTx.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teams' AND record_key=?`, raw, workspace, target.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := view.scaleP21SW22ReadTeam(t.Context(), budgetTx, workspace, target.ID); !errors.Is(err, ErrMetadataPageBudget) {
			t.Fatalf("oversized selected team preflight: %v", err)
		}
	}()
	if got, err := view.TeamRecordForAccess(t.Context(), fresh, target.ID); err != nil || got.Icon != target.Icon {
		t.Fatal("team reader did not recover after rolled-back budget failure", err)
	}
	closed, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Rollback(); err != nil {
		t.Fatal(err)
	}
	broken := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: closed})
	if _, err := view.TeamRecordForAccess(broken, fresh, target.ID); err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Fatal("storage failure became a missing team", err)
	}
	user, verification, err := f.Register(t.Context(), "SW22 held member", idgen.New("sw22-team-member-")+"@example.test", "sw22-held-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, _, err := f.WorkspaceRole(ctx, f.data.Workspace.ID, user.ID); err == nil {
			if err := f.RemoveMember(ctx, f.data.Workspace.ID, user.ID); err != nil {
				t.Error(err)
			}
		}
		if err := f.EraseAccount(ctx, user.ID); err != nil {
			t.Error(err)
		}
	})
	if err := f.VerifyEmail(t.Context(), verification); err != nil {
		t.Fatal(err)
	}
	invitation, err := f.Invite(t.Context(), f.data.Workspace.ID, owner, user.Email, "member", []string{target.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.AcceptInvitation(t.Context(), invitation.Token, user.ID); err != nil {
		t.Fatal(err)
	}
	held, err := view.ResolveAccess(t.Context(), workspace, user.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	var revokeErr error
	revoked := false
	read := WithSQLStatementObserver(t.Context(), func(query string) {
		if !revoked && strings.Contains(query, "field='teams'") && strings.Contains(query, "CASE WHEN LENGTH(data)") {
			revoked = true
			revokeErr = f.SuspendMember(t.Context(), f.data.Workspace.ID, user.ID)
		}
	})
	if _, err := view.TeamRecordForAccess(read, held, target.ID); !errors.Is(err, ErrAuthForbidden) || !revoked || revokeErr != nil {
		t.Fatalf("in-flight team read retained revoked authority: revoked=%t mutation=%v read=%v", revoked, revokeErr, err)
	}
	if view.WorkspaceMetadataDecodes() != before || view.metadataResidency.work.used != 0 {
		t.Fatal("team point reader decoded full metadata or leaked work admission")
	}
}
