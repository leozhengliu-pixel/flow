package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW58DiscussionFixture(t *testing.T, unknown bool) (scaleP09Fixture, *SQLiteStore, *metadataLogicalSource) {
	t.Helper()
	f, _, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace, nativeTime := f.data.Workspace.URLKey, time.Now()
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "threadSubscriptions"), workspace, "scale_p21.sw58_seed", "", nil, func(data *domain.Bootstrap) error {
		data.ThreadSubscriptions = []domain.ThreadSubscription{
			{ID: "sw58-target", UserID: f.data.Viewer.ID, IssueID: "sw58-issue", CommentID: "before-comment", State: "subscribed", CreatedAt: nativeTime, UpdatedAt: nativeTime},
			{ID: "sw58-untouched", UserID: f.data.Viewer.ID, IssueID: "sw58-issue", CommentID: "untouched-comment", State: "subscribed", CreatedAt: nativeTime, UpdatedAt: nativeTime},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	if unknown {
		warm, found := f.WorkspaceMetadata(workspace)
		if !found {
			t.Fatal("missing native source")
		}
		stamp, err := readMetadataStamp(t.Context(), f.db, workspace)
		if err != nil {
			t.Fatal(err)
		}
		stamp.revision = -1
		source, err := captureMetadataLogicalSource(t.Context(), warm, stamp)
		if err != nil {
			t.Fatal(err)
		}
		view.installMetadataLogicalSource(workspace, source)
	}
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Release)
	return f, view, source
}

func scaleP21SW58StageDiscussionRows(t *testing.T, f scaleP09Fixture, tx *sqlTx) {
	t.Helper()
	stamp := time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC)
	issue := domain.Issue{
		ID: "sw58-issue", Identifier: "SW58-1", Title: "staged issue",
		Team: f.data.Teams[0], Creator: f.data.Viewer, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := f.writeIssueRecord(t.Context(), tx, f.data.Workspace.URLKey, issue); err != nil {
		t.Fatal(err)
	}
	comment := domain.Comment{ID: "sw58-comment", Body: "staged comment", User: f.data.Viewer, CreatedAt: stamp}
	if err := writeContentRecord(t.Context(), tx, f.data.Workspace.URLKey, "comment", issue.ID, comment); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW58AssertDiscussionRows(t *testing.T, reader metadataRowReader, workspace string, present bool) {
	t.Helper()
	var title string
	err := reader.QueryRowContext(t.Context(), `SELECT title FROM issue_records WHERE workspace_key=? AND id='sw58-issue'`, workspace).Scan(&title)
	if !present {
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("rolled-back issue survived", err)
		}
	} else if err != nil || title != "staged issue" {
		t.Fatal("caller transaction lost its staged issue", title, err)
	}
	var raw []byte
	err = reader.QueryRowContext(t.Context(), `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='comment' AND resource_id='sw58-issue' AND id='sw58-comment'`, workspace).Scan(&raw)
	if !present {
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("rolled-back content survived", err)
		}
	} else {
		var comment domain.Comment
		if err != nil || json.Unmarshal(raw, &comment) != nil || comment.Body != "staged comment" {
			t.Fatal("caller transaction lost its staged content", err)
		}
	}
}

