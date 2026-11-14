package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21MemberDirectoryExpected(data domain.Bootstrap, members []domain.WorkspaceMember) (map[string]domain.User, map[string]domain.Team) {
	users, teams := map[string]domain.User{}, map[string]domain.Team{}
	for _, user := range data.Users {
		users[user.ID] = domain.User{ID: user.ID, Name: user.Name, DisplayName: user.DisplayName, Email: user.Email}
	}
	for _, member := range members {
		user := member.User
		users[user.ID] = domain.User{ID: user.ID, Name: user.Name, DisplayName: user.DisplayName, Email: user.Email}
	}
	for _, team := range data.Teams {
		teams[team.ID] = domain.Team{ID: team.ID, Name: team.Name, Key: team.Key}
	}
	return users, teams
}

func TestScaleP21HotMemberDirectoryInstalledScalars(t *testing.T) {
	values := map[string]domain.Bootstrap{"physical\x00key": {
		Workspace: domain.Workspace{ID: "workspace\x00id", URLKey: "logical"},
		Users: []domain.User{
			{ID: "same", Name: "first", AvatarURL: strings.Repeat("ignored ", 500000)},
			{ID: "same", Name: "last\x00\xff", DisplayName: "display\x00name", Email: "native\x00email"},
			{ID: "", Name: "empty identity"},
		},
		Teams: []domain.Team{
			{ID: "same", Name: "first", Color: strings.Repeat("ignored ", 500000)},
			{ID: "same", Name: "last\x00team", Key: "key\x00\xff"},
			{ID: "", Name: "empty team"},
		},
		Projects: []domain.Project{{ID: "ignored", Description: strings.Repeat("unrelated ", 500000)}},
	}}
	literal := &SQLiteStore{workspaces: values, lastWorkspaceKey: "physical\x00key"}
	view := scaleP21HotLogoStore(t, values)
	view.lastWorkspaceKey = "physical\x00key"
	for _, key := range []string{"physical\x00key", ""} {
		want, found := literal.WorkspaceMetadataFields(key, "users", "teams")
		users, teams := scaleP21MemberDirectoryExpected(want, nil)
		read, gotFound, err := view.BeginMemberDirectoryRead(t.Context(), key, false)
		if err != nil || found != gotFound || !reflect.DeepEqual(users, read.Users) || !reflect.DeepEqual(teams, read.Teams) {
			t.Fatal("installed scalar directory changed", key, gotFound, err)
		}
		if view.metadataResidency.work.used == 0 || read.reserved > 1<<20 {
			t.Fatal("directory did not retain a bounded scalar allowance", read.reserved)
		}
		read.Close()
		read.Close()
		if read.Users != nil || read.Teams != nil || view.metadataResidency.work.used != 0 {
			t.Fatal("directory close did not clear/release")
		}
	}
	for _, key := range []string{"logical", "physical", "missing"} {
		if read, found, err := view.BeginMemberDirectoryRead(t.Context(), key, false); err != nil || found || read != nil {
			t.Fatal("physical key identity changed", key, found, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := view.BeginMemberDirectoryRead(cancelled, "", false); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled read accepted", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	if _, _, err := view.BeginMemberDirectoryRead(t.Context(), "", false); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("saturated read accepted", err)
	}
	view.metadataResidency.work.limit = limit
	if view.metadataResidency.work.used != 0 {
		t.Fatal("failed directory read leaked admission")
	}
	oversized := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"large": {
		Users: []domain.User{{ID: "large", DisplayName: strings.Repeat("x", 3<<20)}},
	}})
	if _, _, err := oversized.BeginMemberDirectoryRead(t.Context(), "large", false); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("oversized selected scalar silently truncated", err)
	}
	if oversized.metadataResidency.work.used != 0 {
		t.Fatal("partial scalar extraction leaked admission")
	}
}

func TestScaleP21HotMemberDirectoryInstallationProjection(t *testing.T) {
	for _, raw := range []string{
		`null`,
		`{}`,
		`{"USERID":"native\u0000id","clientId":"client","name":"first","Name":null}`,
		`{"userId":"u","clientId":"a","clientId":"b","name":"final","scopes":[null,"read"],"teamIds":[],"createdAt":null}`,
		`{"userId":null,"name":"\ud800","active":null,"createdAt":"2026-11-14T01:02:03.123456789123456789+05:45"}`,
		`{"name":"` + strings.Repeat("discarded name ", 100000) + `","name":"final"}`,
		`{"name":"ok","unknown":{"huge":"` + strings.Repeat("ignored", 100000) + `"},"scopes":["` + strings.Repeat("ignored", 100000) + `"]}`,
		`{"avatarUrl":42}`,
		`{"scopes":[7]}`,
		`{"teamIds":{}}`,
		`{"active":"false"}`,
		`{"createdAt":"bad"}`,
		`{"updatedAt":"2026-11-14T01:02:03Z\u0000"}`,
		`{"name":"before","name":false,"name":"after"}`,
		`{"unknown":[{"nested":true}],`,
		`[]`,
	} {
		var want domain.ApplicationInstallation
		wantErr := json.Unmarshal([]byte(raw), &want)
		charged := 0
		got, gotErr := memberDirectoryProjectInstallation(t.Context(), strings.NewReader(raw), int64(len(raw)), func(cost int) error {
			charged += cost
			return nil
		}, nil)
		if (wantErr == nil) != (gotErr == nil) {
			t.Fatalf("typed validation differs for %.160s: literal=%v projected=%v", raw, wantErr, gotErr)
		}
		if wantErr == nil && (want.UserID != got.UserID || want.ClientID != got.ClientID || want.Name != got.Name) {
			t.Fatalf("typed merge changed selected scalars for %.160s", raw)
		}
		if charged > 128<<10 {
			t.Fatal("ignored installation body became retained output", charged)
		}
	}
}

