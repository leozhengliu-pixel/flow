package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW122Equal(t *testing.T, view, literal *SQLiteStore, ctx context.Context, workspace, user string, passkeys bool) *AccountCollectionRead {
	t.Helper()
	wantKeys, wantSettings, wantFound := literal.AccountCollections(workspace, user)
	if !passkeys {
		wantKeys = nil
	}
	queries := 0
	read, found, err := view.BeginAccountCollectionRead(WithSQLStatementObserver(ctx, func(string) { queries++ }), workspace, user, passkeys)
	if err != nil || found != wantFound {
		t.Fatalf("workspace selection changed: user=%q found=%v/%v err=%v", user, wantFound, found, err)
	}
	if !found {
		if read != nil || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("missing workspace retained an account read")
		}
		return nil
	}
	if read == nil || !reflect.DeepEqual(read.Passkeys, wantKeys) || !reflect.DeepEqual(read.Settings, wantSettings) {
		t.Fatalf("native account values changed for user %q passkeys=%v", user, passkeys)
	}
	if queries != 0 || view.WorkspaceMetadataDecodes() != 0 || view.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("account read consulted SQL/full metadata or lost admission", queries)
	}
	return read
}

func TestScaleP21SW122AccountCollectionNative(t *testing.T) {
	at, enabled := time.Now(), false
	originalAt := at
	large := strings.Repeat("selected legal settings ", 20000)
	settings := domain.UserSettings{
		UserID: "different-native-id", Language: "zh-CN", AgentInstructions: large,
		EnabledCodingTools: []string{"shell", "editor"}, SuggestedActionsOnComments: &enabled,
		CommitSigningKey: &domain.CommitSigningKey{Name: "original", Type: "gpg", AddedAt: at}, UpdatedAt: at,
	}
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "id", URLKey: "logical"},
		UserSettings: map[string]domain.UserSettings{
			"target": settings, "": {UserID: "", EnabledCodingTools: []string{}, UpdatedAt: at},
			"user\x00id": {UserID: "native\x00value", UpdatedAt: at},
		},
		Passkeys: []domain.Passkey{
			{ID: "duplicate", UserID: "target", Name: "first", CredentialJSON: strings.Repeat("credential ", 20000), CreatedAt: at, LastUsedAt: &at},
			{ID: "unrelated", UserID: "other", CredentialJSON: large},
			{ID: "duplicate", UserID: "target", Name: "second", CreatedAt: at},
			{ID: "", UserID: "", CreatedAt: at},
			{ID: "nul", UserID: "user\x00id", CreatedAt: at},
		},
	}
	physical := "physical\x00workspace"
	values := map[string]domain.Bootstrap{physical: data, "nil": {}, "empty": {Passkeys: []domain.Passkey{}, UserSettings: map[string]domain.UserSettings{}}}
	literal := &SQLiteStore{workspaces: values, lastWorkspaceKey: physical}
	view := scaleP21HotLogoStore(t, values)
	view.lastWorkspaceKey = physical
	for _, workspace := range []string{physical, "", "nil", "empty", "missing", "logical", physical + " "} {
		for _, user := range []string{"target", "other", "", "user\x00id", " target ", "TARGET", "missing"} {
			for _, keys := range []bool{false, true} {
				read := scaleP21SW122Equal(t, view, literal, t.Context(), workspace, user, keys)
				if read == nil {
					continue
				}
				if user == "target" && (workspace == physical || workspace == "") {
					read.Settings.CommitSigningKey.Name = "caller"
					read.Settings.EnabledCodingTools[0] = "caller"
					*read.Settings.SuggestedActionsOnComments = true
					if keys {
						*read.Passkeys[0].LastUsedAt = time.Time{}
					}
					_, current, _ := literal.AccountCollections(physical, user)
					if current.CommitSigningKey.Name != "original" || current.EnabledCodingTools[0] != "shell" || *current.SuggestedActionsOnComments {
						t.Fatal("account output exposed installed pointers")
					}
					if keys {
						currentKeys, _, _ := literal.AccountCollections(physical, user)
						if !currentKeys[0].LastUsedAt.Equal(originalAt) {
							t.Fatal("passkey output exposed installed timestamp pointers")
						}
					}
				}
				read.Close()
				read.Close()
				if read.Passkeys != nil || !reflect.DeepEqual(read.Settings, domain.UserSettings{}) ||
					read.source != nil || read.owner != nil || view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("account close retained output or admission")
				}
			}
		}
	}
	if read, _, err := (&SQLiteStore{}).BeginAccountCollectionRead(t.Context(), "", "target", true); read != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("off store enabled owned account reads", err)
	}
}

func TestScaleP21SW122AccountCollectionGeneration(t *testing.T) {
	before := domain.Bootstrap{UserSettings: map[string]domain.UserSettings{"user": {UserID: "before"}}}
	after := domain.Bootstrap{UserSettings: map[string]domain.UserSettings{"user": {UserID: "after"}}}
	other := domain.Bootstrap{UserSettings: map[string]domain.UserSettings{"user": {UserID: "other"}}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": before, "other": other})
	view.lastWorkspaceKey = "physical"
	old, _, err := view.BeginAccountCollectionRead(t.Context(), "physical", "user", false)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	parent, found, err := view.BeginInstalledMetadataRead(t.Context(), "physical")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer parent.Close()
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	source, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("physical", source)
	for _, closed := range []bool{false, true} {
		if closed {
			parent.Close()
		}
		read, found, err := view.BeginAccountCollectionRead(pinned, "physical", "user", false)
		if err != nil || !found || read.Settings.UserID != "after" {
			t.Fatal("account read inherited an incidental old/closed pin", closed, found, err)
		}
		read.Close()
	}
	view.mu.Lock()
	view.lastWorkspaceKey = "other"
	view.mu.Unlock()
	read, found, err := view.BeginAccountCollectionRead(pinned, "", "user", false)
	if err != nil || !found || read.Settings.UserID != "other" || old.Settings.UserID != "before" {
		t.Fatal("default selection or already-owned account value changed", found, err)
	}
	read.Close()
	old.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("current account selections leaked")
	}
}

