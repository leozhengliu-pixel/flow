package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP21SW137Queries struct {
	total, policy, principal int
}

func (q *scaleP21SW137Queries) observe(query string) {
	q.total++
	if strings.Contains(query, "workspace_metadata_records") && (strings.Contains(query, "teams") || strings.Contains(query, "teamSettings")) {
		q.policy++
	}
	if strings.HasPrefix(query, "SELECT u.id,u.email") && strings.Contains(query, "LEFT JOIN team_memberships") {
		q.principal++
	}
}

func scaleP21SW137Snapshot(t *testing.T, view *SQLiteStore, workspace string) (context.Context, *ResourceRead, *sqlTx, func()) {
	t.Helper()
	tx, err := view.db.BeginTx(t.Context(), metadataReadOptions(view.dialect))
	if err != nil {
		t.Fatal(err)
	}
	owner := &ResourceRead{store: view}
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: view.db, workspace: workspace, reader: tx})
	ctx, closeMemo := withResourcePolicyMemo(ctx, owner)
	if resourcePolicyMemoFor(ctx, view, workspace) == nil {
		t.Fatal("ready owned SQL snapshot did not acquire a policy memo")
	}
	return ctx, owner, tx, func() {
		closeMemo()
		owner.Close()
		_ = tx.Rollback()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Error("policy memo retained work after its owner ended")
		}
	}
}

func scaleP21SW137Install(t *testing.T, view *SQLiteStore, workspace string, data domain.Bootstrap, stamp copyStamp) {
	t.Helper()
	source, err := captureMetadataLogicalSource(t.Context(), data, stamp)
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource(workspace, source)
}

func scaleP21SW137Compare(t *testing.T, old, bounded AccessContext, source domain.Bootstrap, key *domain.APIKey) {
	t.Helper()
	if old.WorkspaceID != bounded.WorkspaceID || old.PrincipalID != bounded.PrincipalID ||
		old.CredentialScope != bounded.CredentialScope || old.AuthzEpoch != bounded.AuthzEpoch ||
		!old.MatchesCredential(key) || !bounded.MatchesCredential(key) ||
		!reflect.DeepEqual(old.policy.teams, bounded.policy.teams) ||
		!reflect.DeepEqual(old.policy.settingsTeams, bounded.policy.settingsTeams) {
		t.Fatal("resolved identities, credential binding or team decisions differ")
	}
	for _, item := range append([]domain.Project(nil), source.Projects...) {
		for _, ref := range append([]string{item.ID, item.SlugID}, item.PreviousSlugIDs...) {
			before, beforeAllowed, beforeErr := resourceReadMetadata(old.policy, source, "projects", ref)
			after, afterAllowed, afterErr := resourceReadMetadata(bounded.policy, source, "projects", ref)
			if beforeAllowed != afterAllowed || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeErr, afterErr) {
				t.Fatal("project permission/old-slug/actor/reference/count output differs", ref)
			}
		}
	}
	for _, item := range source.Initiatives {
		for _, ref := range append([]string{item.ID, item.SlugID}, item.PreviousSlugIDs...) {
			before, beforeAllowed, beforeErr := resourceReadMetadata(old.policy, source, "initiatives", ref)
			after, afterAllowed, afterErr := resourceReadMetadata(bounded.policy, source, "initiatives", ref)
			if beforeAllowed != afterAllowed || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeErr, afterErr) {
				t.Fatal("initiative permission/reference/actor output differs", ref)
			}
		}
	}
	project := func(access AccessContext) domain.Bootstrap {
		data := cloneBootstrap(source)
		policy := cloneBootstrap(access.policy.metadata)
		data.Viewer, data.ViewerRole, data.TeamMembers = policy.Viewer, policy.ViewerRole, policy.TeamMembers
		data.TeamSettings = policy.TeamSettings
		filterBootstrapTeams(&data, access.policy.teams, data.ViewerRole == "guest")
		return domain.Bootstrap{Teams: data.Teams, Projects: data.Projects, Initiatives: data.Initiatives, Documents: data.Documents, Issues: data.Issues}
	}
	if !reflect.DeepEqual(project(old), project(bounded)) {
		t.Fatal("bootstrap document grants, hidden references or actors differ")
	}
}

