package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW69OwnedActor(actor domain.User) domain.User {
	result := cloneSnapshotValue(reflect.ValueOf(actor)).Interface().(domain.User)
	if actor.Via != nil {
		result.Via = cloneSnapshotValue(reflect.ValueOf(actor.Via)).Interface().(*domain.ActorBot)
	}
	return result
}

func scaleP21SW69LiteralActorStep(before domain.Bootstrap, actor *domain.User) domain.Bootstrap {
	next := before
	next.Users = slices.Clone(before.Users)
	if actor == nil {
		return next
	}
	owned := scaleP21SW69OwnedActor(*actor)
	next.Viewer = owned
	for index := range next.Users {
		if next.Users[index].ID == owned.ID {
			next.Users[index] = owned
			return next
		}
	}
	next.Users = append(next.Users, owned)
	return next
}

func TestScaleP21SW69LegacyActorImageNative(t *testing.T) {
	instant := time.Now()
	actor := domain.User{ID: "duplicate", Name: "Complete actor", DisplayName: "Actor <display>\x00\u51c6", Username: "actor-handle",
		JobTitle: "Full actor body", Email: "actor@example.test", AvatarURL: "actor-avatar", Active: true, EmailVerified: true,
		App: true, OAuthClientID: "client", AppScopes: []string{"read", "write"}, AppTeamIDs: []string{},
		OutOfOfficeUntil: &instant, Via: &domain.ActorBot{ID: "app", Type: domain.ActorBotTypeOAuthApplication, Name: "Exact Via", OnBehalfOfID: "person"}}
	for _, test := range []struct {
		name, actorID string
		shape         string
		noActor       bool
	}{
		{name: "replace-first-duplicate", actorID: "duplicate"},
		{name: "replace-header-with-full-body", actorID: "header"},
		{name: "actor-utc-time", actorID: "duplicate", shape: "utc"},
		{name: "append-missing", actorID: "absent"},
		{name: "replace-first-empty-id", actorID: ""},
		{name: "append-nil-users", actorID: "actor", shape: "nil"},
		{name: "append-empty-users", actorID: "actor", shape: "empty"},
		{name: "append-empty-actor-id", actorID: "", shape: "nil"},
		{name: "no-actor", noActor: true},
		{name: "no-actor-nil-users", shape: "nil", noActor: true},
		{name: "no-actor-empty-users", shape: "empty", noActor: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			unused := strings.Repeat("Unrelated user/document full body. ", 10000)
			data := domain.Bootstrap{Workspace: domain.Workspace{ID: "native", URLKey: "logical"}, ViewerRole: "unchanged-role",
				Viewer: domain.User{ID: "stored-viewer", Name: "Stored viewer body", AppScopes: []string{}},
				Users: []domain.User{
					{ID: "duplicate", Name: "First duplicate", JobTitle: unused, AppScopes: nil, AppTeamIDs: []string{}},
					{ID: "header"}, {ID: "duplicate", Name: "Later duplicate", JobTitle: unused, AppScopes: []string{}},
					{ID: "", Name: "First empty ID"}, {ID: "", Name: "Later empty ID"},
				},
				Documents: []domain.Document{{ID: "unrelated", Title: "Full document", Content: unused}},
				Settings:  map[string]any{"native": map[string]any{"integer": int64(9007199254740993), "bytes": []byte{0, 255}, "nil": []string(nil), "empty": []string{}}},
			}
			if test.shape == "nil" {
				data.Users = nil
			} else if test.shape == "empty" {
				data.Users = []domain.User{}
			}
			view := &SQLiteStore{metadataResidency: newMetadataResidency()}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: "native", revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			scope := MetadataRecordScope{Fields: []string{"users", "settings"}}
			before, err := source.Select(t.Context(), scope, 64<<20)
			if err != nil {
				t.Fatal(err)
			}
			var input *domain.User
			if !test.noActor {
				value := scaleP21SW69OwnedActor(actor)
				value.ID = test.actorID
				if test.shape == "utc" {
					utc := time.Date(2026, 11, 14, 1, 2, 3, 456, time.UTC)
					value.OutOfOfficeUntil = &utc
				}
				input = &value
			}
			want := scaleP21SW69LiteralActorStep(before, input)
			queries := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
			image, err := view.prepareLegacyActorImage(ctx, source, input)
			if err != nil || image == nil || image.Source() == nil {
				t.Fatal("actor image failed", err)
			}
			defer image.Close()
			if test.noActor && image.Source() != source {
				t.Fatal("nil actor changed source identity")
			}
			if input != nil {
				input.Name, input.JobTitle = "Caller replacement", "Caller body replacement"
				input.AppScopes[0] = "caller replacement"
				*input.OutOfOfficeUntil = input.OutOfOfficeUntil.Add(time.Hour)
				input.Via.Name = "Caller Via replacement"
				if view.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("actor rewrite has no live allowance")
				}
			}
			got, err := image.Source().Select(t.Context(), scope, 64<<20)
			if err != nil || !reflect.DeepEqual(want, got) {
				t.Fatal("literal Viewer/first User update, full body, Via, native time or role differs", err)
			}
			unchanged, err := source.Select(t.Context(), scope, 64<<20)
			if err != nil || !reflect.DeepEqual(before, unchanged) {
				t.Fatal("preparing an actor image changed the supplied source", err)
			}
			for _, field := range []string{"documents", "settings", "viewerRole"} {
				index, _, _ := metadataLogicalField(field)
				if image.Source().fields[index] != source.fields[index] {
					t.Fatal("actor image rewrote an unrelated native field", field)
				}
			}
			if image.Source().Stamp() != source.Stamp() || queries != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("actor image queried SQL, hydrated metadata or changed the source stamp")
			}
			image.Close()
			image.Close()
			if image.Source() != nil || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("actor image close retained source or work")
			}
		})
	}
}

