package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW83Data(f scaleP09Fixture, repetitions int) domain.Bootstrap {
	data := scaleP21SW78Data(f, repetitions)
	at := time.Date(2026, 11, 14, 1, 2, 3, 456, time.UTC)
	stale := scaleP21SW69OwnedActor(data.Users[0])
	stale.Name, stale.JobTitle = "Stale embedded name", "Stale embedded body"
	data.Members = []domain.WorkspaceMember{{User: stale, Role: "member", Status: "active", JoinedAt: at}}
	data.Initiatives = []domain.Initiative{
		{ID: "sw83-initiative", Name: "Changed initiative", Creator: stale, Owner: &stale, LabelIDs: []string{"sw78-a-label"}},
		{ID: "sw83-untouched-initiative", Name: "Untouched initiative", Description: data.Documents[0].Content},
	}
	data.ProjectUpdates = map[string][]domain.ProjectUpdate{
		"sw78-a-project": {{ID: "sw83-project-update", ProjectID: "sw78-a-project", Body: "Original project update body", User: stale, CreatedAt: at}},
		"nil":            nil, "empty": {},
	}
	data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{
		"sw83-initiative": {{ID: "sw83-initiative-update", InitiativeID: "sw83-initiative", Body: "Original initiative update body", User: stale, CreatedAt: at}},
		"nil":             nil, "empty": {},
	}
	data.ThreadSubscriptions = []domain.ThreadSubscription{
		{ID: "sw83-delete-thread", UserID: data.Users[0].ID, IssueID: "sw83-issue", CommentID: "sw83-comment", State: "subscribed", CreatedAt: at, UpdatedAt: at},
		{ID: "sw83-keep-thread", UserID: data.Users[0].ID, IssueID: "sw83-issue", CommentID: "sw83-other-comment", State: "muted", CreatedAt: at, UpdatedAt: at},
	}
	return data
}

func scaleP21SW83Target(before domain.Bootstrap) domain.Bootstrap {
	next := cloneBootstrap(before)
	refreshDisplayReferences(&next)
	refreshIssueReferences(&next)
	actor := scaleP21SW69OwnedActor(before.Users[0])
	actor.Name, actor.DisplayName = "Requested actor name", "Requested actor display"
	next.Viewer, next.Users[0] = actor, actor
	next.Labels[1].IssueCount++
	next.Projects[1].IssueCount++
	next.ThreadSubscriptions = slices.Clone(next.ThreadSubscriptions[1:])
	return next
}

func scaleP21SW83Peer(t *testing.T, tx *sqlTx, workspace, mode string, before domain.Bootstrap) {
	t.Helper()
	put := func(field, id string, value any, order int) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{field, id}, metadataRecord{data: raw, order: order}, nil); err != nil {
			t.Fatal(err)
		}
	}
	switch mode {
	case "", "unchanged":
	case "actor-deleted-write-directory":
		if err := deleteMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"users", before.Users[0].ID}); err != nil {
			t.Fatal(err)
		}
	case "actor-merged", "actor-conflict":
		user := scaleP21SW69OwnedActor(before.Users[0])
		user.JobTitle = "Canonical actor job title"
		if mode == "actor-conflict" {
			user.Name = "Conflicting canonical actor name"
		}
		put("users", user.ID, user, 71)
	case "project-conflict", "label-and-project-conflicts":
		project := cloneSnapshotValue(reflect.ValueOf(before.Projects[1])).Interface().(domain.Project)
		project.IssueCount += 3
		put("projects", project.ID, project, 79)
		if mode == "label-and-project-conflicts" {
			label := before.Labels[1]
			label.IssueCount += 5
			put("labels", label.ID, label, 73)
		}
	case "display-project-deleted":
		if err := deleteMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"projects", before.Projects[1].ID}); err != nil {
			t.Fatal(err)
		}
	case "update-merged", "update-conflict":
		update := cloneSnapshotValue(reflect.ValueOf(before.ProjectUpdates["sw78-a-project"][0])).Interface().(domain.ProjectUpdate)
		update.Body = "Canonical project update body"
		put("projectUpdates", updateRecordKey("sw78-a-project", update.ID), metadataUpdate{Parent: "sw78-a-project", Item: mustMarshalSW83(t, update)}, 83)
	case "update-deleted":
		if err := deleteMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"projectUpdates", updateRecordKey("sw78-a-project", before.ProjectUpdates["sw78-a-project"][0].ID)}); err != nil {
			t.Fatal(err)
		}
	case "unknown-canonical", "gapped-order", "malformed-unchanged-rows":
		scaleP21SW78Peer(t, tx, workspace, mode, before)
	default:
		t.Fatal("unknown union peer fixture", mode)
	}
}

