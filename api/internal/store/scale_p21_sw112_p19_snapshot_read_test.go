package store

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW112Seed(t *testing.T, f scaleP09Fixture, prefix, body string) {
	t.Helper()
	at := time.Date(2026, 11, 15, 10, 20, 30, 123, time.UTC)
	if err := f.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw112_seed", "", nil, func(data *domain.Bootstrap) error {
		team := data.Teams[0].ID
		data.Projects = []domain.Project{
			{ID: prefix + "project", SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "old"}, Name: "Summary",
				TeamIDs: []string{team}, Description: body, Creator: &data.Viewer, Lead: &data.Viewer, CreatedAt: at, UpdatedAt: at,
				MemberIDs: []string{}, LabelIDs: nil, DependencyIDs: []string{prefix + "hidden"}, IssueCount: 97,
				Resources: []domain.ProjectResource{{ID: prefix + "resource", Title: body}}, Comments: []domain.Comment{{Body: body}},
				DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: body}},
				Milestones:           []domain.ProjectMilestone{{ID: prefix + "mile", Name: "Milestone", Description: body, DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: body}}}},
				IssueCountHistory:    []domain.ProjectProgressHistoryPoint{{}},
			},
			{ID: prefix + "empty", Name: "Empty", TeamIDs: []string{team}, Milestones: []domain.ProjectMilestone{}},
		}
		data.Initiatives = []domain.Initiative{
			{ID: prefix + "initiative", Name: "Parent", ProjectIDs: []string{prefix + "project"}, Creator: data.Viewer,
				Description: body, Resources: []domain.InitiativeResource{{Title: body}}, Comments: []domain.Comment{{Body: body}},
				CommentAttachments: []domain.Attachment{{ID: prefix + "attachment", Title: body}},
				DescriptionHistory: []domain.InitiativeDescriptionRevision{{Description: body}}, CreatedAt: at},
		}
		data.Documents = []domain.Document{{ID: prefix + "document", SlugID: prefix + "document-current", Title: "Summary document",
			Creator: data.Viewer, TeamIDs: []string{team}, Content: body, ContentState: body, ContentData: map[string]any{"body": body},
			ProjectIDs: []string{prefix + "project"}, Revisions: []domain.DocumentRevision{{Content: body}}, CreatedAt: at}}
		data.ProjectUpdates = map[string][]domain.ProjectUpdate{
			prefix + "project": {
				{ID: prefix + "update", ProjectID: prefix + "different", CreatedAt: at, Health: "onTrack", Body: body,
					BodyData: map[string]any{"body": body}, Comments: []domain.Comment{{Body: body}}, User: data.Viewer},
				{ID: prefix + "ignored-update", Body: body},
			},
			prefix + "nil":   nil,
			prefix + "empty": {},
		}
		data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{
			prefix + "initiative": {{ID: prefix + "initiative-update", InitiativeID: prefix + "different", CreatedAt: at, Health: "offTrack", Body: body, User: data.Viewer}},
			prefix + "nil":        nil,
			prefix + "empty":      {},
		}
		data.Settings[prefix+"native"] = map[string]any{"integer": int64(9007199254740993), "time": at, "empty": []string{}, "nil": []string(nil)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW112Comparable(t *testing.T, snapshot ScaleP19Snapshot, started time.Time) ScaleP19Snapshot {
	t.Helper()
	// P19 deliberately omits stored notification preferences, so its literal
	// normalizer creates fresh defaults on every call. Only that generated
	// clock field is normalized; all preference values and other native fields
	// remain part of the exact differential.
	snapshot.Data.NotificationPreferences = maps.Clone(snapshot.Data.NotificationPreferences)
	for id, preference := range snapshot.Data.NotificationPreferences {
		if preference.UpdatedAt.Before(started) || preference.UpdatedAt.After(time.Now()) {
			t.Errorf("fresh default preference timestamp outside read interval for %s", id)
		}
		preference.UpdatedAt = time.Time{}
		snapshot.Data.NotificationPreferences[id] = preference
	}
	return snapshot
}

func scaleP21SW112Compare(t *testing.T, want ScaleP19Snapshot, read *P19SnapshotRead, started time.Time) {
	t.Helper()
	want = scaleP21SW112Comparable(t, want, started)
	var got ScaleP19Snapshot
	if read != nil {
		got = scaleP21SW112Comparable(t, read.Snapshot, started)
	}
	if read == nil || !reflect.DeepEqual(got, want) {
		if read != nil {
			// Keep failure output bounded even when a selected auxiliary
			// field deliberately contains a large legal value.
			t.Errorf("native snapshot differs: workspace=%s revision=%s/%s projects=%d/%d initiatives=%d/%d",
				want.Data.Workspace.ID, want.Revision, read.Snapshot.Revision,
				len(want.Data.Projects), len(read.Snapshot.Data.Projects), len(want.Data.Initiatives), len(read.Snapshot.Data.Initiatives))
			left, right := reflect.ValueOf(want.Data), reflect.ValueOf(got.Data)
			for i := 0; i < left.NumField(); i++ {
				if !reflect.DeepEqual(left.Field(i).Interface(), right.Field(i).Interface()) {
					t.Errorf("differing Bootstrap field: %s", left.Type().Field(i).Name)
				}
			}
		} else {
			t.Error("native snapshot missing")
		}
		t.FailNow()
	}
}

func TestScaleP21SW112P19SnapshotNativeWarmReloadBodyGrowth(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	work := map[string]int{}
	auxiliaryCosts := map[string]uint64{}
	for _, size := range []int{2000, 20000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			// Each size starts with the same fixture shape. Reusing a fixture
			// after its reload would change selected auxiliary native shapes,
			// independently of the excluded body-size comparison.
			f := scaleP09Database(t)
			workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw112-")
			scaleP21SW112Seed(t, f, prefix, strings.Repeat("Excluded P19 bodies and histories. ", size))
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			for _, reload := range []bool{false, true} {
				if reload {
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				for _, unfiltered := range []bool{true, false} {
					var oldQueries, newQueries int
					started := time.Now()
					want, err := f.ReadScaleP19Snapshot(WithSQLStatementObserver(t.Context(), func(string) { oldQueries++ }), workspace, f.data.Viewer.ID, unfiltered)
					if err != nil {
						t.Fatal(err)
					}
					// A literal read may recover an unknown installed copy.
					// Capture the source it actually selected for this differential.
					if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
						t.Fatal(err)
					}
					countWork, rowsWork := 0, 0
					ctx := WithSQLStatementObserver(t.Context(), func(query string) {
						newQueries++
						if strings.Contains(query, "SELECT COUNT(*),COALESCE(SUM(LENGTH(field)+LENGTH(record_key)),0) FROM record_versions") {
							countWork = view.MetadataWorkUsedForTesting()
						}
						if strings.Contains(query, "SELECT field,record_key,version FROM record_versions") {
							rowsWork = view.MetadataWorkUsedForTesting()
						}
					})
					read, err := view.BeginP19SnapshotRead(ctx, workspace, f.data.Viewer.ID, unfiltered)
					if err != nil {
						t.Fatal(size, reload, unfiltered, err)
					}
					defer read.Close()
					scaleP21SW112Compare(t, want, read, started)
					if countWork == 0 || rowsWork <= countWork {
						t.Fatal("version rows transferred before count/key admission", countWork, rowsWork)
					}
					key := fmt.Sprint(reload, "/", unfiltered)
					for _, name := range p19SnapshotFields {
						switch name {
						case "projects", "initiatives", "documents", "projectUpdates", "initiativeUpdates":
							continue
						}
						index, _, _ := metadataLogicalField(name)
						cost := read.owner.source.fields[index].cost
						fieldKey := key + "/" + name
						if size == 2000 {
							auxiliaryCosts[fieldKey] = cost
						} else if cost != auxiliaryCosts[fieldKey] {
							t.Fatal("selected auxiliary input cost changed across body sizes", name, key, cost, auxiliaryCosts[fieldKey])
						}
					}
					held := view.MetadataWorkUsedForTesting()
					if work[key] == 0 {
						work[key] = held
					}
					if held != work[key] || read.work > p19SnapshotMaxWork {
						t.Fatal("10x excluded body/history growth changed retained work", size, key, held, work[key], read.work)
					}
					t.Log("body repetitions/reloaded/unfiltered/admitted work/legacy SQL/bounded SQL", size, reload, unfiltered, held, oldQueries, newQueries)
					read.Snapshot.Data.Projects[0].Name = "caller mutation"
					read.Close()
					read.Close()
					if view.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(read.Snapshot, ScaleP19Snapshot{}) {
						t.Fatal("snapshot close retained output/work")
					}
					current, err := f.ReadScaleP19Snapshot(t.Context(), workspace, f.data.Viewer.ID, unfiltered)
					if err != nil {
						t.Fatal("caller changed installed source", err)
					}
					scaleP21SW112Compare(t, want, &P19SnapshotRead{Snapshot: current}, started)
				}
			}
			if view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("snapshot hydrated full workspace")
			}
		})
	}
}

