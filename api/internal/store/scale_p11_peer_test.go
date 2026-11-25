package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP11Stores(t *testing.T) (*SQLiteStore, *SQLiteStore, string) {
	t.Helper()
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	config := DatabaseConfig{Driver: os.Getenv("FLOW_TEST_DATABASE_DRIVER"), URL: os.Getenv("FLOW_TEST_DATABASE_URL"), MaxOpenConns: 4}
	if (config.Driver == "") != (config.URL == "") {
		t.Fatal("both database test variables are required")
	}
	if config.Driver == "" {
		config.Driver, config.Path = "sqlite", filepath.Join(t.TempDir(), "peer.db")
	} else if config.Driver == "sqlite" {
		config.Path = config.URL
	}
	writer, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	user, _, err := writer.Register(t.Context(), "P11 store owner", idgen.New("p11-store-")+"@example.test", "p11-store-password")
	if err != nil {
		t.Fatal(err)
	}
	workspace := idgen.New("p11-store-")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := writer.DeleteWorkspace(ctx, workspace); err != nil {
			t.Error(err)
		}
		if err := writer.EraseAccount(ctx, user.ID); err != nil {
			t.Error(err)
		}
	})
	if _, err := writer.CreateWorkspace(ContextWithActor(t.Context(), user), "P11 store workspace", workspace, "us"); err != nil {
		t.Fatal(err)
	}
	if err := writer.MutateWorkspace(t.Context(), workspace, "test.p11_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "p11-record", Name: "before", Description: "body", TeamIDs: []string{}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	peer, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	if err := peer.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	return writer, peer, workspace
}

func TestScaleP11StoreInstallsCommittedPageWithoutFullDecode(t *testing.T) {
	writer, peer, workspace := scaleP11Stores(t)
	state, err := peer.WorkspacePeerState(t.Context(), workspace)
	if err != nil || !state.Current {
		t.Fatalf("initial SQL copy: %+v %v", state, err)
	}
	baseline := strconv.FormatInt(state.Head, 10)
	if _, err := peer.ApplyCommittedPage(t.Context(), state.WorkspaceID, CommittedPage{Next: baseline, Head: baseline}); err != nil {
		t.Fatal(err)
	}
	if err := writer.MutateWorkspace(t.Context(), workspace, "project.updated", "p11-record", map[string]string{"name": "after"}, func(data *domain.Bootstrap) error {
		data.Projects[0].Name = "after"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, err = peer.WorkspacePeerState(t.Context(), workspace)
	if err != nil || state.Current {
		t.Fatalf("SQL authority missed an unpublished commit: %+v %v", state, err)
	}
	page, err := peer.ReadCommittedChanges(t.Context(), state.WorkspaceID, baseline, 100, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	decodes := peer.WorkspaceMetadataDecodes()
	partial := page
	partial.Transactions = slices.Clone(page.Transactions)
	if len(partial.Transactions) == 0 {
		t.Fatal("journal returned no transaction for the metadata write")
	}
	partial.Transactions[0].Records = nil
	if _, err := peer.ApplyCommittedPage(t.Context(), state.WorkspaceID, partial); !errors.Is(err, ErrPeerSnapshotRequired) {
		t.Fatalf("incomplete transaction was accepted: %v", err)
	}
	applied, err := peer.ApplyCommittedPage(t.Context(), state.WorkspaceID, page)
	if err != nil || applied != page.Next {
		t.Fatalf("apply=%s next=%s error=%v", applied, page.Next, err)
	}
	if peer.WorkspaceMetadataDecodes() != decodes {
		t.Fatal("record install decoded the full workspace")
	}
	state, err = peer.WorkspacePeerState(t.Context(), workspace)
	if err != nil || !state.Current {
		t.Fatalf("installed copy is not exact at the SQL revision: %+v %v", state, err)
	}
	want, _ := writer.WorkspaceMetadata(workspace)
	got, _ := peer.WorkspaceMetadata(workspace)
	if got.Projects[0].Name != want.Projects[0].Name || got.Projects[0].Description != want.Projects[0].Description {
		t.Fatal("installed record differs from the writer")
	}
}

func TestScaleP11StoreRejectsPartialOrUnknownPage(t *testing.T) {
	_, peer, workspace := scaleP11Stores(t)
	state, err := peer.WorkspacePeerState(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	head := strconv.FormatInt(state.Head, 10)
	for _, page := range []CommittedPage{{Next: "invalid", Head: head}, {Next: head, Head: head, HasMore: true}, {Next: head, Head: head, ResyncRequired: true}, {Next: "-1", Head: "-1"}} {
		if _, err := peer.ApplyCommittedPage(t.Context(), state.WorkspaceID, page); !errors.Is(err, ErrPeerSnapshotRequired) {
			t.Fatalf("partial/invalid page returned %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := peer.WorkspacePeerState(ctx, workspace); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled SQL authority check returned %v", err)
	}
}

func TestScaleP11AuthorityRejectsPinnedRead(t *testing.T) {
	writer, peer, workspace := scaleP11Stores(t)
	readCtx, release, err := peer.BeginWorkspaceRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := writer.MutateWorkspace(t.Context(), workspace, "project.updated", "p11-record", nil, func(data *domain.Bootstrap) error {
		data.Projects[0].Name = "committed after read snapshot"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.WorkspacePeerState(readCtx, workspace); !errors.Is(err, ErrPeerSnapshotRequired) {
		t.Fatalf("pinned SQL snapshot certified current authorization: %v", err)
	}
}

func TestScaleP11ProjectReplacementKeepsCurrentStatusReference(t *testing.T) {
	writer, peer, workspace := scaleP11Stores(t)
	old := domain.ProjectStatus{ID: "p11-status", Name: "old status"}
	if err := writer.MutateWorkspace(t.Context(), workspace, "test.p11_status", "", nil, func(data *domain.Bootstrap) error {
		data.ProjectStatuses = append(data.ProjectStatuses, old)
		data.Projects[0].Status = old
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	updated := old
	updated.Name = "current status"
	raw, err := json.Marshal(updated)
	if err != nil {
		t.Fatal(err)
	}
	// Status edits need not rewrite projects' embedded display references.
	if _, err := writer.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projectStatuses' AND record_key=?`, raw, workspace, old.ID); err != nil {
		t.Fatal(err)
	}
	if err := peer.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	state, err := peer.WorkspacePeerState(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	head := strconv.FormatInt(state.Head, 10)
	if _, err := peer.ApplyCommittedPage(t.Context(), state.WorkspaceID, CommittedPage{Next: head, Head: head}); err != nil {
		t.Fatal(err)
	}
	if err := writer.MutateWorkspace(t.Context(), workspace, "project.updated", "p11-record", nil, func(data *domain.Bootstrap) error { data.Projects[0].Name = "renamed"; return nil }); err != nil {
		t.Fatal(err)
	}
	page, err := peer.ReadCommittedChanges(t.Context(), state.WorkspaceID, head, 100, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.ApplyCommittedPage(t.Context(), state.WorkspaceID, page); err != nil {
		t.Fatal(err)
	}
	data, _ := peer.WorkspaceMetadata(workspace)
	if data.Projects[0].Status.Name != updated.Name || data.Projects[0].Name != "renamed" {
		t.Fatal("small replacement restored an obsolete embedded status")
	}
}
