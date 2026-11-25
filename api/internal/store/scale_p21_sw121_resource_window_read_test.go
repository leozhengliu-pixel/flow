package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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

func TestScaleP21SW121TypedWindowDecodeAdmission(t *testing.T) {
	for _, raw := range []string{
		`{"users":[{},null,{},null]}`,
		`{"users":[{"via":null},null]}`,
		`{"documentTemplates":[{},null,{}]}`,
	} {
		cost, err := resourceWindowTypedJSONWork(t.Context(), []byte(raw), reflect.TypeFor[domain.Bootstrap]())
		if err != nil {
			t.Fatal(err)
		}
		untyped, err := metadataAggregatePayloadJSONWork(t.Context(), []byte(raw))
		if err != nil || cost <= untyped+uint64(reflect.TypeFor[domain.Bootstrap]().Size())*2 {
			t.Fatal("typed child container expansion was not charged", cost, untyped, err)
		}
		var data domain.Bootstrap
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Fatal(err)
		}
		minimum := uint64(reflect.TypeFor[domain.Bootstrap]().Size())*2 +
			uint64(len(data.Users))*uint64(reflect.TypeFor[domain.User]().Size())*2 +
			uint64(len(data.DocumentTemplates))*uint64(reflect.TypeFor[domain.DocumentTemplate]().Size())*2
		if cost < minimum {
			t.Fatal("empty/null typed rows escaped backing storage accounting", cost, minimum)
		}
	}
	store := &SQLiteStore{metadataResidency: newMetadataResidency()}
	read := &ResourceWindowRead{owner: &ResourceRead{store: store}, work: resourceWindowMaxWork - 1}
	if err := read.admitJSON(t.Context(), []byte(`{"users":[null,{},null]}`), reflect.TypeFor[domain.Bootstrap]()); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("typed decode crossed refused admission", err)
	}
	read.Close()
	if store.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("refused typed decode leaked")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resourceWindowTypedJSONWork(ctx, []byte(`{"users":[{},null]}`), reflect.TypeFor[domain.Bootstrap]()); !errors.Is(err, context.Canceled) {
		t.Fatal("typed preflight ignored cancellation", err)
	}
	count := resourceWindowMaxWork/(int(reflect.TypeFor[domain.User]().Size())*2+128) + 1
	dense := []byte(`{"users":[` + strings.Repeat("null,", count) + `null]}`)
	if _, err := resourceWindowTypedJSONWork(t.Context(), dense, reflect.TypeFor[domain.Bootstrap]()); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("dense typed null expansion escaped fixed bound", err)
	}
}

func scaleP21SW121FixTimes(value reflect.Value, at time.Time) {
	if value.Type() == reflect.TypeFor[time.Time]() {
		if !value.Interface().(time.Time).IsZero() {
			value.Set(reflect.ValueOf(at))
		}
		return
	}
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() {
			scaleP21SW121FixTimes(value.Elem(), at)
		}
	case reflect.Interface:
		if !value.IsNil() {
			copy := reflect.New(value.Elem().Type()).Elem()
			copy.Set(value.Elem())
			scaleP21SW121FixTimes(copy, at)
			value.Set(copy)
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).PkgPath == "" {
				scaleP21SW121FixTimes(value.Field(i), at)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			scaleP21SW121FixTimes(value.Index(i), at)
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			copy := reflect.New(value.Type().Elem()).Elem()
			copy.Set(iter.Value())
			scaleP21SW121FixTimes(copy, at)
			value.SetMapIndex(iter.Key(), copy)
		}
	}
}

