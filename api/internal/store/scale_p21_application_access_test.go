package store

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21ApplicationAccessTypedInstallation(t *testing.T) {
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{})
	for _, raw := range []string{
		`null`,
		`{}`,
		`{"USERID":"app_json","userId":null,"clientId":"client","name":"first","name":null,"scopes":["one","two"],"SCOPES":[null],"teamIds":["team"],"teamIds":[],"active":true,"active":null,"createdAt":"2026-11-14T01:02:03.123456789123+07:00"}`,
		`{"scopes":["one","two"],"scopes":[null,null,"three"],"scopes":null,"teamIds":["team"],"teamIds":[null],"allTeams":true,"withoutRealtime":true,"serverSuspended":true}`,
		`{"ignored":{"deep":[true,null,123,{"data":"text"}]},"createdAt":null,"updatedAt":"2026-11-14T01:02:03Z","id":"installation","installedBy":"owner","webhookUrl":"https://example.test/hook"}`,
		`{"workspaceKey":"\u0000physical","clientId":"","name":"escaped \ud800","avatarUrl":"\ufffd"}`,
		`{"active":"bad"}`,
		`{"webhookUrl":[]}`,
		`{"scopes":[1]}`,
		`{"teamIds":{}}`,
		`{"createdAt":"invalid"}`,
		`{"updatedAt":true}`,
		`{"unknown":[1,]}`,
		`true`,
	} {
		var want domain.ApplicationInstallation
		wantErr := json.Unmarshal([]byte(raw), &want)
		read := &ApplicationAccessRead{store: repo}
		ranges, err := read.installation(t.Context(), strings.NewReader(raw), int64(len(raw)), "full")
		if err == nil {
			for _, name := range []string{"Name", "AvatarURL"} {
				if part, ok := ranges[name]; ok {
					err = read.stringAt(t.Context(), strings.NewReader(raw), part, reflect.ValueOf(&read.Installation).Elem().FieldByName(name))
					if err != nil {
						break
					}
				}
			}
		}
		if (err != nil) != (wantErr != nil) || err == nil && !reflect.DeepEqual(read.Installation, want) {
			t.Fatalf("typed installation differs for %s: old=%+v (%v) new=%+v (%v)", raw, want, wantErr, read.Installation, err)
		}
		read.Close()
		if repo.metadataResidency.work.used != 0 {
			t.Fatal("typed installation leaked admission")
		}
	}
}