func TestScaleP21HotMemberDirectoryMembershipOverlay(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	f.db.SetMaxOpenConns(1)
	workspace := f.data.Workspace.URLKey
	ctx := ContextWithActor(t.Context(), f.data.Viewer)
	foreign, err := f.CreateWorkspace(ctx, "Member app home", idgen.New("member-home-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.DeleteWorkspace(context.Background(), foreign.Workspace.URLKey) })
	appID, clientID := idgen.New("app_member_directory_"), idgen.New("member-client-")
	installationID := idgen.New("member-installation-")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,active,created_at,updated_at) VALUES(?,NULL,'old app','old app','','',1,?,?)`, appID, now, now); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.db.ExecContext(context.Background(), `DELETE FROM application_installations WHERE id=?`, installationID)
		_, _ = f.db.ExecContext(context.Background(), `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, appID)
		_, _ = f.db.ExecContext(context.Background(), `DELETE FROM auth_users WHERE id=?`, appID)
	})
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,'app','active',?,NULL)`, f.data.Workspace.ID, appID, now); err != nil {
		t.Fatal(err)
	}
	valid, _ := json.Marshal(domain.ApplicationInstallation{UserID: appID, ClientID: clientID, WorkspaceKey: workspace, Name: strings.Repeat("overridden installation name ", 100000), Scopes: []string{strings.Repeat("unrelated scope ", 100000)}, Active: true})
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO application_installations(id,workspace_key,client_id,user_id,data,webhook_secret) VALUES(?,?,?,?,?,'')`, installationID, workspace, clientID, appID, valid); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE auth_users SET name='member account',display_name='member profile' WHERE id=?`, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "users"), workspace, "scale_p21.member_names_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Users = []domain.User{
			{ID: f.data.Viewer.ID, Name: "installed owner", DisplayName: "installed fallback", Email: "installed@example.test"},
			{ID: appID, Name: "installed app", DisplayName: "installed app fallback"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "oauthApplications"), foreign.Workspace.URLKey, "scale_p21.member_global_app", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = []domain.OAuthApplication{
			{ID: idgen.New("member-app-"), ClientID: clientID, Name: strings.Repeat("overridden global name ", 100000)},
			{ID: idgen.New("member-app-"), ClientID: clientID, Name: "global final"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, malformed := range []bool{false, true, false} {
		raw := valid
		if malformed {
			raw = []byte(`{"name":"unrelated broken app","scopes":[7]}`)
		}
		if _, err := f.db.ExecContext(t.Context(), `UPDATE application_installations SET data=? WHERE id=?`, raw, installationID); err != nil {
			t.Fatal(err)
		}
		data, _ := f.WorkspaceMetadataFields(workspace, "users", "teams")
		members, listErr := f.ListMembers(t.Context(), data.Workspace.ID)
		if malformed != (listErr != nil) {
			t.Fatal("fixture lost all-or-none member error", listErr)
		}
		users, teams := scaleP21MemberDirectoryExpected(data, members)
		var queries []string
		readCtx := WithSQLStatementObserver(t.Context(), func(query string) { queries = append(queries, query) })
		read, found, err := view.BeginMemberDirectoryRead(readCtx, workspace, true)
		if err != nil || !found || !reflect.DeepEqual(users, read.Users) || !reflect.DeepEqual(teams, read.Teams) {
			t.Fatalf("membership overlay changed malformed=%t err=%v got=%v want=%v", malformed, err, read.Users, users)
		}
		if !malformed && read.Users[appID].Name != "global final" {
			t.Fatal("installation home narrowed global OAuth name precedence")
		}
		if malformed && read.Users[f.data.Viewer.ID].DisplayName != "installed fallback" {
			t.Fatal("unrelated invalid app allowed partial member overlay")
		}
		for _, query := range queries {
			if strings.Contains(query, "SELECT data FROM application_installations") {
				t.Fatal("bounded member directory fetched complete installation JSON")
			}
		}
		read.Close()
		if view.metadataResidency.work.used != 0 {
			t.Fatal("member overlay leaked work", view.metadataResidency.work.used)
		}
	}
	// Scanner input errors must remain errors rather than a successful scalar
	// prefix. A constant-size reader is sufficient for all installation fields.
	raw := []byte(`{"name":"prefix"} trailing`)
	if _, err := memberDirectoryProjectInstallation(t.Context(), bytes.NewReader(raw), int64(len(raw)), func(int) error { return nil }, nil); err == nil {
		t.Fatal("trailing installation bytes accepted")
	}
}