func scaleP21SW121Seed(t *testing.T, f scaleP09Fixture, prefix, body string) {
	t.Helper()
	if err := f.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 15, 12, 13, 14, 15, time.UTC)
	actor := f.data.Viewer
	actor.Via = &domain.ActorBot{ID: prefix + "actor", Type: "oauthApplication", Name: "Native actor"}
	if err := f.MutateWorkspace(ContextWithActor(t.Context(), actor), f.data.Workspace.URLKey, "scale_p21.sw121_seed", "", nil, func(data *domain.Bootstrap) error {
		team := data.Teams[0].ID
		data.NextIssueNumber = 73
		data.Projects = []domain.Project{{ID: prefix + "project", Name: "Native\x00project", SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "old"},
			TeamIDs: []string{team}, Description: body, Resources: []domain.ProjectResource{{ID: prefix + "resource", Title: "Keep resource"}},
			Milestones: []domain.ProjectMilestone{{ID: prefix + "milestone", Description: body}}, Comments: []domain.Comment{{Body: body}},
			DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: body}}, CreatedAt: at, UpdatedAt: at}}
		data.Initiatives = []domain.Initiative{{ID: prefix + "initiative", Name: "Initiative", Description: body, Creator: actor,
			ProjectIDs: []string{prefix + "project"}, DescriptionHistory: []domain.InitiativeDescriptionRevision{{Description: body}},
			Resources: []domain.InitiativeResource{{ID: prefix + "resource", Title: "Keep resource"}}, CreatedAt: at}}
		doc := domain.Document{ID: prefix + "document", SlugID: prefix + "doc-current", PreviousSlugIDs: []string{prefix + "doc-old"}, Title: "Document",
			Creator: actor, TeamIDs: []string{team}, ProjectIDs: []string{prefix + "project"}, Content: body, ContentState: body,
			ContentData: map[string]any{"body": body}, Revisions: []domain.DocumentRevision{{Content: body}}, CreatedAt: at, UpdatedAt: at}
		data.Documents = []domain.Document{doc}
		data.Cycles = []domain.Cycle{{ID: prefix + "cycle", Name: "Cycle", TeamID: team, Description: body, Resources: []domain.CycleResource{}, CreatedAt: at}}
		payload, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		data.Trash = []domain.TrashEntry{{ID: prefix + "trash", ResourceType: "document", ResourceID: doc.ID, Title: doc.Title, Payload: payload,
			DeletedBy: actor, DeletedAt: at, ExpiresAt: at.AddDate(0, 0, 30), Links: &domain.TrashLinks{}}}
		data.DocumentTemplates = []domain.DocumentTemplate{{ID: prefix + "template", Name: "Keep template", Content: "Complete template content"}}
		data.Settings[prefix+"native"] = map[string]any{"integer": int64(9007199254740993), "time": at, "empty": []string{}, "nil": []string(nil)}
		// Stable encoded timestamp widths matter to canonical byte admission,
		// in addition to the native auxiliary cost equality checked below.
		selected := reflect.ValueOf(data).Elem()
		for _, name := range append([]string{"workspace", "viewer"}, resourceWindowCoreFields...) {
			index, _, _ := metadataLogicalField(name)
			scaleP21SW121FixTimes(selected.Field(index), at)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW121Comparable(t *testing.T, data domain.Bootstrap, started time.Time) domain.Bootstrap {
	t.Helper()
	// Canonical normalization creates these omitted collections afresh. Their
	// generated clocks are checked, while every other value stays exact.
	data.NotificationPreferences = maps.Clone(data.NotificationPreferences)
	for id, preference := range data.NotificationPreferences {
		if preference.UpdatedAt.Before(started) || preference.UpdatedAt.After(time.Now()) {
			t.Errorf("default preference clock outside read interval for %s", id)
		}
		preference.UpdatedAt = time.Time{}
		data.NotificationPreferences[id] = preference
	}
	data.CustomerStatuses = append(data.CustomerStatuses[:0:0], data.CustomerStatuses...)
	for i := range data.CustomerStatuses {
		status := &data.CustomerStatuses[i]
		if status.CreatedAt.Before(started) || status.CreatedAt.After(time.Now()) || status.UpdatedAt.Before(started) || status.UpdatedAt.After(time.Now()) {
			t.Errorf("default customer status clocks outside read interval for %s", status.ID)
		}
		status.CreatedAt, status.UpdatedAt = time.Time{}, time.Time{}
	}
	return data
}

func scaleP21SW121Compare(t *testing.T, want domain.Bootstrap, revision string, read *ResourceWindowRead, started time.Time) {
	t.Helper()
	if read == nil {
		t.Fatal("missing owned window")
	}
	expected := scaleP21SW121Comparable(t, want, started)
	actual := scaleP21SW121Comparable(t, read.Data, started)
	if read.Revision != revision || !reflect.DeepEqual(expected, actual) {
		left, right := reflect.ValueOf(expected), reflect.ValueOf(actual)
		for i := 0; i < left.NumField(); i++ {
			if !reflect.DeepEqual(left.Field(i).Interface(), right.Field(i).Interface()) {
				t.Errorf("window differs in native Bootstrap.%s", left.Type().Field(i).Name)
			}
		}
		t.Fatalf("native window/revision mismatch: %s/%s", revision, read.Revision)
	}
}

func TestScaleP21SW121WindowNativeSourceAndPayload(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw121-native-")
	scaleP21SW121Seed(t, f, prefix, strings.Repeat("Excluded payload and body. ", 1000))
	known, exact := f.copyRevisionOf(workspace)
	stamp, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil || !exact || known.workspaceID != stamp.workspaceID || known.revision != stamp.revision {
		t.Fatal("native witness must use certified warm literal source", exact, err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, kind := range []string{"core", "documents", "projects", "initiatives", "cycles", "trash", "users", "members", "teamMembers", "departedMembers", "invitations"} {
		started := time.Now()
		want, revision, err := f.ResourceWindowMetadata(t.Context(), workspace, "", nil, kind)
		if err != nil {
			t.Fatal(kind, err)
		}
		read, err := view.BeginResourceWindowRead(t.Context(), workspace, "", nil, kind)
		if err != nil {
			t.Fatal(kind, err)
		}
		defer read.Close()
		scaleP21SW121Compare(t, want, revision, read, started)
		if read.Data.NextIssueNumber != 73 || read.Data.Viewer.Via == nil || read.Data.Viewer.Via.ID != prefix+"actor" {
			t.Fatal("raw warm window lost supported native scalar/actor attribution", kind, read.Data.NextIssueNumber, read.Data.Viewer.Via)
		}
		if read.Data.Viewer.Via == want.Viewer.Via {
			t.Fatal("window shares mutable native actor")
		}
		read.Data.Viewer.Via.Name = "caller replacement"
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("native window leaked work")
		}
	}
	var workers sync.WaitGroup
	for range 3 {
		workers.Go(func() {
			read, err := view.BeginResourceWindowRead(t.Context(), workspace, "", nil, "documents")
			if err != nil {
				t.Error(err)
				return
			}
			defer read.Close()
			if len(read.Data.Documents) != 1 || read.Data.Documents[0].Title != "Document" || read.Data.NextIssueNumber != 73 {
				t.Error("concurrent native source choice changed")
			}
		})
	}
	workers.Wait()
	// Warm full sources retain occurrences and complete field nil shapes.
	f.mu.Lock()
	data := f.workspaces[workspace]
	doc := data.Documents[0]
	doc.Title = "Later duplicate"
	data.Documents = append(data.Documents, doc, domain.Document{ID: "", SlugID: "", Permissions: []domain.DocumentPermission{}})
	data.Users = nil
	f.workspaces[workspace] = data
	f.mu.Unlock()
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	want, revision, err := f.ResourceWindowMetadata(t.Context(), workspace, "", nil, "documents")
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginResourceWindowRead(t.Context(), workspace, "", nil, "documents")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	scaleP21SW121Compare(t, want, revision, read, started)
	if len(read.Data.Documents) != 3 || read.Data.Documents[1].Title != "Later duplicate" || read.Data.Users != nil {
		t.Fatal("warm duplicate order/nil native directory changed")
	}
	read.Close()
	for _, payload := range []string{
		`null`,
		`{"ID":"first","id":null,"id":"last","TEAMIDS":["one"],"teamIds":null,"TITLE":"case","ignored":{"body":"unused"}}`,
		`{"id":"valid"} {"id":"trailing"}`,
		`{"title":42}`,
		`{"id":`,
		``,
	} {
		f.mu.Lock()
		data := f.workspaces[workspace]
		data.Trash = append([]domain.TrashEntry(nil), data.Trash...)
		data.Trash[0].Payload = json.RawMessage(payload)
		f.workspaces[workspace] = data
		f.mu.Unlock()
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		want, revision, legacyErr := f.ResourceWindowMetadata(t.Context(), workspace, "", nil, "trash")
		read, boundedErr := view.BeginResourceWindowRead(t.Context(), workspace, "", nil, "trash")
		if legacyErr != nil {
			if read != nil || !errors.Is(legacyErr, ErrResourceAccessChanged) || !errors.Is(boundedErr, ErrResourceAccessChanged) {
				t.Fatal("literal payload error changed", payload, legacyErr, boundedErr)
			}
		} else {
			if boundedErr != nil {
				t.Fatal(payload, boundedErr)
			}
			scaleP21SW121Compare(t, want, revision, read, started)
			read.Close()
		}
		if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("native payload output/failure leaked or hydrated")
		}
	}
}

func TestScaleP21SW121WindowBodyBoundAndCanonical(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	held := map[string]int{}
	auxiliary := map[string]uint64{}
	for _, size := range []int{1000, 10000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := scaleP09Database(t)
			workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw121-size-")
			scaleP21SW121Seed(t, f, prefix, strings.Repeat("Excluded body. ", size))
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			for _, state := range []string{"warm", "reloaded", "canonical"} {
				if state == "reloaded" {
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				if state == "canonical" {
					f.mu.Lock()
					f.forgetCopyRevisionLocked(workspace)
					f.mu.Unlock()
				}
				if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
					t.Fatal(err)
				}
				for _, kind := range []string{"core", "trash", "members"} {
					started := time.Now()
					legacySQL, boundedSQL := 0, 0
					want, revision, err := f.ResourceWindowMetadata(WithSQLStatementObserver(t.Context(), func(string) { legacySQL++ }), workspace, "", nil, kind)
					if err != nil {
						t.Fatal(state, kind, err)
					}
					read, err := view.BeginResourceWindowRead(WithSQLStatementObserver(t.Context(), func(string) { boundedSQL++ }), workspace, "", nil, kind)
					if err != nil {
						t.Fatal(state, kind, err)
					}
					defer read.Close()
					scaleP21SW121Compare(t, want, revision, read, started)
					key := state + "/" + kind
					if state != "canonical" {
						for _, field := range append([]string{"workspace", "viewer", "viewerRole"}, resourceWindowCoreFields...) {
							index, _, _ := metadataLogicalField(field)
							cost := read.owner.source.fields[index].cost
							name := key + "/" + field
							if size == 1000 {
								auxiliary[name] = cost
							} else if auxiliary[name] != cost {
								t.Fatal("selected auxiliary source differs across body sizes", name, cost, auxiliary[name])
							}
						}
					}
					work := view.MetadataWorkUsedForTesting()
					if size == 1000 {
						held[key] = work
					} else if held[key] != work {
						t.Fatal("10x excluded body growth changed admission", key, held[key], work)
					}
					t.Log("state/kind/body repetitions/admission/old SQL/new SQL", state, kind, size, work, legacySQL, boundedSQL)
					read.Close()
					if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
						t.Fatal("window body-bound proof leaked or hydrated")
					}
				}
			}
		})
	}
}