func TestScaleP21SW112P19SnapshotPrincipalDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, workspaceID, prefix := f.data.Workspace.URLKey, f.data.Workspace.ID, idgen.New("sw112-policy-")
	public, private, child := prefix+"public", prefix+"private", prefix+"child"
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw112_teams", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: public, Key: public}, domain.Team{ID: private, Key: private, Private: true}, domain.Team{ID: child, Key: child})
		data.TeamSettings[public] = domain.TeamSettings{TeamID: public, Access: "public"}
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, Access: "public", ParentTeamID: private}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	identity := func(role string, teams []string) domain.User {
		t.Helper()
		user, token, err := f.Register(t.Context(), "SW112 "+role, idgen.New("sw112-person-")+"@example.test", "sw112-password")
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
	admin := identity("admin", []string{private})
	member := identity("member", []string{public})
	guest := identity("guest", []string{public})
	inherited := identity("member", []string{private})
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw112_policy", "", nil, func(data *domain.Bootstrap) error {
		for _, user := range []domain.User{admin, member, guest, inherited} {
			data.UserSettings[user.ID] = defaultUserSettings(user.ID)
		}
		data.Projects = []domain.Project{
			{ID: prefix + "shared", Name: "Shared", SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "old"}, TeamIDs: []string{public, private},
				DependencyIDs: []string{prefix + "hidden"}, IssueCount: 97, Creator: &admin, Lead: &admin},
			{ID: prefix + "hidden", Name: "Hidden", TeamIDs: []string{private}, Creator: &admin},
			{ID: prefix + "inherited", Name: "Child", TeamIDs: []string{child}},
		}
		data.Initiatives = []domain.Initiative{{ID: prefix + "initiative", Name: "Hierarchy", Creator: admin, ProjectIDs: []string{prefix + "shared", prefix + "hidden"}, ParentInitiativeIDs: []string{prefix + "parent"}}}
		data.Documents = []domain.Document{{ID: prefix + "document", SlugID: prefix + "document-current", PreviousSlugIDs: []string{prefix + "document-old"},
			Title: "Guest grant", Creator: admin, TeamIDs: []string{private}, ProjectIDs: []string{prefix + "hidden"},
			CreatedAt:   time.Date(2026, 11, 15, 10, 20, 30, 123, time.UTC),
			Permissions: []domain.DocumentPermission{{ID: prefix + "grant", SubjectType: "user", SubjectID: guest.ID, Role: "viewer"}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, reload := range []bool{false, true} {
		if reload {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
		}
		for _, principal := range []struct {
			name string
			user domain.User
		}{
			{"admin", admin}, {"admin-private-outsider", f.data.Viewer},
			{"member-private-outsider", member}, {"guest-explicit-grant", guest},
			{"sub-team-inheritance", inherited},
		} {
			t.Run(fmt.Sprint(reload, "/", principal.name), func(t *testing.T) {
				started := time.Now()
				want, err := f.ReadScaleP19Snapshot(t.Context(), workspace, principal.user.ID, false)
				if err != nil {
					t.Fatal(err)
				}
				read, err := view.BeginP19SnapshotRead(t.Context(), workspace, principal.user.ID, false)
				if err != nil {
					t.Fatal(err)
				}
				defer read.Close()
				scaleP21SW112Compare(t, want, read, started)
				read.Close()
				if view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("principal snapshot leaked work")
				}
			})
		}
	}
	queued, err := f.ResolveAccess(t.Context(), workspace, inherited.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	queuedBounded, err := view.ResolveAccess(t.Context(), workspace, inherited.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.ValidateAccess(t.Context(), queued); err != nil {
		t.Fatal("literal queued authority was invalid before revocation", err)
	}
	if err := view.ValidateAccess(t.Context(), queuedBounded); err != nil {
		t.Fatal("bounded queued authority was invalid before revocation", err)
	}
	if err := f.SetTeamMembership(t.Context(), workspaceID, private, inherited.ID, "member", false); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"queued", "replayed"} {
		if !errors.Is(f.ValidateAccess(t.Context(), queued), ErrAuthForbidden) || !errors.Is(view.ValidateAccess(t.Context(), queuedBounded), ErrAuthForbidden) {
			t.Fatal("revoked principal retained authority", phase)
		}
	}
	started := time.Now()
	want, err := f.ReadScaleP19Snapshot(t.Context(), workspace, inherited.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginP19SnapshotRead(t.Context(), workspace, inherited.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	scaleP21SW112Compare(t, want, read, started)
	read.Close()
	other, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "SW112 other", idgen.New("sw112-other-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.DeleteWorkspace(context.Background(), other.Workspace.URLKey); err != nil {
			t.Error(err)
		}
	})
	otherView, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), other.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer otherView.releaseMetadataLogicalSources()
	_, legacyErr := f.ReadScaleP19Snapshot(t.Context(), other.Workspace.URLKey, member.ID, false)
	cross, boundedErr := otherView.BeginP19SnapshotRead(t.Context(), other.Workspace.URLKey, member.ID, false)
	if cross != nil || !errors.Is(legacyErr, ErrAuthForbidden) || !errors.Is(boundedErr, ErrAuthForbidden) {
		t.Fatal("cross-workspace principal changed", legacyErr, boundedErr)
	}
	if view.MetadataWorkUsedForTesting() != 0 || otherView.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("principal snapshots leaked or hydrated metadata")
	}
}