func scaleP21SW58DiscussionRow(t *testing.T, reader metadataRowReader, workspace string) []byte {
	t.Helper()
	var raw []byte
	err := reader.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='threadSubscriptions' AND record_key='sw58-target'`, workspace).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func scaleP21SW58AssertInstalled(t *testing.T, view *SQLiteStore, workspace string, prior *metadataLogicalSource, same bool) *metadataLogicalSource {
	t.Helper()
	current, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if (current == prior) != same {
		current.Release()
		t.Fatal("installed source changed outside the caller's install boundary")
	}
	return current
}

func TestScaleP21SW58DiscussionMetadataAtomicPublication(t *testing.T) {
	for _, mode := range []string{
		"point-update", "point-delete", "complete-field", "peer-field-merge",
		"peer-deleted", "complete-peer-merge", "unknown-source", "noop-core-declaration", "rollback", "commit-rejected",
	} {
		t.Run(mode, func(t *testing.T) {
			f, view, source := scaleP21SW58DiscussionFixture(t, mode == "unknown-source")
			workspace := f.data.Workspace.URLKey
			readScope := MetadataRecordScope{Fields: []string{"threadSubscriptions", "integrationConnections", "settings", "cycles", "userSettings"}}
			native, err := source.Select(t.Context(), readScope, 2<<20)
			if err != nil {
				t.Fatal(err)
			}
			scope := MetadataRecordScope{Write: []RecordKey{{Kind: "threadSubscriptions", ID: "sw58-target"}}}
			complete := mode == "complete-field" || mode == "complete-peer-merge"
			if complete {
				scope = MetadataRecordScope{Fields: []string{"threadSubscriptions"}}
			}
			if mode == "noop-core-declaration" {
				scope.Fields = []string{"viewer"}
			}
			before, err := source.Select(t.Context(), scope, 2<<20)
			if err != nil {
				t.Fatal(err)
			}
			next := cloneBootstrap(before)
			next.ThreadSubscriptions[0].State = "muted"
			next.ThreadSubscriptions[0].UpdatedAt = next.ThreadSubscriptions[0].UpdatedAt.Add(time.Hour)
			if mode == "point-delete" {
				next.ThreadSubscriptions = []domain.ThreadSubscription{}
			}
			if complete {
				appended := next.ThreadSubscriptions[0]
				appended.ID, appended.CommentID = "sw58-appended", "appended-comment"
				next.ThreadSubscriptions = []domain.ThreadSubscription{next.ThreadSubscriptions[0], appended}
			}
			peerMerge := mode == "peer-field-merge" || mode == "complete-peer-merge"
			if peerMerge || mode == "peer-deleted" {
				peer, err := f.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Rollback()
				key := metadataRecordKey{"threadSubscriptions", "sw58-target"}
				if mode == "peer-deleted" {
					err = deleteMetadataRecordRow(t.Context(), peer, workspace, key)
				} else {
					item := before.ThreadSubscriptions[0]
					item.CommentID = "peer-comment"
					raw, marshalErr := json.Marshal(item)
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					err = writeMetadataRecordRow(t.Context(), peer, workspace, key, metadataRecord{data: raw}, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := peer.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			canonicalBefore := scaleP21SW58DiscussionRow(t, f.db, workspace)
			immutableBefore, immutableNext := cloneBootstrap(before), cloneBootstrap(next)
			view.writeMu.Lock()
			defer view.writeMu.Unlock()
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			base, err := readMetadataStamp(t.Context(), tx, workspace)
			if err != nil {
				t.Fatal(err)
			}
			scaleP21SW58StageDiscussionRows(t, f, tx)
			view.lastWorkspaceKey = "sw58-retained-default"
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			exact := source.Stamp().revision >= 0 && source.Stamp() == base
			publication, err := view.persistDiscussionMetadata(t.Context(), tx, workspace, source, before, next, scope, base, exact)
			if err != nil || publication == nil || publication.candidate == nil {
				t.Fatal("metadata was not prepared in the borrowed transaction", err)
			}
			defer publication.Close()
			if !reflect.DeepEqual(before, immutableBefore) || !reflect.DeepEqual(next, immutableNext) {
				t.Fatal("canonical rebase mutated native callback pre/postimages")
			}
			if err := publication.Install(); !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("uncommitted metadata publication installed", err)
			}
			scaleP21SW58AssertInstalled(t, view, workspace, source, true).Release()
			scaleP21SW58AssertDiscussionRows(t, tx, workspace, true)
			rollback := mode == "rollback" || mode == "commit-rejected"
			if mode == "rollback" {
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			} else if mode == "commit-rejected" {
				injected := errors.New("sw58 rejected prepared commit")
				tx.writeErr = injected
				if err := tx.Commit(); !errors.Is(err, injected) {
					t.Fatal("prepared commit failure changed", err)
				}
			} else if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			// Commit never publishes the returned candidate on the caller's behalf.
			scaleP21SW58AssertInstalled(t, view, workspace, source, true).Release()
			scaleP21SW58AssertDiscussionRows(t, f.db, workspace, !rollback)
			if rollback {
				if err := publication.Install(); !errors.Is(err, ErrBoundedMetadataCoverage) {
					t.Fatal("failed transaction publication installed", err)
				}
				publication.Close()
				if !scaleP21SW36JSONEqual(scaleP21SW58DiscussionRow(t, f.db, workspace), canonicalBefore) ||
					metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
					t.Fatal("rollback changed canonical metadata or leaked prepared source work")
				}
			} else {
				expected := cloneBootstrap(native)
				var canonicalWanted []byte
				switch {
				case mode == "point-delete" || mode == "peer-deleted":
					expected.ThreadSubscriptions = expected.ThreadSubscriptions[1:]
				case complete:
					expected.ThreadSubscriptions = cloneBootstrap(next).ThreadSubscriptions
				default:
					expected.ThreadSubscriptions[0] = next.ThreadSubscriptions[0]
				}
				if mode != "point-delete" && mode != "peer-deleted" {
					item := next.ThreadSubscriptions[0]
					if peerMerge {
						item.CommentID = "peer-comment"
					}
					canonicalWanted, err = json.Marshal(item)
					if err != nil {
						t.Fatal(err)
					}
					if peerMerge {
						if err := json.Unmarshal(canonicalWanted, &expected.ThreadSubscriptions[0]); err != nil {
							t.Fatal(err)
						}
					}
				}
				if !scaleP21SW36JSONEqual(scaleP21SW58DiscussionRow(t, f.db, workspace), canonicalWanted) {
					t.Fatal("canonical metadata lost callback delta or peer merge")
				}
				if err := publication.Install(); err != nil {
					t.Fatal(err)
				}
				current := scaleP21SW58AssertInstalled(t, view, workspace, source, false)
				actual, selectErr := current.Select(t.Context(), readScope, 2<<20)
				stamp, stampErr := readMetadataStamp(t.Context(), f.db, workspace)
				if !exact {
					stamp.revision = -1
				}
				if selectErr != nil || stampErr != nil || current.Stamp() != stamp || !reflect.DeepEqual(actual, expected) {
					t.Fatal("installed native/merged postimage or source certainty changed", selectErr, stampErr, changedMetadataFields(expected, actual))
				}
				current.Release()
				var count int
				if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='threadSubscriptions'`, workspace).Scan(&count); err != nil || count != len(expected.ThreadSubscriptions) {
					t.Fatal("complete field deletion/append or skipped-row count changed", count, err)
				}
			}
			retained, err := source.Select(t.Context(), readScope, 2<<20)
			if err != nil || !reflect.DeepEqual(retained, native) || view.lastWorkspaceKey != "sw58-retained-default" {
				t.Fatal("publication changed retained native source or default workspace", err)
			}
			publication.Close()
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("discussion metadata persistence leaked work or decoded complete metadata")
			}
		})
	}
}