func TestScaleP21SW121WindowPermissionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, workspaceID, prefix := f.data.Workspace.URLKey, f.data.Workspace.ID, idgen.New("sw121-policy-")
	scaleP21SW121Seed(t, f, prefix, "Excluded")
	public, private, child := prefix+"public", prefix+"private", prefix+"child"
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw121_teams", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: public, Key: public}, domain.Team{ID: private, Key: private, Private: true}, domain.Team{ID: child, Key: child})
		data.TeamSettings[public] = domain.TeamSettings{TeamID: public, Access: "public"}
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, Access: "public", ParentTeamID: private}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	join := func(role string, teams []string) domain.User {
		t.Helper()
		user, token, err := f.Register(t.Context(), "SW121 "+role, idgen.New("sw121-user-")+"@example.test", "sw121-password")
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
	}{
		{"admin", admin}, {"admin-private-outsider", f.data.Viewer}, {"member-private-outsider", member},
		{"guest-explicit-grant", guest}, {"sub-team-inheritance", inherited},
	}
	keys := map[string][]domain.APIKey{}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw121_policy", "", nil, func(data *domain.Bootstrap) error {
		at := time.Date(2026, 11, 15, 12, 13, 14, 15, time.UTC)
		for _, principal := range principals {
			data.UserSettings[principal.user.ID] = defaultUserSettings(principal.user.ID)
			for _, restriction := range []string{"all", "empty", "set"} {
				id := idgen.New("sw121-key-")
				key := domain.APIKey{ID: id, CreatorID: principal.user.ID, SecretHash: fmt.Sprintf("%x", sha256.Sum256([]byte(id))), Scopes: []string{"read"}, TeamRestriction: "all", CreatedAt: at}
				if restriction != "all" {
					key.TeamRestriction, key.TeamIDs = "selected", []string{}
				}
				if restriction == "set" {
					key.TeamIDs = []string{public}
				}
				keys[principal.user.ID] = append(keys[principal.user.ID], key)
				data.APIKeys = append(data.APIKeys, key)
			}
		}
		data.Projects = []domain.Project{
			{ID: prefix + "shared", Name: "Shared", SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "old"}, TeamIDs: []string{public, private},
				DependencyIDs: []string{prefix + "hidden"}, IssueCount: 97, Creator: &admin, Lead: &admin},
			{ID: prefix + "hidden", Name: "Hidden", TeamIDs: []string{private}, Creator: &admin},
			{ID: prefix + "child", Name: "Child", TeamIDs: []string{child}},
		}
		data.Initiatives = []domain.Initiative{{ID: prefix + "initiative", Name: "Initiative", Creator: admin, ProjectIDs: []string{prefix + "shared", prefix + "hidden"}, CreatedAt: at}}
		data.Documents, data.Trash = nil, nil
		for i, team := range []string{public, private, child} {
			doc := domain.Document{ID: fmt.Sprint(prefix, "doc-", i), SlugID: fmt.Sprint(prefix, "current-", i), PreviousSlugIDs: []string{fmt.Sprint(prefix, "old-", i)},
				Title: "Document", Creator: admin, TeamIDs: []string{team}, ProjectIDs: []string{prefix + "shared", prefix + "hidden"}, Content: "Excluded", CreatedAt: at,
				Permissions: []domain.DocumentPermission{{ID: fmt.Sprint(prefix, "grant-", i), SubjectType: "user", SubjectID: guest.ID, Role: "viewer"}}}
			data.Documents = append(data.Documents, doc)
			payload, err := json.Marshal(doc)
			if err != nil {
				return err
			}
			data.Trash = append(data.Trash, domain.TrashEntry{ID: fmt.Sprint(prefix, "trash-", i), ResourceType: "document", ResourceID: doc.ID, Title: doc.Title,
				TeamIDs: doc.TeamIDs, Payload: payload, DeletedBy: admin, DeletedAt: at, ExpiresAt: at.AddDate(0, 0, 30)})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, state := range []string{"warm", "reloaded", "canonical"} {
		if state == "reloaded" {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if state == "canonical" {
			f.mu.Lock()
			f.forgetCopyRevisionLocked(workspace)
			f.mu.Unlock()
		}
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
			t.Fatal(err)
		}
		for _, principal := range principals {
			credentials := []*domain.APIKey{nil}
			for i := range keys[principal.user.ID] {
				credentials = append(credentials, &keys[principal.user.ID][i])
			}
			for i, key := range credentials {
				t.Run(fmt.Sprint(state, "/", principal.name, "/", i), func(t *testing.T) {
					started := time.Now()
					want, revision, err := f.ResourceWindowMetadata(t.Context(), workspace, principal.user.ID, key, "trash")
					if err != nil {
						t.Fatal(err)
					}
					read, err := view.BeginResourceWindowRead(t.Context(), workspace, principal.user.ID, key, "trash")
					if err != nil {
						t.Fatal(err)
					}
					defer read.Close()
					scaleP21SW121Compare(t, want, revision, read, started)
					read.Close()
					if view.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("M window leaked work")
					}
				})
			}
		}
	}
	before, err := f.ResolveAccess(t.Context(), workspace, inherited.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	boundedBefore, err := view.ResolveAccess(t.Context(), workspace, inherited.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.ValidateAccess(t.Context(), before); err != nil {
		t.Fatal(err)
	}
	if err := view.ValidateAccess(t.Context(), boundedBefore); err != nil {
		t.Fatal(err)
	}
	if err := f.SetTeamMembership(t.Context(), workspaceID, private, inherited.ID, "member", false); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"queued", "replayed"} {
		if !errors.Is(f.ValidateAccess(t.Context(), before), ErrAuthForbidden) || !errors.Is(view.ValidateAccess(t.Context(), boundedBefore), ErrAuthForbidden) {
			t.Fatal("revoked queued/replayed authority survived", phase)
		}
	}
	other, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "SW121 other", idgen.New("sw121-other-"), "us")
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
	_, _, oldErr := f.ResourceWindowMetadata(t.Context(), other.Workspace.URLKey, member.ID, nil, "members")
	read, newErr := otherView.BeginResourceWindowRead(t.Context(), other.Workspace.URLKey, member.ID, nil, "members")
	if read != nil || !errors.Is(oldErr, ErrAuthForbidden) || !errors.Is(newErr, ErrAuthForbidden) {
		t.Fatal("cross-workspace principal changed", oldErr, newErr)
	}
	if view.MetadataWorkUsedForTesting() != 0 || otherView.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("M/cross-workspace window leaked or hydrated")
	}
}

