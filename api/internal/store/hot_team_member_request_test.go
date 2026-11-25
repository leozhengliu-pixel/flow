package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21TeamMemberRequestReference(teams []domain.Team, ref string) (domain.Team, bool) {
	index := slices.IndexFunc(teams, func(team domain.Team) bool { return team.ID == ref })
	if index < 0 {
		index = slices.IndexFunc(teams, func(team domain.Team) bool { return strings.EqualFold(team.Key, ref) })
	}
	if index < 0 {
		return domain.Team{}, false
	}
	team := teams[index]
	return domain.Team{ID: team.ID, Key: team.Key}, true
}

func TestScaleP21HotTeamMemberRequestNativeDevelopment(t *testing.T) {
	zone := time.FixedZone(strings.Repeat("ignored native location ", 100000), 7*3600+123)
	stamp := time.Date(2026, 11, 14, 1, 2, 3, 456789123, zone)
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "native\x00workspace", URLKey: "logical"},
		Teams: []domain.Team{
			{ID: "key-before-id", Key: "exact", Name: strings.Repeat("ignored name ", 300000)},
			{ID: "exact", Key: "other"}, {ID: "first", Key: "duplicate"}, {ID: "second", Key: "DUPLICATE"},
			{ID: "native\x00id", Key: "N\x00K"}, {ID: "", Key: "empty"}, {ID: "unicode", Key: "\u212a"},
		},
		TeamMembers: []domain.TeamMember{
			{TeamID: "first", UserID: "native\x00\xff", Role: "owner", JoinedAt: stamp, Managed: true, Origin: "sync"},
			{TeamID: "elsewhere", UserID: strings.Repeat("ignored member ", 300000), JoinedAt: stamp},
			{TeamID: "first", UserID: "second", Role: "member", JoinedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)},
			{TeamID: "", UserID: "empty-team", JoinedAt: stamp},
		},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical\x00key": data})
	view.lastWorkspaceKey = "physical\x00key"
	for _, ref := range []string{"exact", "duplicate", "DUPLICATE", "N\x00K", "native\x00id", "", "k", "missing", " duplicate "} {
		wantTeam, wantFound := scaleP21TeamMemberRequestReference(data.Teams, ref)
		read, found, err := view.BeginTeamMemberRequestRead(t.Context(), "", ref, nil, true)
		if !wantFound {
			if !found || !errors.Is(err, sql.ErrNoRows) || read != nil {
				t.Fatal("missing team boundary changed", ref, found, err)
			}
			continue
		}
		if err != nil || !found || read.Team != wantTeam || read.Data.Workspace.ID != data.Workspace.ID {
			t.Fatal("ordered native reference changed", ref, found, err)
		}
		wantMembers := []domain.TeamMember{}
		for _, member := range data.TeamMembers {
			if member.TeamID == wantTeam.ID {
				wantMembers = append(wantMembers, member)
			}
		}
		if len(read.Data.TeamMembers) != len(wantMembers) {
			t.Fatal("selected development rows changed", ref)
		}
		for index := range wantMembers {
			want, wantErr := json.Marshal(wantMembers[index])
			got, gotErr := json.Marshal(read.Data.TeamMembers[index])
			if !bytes.Equal(want, got) || (wantErr == nil) != (gotErr == nil) {
				t.Fatal("development membership wire/error behavior changed", ref, string(want), string(got), wantErr, gotErr)
			}
		}
		if view.metadataResidency.work.used == 0 || read.reserved > 1<<20 {
			t.Fatal("ignored names/member bodies/location consumed retained work", read.reserved)
		}
		read.Close()
		read.Close()
		if read.Data.TeamMembers != nil || read.Team.ID != "" || view.metadataResidency.work.used != 0 {
			t.Fatal("development read leaked/retained output")
		}
	}
	if read, found, err := view.BeginTeamMemberRequestRead(t.Context(), "logical", "exact", nil, true); err != nil || found || read != nil {
		t.Fatal("logical key replaced exact physical identity", found, err)
	}
	parent, release, err := view.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	read, _, err := view.BeginTeamMemberRequestRead(parent, "", "exact", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if view.metadataResidency.work.used != read.reserved || read.Team.ID != "exact" {
		t.Fatal("returned identity borrowed its parent's allowance")
	}
	read.Close()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := view.BeginTeamMemberRequestRead(cancelled, "", "exact", nil, true); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation accepted", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	if _, _, err := view.BeginTeamMemberRequestRead(t.Context(), "", "exact", nil, true); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("saturated request read accepted", err)
	}
	view.metadataResidency.work.limit = limit
	if view.metadataResidency.work.used != 0 {
		t.Fatal("failed request read leaked admission")
	}
}