func TestScaleP21SW58DiscussionMetadataFailureLeavesCallerTransaction(t *testing.T) {
	for _, mode := range []string{"content-input", "policy-edit", "prepare-budget"} {
		t.Run(mode, func(t *testing.T) {
			f, view, source := scaleP21SW58DiscussionFixture(t, false)
			workspace := f.data.Workspace.URLKey
			scope := MetadataRecordScope{Write: []RecordKey{{Kind: "threadSubscriptions", ID: "sw58-target"}}}
			before, err := source.Select(t.Context(), scope, 2<<20)
			if err != nil {
				t.Fatal(err)
			}
			next := cloneBootstrap(before)
			next.ThreadSubscriptions[0].State = "muted"
			if mode == "content-input" {
				next.Comments = map[string][]domain.Comment{"sw58-issue": {{ID: "must-not-persist"}}}
			}
			if mode == "policy-edit" {
				next.Viewer.Name += " changed policy"
			}
			canonicalBefore := scaleP21SW58DiscussionRow(t, f.db, workspace)
			view.writeMu.Lock()
			defer view.writeMu.Unlock()
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			base, err := readMetadataStamp(t.Context(), tx, workspace)
			if err != nil {
				t.Fatal(err)
			}
			scaleP21SW58StageDiscussionRows(t, f, tx)
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			publication, err := func() (*metadataLogicalPublication, error) {
				if mode == "prepare-budget" {
					reserved := int64(metadataLogicalDescriptorLimit) - metadataLogicalDescriptorsUsed.Load()
					if !reserveMetadataLogicalBytes(&metadataLogicalDescriptorsUsed, reserved, metadataLogicalDescriptorLimit) {
						t.Fatal("failed to reserve source descriptor budget")
					}
					defer metadataLogicalDescriptorsUsed.Add(-reserved)
				}
				return view.persistDiscussionMetadata(context.Background(), tx, workspace, source, before, next, scope, base, true)
			}()
			if publication != nil {
				publication.Close()
				t.Fatal("failed metadata preparation returned a publication")
			}
			want := ErrMetadataRecordQuery
			if mode == "policy-edit" {
				want = ErrBoundedMetadataCoverage
			}
			if mode == "prepare-budget" {
				want = ErrMetadataWorkBudget
			}
			if !errors.Is(err, want) {
				t.Fatal("metadata preparation failure changed", err)
			}
			scaleP21SW58AssertDiscussionRows(t, tx, workspace, true)
			scaleP21SW58AssertInstalled(t, view, workspace, source, true).Release()
			if err := tx.Rollback(); err != nil {
				t.Fatal("helper consumed the caller's transaction", err)
			}
			scaleP21SW58AssertDiscussionRows(t, f.db, workspace, false)
			if !scaleP21SW36JSONEqual(scaleP21SW58DiscussionRow(t, f.db, workspace), canonicalBefore) ||
				metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors ||
				view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("failed preparation changed metadata or leaked candidate/source work")
			}
		})
	}
}