func TestScaleP21SW112P19SnapshotStampPinAndOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw112-stamp-")
	scaleP21SW112Seed(t, f, prefix, "excluded")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	parent, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer parent.Close()
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw112_changed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects[0].Name = "current generation"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	want, err := f.ReadScaleP19Snapshot(t.Context(), workspace, "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, closeParent := range []bool{false, true} {
		if closeParent {
			parent.Close()
		}
		read, err := view.BeginP19SnapshotRead(pinned, workspace, "", true)
		if err != nil {
			t.Fatal(closeParent, err)
		}
		defer read.Close()
		scaleP21SW112Compare(t, want, read, started)
		read.Close()
	}
	caller := workspaceRead{database: view.db, workspace: workspace, metadata: domain.Bootstrap{Workspace: domain.Workspace{ID: "poison"}}}
	legacyCaller := caller
	legacyCaller.database = f.db
	_, literalErr := f.ReadScaleP19Snapshot(context.WithValue(t.Context(), workspaceReadKey{}, legacyCaller), workspace, "", true)
	callerRead, callerErr := view.BeginP19SnapshotRead(context.WithValue(t.Context(), workspaceReadKey{}, caller), workspace, "", true)
	if callerRead != nil || literalErr == nil || callerErr == nil || literalErr.Error() != callerErr.Error() {
		t.Fatal("caller SQL snapshot refusal changed", literalErr, callerErr)
	}
	parent.Close()
	tx, err := view.db.BeginTx(t.Context(), metadataReadOptions(view.dialect))
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := readMetadataStamp(t.Context(), tx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	wrong := stamp
	wrong.revision++
	read, err := view.p19SnapshotAt(t.Context(), workspace, "", true, tx, wrong)
	_ = tx.Rollback()
	if read != nil || !errors.Is(err, ErrResourceAccessChanged) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("uncertified generation escaped or leaked", err)
	}
	// Unknown installed revision takes the literal reload-recovery path.
	f.mu.Lock()
	f.forgetCopyRevisionLocked(workspace)
	f.mu.Unlock()
	started = time.Now()
	want, err = f.ReadScaleP19Snapshot(t.Context(), workspace, "", true)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.RLock()
	unknown, captureErr := captureMetadataLogicalSource(t.Context(), f.workspaces[workspace], copyStamp{workspaceID: f.data.Workspace.ID, revision: -1})
	f.mu.RUnlock()
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	view.installMetadataLogicalSource(workspace, unknown)
	read, err = view.BeginP19SnapshotRead(t.Context(), workspace, "", true)
	if err != nil {
		t.Fatal("unknown revision recovery", err)
	}
	defer read.Close()
	scaleP21SW112Compare(t, want, read, started)
	read.Close()
	var workers sync.WaitGroup
	for range 3 {
		workers.Go(func() {
			read, err := view.BeginP19SnapshotRead(t.Context(), workspace, "", true)
			if err != nil {
				t.Error(err)
				return
			}
			defer read.Close()
			if !reflect.DeepEqual(scaleP21SW112Comparable(t, read.Snapshot, started), scaleP21SW112Comparable(t, want, started)) {
				t.Error("concurrent independent P19 snapshot changed")
			}
		})
	}
	workers.Wait()
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("stamp/pin/recovery retained work or hydrated full workspace")
	}
}

