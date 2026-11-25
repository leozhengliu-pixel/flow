package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21DirectoryJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestScaleP21DocumentDirectorySourcesOrdinalsAndSearch(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("p21-directory-")
	at := time.Date(2025, 3, 4, 5, 6, 7, 8, time.FixedZone("directory", 8*3600))
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.directory_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = []domain.Document{
			{ID: target, Title: "Boundary", Content: "Start " + strings.Repeat("x", 32761) + "\u03a9 \u1e9e\xff", ContentState: "state-only", ContentData: map[string]any{"native": int64(7)}, Creator: data.Viewer, UpdatedAt: at, Revisions: []domain.DocumentRevision{{ID: "history", Content: "Full history", Author: data.Viewer}}},
			{ID: target + "-second", Title: "Second", Content: "second body", Creator: data.Viewer, UpdatedAt: at},
		}
		data.Favorites = append(data.Favorites, domain.Favorite{ID: target + "-favorite", UserID: data.Viewer.ID, ResourceType: "document", ResourceID: target})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"warm", "reloaded", "canonical", "duplicate"} {
		t.Run(state, func(t *testing.T) {
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
			if state == "duplicate" {
				f.mu.Lock()
				data := f.workspaces[workspace]
				duplicate := data.Documents[0]
				duplicate.Title, duplicate.Content = "Other occurrence", "other body"
				data.Documents = append(slices.Clone(data.Documents), duplicate)
				f.workspaces[workspace] = data
				f.mu.Unlock()
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			literal, err := f.PagedWorkspaceMetadata(t.Context(), workspace, f.data.Viewer.ID)
			if err != nil {
				t.Fatal(err)
			}
			read, err := view.BeginDocumentDirectoryRead(t.Context(), workspace, f.data.Viewer.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(read.Entries) != len(literal.Documents) {
				t.Fatalf("occurrences lost: literal=%d bounded=%d", len(literal.Documents), len(read.Entries))
			}
			base := view.MetadataWorkUsedForTesting()
			for index, entry := range read.Entries {
				want := literal.Documents[index]
				if entry.Document.Content != "" || entry.Document.ContentState != "" || entry.Document.ContentData != nil || entry.Document.Revisions != nil || entry.Document.Attachments != nil {
					t.Fatal("header retained body/history fields")
				}
				for _, query := range []string{"", "boundary start", "\u03c9", "\u00df", "\ufffd", "state-only", "missing", "other body"} {
					search := strings.ToLower(query)
					got, err := read.MatchSearch(t.Context(), entry.Ordinal, search)
					if err != nil || got != strings.Contains(strings.ToLower(want.Title+" "+want.Content), search) {
						t.Fatalf("search differs for occurrence %d query=%q match=%t err=%v", index, search, got, err)
					}
					if view.MetadataWorkUsedForTesting() != base {
						t.Fatal("streamed search retained work")
					}
				}
				value, err := read.Load(t.Context(), entry.Ordinal)
				if err != nil {
					t.Fatal(err)
				}
				if scaleP21DirectoryJSON(t, want) != scaleP21DirectoryJSON(t, value.Document) || value.RevisionError != nil {
					t.Fatal("complete occurrence differs from literal source", value.RevisionError)
				}
				if view.MetadataWorkUsedForTesting() <= base {
					t.Fatal("complete document has no output owner")
				}
				if value.Document.ContentData != nil {
					value.Document.ContentData["changed"] = true
				}
				value.Close()
				if view.MetadataWorkUsedForTesting() != base {
					t.Fatal("document admission was not released between occurrences")
				}
				again, err := read.Load(t.Context(), entry.Ordinal)
				if err != nil || scaleP21DirectoryJSON(t, again.Document) != scaleP21DirectoryJSON(t, want) {
					t.Fatal("mutable output escaped into the immutable directory source", err)
				}
				again.Close()
			}
			read.Close()
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("directory leaked admission or decoded full metadata")
			}
		})
	}
}

