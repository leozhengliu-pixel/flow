package store

import (
	"errors"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// Every way a bounded store creates a workspace leaves it served: its
// installed logical source and resident policy image are in place before the
// creation returns, a peer store sharing the database installs it on its
// reload, and a flag-on restart's coverage check accepts it.
func TestCreatedWorkspaceIsInstalled(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	path := filepath.Join(t.TempDir(), "sw602.db")
	open := func(bounded bool) *SQLiteStore {
		t.Helper()
		value := "off"
		if bounded {
			value = "on"
		}
		t.Setenv("FLOW_BOUNDED_METADATA", value)
		repo, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path})
		if err != nil {
			t.Fatalf("open bounded=%v: %v", bounded, err)
		}
		if repo.BoundedMetadataEnabled() != bounded {
			t.Fatalf("bounded=%v store opened with bounded=%v", bounded, repo.BoundedMetadataEnabled())
		}
		return repo
	}
	ctx := t.Context()
	repo := open(false)
	user, token, err := repo.Register(ctx, "SW602 owner", idgen.New("sw602-")+"@example.test", "sw602-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.VerifyEmail(ctx, token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	actor := ContextWithActor(ctx, user)
	if _, err := repo.CreateWorkspace(actor, "SW602 legacy", "sw602-legacy", "us"); err != nil {
		t.Fatal(err)
	}
	if err := completeCatalog(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}

	repo = open(true)
	peer := open(true)
	created := map[string]domain.Bootstrap{}
	// The plain creation (request context, no COLD marker) and the seeded
	// one (onboarding's tutorial, perf-seed).
	plain, err := repo.CreateWorkspace(actor, "SW602 plain", "sw602-plain", "us")
	if err != nil {
		t.Fatal(err)
	}
	created["sw602-plain"] = plain
	seeded, err := repo.CreateWorkspaceWith(WithColdWork(actor, "sw602"), "SW602 seeded", "sw602-seeded", "us", func(data *domain.Bootstrap) error {
		data.Labels = append(data.Labels, domain.IssueLabel{ID: idgen.New("label_sw602_"), Name: "SW602 seeded label", Color: "#4CB782"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	created["sw602-seeded"] = seeded
	if _, err := repo.CreateWorkspace(actor, "SW602 again", "sw602-plain", "us"); !errors.Is(err, ErrWorkspaceKeyTaken) {
		t.Fatalf("a taken key: %v", err)
	}
	for key, data := range created {
		if !repo.InstalledWorkspaceForTesting(key) {
			t.Errorf("%s: no installed logical source after the creation", key)
		}
		stored, ok := repo.storedWorkspace(key)
		if !ok || stored.Workspace.ID != data.Workspace.ID || len(stored.Teams) != len(data.Teams) {
			t.Errorf("%s: resident image %v %+v does not hold the created workspace", key, ok, stored.Workspace)
		}
		// The resident image holds core and policy fields only, as a
		// flag-on start installs it.
		if len(stored.Labels) != 0 || len(stored.States) != 0 {
			t.Errorf("%s: the resident image holds record fields (%d labels, %d states)", key, len(stored.Labels), len(stored.States))
		}
		read, found, err := repo.BeginInstalledMetadataRead(ctx, key)
		if err != nil || !found {
			t.Fatalf("%s: installed read: %v %v", key, found, err)
		}
		read.Close()
	}
	repo.mu.RLock()
	last := repo.lastWorkspaceKey
	repo.mu.RUnlock()
	if last != "sw602-seeded" {
		t.Errorf("last workspace %q, want the one created last", last)
	}
	// A peer learns of the creation by its workspace event and reloads.
	if err := peer.ReloadAllWorkspaces(ctx); err != nil {
		t.Fatalf("peer reload: %v", err)
	}
	for key := range created {
		if !peer.InstalledWorkspaceForTesting(key) {
			t.Errorf("%s: the peer did not install the created workspace", key)
		}
	}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	// The restart's coverage check accepts the created workspaces.
	repo = open(true)
	defer repo.Close()
	for key := range created {
		if !repo.InstalledWorkspaceForTesting(key) {
			t.Errorf("%s: not installed after the restart", key)
		}
	}
}
