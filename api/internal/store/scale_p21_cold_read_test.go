package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21ColdReadFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, string) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	stamp := time.Now()
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.cold_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["cold-typed"] = map[string]any{
			"integer": int64(9007199254740993), "bytes": []byte{0, 255},
			"nil": []string(nil), "empty": []string{}, "native": stamp,
			"body": strings.Repeat("cold\x00", 600000),
		}
		data.IntegrationConnections = []domain.IntegrationConnection{{
			ID: idgen.New("p21-cold-"), Provider: "warm\x00provider",
			Scopes: nil, Channels: []string{}, CreatedAt: stamp,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	view.metadataResidency.work.limit = 1
	return f, view, workspace
}

func TestScaleP21ColdReadOffIdentity(t *testing.T) {
	repo := &SQLiteStore{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	want := errors.New("literal callback")
	called := false
	err := repo.withColdMetadataRead(ctx, func(got context.Context, source *SQLiteStore) error {
		called = true
		if got != ctx || source != repo {
			t.Fatal("off mode changed context or receiver")
		}
		return want
	})
	if !called || err != want {
		t.Fatal("off mode did not forward literal callback", err)
	}
}

func TestScaleP21ColdReadInstalledSourceAndReload(t *testing.T) {
	f, view, workspace := scaleP21ColdReadFixture(t)
	// Registry identity and default must remain independent of logical URLKey.
	physical := workspace + "\x00physical"
	view.mu.Lock()
	view.workspaces[physical] = view.workspaces[workspace]
	delete(view.workspaces, workspace)
	view.lastWorkspaceKey = physical
	view.viewer = domain.User{
		ID: "installed\x00viewer", AppScopes: []string{"read"}, AppTeamIDs: []string{},
	}
	view.mu.Unlock()
	view.metadataResidency.mu.Lock()
	view.metadataResidency.logical[physical] = view.metadataResidency.logical[workspace]
	delete(view.metadataResidency.logical, workspace)
	view.metadataResidency.mu.Unlock()
	// Use a native timestamp and attribution in the store-level viewer too.
	stamp := time.Now()
	view.viewer.OutOfOfficeUntil = &stamp
	view.viewer.Via = &domain.ActorBot{ID: "via", Name: "native"}
	for _, phase := range []string{"warm", "reloaded"} {
		want, found := f.WorkspaceMetadata(workspace)
		if !found {
			t.Fatal("owned workspace missing")
		}
		var held *SQLiteStore
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			t.Errorf("cold facade hydrated SQL during %s: %s", phase, query)
		})
		if err := view.withColdMetadataRead(ctx, func(bound context.Context, facade *SQLiteStore) error {
			held = facade
			if facade == view || facade.BoundedMetadataEnabled() || facade.db != f.db || facade.lastWorkspaceKey != physical {
				t.Fatal("facade changed DB ownership, residency or physical default")
			}
			got, found := facade.WorkspaceMetadata("")
			if !found || !reflect.DeepEqual(got, want) {
				t.Fatalf("%s facade changed full installed metadata", phase)
			}
			if _, found := facade.WorkspaceMetadata(workspace); found {
				t.Fatal("logical URLKey became a registry alias")
			}
			raw := facade.workspaces[physical]
			for index, team := range raw.Teams {
				if raw.TeamByID[team.ID] != index || team.Key != "" && raw.TeamByKey[strings.ToLower(team.Key)] != team.ID {
					t.Fatal("facade did not rebuild legacy team directory")
				}
			}
			if facade.viewer.OutOfOfficeUntil == &stamp ||
				!reflect.DeepEqual(facade.viewer, view.viewer) ||
				facade.viewer.OutOfOfficeUntil.Location() != stamp.Location() ||
				facade.viewer.Via == view.viewer.Via {
				t.Fatal("store viewer lost native value or ownership")
			}
			facade.viewer.AppScopes[0] = "changed"
			return view.withColdMetadataRead(bound, func(nested context.Context, same *SQLiteStore) error {
				if same != facade || nested != bound {
					t.Fatal("same-owner nesting did not reuse facade and context")
				}
				return nil
			})
		}); err != nil {
			t.Fatal(err)
		}
		if len(held.workspaces) != 0 || held.viewer.ID != "" || held.lastWorkspaceKey != "" || view.viewer.AppScopes[0] != "read" {
			t.Fatal("callback graph escaped or mutated owner viewer")
		}
		if phase == "warm" {
			f.mu.Lock()
			f.forgetCopyRevisionLocked(workspace)
			f.mu.Unlock()
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			view.metadataResidency.work.limit = metadataWorkBytes
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			view.metadataResidency.work.limit = 1
			// Refresh reinstalls the physical source under the normal key.
			view.mu.Lock()
			view.workspaces[physical] = view.workspaces[workspace]
			delete(view.workspaces, workspace)
			view.mu.Unlock()
			view.metadataResidency.mu.Lock()
			old := view.metadataResidency.logical[physical]
			view.metadataResidency.logical[physical] = view.metadataResidency.logical[workspace]
			delete(view.metadataResidency.logical, workspace)
			view.metadataResidency.mu.Unlock()
			old.Release()
		}
	}
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("cold read used HOT admission or full SQL decode")
	}
}