func TestScaleP21SW122AccountCollectionAdmission(t *testing.T) {
	values := map[string]domain.Bootstrap{"workspace": {
		UserSettings: map[string]domain.UserSettings{"user": {UserID: "user", AgentInstructions: strings.Repeat("settings ", 10000)}},
		Passkeys:     []domain.Passkey{{ID: "key", UserID: "user", CredentialJSON: strings.Repeat("credential ", 30000)}},
	}}
	view := scaleP21HotLogoStore(t, values)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if read, _, err := view.BeginAccountCollectionRead(ctx, "workspace", "user", true); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled account read escaped", err)
	}
	read, _, err := view.BeginAccountCollectionRead(t.Context(), "workspace", "user", true)
	if err != nil {
		t.Fatal(err)
	}
	full := view.MetadataWorkUsedForTesting()
	read.Close()
	read, _, err = view.BeginAccountCollectionRead(t.Context(), "workspace", "user", false)
	if err != nil {
		t.Fatal(err)
	}
	settingsOnly := view.MetadataWorkUsedForTesting()
	read.Close()
	if settingsOnly >= full {
		t.Fatal("settings-only read loaded passkey credentials", settingsOnly, full)
	}
	budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
	for _, ceiling := range []int{1, 128 << 10, full - 1} {
		budget.limit = ceiling
		read, _, err := view.BeginAccountCollectionRead(t.Context(), "workspace", "user", true)
		if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("partial account refusal escaped or leaked", ceiling, err)
		}
	}
	budget.limit = settingsOnly
	read, _, err = view.BeginAccountCollectionRead(t.Context(), "workspace", "user", false)
	if err != nil {
		t.Fatal("credential bodies consumed settings-only quota", err)
	}
	read.Close()
	budget.limit = limit
	ctx, cancel = context.WithCancel(t.Context())
	read, _, err = view.BeginAccountCollectionRead(ctx, "workspace", "user", true)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if read.Settings.UserID != "user" || len(read.Passkeys) != 1 || view.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("cancellation released account output before its final use")
	}
	read.Close()
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 3 {
				read, found, err := view.BeginAccountCollectionRead(t.Context(), "workspace", "user", true)
				if err != nil || !found || read.Settings.UserID != "user" || len(read.Passkeys) != 1 {
					t.Error("concurrent account read differs", found, err)
					return
				}
				read.Close()
			}
		})
	}
	workers.Wait()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("account cancellation/concurrency leaked admission")
	}
}

func TestScaleP21SW122AccountCollectionWarmReloadBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, user := f.data.Workspace.URLKey, f.data.Viewer.ID
	prefix := idgen.New("sw122-")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	expected := map[bool]map[bool]int{false: {}, true: {}}
	at := time.Date(2026, 11, 15, 12, 0, 0, 123, time.UTC)
	for _, size := range []int{2000, 20000} {
		unused := strings.Repeat("Unrelated account content. ", size)
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw122_seed", "", nil, func(data *domain.Bootstrap) error {
			data.UserSettings[user] = domain.UserSettings{UserID: user, AgentInstructions: "selected", EnabledCodingTools: []string{}, UpdatedAt: at,
				CommitSigningKey: &domain.CommitSigningKey{Name: "selected", Type: "ssh", AddedAt: at}}
			data.UserSettings[prefix+"other"] = domain.UserSettings{UserID: prefix + "other", AgentInstructions: unused, UpdatedAt: at}
			data.Passkeys = []domain.Passkey{
				{ID: prefix + "first", UserID: user, Name: "first", CredentialJSON: `{"selected":true}`, CreatedAt: at, LastUsedAt: &at},
				{ID: prefix + "other", UserID: prefix + "other", Name: unused, CredentialJSON: unused, CreatedAt: at},
				{ID: prefix + "second", UserID: user, Name: "second", CreatedAt: at},
			}
			replaced := false
			for index := range data.Documents {
				if data.Documents[index].ID == prefix+"unrelated" {
					data.Documents[index].Content = unused
					replaced = true
				}
			}
			if !replaced {
				data.Documents = append(data.Documents, domain.Document{ID: prefix + "unrelated", Content: unused, Creator: data.Viewer, CreatedAt: at})
			}
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
			for _, keys := range []bool{false, true} {
				read := scaleP21SW122Equal(t, view, f.SQLiteStore, t.Context(), workspace, user, keys)
				held := view.MetadataWorkUsedForTesting()
				if expected[reload][keys] == 0 {
					expected[reload][keys] = held
				}
				if held > expected[reload][keys] {
					t.Fatal("10x unrelated content increased account work", reload, keys, held, expected[reload][keys])
				}
				t.Logf("reload=%v passkeys=%v body-scale=%d retained-work=%d SQL=0", reload, keys, size/2000, held)
				read.Close()
				if view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("database-backed account selection leaked admission")
				}
			}
		}
	}
}