func TestScaleP21SW121WindowCancellationBudgetAndRevision(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw121-budget-")
	scaleP21SW121Seed(t, f, prefix, "Excluded")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := view.BeginResourceWindowRead(ctx, workspace, "", nil, "trash"); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled window escaped", err)
	}
	budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
	budget.limit = 1
	read, err := view.BeginResourceWindowRead(t.Context(), workspace, "", nil, "trash")
	budget.limit = limit
	if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("unadmitted window escaped or leaked", err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	read, err = view.BeginResourceWindowRead(ctx, workspace, "", nil, "trash")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if len(read.Data.Trash) == 0 || view.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("cancel prematurely released owned window")
	}
	read.Close()
	changed := false
	stamps := 0
	ctx = WithSQLStatementObserver(t.Context(), func(query string) {
		if !strings.HasPrefix(query, "SELECT "+metadataStampColumns) {
			return
		}
		stamps++
		if stamps == 2 {
			changed = true
			if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_cache_revisions SET revision=revision+1 WHERE workspace_key=? AND kind=?`, workspace, cacheKindMetadata); err != nil {
				t.Error(err)
			}
		}
	})
	read, err = view.BeginResourceWindowRead(ctx, workspace, "", nil, "core")
	if !changed || read != nil || !errors.Is(err, ErrResourceAccessChanged) || !errors.Is(err, ErrAuthForbidden) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("changed trailing revision escaped or leaked", changed, stamps, err)
	}
	for _, kind := range []string{"invalid", ""} {
		_, _, oldErr := f.ResourceWindowMetadata(t.Context(), workspace, "", nil, kind)
		read, newErr := view.BeginResourceWindowRead(t.Context(), workspace, "", nil, kind)
		if read != nil || !errors.Is(oldErr, ErrMetadataRecordQuery) || !errors.Is(newErr, ErrMetadataRecordQuery) {
			t.Fatal("invalid kind changed", kind, oldErr, newErr)
		}
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw121_large_aux", "", nil, func(data *domain.Bootstrap) error {
		data.Settings[prefix+"selected"] = strings.Repeat("Selected auxiliary field. ", 150000)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	read, err = view.BeginResourceWindowRead(t.Context(), workspace, "", nil, "core")
	if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("oversized complete auxiliary graph escaped or leaked", err)
	}
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("failure paths retained work or hydrated metadata")
	}
}

func TestScaleP21SW121WindowCanonicalKeyTransferGuard(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("PostgreSQL/MySQL enforce the 191-character/byte key schema before this oversized-key witness")
	}
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw121-key-")
	scaleP21SW121Seed(t, f, prefix, "Excluded")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	key := strings.Repeat("oversized", resourceWindowKeyBytes)
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,?)`, workspace, "documents", key, 9999, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.ExecContext(context.Background(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, "documents", key); err != nil {
			t.Error(err)
		}
	})
	f.mu.Lock()
	f.forgetCopyRevisionLocked(workspace)
	f.mu.Unlock()
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	guarded := false
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.Contains(query, "CASE WHEN LENGTH(record_key)<=") {
			guarded = true
		}
	})
	read, err := view.BeginResourceWindowRead(ctx, workspace, "", nil, "documents")
	if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || !guarded || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("oversized canonical key crossed SQL transfer guard or leaked", guarded, err)
	}
}
