package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestScaleP21LogicalPublication(t *testing.T) {
	for _, exact := range []bool{true, false} {
		t.Run(map[bool]string{true: "exact", false: "unknown"}[exact], func(t *testing.T) {
			f, warm, base := scaleP21SW27WarmLogicalFixture(t)
			workspace := warm.Workspace.URLKey
			view, err := f.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			sourceStamp := base
			if !exact {
				sourceStamp.revision = -1
			}
			source, err := captureMetadataLogicalSource(t.Context(), warm, sourceStamp)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			scope := MetadataRecordScope{Write: []RecordKey{{Kind: "integrationConnections", ID: warm.IntegrationConnections[0].ID}}}
			before, err := source.Select(t.Context(), scope, 2<<20)
			if err != nil {
				t.Fatal(err)
			}
			next := cloneBootstrap(before)
			next.IntegrationConnections[0].Status = "publication-updated"
			changes, _, err := scaleP16RecordChanges(before, next)
			if err != nil {
				t.Fatal(err)
			}
			view.writeMu.Lock()
			defer view.writeMu.Unlock()
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err := scaleP16WriteChanges(t.Context(), tx, workspace, newIssueReferences(next), changes); err != nil {
				t.Fatal(err)
			}
			view.mu.Lock()
			view.lastWorkspaceKey = "retained-default"
			view.mu.Unlock()
			publication, err := view.prepareMetadataLogicalDeltaPublication(t.Context(), tx, workspace, source, before, next, scope, changes, nil, base, exact, false)
			if err != nil {
				t.Fatal(err)
			}
			defer publication.Close()
			if err := publication.Install(); !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("installed an uncommitted source", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			final, err := readMetadataStamp(t.Context(), f.db, workspace)
			if err != nil {
				t.Fatal(err)
			}
			if err := publication.Install(); err != nil {
				t.Fatal(err)
			}
			publication.Close()
			if err := publication.Install(); !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("publication transferred ownership twice", err)
			}
			installed, err := view.retainMetadataLogicalSource(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer installed.Release()
			wantStamp := final
			if !exact {
				wantStamp.revision = -1
			}
			if installed.Stamp() != wantStamp || view.lastWorkspaceKey != "retained-default" {
				t.Fatal("source certainty or default workspace changed", installed.Stamp(), wantStamp, view.lastWorkspaceKey)
			}
			readScope := MetadataRecordScope{Fields: []string{"integrationConnections", "settings", "cycles", "userSettings"}}
			actual, err := installed.Select(t.Context(), readScope, 2<<20)
			if err != nil {
				t.Fatal(err)
			}
			expected := metadataRecordSnapshot(warm, readScope, metadataRecordFields(readScope))
			expected.IntegrationConnections[0].Status = "publication-updated"
			// A selected three-way merge adopts its actual canonical postimage;
			// native values in unselected fields must remain untouched.
			if changes[0].merged {
				if err := setMetadataRecords(&expected, map[metadataRecordKey][]byte{
					{changes[0].field, changes[0].key}: changes[0].raw,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("publication changed values outside its native/merged delta: %v", changedMetadataFields(expected, actual))
			}
			old, err := source.Select(t.Context(), scope, 2<<20)
			if err != nil || !reflect.DeepEqual(old, before) {
				t.Fatal("publication changed a retained preimage", err)
			}
		})
	}
}

func TestScaleP21LogicalPublicationFailures(t *testing.T) {
	f, warm, base := scaleP21SW27WarmLogicalFixture(t)
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace := warm.Workspace.URLKey
	for _, mode := range []string{"cancelled", "shared-candidate", "rollback", "stale-base"} {
		t.Run(mode, func(t *testing.T) {
			candidate, err := captureMetadataLogicalSource(t.Context(), warm, base)
			if err != nil {
				t.Fatal(err)
			}
			defer candidate.Release()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if mode == "cancelled" {
				cancel()
			}
			if mode == "shared-candidate" {
				if err := candidate.Retain(); err != nil {
					t.Fatal(err)
				}
				defer candidate.Release()
			}
			attemptBase := base
			if mode == "stale-base" {
				attemptBase.revision--
			}
			publication, err := view.prepareMetadataLogicalPublication(ctx, tx, workspace, candidate, attemptBase, true, true)
			if mode == "cancelled" || mode == "shared-candidate" {
				want := context.Canceled
				if mode == "shared-candidate" {
					want = ErrMetadataRecordQuery
				}
				if !errors.Is(err, want) || publication != nil {
					t.Fatal("invalid preparation accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// The deferred release belongs to publication after transfer.
			if err := candidate.Retain(); err != nil {
				t.Fatal(err)
			}
			defer publication.Close()
			if mode == "rollback" {
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			} else if err := tx.Commit(); !errors.Is(err, ErrConcurrentWrite) {
				t.Fatal("incomplete logical base committed", err)
			}
			if err := publication.Install(); !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("failed transaction installed source", err)
			}
		})
	}
}