func TestScaleP21HotTeamMemberRequestSourceAndAuthority(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, principal := f.data.Workspace.URLKey, f.data.Viewer.ID
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	target := idgen.New("team_member_request_")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "teamSettings"), workspace, "scale_p21.team_member_request_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams,
			domain.Team{ID: target + "-key-before-id", Key: target},
			domain.Team{ID: target, Key: "native\x00key", Name: strings.Repeat("unused name ", 100000)},
			domain.Team{ID: target + "-first", Key: "duplicate"},
			domain.Team{ID: target + "-second", Key: "DUPLICATE"},
		)
		data.TeamSettings[target] = domain.TeamSettings{AgentGuidance: strings.Repeat("unused guidance ", 100000)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The direct legacy metadata reader still chooses this native generation
	// after losing its copy certification. SQL must not normalize its key.
	f.mu.Lock()
	f.forgetCopyRevisionLocked(workspace)
	f.mu.Unlock()
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	data, err := f.PagedWorkspaceMetadata(t.Context(), workspace, principal)
	if err != nil {
		t.Fatal(err)
	}
	access, err := view.ResolveAccess(t.Context(), workspace, principal, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{target, "native\x00key", "nativekey", "duplicate", "missing"} {
		want, wantFound := scaleP21TeamMemberRequestReference(data.Teams, ref)
		read, found, err := view.BeginTeamMemberRequestRead(t.Context(), workspace, ref, &access, false)
		if !wantFound {
			if !found || !errors.Is(err, sql.ErrNoRows) || read != nil {
				t.Fatal("missing native reference changed", ref, found, err)
			}
			continue
		}
		if err != nil || !found || read.Team != want || read.Data.Workspace.ID != data.Workspace.ID {
			t.Fatal("unknown revision normalized installed team identity", ref, found, err)
		}
		read.Close()
	}
	// An explicit parent snapshot has precedence over the installed map.
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	snapshotData := domain.Bootstrap{
		Workspace:    f.data.Workspace,
		Teams:        []domain.Team{{ID: "snapshot-only", Key: "SNAPSHOT"}},
		TeamSettings: map[string]domain.TeamSettings{},
	}
	snapshot := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx, metadata: snapshotData})
	current, err := view.ResolveAccess(snapshot, workspace, principal, nil)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	read, found, err := view.BeginTeamMemberRequestRead(snapshot, workspace, "snapshot", &current, false)
	if err != nil || !found || read.Team.ID != "snapshot-only" {
		tx.Rollback()
		t.Fatal("explicit snapshot lost its source precedence", found, err)
	}
	read.Close()
	if _, _, err := view.BeginTeamMemberRequestRead(snapshot, workspace, target, &current, false); !errors.Is(err, sql.ErrNoRows) {
		tx.Rollback()
		t.Fatal("explicit snapshot silently read the later installed registry", err)
	}
	view.mu.Lock()
	installed := view.workspaces[workspace]
	delete(view.workspaces, workspace)
	view.mu.Unlock()
	read, found, err = view.BeginTeamMemberRequestRead(snapshot, workspace, "snapshot", &current, false)
	view.mu.Lock()
	view.workspaces[workspace] = installed
	view.mu.Unlock()
	if err != nil || !found || read.Team.ID != "snapshot-only" {
		tx.Rollback()
		t.Fatal("explicit parent source depended on current installed-map membership", found, err)
	}
	read.Close()
	tx.Rollback()
	forged := access
	forged.WorkspaceID += "-foreign"
	if _, _, err := view.BeginTeamMemberRequestRead(t.Context(), workspace, target, &forged, false); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("forged access binding accepted", err)
	}
	if _, _, err := view.BeginTeamMemberRequestRead(t.Context(), workspace+"-foreign", target, &access, false); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("foreign workspace binding accepted", err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, principal); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.db.ExecContext(context.Background(), `UPDATE workspace_memberships SET status='active' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, principal)
	})
	if _, _, err := view.BeginTeamMemberRequestRead(t.Context(), workspace, target, &access, false); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("retained request survived membership revocation", err)
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("request projection decoded full metadata or leaked work")
	}
	if !reflect.DeepEqual(snapshotData.Teams, []domain.Team{{ID: "snapshot-only", Key: "SNAPSHOT"}}) {
		t.Fatal("request projection modified its parent snapshot")
	}
}