func scaleP21ApplicationAccessRow(t *testing.T, f scaleP09Fixture, workspace, raw string) string {
	t.Helper()
	id, user, client := idgen.New("p21-installation-"), idgen.New("app_p21-"), idgen.New("p21-client-")
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO application_installations(id,workspace_key,client_id,user_id,data,webhook_secret) VALUES(?,?,?,?,?,'')`, id, workspace, client, user, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.ExecContext(context.Background(), `DELETE FROM application_installations WHERE id=?`, id); err != nil {
			t.Error(err)
		}
	})
	return user
}

func TestScaleP21ApplicationAccessNativeAndOutputOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	until := time.Now().UTC()
	wide := strings.Repeat("unrelated native application field ", 500)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "teamSettings", "oauthApplications"), workspace, "scale_p21.application_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = []domain.Team{
			{ID: "public", Name: wide},
			{ID: "private", Private: true},
			{ID: "child"},
			{ID: "retired", RetiredAt: &until},
			{ID: "duplicate", Private: true},
			{ID: "duplicate"},
			{ID: "cycle"},
			{ID: "missing-parent"},
		}
		data.TeamSettings = map[string]domain.TeamSettings{
			"child":          {ParentTeamID: "private"},
			"cycle":          {ParentTeamID: "cycle"},
			"missing-parent": {ParentTeamID: "absent"},
			wide:             {Access: wide, ParentTeamID: wide},
		}
		data.OAuthApplications = []domain.OAuthApplication{
			{ID: "first", ClientID: "flow_client_native", Name: wide, LogoURL: wide, Scopes: []string{"read", "write", "read"}, Description: wide},
			{ID: "last", ClientID: "flow_client_native", Name: "native\x00name", LogoURL: "native\x00logo", Scopes: []string{"read"}},
			{ID: "unrelated", ClientID: wide, Name: wide, LogoURL: wide, Scopes: []string{wide}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw := `{"id":"json-installation","workspaceKey":` + string(mustMarshalScaleP21Application(t, workspace)) + `,"userId":"app_json_user","clientId":"flow_client_native","name":"installation name","avatarUrl":"installation logo","scopes":["read","write","read"],"teamIds":["private"],"allPublicTeams":true,"active":true,"builtin":true,"webhookUrl":"https://example.test/hook","installedBy":"owner","createdAt":"2026-11-14T01:02:03Z","updatedAt":"2026-11-14T02:03:04Z"}`
	lookup := scaleP21ApplicationAccessRow(t, f, workspace, raw)
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	for _, reload := range []bool{false, true} {
		if reload {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
		}
		want, err := f.ApplicationByUser(t.Context(), lookup)
		if err != nil {
			t.Fatal(err)
		}
		read, err := view.BeginApplicationAccessRead(t.Context(), lookup, "full")
		if err != nil || !reflect.DeepEqual(read.Installation, want) || !reflect.DeepEqual(read.User, want.User()) {
			t.Fatalf("installed native application differs reload=%t: error=%v", reload, err)
		}
		if view.metadataResidency.work.used == 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("application output lacks independent ownership or decoded full metadata")
		}
		read.Close()
		read.Close()
		if view.metadataResidency.work.used != 0 || !reflect.DeepEqual(read.Installation, domain.ApplicationInstallation{}) {
			t.Fatal("application close leaked output/admission")
		}
		var closes []func()
		ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool { closes = append(closes, close); return true })
		got, err := view.ApplicationByUser(context.WithoutCancel(ctx), lookup)
		if err != nil || !reflect.DeepEqual(got, want) || len(closes) != 1 {
			t.Fatal("by-value output did not transfer lifetime", err, len(closes))
		}
		user := domain.User{ID: lookup}
		if err := view.decorateAppUser(ctx, &user); err != nil || !reflect.DeepEqual(user, want.User()) || len(closes) != 2 {
			t.Fatal("app user projection differs or lacks owner", err)
		}
		items, err := view.ListApplications(ctx, workspace)
		prior, priorErr := f.ListApplications(t.Context(), workspace)
		if err != nil || priorErr != nil || !reflect.DeepEqual(items, prior) || len(closes) != 3 {
			t.Fatal("ordered complete application output differs", err, priorErr)
		}
		for _, close := range closes {
			close()
		}
		if view.metadataResidency.work.used != 0 {
			t.Fatal("transferred application outputs leaked admission")
		}
		if _, err := view.ApplicationByUser(t.Context(), lookup); !errors.Is(err, ErrBoundedMetadataCoverage) || view.metadataResidency.work.used != 0 {
			t.Fatal("by-value application escaped without output owner", err)
		}
	}
}

func mustMarshalScaleP21Application(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestScaleP21ApplicationAccessSnapshotBudgetAndIgnoredBodies(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	held := 0
	var lookup string
	for _, count := range []int{10000, 100000} {
		raw := `{"workspaceKey":` + string(mustMarshalScaleP21Application(t, workspace)) + `,"userId":"app_json","clientId":"flow_client_missing","name":"application","active":true,"webhookUrl":"` + strings.Repeat("unused ", count) + `","unknown":{"body":"` + strings.Repeat("ignored ", count) + `"}}`
		lookup = scaleP21ApplicationAccessRow(t, f, workspace, raw)
		read, err := view.BeginApplicationAccessRead(t.Context(), lookup, "user")
		if err != nil || read.User.Active || read.User.ID != "app_json" {
			t.Fatal("missing flow client / JSON identity changed", err)
		}
		if held == 0 {
			held = view.metadataResidency.work.used
		}
		if view.metadataResidency.work.used != held {
			t.Fatal("10x unused body increased retained admission", held, view.metadataResidency.work.used)
		}
		read.Close()
		t.Logf("unused_repetitions=%d held_work=%d", count, held)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	var closes []func()
	ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool { closes = append(closes, close); return true })
	if _, err := view.ApplicationByUser(ctx, lookup); !errors.Is(err, ErrMetadataWorkBudget) || !errors.Is(metadataOutputError(ctx), ErrMetadataWorkBudget) {
		t.Fatal("application budget error was swallowed or not latched", err)
	}
	view.metadataResidency.work.limit = limit
	if view.metadataResidency.work.used != 0 || len(closes) != 0 {
		t.Fatal("partial application read leaked ownership")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := view.BeginApplicationAccessRead(cancelled, lookup, "user"); !errors.Is(err, context.Canceled) || view.metadataResidency.work.used != 0 {
		t.Fatal("cancellation leaked admission", err)
	}
	f.db.SetMaxOpenConns(1)
	ctx, cancel = context.WithTimeout(t.Context(), raceWatchdog(10*time.Second))
	defer cancel()
	tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	snapshot := context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
	read, err := view.BeginApplicationAccessRead(snapshot, lookup, "user")
	if err != nil {
		t.Fatal("borrowed pool-one snapshot failed", err)
	}
	read.Close()
	list, err := view.BeginApplicationListRead(snapshot, workspace)
	if err != nil {
		t.Fatal("borrowed pool-one list failed", err)
	}
	list.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("snapshot reads leaked work")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	scaleP21ApplicationAccessRow(t, f, workspace, `{"scopes":[1]}`)
	if list, err := view.BeginApplicationListRead(t.Context(), workspace); err == nil || list != nil || view.metadataResidency.work.used != 0 {
		t.Fatal("failed typed application list returned partial output or retained children", err)
	}
}

func TestScaleP21ApplicationAccessNativeGrantMatrix(t *testing.T) {
	now := time.Now()
	teams := []domain.Team{{ID: ""}, {ID: "public"}, {ID: "private", Private: true}, {ID: "child"}, {ID: "archived", ArchivedAt: &now}, {ID: "retired", RetiredAt: &now}, {ID: "duplicate", Private: true}, {ID: "duplicate"}, {ID: "cycle"}, {ID: "orphan"}}
	settings := map[string]domain.TeamSettings{"child": {ParentTeamID: "private"}, "cycle": {ParentTeamID: "cycle"}, "orphan": {ParentTeamID: "missing"}}
	sourceData := domain.Bootstrap{Teams: teams, TeamSettings: settings}
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": sourceData})
	for _, flags := range []domain.ApplicationInstallation{
		{}, {TeamIDs: []string{"private", "", "duplicate"}}, {AllTeams: true}, {AllPublicTeams: true}, {AllPublicTeams: true, TeamIDs: []string{"child"}}, {AllTeams: true, AllPublicTeams: true},
	} {
		want := flags
		want.ResolveTeamAccess(teams, settings)
		read := &ApplicationAccessRead{store: repo, Installation: flags}
		if err := read.teams(t.Context(), repo.metadataResidency.logical["physical"]); err != nil || !reflect.DeepEqual(read.Installation, want) {
			t.Fatalf("native team grants changed: got=%v want=%v error=%v", read.Installation.GrantedTeamIDs, want.GrantedTeamIDs, err)
		}
		read.Close()
	}
	if repo.metadataResidency.work.used != 0 {
		t.Fatal("grant matrix leaked work")
	}
}

func TestScaleP21ApplicationAccessGlobalAllMatchesAndBodyBound(t *testing.T) {
	held := 0
	for _, count := range []int{10000, 100000} {
		wide := strings.Repeat("unused native registration ", count)
		data := domain.Bootstrap{
			Teams:        []domain.Team{{ID: "public", Name: wide}},
			TeamSettings: map[string]domain.TeamSettings{wide: {ParentTeamID: wide, Access: wide}},
			OAuthApplications: []domain.OAuthApplication{
				{ClientID: "", Name: wide, LogoURL: wide, Description: wide, Scopes: []string{"read", "write"}},
				{ClientID: "", Name: "winner\x00name", LogoURL: "winner\x00logo", Description: wide, Scopes: []string{"read"}},
				{ClientID: wide, Name: wide, Scopes: []string{wide}},
			},
		}
		repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
		read := &ApplicationAccessRead{store: repo, Installation: domain.ApplicationInstallation{Scopes: []string{"read", "write", "read"}, AllPublicTeams: true}}
		source := repo.metadataResidency.logical["physical"]
		if err := read.teams(t.Context(), source); err != nil {
			t.Fatal(err)
		}
		found, err := read.global(t.Context(), []*metadataLogicalSource{source})
		want := domain.ApplicationInstallation{Scopes: []string{"read", "read"}, Name: "winner\x00name", AvatarURL: "winner\x00logo", AllPublicTeams: true, GrantedTeamIDs: []string{"public"}}
		if err != nil || !found || !reflect.DeepEqual(read.Installation, want) {
			t.Fatal("empty client/all-match native resolver changed", found, err)
		}
		if held == 0 {
			held = repo.metadataResidency.work.used
		}
		if repo.metadataResidency.work.used != held {
			t.Fatal("10x overwritten/unrelated native fields increased retained work", repo.metadataResidency.work.used, held)
		}
		read.Close()
		t.Logf("native_unused_repetitions=%d held_work=%d", count, held)
	}
}

func TestScaleP21ApplicationAccessCapturedSourcesAndConcurrentReads(t *testing.T) {
	before := domain.Bootstrap{
		Teams:             []domain.Team{{ID: "before"}},
		OAuthApplications: []domain.OAuthApplication{{ClientID: "client", Name: "before", Scopes: []string{"read"}}},
	}
	after := domain.Bootstrap{
		Teams:             []domain.Team{{ID: "after"}},
		OAuthApplications: []domain.OAuthApplication{{ClientID: "client", Name: "after", Scopes: []string{"write"}}},
	}
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": before})
	repo.lastWorkspaceKey = "physical"
	read := &ApplicationAccessRead{store: repo, Installation: domain.ApplicationInstallation{WorkspaceKey: "physical", ClientID: "client", AllTeams: true, Scopes: []string{"read", "write"}}}
	home, sources, err := read.sources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	repo.installMetadataLogicalSource("physical", replacement)
	if err := read.teams(t.Context(), home); err != nil {
		t.Fatal(err)
	}
	if _, err := read.global(t.Context(), sources); err != nil || read.Installation.Name != "before" || !reflect.DeepEqual(read.Installation.GrantedTeamIDs, []string{"before"}) || !reflect.DeepEqual(read.Installation.Scopes, []string{"read"}) {
		t.Fatal("source installation changed an existing application read", err)
	}
	for _, source := range sources {
		source.Release()
	}
	read.Close()
	probe := &ApplicationAccessRead{store: repo, Installation: domain.ApplicationInstallation{WorkspaceKey: ""}}
	home, sources, err = probe.sources(t.Context())
	if err != nil || home != nil {
		t.Fatal("empty JSON workspace incorrectly selected default", err)
	}
	for _, source := range sources {
		source.Release()
	}
	probe.Close()
	var workers sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 10 {
				read := &ApplicationAccessRead{store: repo, Installation: domain.ApplicationInstallation{WorkspaceKey: "physical", ClientID: "client", AllTeams: true, Scopes: []string{"read", "write"}}}
				home, sources, err := read.sources(t.Context())
				if err == nil {
					err = read.teams(t.Context(), home)
				}
				if err == nil {
					_, err = read.global(t.Context(), sources)
				}
				for _, source := range sources {
					source.Release()
				}
				if err == nil && read.Installation.Name != "after" {
					err = ErrBoundedMetadataCoverage
				}
				read.Close()
				if err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if repo.metadataResidency.work.used != 0 {
		t.Fatal("concurrent application reads leaked work")
	}
}

func TestScaleP21ApplicationAccessLegacyResolverBoundary(t *testing.T) {
	expected := map[string]int{"ApplicationByUser": 1, "ListApplications": 1}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			count := 0
			ast.Inspect(fn, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if ok && selector.Sel.Name == "resolveApplicationAccess" {
					count++
				}
				return true
			})
			if count == 0 {
				continue
			}
			if path != "application_agents.go" || expected[fn.Name.Name] != count {
				t.Fatalf("unreviewed legacy resolver reference in %s:%s", path, fn.Name.Name)
			}
			delete(expected, fn.Name.Name)
			guard, ok := fn.Body.List[0].(*ast.IfStmt)
			if !ok {
				t.Fatal("application resolver caller lacks initial bounded branch")
			}
			call, ok := guard.Cond.(*ast.CallExpr)
			if !ok {
				t.Fatal("application resolver branch is not a store flag call")
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "BoundedMetadataEnabled" {
				t.Fatal("application resolver branch does not select bounded representation")
			}
			if _, ok := guard.Body.List[len(guard.Body.List)-1].(*ast.ReturnStmt); !ok {
				t.Fatal("bounded application branch falls through to legacy resolver")
			}
			ast.Inspect(guard.Body, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "resolveApplicationAccess" {
					t.Fatal("bounded application branch calls legacy resolver")
				}
				return true
			})
		}
	}
	if len(expected) != 0 {
		t.Fatal("legacy resolver caller set changed", expected)
	}
}