func TestScaleP21SW137InstalledPolicyMemoPermissionMatrix(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, workspaceID, prefix := f.data.Workspace.URLKey, f.data.Workspace.ID, idgen.New("sw137-")
	public, private, child := prefix+"public", prefix+"private", prefix+"child"
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw137_teams", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: public, Key: "PUBLIC"}, domain.Team{ID: private, Key: "PRIVATE", Private: true}, domain.Team{ID: child, Key: "CHILD"})
		for _, id := range []string{public, private, child} {
			data.TeamSettings[id] = domain.TeamSettings{TeamID: id, Access: "public", SettingsPermission: "teamMembers", LabelPermission: "teamMembers", MemberPermission: "teamMembers"}
		}
		settings := data.TeamSettings[private]
		settings.Access = "private"
		data.TeamSettings[private] = settings
		settings = data.TeamSettings[child]
		settings.ParentTeamID = private
		data.TeamSettings[child] = settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	join := func(role string, teams []string) domain.User {
		t.Helper()
		user, token, err := f.Register(t.Context(), "SW137 "+role, idgen.New("sw137-user-")+"@example.test", "sw137-password")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := f.ErasePersonFromWorkspace(context.Background(), workspace, user.ID); err != nil {
				t.Error(err)
			}
			if err := f.EraseAccount(context.Background(), user.ID); err != nil {
				t.Error(err)
			}
		})
		if err := f.VerifyEmail(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		invitation, err := f.Invite(t.Context(), workspaceID, f.data.Viewer.ID, user.Email, role, teams)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.AcceptInvitation(t.Context(), invitation.Token, user.ID); err != nil {
			t.Fatal(err)
		}
		return user
	}
	admin := join("admin", []string{private})
	member := join("member", []string{public})
	guest := join("guest", []string{public})
	inherited := join("member", []string{private})
	principals := []struct {
		name string
		user domain.User
	}{{"admin", admin}, {"admin-private-outsider", f.data.Viewer}, {"member-private-outsider", member}, {"guest-explicit", guest}, {"sub-team-inheritance", inherited}}
	keys := []*domain.APIKey{nil,
		{ID: prefix + "all", TeamRestriction: "all", Scopes: []string{"read"}},
		{ID: prefix + "empty", TeamRestriction: "selected", TeamIDs: []string{}, Scopes: []string{"read"}},
		{ID: prefix + "set", TeamRestriction: "selected", TeamIDs: []string{public}, Scopes: []string{"read"}},
		{ID: prefix + "app", Actor: "app", TeamRestriction: "selected", TeamIDs: []string{public}, Scopes: []string{"read"}},
	}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw137_resources", "", nil, func(data *domain.Bootstrap) error {
		actor := admin
		actor.AppTeamIDs = []string{public, private}
		data.Projects = []domain.Project{
			{ID: prefix + "visible", SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "old"}, Name: "Visible", TeamIDs: []string{public, private},
				DependencyIDs: []string{prefix + "hidden"}, IssueCount: 79, Creator: &actor, Lead: &actor,
				Initiatives: []string{prefix + "initiative"}, Comments: []domain.Comment{{ID: prefix + "comment", User: actor, Body: "Visible actor"}}},
			{ID: prefix + "hidden", TeamIDs: []string{private}, Creator: &actor},
			{ID: prefix + "inherited", TeamIDs: []string{child}},
		}
		data.Initiatives = []domain.Initiative{{ID: prefix + "initiative", SlugID: prefix + "initiative-current", PreviousSlugIDs: []string{prefix + "initiative-old"},
			Name: "Mixed references", ProjectIDs: []string{prefix + "visible", prefix + "hidden"}, ContributingTeamIDs: []string{public, private}, Creator: actor, Owner: &actor}}
		data.Documents = []domain.Document{
			{ID: prefix + "document-private", TeamIDs: []string{private}, Creator: actor, ProjectIDs: []string{prefix + "hidden"},
				Permissions: []domain.DocumentPermission{{ID: prefix + "grant", SubjectType: "user", SubjectID: guest.ID, Role: "viewer"}}},
			{ID: prefix + "document-child", TeamIDs: []string{child}, Creator: actor},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"warm", "reloaded", "unknown", "sql-drift"} {
		t.Run(state, func(t *testing.T) {
			if state == "reloaded" {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			if state == "unknown" {
				data, _ := f.WorkspaceMetadata(workspace)
				scaleP21SW137Install(t, view, workspace, data, copyStamp{workspaceID: workspaceID, revision: -1})
			}
			if state == "sql-drift" {
				if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw137_changed", "", nil, func(data *domain.Bootstrap) error {
					for i := range data.Teams {
						if data.Teams[i].ID == private {
							data.Teams[i].Private = false
						}
					}
					settings := data.TeamSettings[private]
					settings.Access = "public"
					data.TeamSettings[private] = settings
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			data, _ := f.WorkspaceMetadata(workspace)
			ctx, owner, _, close := scaleP21SW137Snapshot(t, view, workspace)
			defer close()
			oldQueries, newQueries := &scaleP21SW137Queries{}, &scaleP21SW137Queries{}
			oldCtx, newCtx := WithSQLStatementObserver(ctx, oldQueries.observe), WithSQLStatementObserver(ctx, newQueries.observe)
			held, pairs, firstPolicy, firstSQL := 0, 0, 0, 0
			matching := false
			for _, principal := range principals {
				for _, key := range keys {
					want, oldErr := f.ResolveAccess(oldCtx, workspace, principal.user.ID, key)
					got, newErr := view.ResolveAccess(newCtx, workspace, principal.user.ID, key)
					if oldErr != nil || newErr != nil {
						t.Fatal(state, principal.name, oldErr, newErr)
					}
					memo := resourcePolicyMemoFor(ctx, view, workspace)
					if _, present := memo.lookup(got.policy.stamp, got.AuthzEpoch); !present {
						t.Fatal("memo did not retain the resolved SQL version", state, principal.name, memo.stamp, got.policy.stamp, memo.epoch, got.AuthzEpoch)
					}
					if pairs == 0 {
						firstPolicy, firstSQL = newQueries.policy, newQueries.total
						source := view.metadataResidency.logical[workspace]
						matching = source.Stamp().revision >= 0 && source.Stamp() == got.policy.stamp
					} else if newQueries.policy != firstPolicy || newQueries.total != firstSQL+pairs*2 {
						t.Fatal("memo repeated policy recovery or skipped live stamp/principal checks", state, newQueries, firstPolicy, firstSQL, pairs)
					}
					scaleP21SW137Compare(t, want, got, data, key)
					if held == 0 {
						held = view.MetadataWorkUsedForTesting()
					}
					if held == 0 || view.MetadataWorkUsedForTesting() != held {
						t.Fatal("principal/credential calls changed memo ownership", state, held, view.MetadataWorkUsedForTesting())
					}
					pairs++
				}
			}
			if newQueries.principal != pairs || oldQueries.principal != pairs || owner.closed {
				t.Fatal("memo skipped live principal/credential checks", oldQueries, newQueries)
			}
			if matching {
				if newQueries.policy != 0 || newQueries.total > oldQueries.total {
					t.Fatal("matching installed policy recovered canonical rows or regressed SQL", oldQueries, newQueries)
				}
			} else if newQueries.policy == 0 {
				t.Fatal("uncertified/stale installed source bypassed canonical policy")
			}
			t.Log("M pairs / matching native stamp / literal SQL / bounded SQL / first SQL / policy SQL / held work", pairs, matching, oldQueries.total, newQueries.total, firstSQL, newQueries.policy, held)
		})
	}
	other, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "SW137 other", idgen.New("sw137-other-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.DeleteWorkspace(context.Background(), other.Workspace.URLKey); err != nil {
			t.Error(err)
		}
	})
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	ctx, _, tx, close := scaleP21SW137Snapshot(t, view, workspace)
	defer close()
	oldAccess, err := f.ResolveAccess(ctx, workspace, inherited.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	boundedAccess, err := view.ResolveAccess(ctx, workspace, inherited.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, beforeErr := f.ResolveAccess(ctx, other.Workspace.URLKey, member.ID, nil)
	_, afterErr := view.ResolveAccess(ctx, other.Workspace.URLKey, member.ID, nil)
	if !errors.Is(beforeErr, ErrAuthForbidden) || !errors.Is(afterErr, ErrAuthForbidden) {
		t.Fatal("memo crossed workspace membership", beforeErr, afterErr)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := f.SetTeamMembership(t.Context(), workspaceID, private, inherited.ID, "member", false); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"queued", "replayed"} {
		if !errors.Is(f.ValidateAccess(t.Context(), oldAccess), ErrAuthForbidden) || !errors.Is(view.ValidateAccess(t.Context(), boundedAccess), ErrAuthForbidden) {
			t.Fatal("memo policy survived authority revocation", phase)
		}
	}
}

func TestScaleP21SW137InstalledPolicyMemoSourcesAndSnapshots(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, principal := f.data.Workspace.URLKey, f.data.Viewer.ID
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw137_canonical", "", nil, func(data *domain.Bootstrap) error {
		data.Teams[0].Key = "CANONICAL"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	old, err := f.ResolveAccess(t.Context(), workspace, principal, nil)
	if err != nil {
		t.Fatal(err)
	}
	stamp := old.policy.stamp
	native, _ := f.WorkspaceMetadata(workspace)
	// Ordinary callback-native domain values can differ from recovery rows.
	// A certified installed generation must keep the literal source precedence.
	native.Teams[0].Key = "CALLBACK"
	f.mu.Lock()
	f.workspaces[workspace] = cloneBootstrap(native)
	f.setCopyRevisionLocked(workspace, stamp)
	f.mu.Unlock()
	scaleP21SW137Install(t, view, workspace, native, stamp)
	for _, state := range []string{"matching", "unknown", "stale", "newer", "wrong-workspace"} {
		t.Run(state, func(t *testing.T) {
			selected := stamp
			switch state {
			case "unknown":
				selected.revision = -1
			case "stale":
				selected.revision--
			case "newer":
				selected.revision++
			case "wrong-workspace":
				selected.workspaceID += "-other"
			}
			scaleP21SW137Install(t, view, workspace, native, selected)
			ctx, _, _, close := scaleP21SW137Snapshot(t, view, workspace)
			defer close()
			queries := &scaleP21SW137Queries{}
			got, err := view.ResolveAccess(WithSQLStatementObserver(ctx, queries.observe), workspace, principal, nil)
			if err != nil {
				t.Fatal(err)
			}
			if state == "matching" {
				want, err := f.ResolveAccess(ctx, workspace, principal, nil)
				if err != nil || !reflect.DeepEqual(resourcePolicyMetadata(got.policy.metadata), resourcePolicyMetadata(want.policy.metadata)) || queries.policy != 0 {
					t.Fatal("matching native policy lost literal precedence", queries, err)
				}
			} else {
				canonical, err := view.ResolveAccess(t.Context(), workspace, principal, nil)
				if err != nil || !reflect.DeepEqual(resourcePolicyMetadata(got.policy.metadata), resourcePolicyMetadata(canonical.policy.metadata)) || queries.policy == 0 {
					t.Fatal("unmatched source bypassed canonical recovery", state, queries, err)
				}
			}
		})
	}
	scaleP21SW137Install(t, view, workspace, native, stamp)
	ctx, owner, tx, close := scaleP21SW137Snapshot(t, view, workspace)
	defer close()
	got, err := view.ResolveAccess(ctx, workspace, principal, nil)
	if err != nil || got.policy.metadata.Teams[0].Key != "CALLBACK" {
		t.Fatal("matching snapshot fixture did not use native policy", err)
	}
	memo := resourcePolicyMemoFor(ctx, view, workspace)
	memo.close()
	queries := &scaleP21SW137Queries{}
	got, err = view.ResolveAccess(WithSQLStatementObserver(ctx, queries.observe), workspace, principal, nil)
	if err != nil || queries.policy == 0 || got.policy.metadata.Teams[0].Key == "CALLBACK" {
		t.Fatal("closed memo introduced an error or reused native policy", queries, err)
	}
	owner.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("closed memo/owner leaked work")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := view.ResolveAccess(cancelled, workspace, principal, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation bypassed policy admission", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	_, oldErr := f.ResolveAccess(ctx, workspace, principal, nil)
	_, boundedErr := view.ResolveAccess(ctx, workspace, principal, nil)
	if !errors.Is(oldErr, sql.ErrTxDone) || !errors.Is(boundedErr, sql.ErrTxDone) {
		t.Fatal("closed SQL snapshot was revived by installed policy", oldErr, boundedErr)
	}
}

func TestScaleP21SW137InstalledPolicyMemoLargeAndQuota(t *testing.T) {
	stamp := copyStamp{workspaceID: "sw137-native", revision: 9}
	held := 0
	for _, size := range []int{1000, 10000} {
		data := domain.Bootstrap{Workspace: domain.Workspace{ID: stamp.workspaceID, URLKey: "workspace"}, TeamSettings: map[string]domain.TeamSettings{}}
		for i := range 512 {
			id := fmt.Sprintf("sw137-team-%04d", i)
			data.Teams = append(data.Teams, domain.Team{ID: id, Key: fmt.Sprintf("T%d", i), Name: strings.Repeat("n", size), Private: i%2 == 0})
			data.TeamSettings[id] = domain.TeamSettings{TeamID: id, Access: "public", SettingsPermission: "teamMembers",
				LabelPermission: "teamMembers", MemberPermission: "teamMembers", AgentGuidance: strings.Repeat("g", size)}
		}
		view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"workspace": data})
		scaleP21SW137Install(t, view, "workspace", data, stamp)
		view.db = &sqlDatabase{}
		view.db.journalReady.Store(true)
		tx := &sqlTx{database: view.db}
		base := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: view.db, workspace: "workspace", reader: tx})
		source := view.metadataResidency.logical["workspace"]
		source.mu.Lock()
		refs := source.refs
		source.mu.Unlock()
		run := func(ctx context.Context, measure bool) error {
			owner := &ResourceRead{store: view}
			ctx, closeMemo := withResourcePolicyMemo(ctx, owner)
			defer owner.Close()
			defer closeMemo()
			memo := resourcePolicyMemoFor(ctx, view, "workspace")
			before := view.MetadataWorkUsedForTesting()
			got, present, err := memo.installed(ctx, stamp, "authority")
			if err != nil {
				return err
			}
			if !present || !reflect.DeepEqual(got, resourcePolicyMetadata(data)) {
				t.Error("large legal policy projection differs")
			}
			if measure {
				work := view.MetadataWorkUsedForTesting() - before
				if held == 0 {
					held = work
				}
				if work != held || work <= 64<<10 {
					t.Error("10x excluded team bodies changed retained policy work", size, work, held)
				}
				t.Log("ignored body bytes per record / retained policy work", size, work)
			}
			return nil
		}
		if err := run(base, true); err != nil {
			t.Fatal(err)
		}
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("large policy owner leaked")
		}
		limit := view.metadataResidency.work.limit
		view.metadataResidency.work.limit = 2 << 20
		if err := run(base, true); !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("partial native policy admission escaped quota or leaked", err)
		}
		view.metadataResidency.work.limit = limit
		if err := source.Retain(); err != nil {
			t.Fatal(err)
		}
		transient := &ResourceRead{store: view, source: source}
		policy, err := transient.nativeEventPolicy(base)
		if err != nil {
			transient.Close()
			t.Fatal(err)
		}
		projectedWork := view.MetadataWorkUsedForTesting()
		retainedWork, err := metadataAggregateNativeCost(base, reflect.ValueOf(policy), 0)
		transient.Close()
		if err != nil {
			t.Fatal(err)
		}
		view.metadataResidency.work.limit = projectedWork + int(retainedWork) + 64<<10 - 1
		if err := run(base, true); !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("native-to-memo transfer escaped refused retention or leaked", err)
		}
		t.Log("native-to-memo transfer quota checked", projectedWork, retainedWork+64<<10)
		view.metadataResidency.work.limit = limit
		cancelled, cancel := context.WithCancel(base)
		cancel()
		if err := run(cancelled, true); !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("cancelled native policy retained work", err)
		}
		// Each independent memo can project and release the shared immutable source.
		var jobs sync.WaitGroup
		for range 4 {
			jobs.Go(func() {
				if err := run(base, false); err != nil {
					t.Error(err)
				}
			})
		}
		jobs.Wait()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("concurrent native memos leaked")
		}
		source.mu.Lock()
		retained := source.refs
		source.mu.Unlock()
		if retained != refs {
			t.Fatal("native policy lookup retained its transient logical source", retained, refs)
		}
		raw, err := json.Marshal(resourcePolicyMetadata(data))
		if err != nil || len(raw) == 0 {
			t.Fatal(err)
		}
		t.Log("legal policy encoded bytes", len(raw))
	}
}