type scaleP21ColdBlockingWriter struct {
	started chan struct{}
	resume  chan struct{}
}

func (w scaleP21ColdBlockingWriter) Write(data []byte) (int, error) {
	close(w.started)
	<-w.resume
	return len(data), nil
}

func TestScaleP21ColdReadGateEncodingLifetimeAndCancellation(t *testing.T) {
	f, view, workspace := scaleP21ColdReadFixture(t)
	second := scaleP21SW0SelectedExtraWorkspace(t, f, idgen.New("p21-cold-second-")).Workspace.URLKey
	view.metadataResidency.work.limit = metadataWorkBytes
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, second); err != nil {
		t.Fatal(err)
	}
	view.metadataResidency.work.limit = 1
	view.metadataResidency.mu.Lock()
	old := view.metadataResidency.logical[workspace]
	path := old.stores[0].file.Name()
	view.metadataResidency.mu.Unlock()
	writer := scaleP21ColdBlockingWriter{make(chan struct{}), make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- view.withColdMetadataRead(t.Context(), func(ctx context.Context, facade *SQLiteStore) error {
			data, _ := facade.WorkspaceMetadata(workspace)
			return json.NewEncoder(writer).Encode(data)
		})
	}()
	select {
	case <-writer.started:
	case <-time.After(10 * time.Second):
		t.Fatal("encoding did not begin")
	}
	replacement, err := captureMetadataLogicalSource(t.Context(), f.data, copyStamp{workspaceID: f.data.Workspace.ID, revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource(workspace, replacement)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("installed source retired during callback encoding", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	queued := make(chan error, 1)
	requested := make(chan struct{})
	go func() {
		close(requested)
		queued <- view.withColdMetadataRead(ctx, func(_ context.Context, facade *SQLiteStore) error {
			_, found := facade.WorkspaceMetadata(second)
			if found {
				return errors.New("cross-workspace callback entered occupied replica slot")
			}
			return errors.New("queued callback ran")
		})
	}()
	<-requested
	select {
	case err := <-queued:
		t.Fatal("cross-workspace request did not wait for encoding", err)
	case <-time.After(25 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("queued cancellation invoked callback", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued cancellation did not return")
	}
	next := make(chan error, 1)
	go func() {
		next <- view.withColdMetadataRead(t.Context(), func(_ context.Context, facade *SQLiteStore) error {
			if _, found := facade.WorkspaceMetadata(second); !found {
				return errors.New("second workspace missing")
			}
			return nil
		})
	}()
	select {
	case err := <-next:
		t.Fatal("uncancelled second workspace bypassed encoding slot", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(writer.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("callback retained retired source pin", err)
	}
	select {
	case err := <-next:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("slot did not recover for another workspace")
	}
}

func TestScaleP21ColdReadErrorPanicAndMissingSource(t *testing.T) {
	_, view, workspace := scaleP21ColdReadFixture(t)
	view.metadataResidency.mu.Lock()
	source := view.metadataResidency.logical[workspace]
	view.metadataResidency.mu.Unlock()
	assertReleased := func() {
		t.Helper()
		source.mu.Lock()
		refs := source.refs
		source.mu.Unlock()
		if refs != 1 || len(view.metadataResidency.coldSlot) != 0 {
			t.Fatalf("cold lifetime leaked: refs=%d slot=%d", refs, len(view.metadataResidency.coldSlot))
		}
	}
	want := errors.New("callback error")
	if err := view.withColdMetadataRead(t.Context(), func(context.Context, *SQLiteStore) error { return want }); err != want {
		t.Fatal("callback error lost", err)
	}
	assertReleased()
	var held *SQLiteStore
	func() {
		defer func() {
			if recover() != "cold panic" {
				t.Error("callback panic changed")
			}
		}()
		_ = view.withColdMetadataRead(t.Context(), func(_ context.Context, facade *SQLiteStore) error {
			held = facade
			panic("cold panic")
		})
	}()
	assertReleased()
	if len(held.workspaces) != 0 || held.lastWorkspaceKey != "" {
		t.Fatal("panic retained facade graph")
	}
	view.metadataResidency.mu.Lock()
	delete(view.metadataResidency.logical, workspace)
	view.metadataResidency.mu.Unlock()
	err := view.withColdMetadataRead(t.Context(), func(context.Context, *SQLiteStore) error {
		t.Error("missing source invoked callback")
		return nil
	})
	view.metadataResidency.mu.Lock()
	view.metadataResidency.logical[workspace] = source
	view.metadataResidency.mu.Unlock()
	if !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("missing source silently fell back", err)
	}
	assertReleased()
	index, _, _ := metadataLogicalField("settings")
	original := source.fields[index]
	source.fields[index].unavailable = true
	err = view.withColdMetadataRead(t.Context(), func(context.Context, *SQLiteStore) error {
		t.Error("unavailable decoded field invoked callback")
		return nil
	})
	source.fields[index] = original
	if !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("decode failure silently fell back", err)
	}
	assertReleased()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := view.withColdMetadataRead(cancelled, func(context.Context, *SQLiteStore) error {
		t.Error("cancelled pin invoked callback")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled pin did not stop", err)
	}
	assertReleased()
}