func TestScaleP21SW69LegacyActorImageOwnership(t *testing.T) {
	data := domain.Bootstrap{ViewerRole: "stored-role", Viewer: domain.User{ID: "before"}, Users: []domain.User{{ID: "before", Name: "Before"}}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"registry": {Viewer: domain.User{ID: "wrong-source"}}})
	parent, found, err := view.BeginInstalledMetadataRead(t.Context(), "registry")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer parent.Close()
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, noActor := range []bool{false, true} {
		source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
		if err != nil {
			t.Fatal(err)
		}
		before, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{"users"}}, 2<<20)
		if err != nil {
			source.Release()
			t.Fatal(err)
		}
		var actor *domain.User
		if !noActor {
			actor = &domain.User{ID: "after", Name: "After", Via: &domain.ActorBot{ID: "caller-app"}}
		}
		want := scaleP21SW69LiteralActorStep(before, actor)
		callCtx, cancel := context.WithCancel(pinned)
		image, err := view.prepareLegacyActorImage(callCtx, source, actor)
		if err != nil {
			cancel()
			source.Release()
			t.Fatal(err)
		}
		source.Release()
		held := view.MetadataWorkUsedForTesting()
		cancel()
		if got, err := image.Source().Select(context.WithoutCancel(callCtx), MetadataRecordScope{Fields: []string{"users"}}, 2<<20); err != nil || !reflect.DeepEqual(got, want) || view.MetadataWorkUsedForTesting() < held {
			image.Close()
			t.Fatal("image borrowed caller source lifetime or inherited an incidental pin", noActor, err)
		}
		image.Close()
		if !noActor {
			parent.Close()
		}
	}
	parent.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("independent actor image lifetimes leaked work")
	}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	actor := &domain.User{ID: "after", Name: "After"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, input := range []*domain.User{nil, actor} {
		if image, err := view.prepareLegacyActorImage(ctx, source, input); image != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("canceled actor image escaped or leaked", err)
		}
	}
	budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
	budget.limit = 1
	image, err := view.prepareLegacyActorImage(t.Context(), source, actor)
	budget.limit = limit
	if image != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("actor image escaped admission or leaked", err)
	}
}

func TestScaleP21SW69LegacyActorImageWarmReloadBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	expected := map[bool]int{}
	for _, size := range []int{2000, 20000} {
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "users", "documents", "settings"), workspace, "scale_p21.sw69_seed", "", nil, func(data *domain.Bootstrap) error {
			unused := strings.Repeat("Unrelated full user/document body. ", size)
			data.Users = []domain.User{
				{ID: data.Viewer.ID, Name: "First matching user", JobTitle: unused},
				{ID: "unrelated", Name: "Unrelated", JobTitle: unused, AppScopes: []string{}},
				{ID: data.Viewer.ID, Name: "Later matching user", JobTitle: unused},
			}
			data.Documents = []domain.Document{{ID: "unrelated", Content: unused, Creator: domain.User{JobTitle: unused}}}
			data.Settings["sw69-native"] = map[string]any{"integer": int64(9007199254740993), "nil": []string(nil), "empty": []string{}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			before, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{"users", "settings"}}, 64<<20)
			if err != nil {
				source.Release()
				t.Fatal(err)
			}
			when := time.Now()
			actor := &domain.User{ID: f.data.Viewer.ID, Name: "Replacement full actor", JobTitle: "Complete replacement body",
				AppScopes: []string{"read"}, AppTeamIDs: []string{}, OutOfOfficeUntil: &when, Via: &domain.ActorBot{ID: "application", Name: "Live attribution"}}
			want := scaleP21SW69LiteralActorStep(before, actor)
			queries := 0
			image, err := view.prepareLegacyActorImage(WithSQLStatementObserver(t.Context(), func(string) { queries++ }), source, actor)
			if err != nil {
				source.Release()
				t.Fatal(err)
			}
			got, err := image.Source().Select(t.Context(), MetadataRecordScope{Fields: []string{"users", "settings"}}, 64<<20)
			if err != nil || !reflect.DeepEqual(got, want) {
				image.Close()
				source.Release()
				t.Fatal("warm/reloaded source-relative actor image differs", reload, err)
			}
			held := view.MetadataWorkUsedForTesting()
			if expected[reload] == 0 {
				expected[reload] = held
			}
			if held != expected[reload] || queries != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("10x unrelated bodies increased actor work or caused SQL/full hydration", reload, held, expected[reload], queries)
			}
			image.Close()
			source.Release()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("warm/reloaded actor image leaked work")
			}
		}
	}
}