func mustMarshalSW83(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func scaleP21SW83MetadataWrite(query string) (bool, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	write := strings.HasPrefix(query, "insert ") || strings.HasPrefix(query, "update ") || strings.HasPrefix(query, "delete ")
	return write && (strings.Contains(query, "workspace_metadata_records") || strings.Contains(query, "workspace_states")), write && strings.HasPrefix(query, "update workspace_states ")
}

func scaleP21SW83Begin(t *testing.T, f scaleP09Fixture, before domain.Bootstrap) (*sqlTx, context.Context) {
	t.Helper()
	raw, err := f.encodeWorkspaceMetadata(before)
	if err != nil {
		t.Fatal(err)
	}
	_, shapes, _, err := splitWorkspaceMetadata(raw)
	if err != nil {
		t.Fatal(err)
	}
	inline := map[string]json.RawMessage{}
	var complete map[string]json.RawMessage
	if err := json.Unmarshal(raw, &complete); err != nil {
		t.Fatal(err)
	}
	seed := before
	for _, field := range []string{"projectUpdates", "initiativeUpdates"} {
		if shapes[field] != "map" {
			continue
		}
		inline[field] = complete[field]
		if field == "projectUpdates" {
			seed.ProjectUpdates = nil
		} else {
			seed.InitiativeUpdates = nil
		}
	}
	tx, ctx := scaleP21SW77Begin(t, f, seed)
	if len(inline) == 0 {
		return tx, ctx
	}
	// Invalid update IDs cannot be passed through the full writer's generic
	// map-row fallback: its project-update mention hook expects wrapped rows.
	// Seed the supported legacy root-inline representation instead.
	if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, before.Workspace.URLKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(root[metadataCollectionsKey], &manifest); err != nil {
		t.Fatal(err)
	}
	for field, value := range inline {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=?`, before.Workspace.URLKey, field).Scan(&count); err != nil || count != 0 {
			t.Fatal("inline update fixture retained synthetic map rows", field, count, err)
		}
		delete(manifest, field)
		root[field] = value
	}
	root[metadataCollectionsKey] = mustMarshalSW83(t, manifest)
	raw = mustMarshalSW83(t, root)
	if _, err := tx.ExecContext(ctx, `UPDATE workspace_states SET data=? WHERE workspace_key=?`, StripJSONNUL(raw), before.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	return tx, ctx
}

func scaleP21SW83SQLDiff(t *testing.T, got, want scaleP21SW76Metadata) {
	t.Helper()
	var changed func(string, json.RawMessage, json.RawMessage)
	changed = func(path string, a, b json.RawMessage) {
		if string(a) == string(b) {
			return
		}
		var left, right map[string]json.RawMessage
		if json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && left != nil && right != nil {
			keys := make([]string, 0, len(left)+len(right))
			for key := range left {
				keys = append(keys, key)
			}
			for key := range right {
				if _, found := left[key]; !found {
					keys = append(keys, key)
				}
			}
			slices.Sort(keys)
			for _, key := range keys {
				changed(path+"."+key, left[key], right[key])
			}
			return
		}
		var first, second []json.RawMessage
		if len(a) > 0 && len(b) > 0 && a[0] == '[' && b[0] == '[' &&
			json.Unmarshal(a, &first) == nil && json.Unmarshal(b, &second) == nil {
			if len(first) != len(second) {
				t.Logf("%s array lengths: got=%d want=%d", path, len(first), len(second))
			}
			for index := range max(len(first), len(second)) {
				var x, y json.RawMessage
				if index < len(first) {
					x = first[index]
				}
				if index < len(second) {
					y = second[index]
				}
				changed(fmt.Sprintf("%s[%d]", path, index), x, y)
			}
			return
		}
		t.Logf("%s: got=%q (%d bytes) want=%q (%d bytes)", path, a[:min(len(a), 256)], len(a), b[:min(len(b), 256)], len(b))
	}
	if got.WorkspaceID != want.WorkspaceID {
		t.Logf("workspace identity: got=%q want=%q", got.WorkspaceID, want.WorkspaceID)
	}
	changed("root", json.RawMessage(got.Root), json.RawMessage(want.Root))
	left, right := map[metadataRecordKey]scaleP21SW23MetadataRow{}, map[metadataRecordKey]scaleP21SW23MetadataRow{}
	for _, row := range got.Rows {
		left[metadataRecordKey{row.Field, row.Key}] = row
	}
	for _, row := range want.Rows {
		right[metadataRecordKey{row.Field, row.Key}] = row
	}
	keys := make([]metadataRecordKey, 0, len(left)+len(right))
	for key := range left {
		keys = append(keys, key)
	}
	for key := range right {
		if _, found := left[key]; !found {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b metadataRecordKey) int {
		if order := strings.Compare(a.field, b.field); order != 0 {
			return order
		}
		return strings.Compare(a.key, b.key)
	})
	for _, key := range keys {
		a, found := left[key]
		b, wanted := right[key]
		if found != wanted || a.Order != b.Order {
			t.Logf("row %s/%s: got present=%t order=%d want present=%t order=%d", key.field, key.key, found, a.Order, wanted, b.Order)
		}
		changed("row."+key.field+"/"+key.key, a.Data, b.Data)
	}
}

func scaleP21SW83Compare(t *testing.T, f scaleP09Fixture, view *SQLiteStore, before, after *metadataLogicalSource, peer string, releaseInput func()) int {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	scope := scaleP21SW72FullScope(t, after)
	base, err := before.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	originalTarget, err := after.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	want, err := after.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	tx, ctx := scaleP21SW83Begin(t, f, base)
	scaleP21SW83Peer(t, tx, workspace, peer, base)
	preimage := scaleP21SW76MetadataImage(t, tx, workspace)
	_, wantErr := f.writeWorkspaceMetadataDelta(ctx, tx, workspace, base, &want)
	wantSQL := scaleP21SW76MetadataImage(t, tx, workspace)
	wantRebased := maps.Clone(tx.rebasedMetadata)
	wantKeys, conflict := conflictedMetadataKeys(wantErr)
	if conflict && !reflect.DeepEqual(preimage, wantSQL) {
		t.Fatal("literal conflict fixture wrote metadata before rebasing every field")
	}
	if peer == "actor-deleted-write-directory" {
		if wantErr != nil || slices.ContainsFunc(want.Users, func(user domain.User) bool { return user.ID == base.Users[0].ID }) {
			t.Fatal("directory witness did not drop the canonical actor", wantErr)
		}
		key := metadataRecordKey{"projects", base.Projects[1].ID}
		find := func(image scaleP21SW76Metadata) []byte {
			for _, row := range image.Rows {
				if (metadataRecordKey{row.Field, row.Key}) == key {
					return row.Data
				}
			}
			return nil
		}
		if reflect.DeepEqual(find(preimage), find(wantSQL)) {
			t.Fatal("directory witness did not require display write after Users rebasing")
		}
	}
	scaleP21SW70Usable(t, tx)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	tx, ctx = scaleP21SW83Begin(t, f, base)
	scaleP21SW83Peer(t, tx, workspace, peer, base)
	if got := scaleP21SW76MetadataImage(t, tx, workspace); !reflect.DeepEqual(got, preimage) {
		t.Fatal("union oracle and bounded writer started from different SQL preimages")
	}
	peak, writes, rootWrites := 0, 0, 0
	ctx, cancel := context.WithCancel(WithSQLStatementObserver(ctx, func(query string) {
		peak = max(peak, view.MetadataWorkUsedForTesting())
		write, root := scaleP21SW83MetadataWrite(query)
		if write {
			writes++
		}
		if root {
			rootWrites++
		}
	}))
	defer cancel()
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	result, gotErr := view.persistLegacyMetadataImage(ctx, tx, workspace, before, after)
	if reflect.TypeOf(wantErr) != reflect.TypeOf(gotErr) || wantErr != nil && wantErr.Error() != gotErr.Error() {
		if result != nil {
			result.Release()
		}
		t.Fatalf("union errors differ: literal %T %v / bounded %T %v", wantErr, wantErr, gotErr, gotErr)
	}
	if gotKeys, gotConflict := conflictedMetadataKeys(gotErr); gotConflict != conflict || conflict && !reflect.DeepEqual(gotKeys, wantKeys) {
		if result != nil {
			result.Release()
		}
		t.Fatal("global field/key conflict order differs", gotKeys, wantKeys)
	}
	if got := scaleP21SW76MetadataImage(t, tx, workspace); !reflect.DeepEqual(got, wantSQL) {
		scaleP21SW83SQLDiff(t, got, wantSQL)
		if result != nil {
			result.Release()
		}
		t.Fatal("union canonical root, rows or collection orders differ")
	}
	if !reflect.DeepEqual(tx.rebasedMetadata, wantRebased) {
		t.Fatal("union rebase markers differ or temporary ID aliases leaked", tx.rebasedMetadata, wantRebased)
	}
	scaleP21SW72Compare(t, before, scope, base)
	scaleP21SW72Compare(t, after, scope, originalTarget)
	scaleP21SW70Usable(t, tx)
	if wantErr != nil {
		if result != nil {
			result.Release()
			t.Fatal("failed union returned a partial native postimage")
		}
		if conflict && writes != 0 {
			t.Fatal("union wrote metadata before collecting all conflicts", writes)
		}
		if view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
			t.Fatal("failed union leaked work or storage")
		}
		return peak
	}
	if result == nil {
		t.Fatal("successful union omitted its native postimage")
	}
	t.Cleanup(result.Release)
	scaleP21SW72Compare(t, result, scope, want)
	if result.Stamp() != after.Stamp() || view.WorkspaceMetadataDecodes() != 0 || rootWrites > 1 {
		t.Fatal("union generation, full decoding or single-root-patch contract changed", rootWrites)
	}
	for index, node := range after.fields {
		name := reflect.TypeFor[domain.Bootstrap]().Field(index).Tag.Get("json")
		switch name {
		case "-", "viewer", "users", "members", "projects", "initiatives", "labels", "projectUpdates", "initiativeUpdates", "threadSubscriptions":
			continue
		}
		if result.fields[index] != node {
			t.Fatal("union rewrote an unmodified native source field", name)
		}
	}
	releaseInput()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	cancel()
	scaleP21SW72Compare(t, result, scope, want)
	result.Release()
	result.Release()
	if _, err := result.Select(t.Context(), scope, 64<<20); !errors.Is(err, errMetadataLogicalClosed) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("released union postimage remained usable or retained work", err)
	}
	return peak
}

func TestScaleP21SW83LegacyMetadataUnionLiteral(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	for _, test := range []struct{ name, shape, peer string }{
		{name: "whole-union"},
		{name: "unchanged", shape: "unchanged"},
		{name: "actor-append", shape: "actor-append"},
		{name: "actor-append-nil-users", shape: "nil-users"},
		{name: "actor-append-empty-users", shape: "empty-users"},
		{name: "actor-merged", peer: "actor-merged"},
		{name: "actor-conflict-after-counts", peer: "actor-conflict"},
		{name: "actor-dropped-write-directory", peer: "actor-deleted-write-directory"},
		{name: "project-conflict-after-labels", peer: "project-conflict"},
		{name: "all-field-conflicts-collected", peer: "label-and-project-conflicts"},
		{name: "display-only-project-deleted", peer: "display-project-deleted"},
		{name: "unknown-root-and-canonical-rows", peer: "unknown-canonical"},
		{name: "gapped-source-orders", peer: "gapped-order"},
		{name: "malformed-unchanged-body", peer: "malformed-unchanged-rows"},
		{name: "update-merged", peer: "update-merged"},
		{name: "update-conflict", peer: "update-conflict"},
		{name: "update-deleted", peer: "update-deleted"},
		{name: "duplicate-users", shape: "duplicate-users"},
		{name: "duplicate-projects", shape: "duplicate-projects"},
		{name: "empty-project-id", shape: "empty-project-id"},
		{name: "long-project-id", shape: "long-project-id"},
		{name: "duplicate-update-id", shape: "duplicate-update-id"},
		{name: "empty-update-id", shape: "empty-update-id"},
		{name: "delete-last-thread", shape: "delete-last-thread"},
		{name: "nil-to-empty-members", shape: "nil-to-empty-members"},
		{name: "native-actor-via", shape: "native-actor-via"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := scaleP21SW83Data(f, 1000)
			switch test.shape {
			case "duplicate-users":
				data.Users = append(data.Users, domain.User{ID: data.Users[0].ID, Name: "Last duplicate directory user", JobTitle: "Last duplicate body"})
			case "duplicate-projects":
				data.Projects = append(data.Projects, data.Projects[1])
			case "empty-project-id":
				data.Projects[1].ID = ""
			case "long-project-id":
				data.Projects[1].ID = strings.Repeat("long-project", 20)
			case "duplicate-update-id":
				values := data.ProjectUpdates["sw78-a-project"]
				data.ProjectUpdates["sw78-a-project"] = append(values, values[0])
			case "empty-update-id":
				data.ProjectUpdates["sw78-a-project"][0].ID = ""
			case "delete-last-thread":
				data.ThreadSubscriptions = data.ThreadSubscriptions[:1]
			case "nil-to-empty-members":
				data.Members = nil
			}
			next := scaleP21SW83Target(data)
			if test.peer == "actor-deleted-write-directory" || test.peer == "display-project-deleted" {
				next.Projects[1].IssueCount = data.Projects[1].IssueCount
			}
			if test.peer == "update-conflict" {
				next.ProjectUpdates["sw78-a-project"][0].Body = "Conflicting callback update body"
			}
			switch test.shape {
			case "unchanged":
				next = cloneBootstrap(data)
			case "actor-append":
				actor := next.Viewer
				actor.ID = "sw83-appended-actor"
				next.Viewer = actor
				next.Users[0] = data.Users[0]
				next.Users = append(next.Users, actor)
			case "nil-users", "empty-users":
				if test.shape == "nil-users" {
					data.Users = nil
				} else {
					data.Users = []domain.User{}
				}
				next.Users = []domain.User{next.Viewer}
			case "nil-to-empty-members":
				next.Members = []domain.WorkspaceMember{}
			}
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			before, after := scaleP21SW78Capture(t, data), scaleP21SW78Capture(t, next)
			if test.shape == "native-actor-via" {
				actor := scaleP21SW69OwnedActor(next.Viewer)
				actor.Via = &domain.ActorBot{ID: "sw83-application", Type: domain.ActorBotTypeOAuthApplication, Name: "Native actor provenance", OnBehalfOfID: "person"}
				image, err := view.prepareLegacyActorImage(t.Context(), after, &actor)
				if err != nil {
					t.Fatal(err)
				}
				native := image.Source()
				if err := native.Retain(); err != nil {
					image.Close()
					t.Fatal(err)
				}
				after.Release()
				after = native
				t.Cleanup(after.Release)
				image.Close()
			}
			scaleP21SW83Compare(t, f, view, before, after, test.peer, func() { before.Release(); after.Release() })
			before.Release()
			after.Release()
			if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("union postimage or source inputs leaked storage")
			}
		})
	}
}

func TestScaleP21SW83LegacyMetadataUnionFailureRelease(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	data := scaleP21SW83Data(f, 1000)
	next := scaleP21SW83Target(data)
	before, after := scaleP21SW78Capture(t, data), scaleP21SW78Capture(t, next)
	scope := scaleP21SW72FullScope(t, after)
	for _, failure := range []string{"cancel-before", "cancel-after-start", "quota"} {
		t.Run(failure, func(t *testing.T) {
			tx, ctx := scaleP21SW83Begin(t, f, data)
			preimage := scaleP21SW76MetadataImage(t, tx, data.Workspace.URLKey)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			want := error(context.Canceled)
			if failure == "cancel-before" {
				cancel()
			}
			if failure == "cancel-after-start" {
				ctx = WithSQLStatementObserver(ctx, func(query string) {
					lower := strings.ToLower(query)
					if strings.Contains(lower, "from workspace_states") || strings.Contains(lower, "from workspace_metadata_records") {
						cancel()
					}
				})
			}
			budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
			if failure == "quota" {
				budget.limit, want = 1, ErrMetadataWorkBudget
			}
			defer func() { budget.limit = limit }()
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			result, err := view.persistLegacyMetadataImage(ctx, tx, data.Workspace.URLKey, before, after)
			budget.limit = limit
			if result != nil {
				result.Release()
				t.Fatal("failed union exposed a postimage")
			}
			if !errors.Is(err, want) || view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("failed union changed its error or leaked ownership", err)
			}
			if got := scaleP21SW76MetadataImage(t, tx, data.Workspace.URLKey); !reflect.DeepEqual(got, preimage) {
				t.Fatal("union preflight failure wrote metadata")
			}
			scaleP21SW72Compare(t, before, scope, data)
			scaleP21SW72Compare(t, after, scope, next)
			scaleP21SW70Usable(t, tx)
		})
	}
}

func TestScaleP21SW83LegacyMetadataUnionWarmReloadBodyBound(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	workspace := f.data.Workspace.URLKey
	f.db.SetMaxOpenConns(4)
	baseline := cloneBootstrap(collectionMetadata(f.data))
	userSettings := defaultUserSettings("sw78-unrelated-user")
	userSettings.UpdatedAt = time.Date(2026, 11, 14, 1, 2, 3, 456, time.UTC)
	baseline.UserSettings[userSettings.UserID] = userSettings
	preferences := defaultNotificationPreferences(userSettings.UserID)
	preferences.UpdatedAt = userSettings.UpdatedAt
	baseline.NotificationPreferences[userSettings.UserID] = preferences
	peaks := map[bool]int{}
	nonbody := map[bool]domain.Bootstrap{}
	for _, size := range []int{2000, 20000} {
		seed := scaleP21SW83Data(f, size)
		f.db.SetMaxOpenConns(4)
		err := func() error {
			restore := ForceFullMutationsForTesting(true)
			defer restore()
			return f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw83_seed", "", nil, func(data *domain.Bootstrap) error {
				*data = cloneBootstrap(baseline)
				data.Users, data.Labels, data.Projects, data.Initiatives = seed.Users, seed.Labels, seed.Projects, seed.Initiatives
				data.Members, data.ProjectUpdates, data.InitiativeUpdates, data.ThreadSubscriptions = seed.Members, seed.ProjectUpdates, seed.InitiativeUpdates, seed.ThreadSubscriptions
				data.Documents, data.Settings = seed.Documents, seed.Settings
				return nil
			})
		}()
		if err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			t.Run(fmt.Sprintf("body-%d/reload-%t", size, reload), func(t *testing.T) {
				f.db.SetMaxOpenConns(4)
				if reload {
					f.mu.Lock()
					f.forgetCopyRevisionLocked(workspace)
					f.mu.Unlock()
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
					t.Fatal(err)
				}
				before, err := view.retainMetadataLogicalSource(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				beforeOwned := true
				t.Cleanup(func() {
					if beforeOwned {
						before.Release()
					}
				})
				data, err := before.Select(t.Context(), scaleP21SW72FullScope(t, before), 64<<20)
				if err != nil {
					t.Fatal(err)
				}
				small := cloneBootstrap(data)
				body := seed.Documents[0].Content
				for _, index := range []int{0, 2} {
					if small.Labels[index].Description != body || small.Projects[index].Description != body {
						t.Fatal("union body-bound fixture lost scaled sibling bodies")
					}
					small.Labels[index].Description, small.Projects[index].Description = "", ""
				}
				if small.Users[1].JobTitle != body || small.Documents[0].Content != body || small.Initiatives[1].Description != body {
					t.Fatal("union body-bound fixture lost scaled unselected bodies")
				}
				small.Users[1].JobTitle, small.Documents[0].Content, small.Initiatives[1].Description = "", "", ""
				if previous, found := nonbody[reload]; found && !reflect.DeepEqual(small, previous) {
					t.Fatal("union body sizes changed nonbody native preimage")
				} else if !found {
					nonbody[reload] = small
				}
				after := scaleP21SW78Capture(t, scaleP21SW83Target(data))
				f.db.SetMaxOpenConns(1)
				peak := scaleP21SW83Compare(t, f, view, before, after, "", func() {
					before.Release()
					beforeOwned = false
					after.Release()
				})
				if peak == 0 {
					t.Fatal("union body-bound case observed no work admission")
				}
				if peaks[reload] == 0 {
					peaks[reload] = peak
				} else if peak != peaks[reload] {
					t.Fatal("10x unrelated bodies increased union SQL-time work", peaks[reload], peak)
				}
				t.Logf("body_size=%d reload=%t union_sql_peak=%d", size, reload, peak)
			})
		}
	}
}

func TestScaleP21SW83LegacyMetadataUnionRejectsOtherEdits(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	data := scaleP21SW83Data(f, 1000)
	before := scaleP21SW78Capture(t, data)
	for _, field := range []string{"documents", "settings"} {
		t.Run(field, func(t *testing.T) {
			next := scaleP21SW83Target(data)
			if field == "documents" {
				next.Documents[0].Title = "Outside the legacy discussion contract"
			} else {
				next.Settings["outside"] = "Unsupported metadata edit"
			}
			after := scaleP21SW78Capture(t, next)
			tx, ctx := scaleP21SW83Begin(t, f, data)
			preimage := scaleP21SW76MetadataImage(t, tx, data.Workspace.URLKey)
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			result, err := view.persistLegacyMetadataImage(ctx, tx, data.Workspace.URLKey, before, after)
			if result != nil {
				result.Release()
				t.Fatal("unsupported edit returned a partial postimage")
			}
			if !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("unsupported metadata edit was not rejected", err)
			}
			if got := scaleP21SW76MetadataImage(t, tx, data.Workspace.URLKey); !reflect.DeepEqual(got, preimage) {
				t.Fatal("unsupported union edit wrote canonical metadata")
			}
			if view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("unsupported union edit leaked")
			}
		})
	}
}

func TestScaleP21SW83LegacyMetadataUnionSelectedGrowth(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	data := scaleP21SW83Data(f, 1000)
	next := cloneBootstrap(data)
	next.Users[0].Name = "Changed requested actor"
	next.Viewer = next.Users[0]
	before, after := scaleP21SW78Capture(t, data), scaleP21SW78Capture(t, next)
	tx, ctx := scaleP21SW83Begin(t, f, data)
	fired, writes := false, 0
	var peerImage scaleP21SW76Metadata
	observed := WithSQLStatementObserver(ctx, func(query string) {
		write, _ := scaleP21SW83MetadataWrite(query)
		if write {
			writes++
		}
		if fired || !strings.Contains(query, "FROM workspace_metadata_records") ||
			!strings.Contains(query, "THEN data ELSE NULL END") || !strings.Contains(query, "collection_order") {
			return
		}
		fired = true
		grown := data.Users[0]
		grown.JobTitle = strings.Repeat("Canonical actor grew after size admission. ", 2000)
		scaleP21SW77Put(t, tx, data.Workspace.URLKey, grown.ID, grown, 0)
		peerImage = scaleP21SW76MetadataImage(t, tx, data.Workspace.URLKey)
	})
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	result, err := view.persistLegacyMetadataImage(observed, tx, data.Workspace.URLKey, before, after)
	if result != nil {
		result.Release()
		t.Fatal("post-preflight growth returned a native postimage")
	}
	if !fired || !errors.Is(err, ErrConcurrentWrite) {
		t.Fatal("actual selected growth did not request retry", fired, err)
	}
	if writes != 0 {
		t.Fatal("selected growth failed after a union metadata write", writes)
	}
	if got := scaleP21SW76MetadataImage(t, tx, data.Workspace.URLKey); !reflect.DeepEqual(got, peerImage) {
		t.Fatal("selected growth changed metadata beyond the injected peer row")
	}
	if view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("selected growth leaked union ownership")
	}
	scaleP21SW70Usable(t, tx)
}

type scaleP21SW83MapOrderNode struct {
	base    metadataLogicalNode
	reverse bool
}

func (n scaleP21SW83MapOrderNode) nodeType() reflect.Type           { return n.base.typ }
func (n scaleP21SW83MapOrderNode) logicalBase() metadataLogicalNode { return n.base }
func (n scaleP21SW83MapOrderNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return n.base.Value(ctx, limit)
}
func (n scaleP21SW83MapOrderNode) child(node metadataLogicalNode) metadataAggregateNode {
	if node.typ == reflect.TypeFor[time.Time]() || node.typ.Kind() == reflect.Slice && node.typ.Elem().Kind() == reflect.Uint8 {
		return metadataAggregateStoredNode{value: node}
	}
	switch node.typ.Kind() {
	case reflect.Struct, reflect.Slice, reflect.Map:
		return scaleP21SW83MapOrderNode{base: node, reverse: n.reverse}
	}
	return metadataAggregateStoredNode{value: node}
}
func (n scaleP21SW83MapOrderNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error { return visit(key, n.child(value)) })
}
func (n scaleP21SW83MapOrderNode) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	type entry struct {
		id         string
		key, value metadataLogicalNode
	}
	entries := []entry{}
	if err := n.base.MapEntries(ctx, func(key, value metadataLogicalNode) error {
		id, err := key.Value(ctx, 4096)
		if err == nil {
			entries = append(entries, entry{id: id.String(), key: key, value: value})
		}
		return err
	}); err != nil {
		return err
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.id, b.id) })
	if n.reverse {
		slices.Reverse(entries)
	}
	for _, item := range entries {
		if err := visit(metadataAggregateStoredNode{value: item.key}, n.child(item.value)); err != nil {
			return err
		}
	}
	return nil
}

func scaleP21SW83OrderMaps(t *testing.T, source *metadataLogicalSource, reverse bool) *metadataLogicalSource {
	t.Helper()
	result, err := source.Transform(t.Context(), func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == "settings" || name == "projects" {
			return scaleP21SW83MapOrderNode{base: node, reverse: reverse}, true, nil
		}
		return metadataAggregateStoredNode{value: node}, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(result.Release)
	source.Release()
	return result
}

func TestScaleP21SW83LegacyMetadataUnionMapOrderEquality(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	for index := range 8 {
		t.Run(fmt.Sprintf("independent-capture-%d", index), func(t *testing.T) {
			data := scaleP21SW83Data(f, 2)
			mixed := map[string]any{
				"nested": map[string]any{"z-last": int64(9007199254740993), "a-first": []any{true, nil, "literal", map[string]any{"right": float64(7), "left": []string{}}}},
				"bytes":  []byte{0, 255}, "nil-slice": []string(nil), "empty-slice": []string{}, "boolean": false,
			}
			data.Settings = map[string]any{"z-last": "unchanged tail", "middle": mixed, "a-first": map[string]any{"second": int64(19), "first": mixed}}
			data.Projects[1].Comments = []domain.Comment{{ID: "sw83-map-comment", User: data.Users[0], Body: "Unchanged comment", BodyData: mixed}}
			next := cloneBootstrap(data)
			if index%2 != 0 {
				next.Projects[1].IssueCount++
			}
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			before := scaleP21SW83OrderMaps(t, scaleP21SW78Capture(t, data), false)
			after := scaleP21SW83OrderMaps(t, scaleP21SW78Capture(t, next), true)
			keys := func(source *metadataLogicalSource) []string {
				field, _, _ := metadataLogicalField("settings")
				result := []string{}
				if err := source.fields[field].Children(t.Context(), func(key reflect.Value, _ metadataLogicalNode) error {
					result = append(result, key.String())
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				return result
			}
			left, right := keys(before), keys(after)
			if len(left) != 3 || !slices.IsSorted(left) || !slices.IsSortedFunc(right, func(a, b string) int { return strings.Compare(b, a) }) || slices.Equal(left, right) {
				t.Fatal("map-equality fixture did not force different frame key order", left, right)
			}
			projectMapKeys := func(source *metadataLogicalSource) []string {
				field, _, _ := metadataLogicalField("projects")
				result := []string{}
				err := source.fields[field].Children(t.Context(), func(index reflect.Value, project metadataLogicalNode) error {
					if index.Int() != 1 {
						return nil
					}
					comments, err := issueHotField(t.Context(), project, "Comments")
					if err != nil {
						return err
					}
					return comments.Children(t.Context(), func(_ reflect.Value, comment metadataLogicalNode) error {
						body, err := issueHotField(t.Context(), comment, "BodyData")
						if err != nil {
							return err
						}
						return body.Children(t.Context(), func(key reflect.Value, _ metadataLogicalNode) error {
							result = append(result, key.String())
							return nil
						})
					})
				})
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			projectLeft, projectRight := projectMapKeys(before), projectMapKeys(after)
			if len(projectLeft) != len(mixed) || !slices.IsSorted(projectLeft) ||
				!slices.IsSortedFunc(projectRight, func(a, b string) int { return strings.Compare(b, a) }) || slices.Equal(projectLeft, projectRight) {
				t.Fatal("nested Project comment map did not force different frame order", projectLeft, projectRight)
			}
			field, _, _ := metadataLogicalField("settings")
			first, err := before.fields[field].Value(t.Context(), 8<<20)
			if err != nil {
				t.Fatal(err)
			}
			second, err := after.fields[field].Value(t.Context(), 8<<20)
			if err != nil || !reflect.DeepEqual(first.Interface(), second.Interface()) {
				t.Fatal("map-order fixture changed semantic Settings values", err)
			}
			scaleP21SW83Compare(t, f, view, before, after, "", func() { before.Release(); after.Release() })
			before.Release()
			after.Release()
			if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("map-order union fixture leaked sources or result")
			}
		})
	}
}
