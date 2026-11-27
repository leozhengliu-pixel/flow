package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"flow/api/internal/domain"
)

// visitPendingProjectProgress looks only at frame headers for projects that
// already have history, so a fully backfilled workspace needs no write.
func visitPendingProjectProgress(ctx context.Context, projects metadataLogicalNode, visit func(int, metadataLogicalNode) error) error {
	return projects.Children(ctx, func(key reflect.Value, project metadataLogicalNode) error {
		history, err := issueHotField(ctx, project, "ProgressHistory")
		if err != nil {
			return err
		}
		count, _, err := metadataLogicalCollectionSize(ctx, history)
		if err != nil || count > 0 {
			return err
		}
		return visit(int(key.Int()), project)
	})
}

func (s *SQLiteStore) backfillInstalledProjectProgress(ctx context.Context, workspace string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	read, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return err
	}
	defer read.Close()
	source := read.source
	index, _, _ := metadataLogicalField("projects")
	projects := source.fields[index]
	err = visitPendingProjectProgress(ctx, projects, func(int, metadataLogicalNode) error { return errMetadataAggregateStop })
	if !errors.Is(err, errMetadataAggregateStop) {
		return err
	}

	tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
	if err != nil {
		return err
	}
	defer tx.Rollback()
	base, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return err
	}
	if source.Stamp() != base {
		// A peer may have built a history or changed its inputs since the
		// startup load. Retry the pending check on the current generation.
		if err := tx.Rollback(); err != nil {
			return err
		}
		read.Close()
		if err := s.reloadResidentWorkspace(ctx, workspace); err != nil {
			return err
		}
		return ErrConcurrentWrite
	}
	next, err := s.stageProjectProgressBackfill(ctx, tx, workspace, source, projects)
	if err != nil || next == nil {
		return err
	}
	defer next.Release()
	actual, err := s.persistLegacyMetadataImagePlan(ctx, tx, workspace, source, next, nil, true)
	if err != nil {
		if errors.Is(err, ErrBoundedMetadataCoverage) {
			if current, stampErr := readMetadataStamp(ctx, tx, workspace); stampErr == nil && current != base {
				return ErrConcurrentWrite
			}
		}
		return err
	}
	s.mu.RLock()
	viewer := s.viewer
	s.mu.RUnlock()
	viewerRaw, err := json.Marshal(viewer)
	if err == nil {
		err = writeAccountMetadata(ctx, tx, workspace, viewerRaw)
	}
	if err != nil {
		actual.Release()
		return err
	}
	// The commit stamp refuses a peer write that raced the rebuild.
	publication, err := s.prepareMetadataLogicalPublication(ctx, tx, workspace, actual, base, true, false)
	if err != nil {
		actual.Release()
		return err
	}
	defer publication.Close()
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := publication.Install(); err != nil {
		return err
	}
	s.dropMetadataCache(ctx, workspace)
	s.mu.Lock()
	if !slices.Contains(s.startupCacheDrops, workspace) {
		s.startupCacheDrops = append(s.startupCacheDrops, workspace)
	}
	s.mu.Unlock()
	return nil
}

// Only one project's inputs and new series are live at a time. Stage changed
// histories on disk and preserve the other project fields as logical frames.
func (s *SQLiteStore) stageProjectProgressBackfill(ctx context.Context, tx *sqlTx, workspace string, source *metadataLogicalSource, projects metadataLogicalNode) (*metadataLogicalSource, error) {
	ctx = metadataLogicalSourceScratchContext(ctx, source)
	stage, err := newLegacyProgressPatches(ctx)
	if err != nil {
		return nil, err
	}
	defer stage.Close()
	index := &legacyProgressIndex{store: s, source: source, stage: stage}
	if err := index.build(ctx, "states", "teamSettings"); err != nil {
		return nil, err
	}
	values, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer values.Close()
	now := time.Now().UTC()
	changed := false
	err = visitPendingProjectProgress(ctx, projects, func(ordinal int, node metadataLogicalNode) error {
		group, err := newLegacyProgressGroup(ctx, s, source, index)
		if err != nil {
			return err
		}
		defer group.close()
		value, err := group.project(ctx, node, legacyProgressProjectFields...)
		if err != nil {
			return err
		}
		project := value.Interface().(domain.Project)
		updated, err := group.rebuildSQL(ctx, tx, workspace, &project, now)
		if err != nil || !updated {
			return err
		}
		changed = true
		return stageLegacyProgressPatch(ctx, stage, values, ordinal, project)
	})
	if err != nil || !changed {
		return nil, err
	}
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == "projects" {
			return legacyProgressCollection{base: node, patches: stage, values: values.storage}, true, nil
		}
		return metadataAggregateStoredNode{node}, false, nil
	})
}
