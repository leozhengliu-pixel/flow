package store

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

const (
	scaleP21SW191Read  = "server/loop_settings.go:getLoopConfig"
	scaleP21SW191Write = "server/loop_settings.go:updateLoopSettings"
)

func TestScaleP21SW191ColdDispatchOffAndRegistration(t *testing.T) {
	repo := &SQLiteStore{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	want := errors.New("literal result")
	for _, write := range []bool{false, true} {
		called := false
		run := func(actual context.Context, source *SQLiteStore) error {
			called = true
			if actual != ctx || source != repo {
				t.Fatal("off dispatch changed the receiver or context")
			}
			return want
		}
		var err error
		if write {
			err = repo.WithColdMetadataMutation(ctx, "unregistered", "", "unregistered", nil, run)
		} else {
			err = repo.WithColdMetadataRead(ctx, "unregistered", run)
		}
		if !called || err != want {
			t.Fatal("off dispatch changed the literal result", err)
		}
	}
	_, view, _ := scaleP21ColdReadFixture(t)
	for _, consumer := range []string{"missing", "server/issue_record_aliases.go:buildIssueRecordAuthorizationData"} {
		err := view.WithColdMetadataRead(t.Context(), consumer, func(context.Context, *SQLiteStore) error {
			t.Fatal("unregistered or HOT consumer entered the cold lifetime")
			return nil
		})
		if !errors.Is(err, ErrBoundedMetadataCoverage) || len(view.metadataResidency.coldSlot) != 0 {
			t.Fatal("invalid cold registration was admitted", consumer, err)
		}
	}
}

func TestScaleP21SW191ColdDispatchNestedAndPublication(t *testing.T) {
	f, view, workspace := scaleP21ColdReadFixture(t)
	expected, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("owned source missing")
	}
	stamp := time.Date(2026, time.November, 16, 5, 30, 0, 123, time.UTC)
	err := view.WithColdMetadataRead(t.Context(), scaleP21SW191Read, func(ctx context.Context, facade *SQLiteStore) error {
		if facade == view || facade.BoundedMetadataEnabled() || facade.db != view.db || len(view.metadataResidency.coldSlot) != 1 {
			t.Fatal("cold dispatch changed database ownership or admission")
		}
		actual, found := facade.WorkspaceMetadata(workspace)
		if !found || !reflect.DeepEqual(actual, expected) {
			t.Fatal("cold dispatch changed accepted native inputs")
		}
		if err := facade.WithColdMetadataRead(ctx, scaleP21SW191Read, func(_ context.Context, nested *SQLiteStore) error {
			if nested != facade || len(view.metadataResidency.coldSlot) != 1 {
				t.Fatal("nested facade read acquired a second lifetime")
			}
			return nil
		}); err != nil {
			return err
		}
		mutated := false
		if err := facade.MutateWorkspace(ctx, workspace, "workspace_preferences.updated", "workspace", nil, func(*domain.Bootstrap) error {
			mutated = true
			return nil
		}); !errors.Is(err, ErrBoundedMetadataCoverage) || mutated {
			t.Fatal("read-only cold facade accepted a write", err)
		}
		payload := map[string]any{"externalLoopTriggers": true}
		return facade.WithColdMetadataMutation(ctx, scaleP21SW191Write, workspace, "workspace_preferences.updated", payload, func(ctx context.Context, nested *SQLiteStore) error {
			if nested != facade || nested.coldWrite == nil || nested.writeMu.shared != &view.writeMu || len(view.metadataResidency.coldSlot) != 1 {
				t.Fatal("nested cold writer lost its shared publication lifetime")
			}
			if err := nested.MutateWorkspace(ctx, workspace, "workspace_preferences.updated", "workspace", payload, func(data *domain.Bootstrap) error {
				data.WorkspaceSettings.ExternalLoopTriggers = true
				data.WorkspaceSettings.UpdatedAt = stamp
				return nil
			}); err != nil {
				return err
			}
			return nested.WithColdMetadataRead(ctx, scaleP21SW191Read, func(_ context.Context, borrowed *SQLiteStore) error {
				current, found := borrowed.WorkspaceMetadata(workspace)
				if borrowed != facade || !found || !current.WorkspaceSettings.ExternalLoopTriggers || !current.WorkspaceSettings.UpdatedAt.Equal(stamp) {
					t.Fatal("nested cold reader lost the committed postimage")
				}
				return nil
			})
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.metadataResidency.coldSlot) != 0 || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cold dispatch retained admission after output")
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	persisted, _ := f.WorkspaceMetadata(workspace)
	if !persisted.WorkspaceSettings.ExternalLoopTriggers || !persisted.WorkspaceSettings.UpdatedAt.Equal(stamp) {
		t.Fatal("cold dispatch failed to persist its postimage")
	}
	if err := view.WithColdMetadataRead(t.Context(), scaleP21SW191Read, func(_ context.Context, facade *SQLiteStore) error {
		current, found := facade.WorkspaceMetadata(workspace)
		if !found || !current.WorkspaceSettings.ExternalLoopTriggers || !current.WorkspaceSettings.UpdatedAt.Equal(stamp) {
			t.Fatal("parent installed generation missed the cold write")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.WithColdMetadataMutation(t.Context(), scaleP21SW191Write, workspace, "issue.updated", nil, func(context.Context, *SQLiteStore) error {
		t.Fatal("unintegrated issue writer was admitted")
		return nil
	}); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("unintegrated writer changed its refusal", err)
	}
}

type scaleP21SW191WaitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *scaleP21SW191WaitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestScaleP21SW191ColdDispatchCancellationAndLifetime(t *testing.T) {
	_, view, _ := scaleP21ColdReadFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- view.WithColdMetadataRead(t.Context(), scaleP21SW191Read, func(context.Context, *SQLiteStore) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	waiting := &scaleP21SW191WaitingContext{Context: ctx, waiting: make(chan struct{})}
	second := make(chan error, 1)
	go func() {
		second <- view.WithColdMetadataRead(waiting, scaleP21SW191Read, func(context.Context, *SQLiteStore) error {
			t.Error("cancelled cold waiter entered the callback")
			return nil
		})
	}()
	<-waiting.waiting
	cancel()
	if err := <-second; !errors.Is(err, context.Canceled) || len(view.metadataResidency.coldSlot) != 1 {
		close(release)
		<-first
		t.Fatal("cancelled wait released another operation's lifetime", err)
	}
	close(release)
	if err := <-first; err != nil || len(view.metadataResidency.coldSlot) != 0 || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cold output lifetime leaked admission", err)
	}
}