func TestScaleP21DocumentDirectoryBodyGrowthAndFailure(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("p21-directory-growth-")
	held := 0
	for _, size := range []int{16 << 10, 160 << 10} {
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.directory_growth", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = []domain.Document{{ID: target, Title: "Stable", Content: strings.Repeat("x", size), ContentState: strings.Repeat("y", size), ContentData: map[string]any{"body": strings.Repeat("z", size)}, Creator: data.Viewer, Revisions: []domain.DocumentRevision{{ID: "history", Content: strings.Repeat("h", size), Author: data.Viewer}}}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		read, err := view.BeginDocumentDirectoryRead(t.Context(), workspace, f.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		base := view.MetadataWorkUsedForTesting()
		if held != 0 && base != held {
			t.Fatalf("header work grew with bodies: %d -> %d", held, base)
		}
		held = base
		value, err := read.Load(t.Context(), read.Entries[0].Ordinal)
		if err != nil || len(value.Document.Content) != size {
			t.Fatal("large body lost", err)
		}
		value.Close()
		limit := view.metadataResidency.work.limit
		view.metadataResidency.work.limit = base + 1
		_, err = read.Load(t.Context(), read.Entries[0].Ordinal)
		view.metadataResidency.work.limit = limit
		if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != base {
			t.Fatal("failed body admission leaked output", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := read.Load(ctx, read.Entries[0].Ordinal); !errors.Is(err, context.Canceled) {
			t.Fatal("load ignored cancellation", err)
		}
		if _, err := read.MatchSearch(ctx, read.Entries[0].Ordinal, "z"); !errors.Is(err, context.Canceled) {
			t.Fatal("search ignored cancellation", err)
		}
		value, err = read.Load(t.Context(), read.Entries[0].Ordinal)
		if err != nil {
			t.Fatal(err)
		}
		read.Close()
		if view.MetadataWorkUsedForTesting() == 0 || len(value.Document.Content) != size {
			t.Fatal("closing the directory released a live document output")
		}
		value.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("directory leaked work after failure")
		}
		view.releaseMetadataLogicalSources()
		t.Logf("body_bytes=%d header_work=%d", size, base)
	}
}

func TestScaleP21DocumentDirectoryRevisionBodies(t *testing.T) {
	for _, mode := range []string{"large", "missing", "invalid-json", "bad-gzip", "empty-key", "cache"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			f := scaleP09Database(t)
			workspace, target := f.data.Workspace.URLKey, idgen.New("p21-directory-revisions-")
			key := target + "-body"
			if mode == "empty-key" {
				key = ""
			}
			body := []byte(`{"content":"` + strings.Repeat("full history ", 6000) + `","contentState":"state","contentData":{"rows":[{},{},{}]}}`)
			stored := gzipBody(body)
			if mode == "invalid-json" {
				stored = gzipBody([]byte(`{"content":`))
			}
			if mode == "bad-gzip" {
				stored = []byte("corrupt gzip")
			}
			if mode != "missing" {
				if _, err := f.db.ExecContext(t.Context(), `INSERT INTO metadata_blobs(workspace_key,blob_key,size,data,created_at,orphaned_at) VALUES(?,?,?,?,0,0)`, workspace, key, 1, stored); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "cache" {
				f.db.content.cache.put(workspace+"/"+key, body)
			}
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.directory_revisions", "", nil, func(data *domain.Bootstrap) error {
				data.Documents = []domain.Document{{ID: target, Creator: data.Viewer, Revisions: []domain.DocumentRevision{
					{ID: "inline", Content: "Original", Author: data.Viewer},
					{ID: "offloaded", Author: data.Viewer, Offloaded: domain.OffloadedContent{"body": {Key: key, Size: 1}}},
					{ID: "duplicate", Author: data.Viewer, Offloaded: domain.OffloadedContent{"body": {Key: key, Size: 1}}},
				}}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			want, err := f.PagedWorkspaceMetadata(t.Context(), workspace, f.data.Viewer.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantDoc := want.Documents[0]
			wantDoc.Revisions = slices.Clone(wantDoc.Revisions)
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			read, err := view.BeginDocumentDirectoryRead(t.Context(), workspace, f.data.Viewer.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			base := view.MetadataWorkUsedForTesting()
			value, err := read.Load(t.Context(), read.Entries[0].Ordinal)
			if err != nil {
				t.Fatal(err)
			}
			legacyErr := f.LoadDocumentRevisionBodies(t.Context(), workspace, wantDoc.Revisions)
			if scaleP21DirectoryJSON(t, wantDoc) != scaleP21DirectoryJSON(t, value.Document) || (legacyErr == nil) != (value.RevisionError == nil) {
				t.Fatalf("revision behavior differs: literal=%v bounded=%v", legacyErr, value.RevisionError)
			}
			if mode == "large" && view.MetadataWorkUsedForTesting() <= base+len(body)*16 {
				t.Fatal("actual inflated body was not admitted")
			}
			value.Close()
			if mode == "large" {
				view.db.content.cache.remove(workspace + "/" + key)
				before, _ := view.OffloadedContentReads()
				limit := view.metadataResidency.work.limit
				view.metadataResidency.work.limit = base + (1 << 20)
				_, err := read.Load(t.Context(), read.Entries[0].Ordinal)
				view.metadataResidency.work.limit = limit
				after, _ := view.OffloadedContentReads()
				if !errors.Is(err, ErrMetadataWorkBudget) || after <= before || view.MetadataWorkUsedForTesting() != base {
					t.Fatal("actual inflated streaming body bypassed admission or leaked work", err)
				}
			}
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("revision output leaked work")
			}
		})
	}
}

func TestScaleP21DocumentDirectoryBorrowedSnapshotAndRevocation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("p21-directory-snapshot-")
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.directory_snapshot", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = []domain.Document{{ID: target, Title: "Original", Creator: data.Viewer}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := f.BeginWorkspaceRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	f.mu.Lock()
	originalSource := f.workspaces[workspace]
	duplicateSource := originalSource
	duplicate := duplicateSource.Documents[0]
	duplicate.Title = "Duplicate installed occurrence"
	duplicateSource.Documents = append(slices.Clone(duplicateSource.Documents), duplicate)
	f.workspaces[workspace] = duplicateSource
	f.mu.Unlock()
	certified, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	certifiedRead, err := certified.BeginDocumentDirectoryRead(ctx, workspace, f.data.Viewer.ID, nil)
	if err != nil || len(certifiedRead.Entries) != 1 || certifiedRead.Entries[0].Document.Title != "Original" {
		t.Fatal("duplicate certified source did not retain the literal caller-snapshot fallback", err)
	}
	certifiedRead.Close()
	certified.releaseMetadataLogicalSources()
	f.mu.Lock()
	f.workspaces[workspace] = originalSource
	f.mu.Unlock()
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.directory_later", "", nil, func(data *domain.Bootstrap) error {
		data.Documents[0].Title = "Later"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	f.db.SetMaxOpenConns(1)
	read, err := view.BeginDocumentDirectoryRead(ctx, workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := read.Load(ctx, read.Entries[0].Ordinal)
	if err != nil || value.Document.Title != "Original" {
		t.Fatal("borrowed directory escaped its snapshot", err)
	}
	value.Close()
	read.Close()
	var one int
	if err := ctx.Value(workspaceReadKey{}).(workspaceRead).reader.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatal("directory closed caller's transaction", err)
	}
	release()
	f.db.SetMaxOpenConns(4)
	read, err = view.BeginDocumentDirectoryRead(t.Context(), workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := read.Load(t.Context(), read.Entries[0].Ordinal); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("queued directory retained revoked authority", err)
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) {
		t.Fatal("revoked directory retained output")
	}
}
