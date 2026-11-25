package store

import (
	"context"
	"errors"
	"reflect"
)

const (
	reviewReferenceWork       = 8 << 20
	reviewReferenceBatchBytes = 2 << 20
	reviewReferenceBatchIDs   = 500
)

// InstalledReviewReferenceVisible keeps the first installed ID-or-slug match,
// then applies the caller's existing issue visibility query. It grants no
// authority and transfers no review body or retained result.
func (s *SQLiteStore) InstalledReviewReferenceVisible(ctx context.Context, workspace, ref string, query IssueRecordQuery) (bool, error) {
	// The original metadata accessor is contextless. Only issue SQL observes
	// request cancellation; an unlinked review remains visible after it.
	sourceCtx := context.WithoutCancel(ctx)
	read, found, err := s.BeginInstalledMetadataRead(sourceCtx, workspace)
	if err != nil || !found {
		return false, err
	}
	defer read.Close()
	bound, err := read.Context(sourceCtx)
	if err != nil {
		return false, err
	}
	work, release, err := s.tryAdmitAdditionalMetadataWork(bound, reviewReferenceWork)
	if err != nil {
		return false, err
	}
	defer release()
	source, err := read.retainSource(work, read.workspace)
	if err != nil {
		return false, err
	}
	defer source.Release()
	visible := false
	err = source.VisitFields(work, func(name string, collection metadataLogicalNode) error {
		if name != "reviews" {
			return nil
		}
		return collection.Children(work, func(_ reflect.Value, review metadataLogicalNode) error {
			matched := false
			var links metadataLogicalNode
			if err := review.Children(work, func(field reflect.Value, value metadataLogicalNode) error {
				switch field.String() {
				case "ID", "SlugID":
					equal, err := value.StringEqual(work, ref)
					matched = matched || equal
					return err
				case "IssueIDs":
					links = value
				}
				return nil
			}); err != nil {
				return err
			}
			if !matched {
				return nil
			}
			var err error
			visible, err = s.installedReviewIssueLinksVisible(work, ctx, links, query)
			if err != nil {
				return err
			}
			return ErrMetadataIterationDone
		})
	})
	if errors.Is(err, ErrMetadataIterationDone) {
		err = nil
	}
	return visible && err == nil, err
}

func (s *SQLiteStore) installedReviewIssueLinksVisible(ctx, queryCtx context.Context, links metadataLogicalNode, query IssueRecordQuery) (bool, error) {
	batch := make([]string, 0, reviewReferenceBatchIDs)
	batchBytes, count := 0, 0
	visible := false
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		allowed, err := s.VisibleIssueRecordIDs(queryCtx, query, batch)
		if err != nil {
			return err
		}
		visible = visible || len(allowed) > 0
		clear(batch)
		batch, batchBytes = batch[:0], 0
		return nil
	}
	err := links.Children(ctx, func(_ reflect.Value, value metadataLogicalNode) error {
		if value.Type().Kind() != reflect.String {
			return ErrMetadataRecordQuery
		}
		if len(batch) == reviewReferenceBatchIDs || value.cost > uint64(reviewReferenceBatchBytes-batchBytes) {
			if err := flush(); err != nil {
				return err
			}
		}
		owned, err := value.Value(ctx, reviewReferenceBatchBytes-batchBytes)
		if err != nil {
			return err
		}
		batch = append(batch, owned.String())
		batchBytes += int(value.cost)
		count++
		return nil
	})
	if err == nil {
		err = flush()
	}
	// Every batch must finish: a later query error overrides earlier matches.
	return err == nil && (count == 0 || visible), err
}