func TestScaleP21SW112P19SnapshotBudgetCancellationAndVersionPreflight(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw112-budget-")
	scaleP21SW112Seed(t, f, prefix, strings.Repeat("Legal excluded body. ", 20000))
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := view.BeginP19SnapshotRead(ctx, workspace, "", true); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled snapshot escaped", err)
	}
	budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
	budget.limit = 1
	read, err := view.BeginP19SnapshotRead(t.Context(), workspace, "", true)
	budget.limit = limit
	if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("unadmitted summary escaped or leaked", err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	read, err = view.BeginP19SnapshotRead(ctx, workspace, "", true)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if len(read.Snapshot.Data.Projects) == 0 || view.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("cancellation prematurely released owned output")
	}
	read.Close()
	tx, err := view.db.BeginTx(t.Context(), metadataReadOptions(view.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	probe := &P19SnapshotRead{owner: &ResourceRead{store: view}, work: p19SnapshotMaxWork - 1,
		Snapshot: ScaleP19Snapshot{Versions: map[string]map[string]string{"projects": {}, "initiatives": {}}}}
	rowsRead := false
	observed := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.Contains(query, "SELECT field,record_key,version FROM record_versions") {
			rowsRead = true
		}
	})
	if err := probe.versions(observed, tx, workspace); !errors.Is(err, ErrMetadataWorkBudget) || rowsRead {
		t.Fatal("version rows crossed refused count/key preflight", rowsRead, err)
	}
	probe.Close()
	_ = tx.Rollback()
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw112_oversized_aux", "", nil, func(data *domain.Bootstrap) error {
		data.Settings[prefix+"selected"] = strings.Repeat("Selected auxiliary field. ", 150000)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	read, err = view.BeginP19SnapshotRead(t.Context(), workspace, "", true)
	if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("oversized complete auxiliary graph escaped fixed ceiling or leaked", err)
	}
	if view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("budget refusal hydrated full workspace")
	}
}
