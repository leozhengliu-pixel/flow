package store

import (
	"context"
	"reflect"
)

// refreshLegacyResourceCountsFromImage uses the already frozen callback image.
// No later SQL generation participates in this legacy pre-persistence pass.
func (s *SQLiteStore) refreshLegacyResourceCountsFromImage(ctx context.Context, source *metadataLogicalSource) (*metadataLogicalSource, error) {
	if s == nil || !s.BoundedMetadataEnabled() || source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	defer source.Release()
	return s.refreshLegacyResourceCountsSource(ctx, nil, "", source, nil, true)
}

func (s *SQLiteStore) countLegacyImageIssues(ctx context.Context, source *metadataLogicalSource, counts *metadataLogicalNormalizer, beginLabels func() error, addLabel func(string) error) error {
	index, _, _ := metadataLogicalField("issues")
	return source.fields[index].Children(ctx, func(_ reflect.Value, issue metadataLogicalNode) error {
		archived, err := issueHotField(ctx, issue, "ArchivedAt")
		if err != nil {
			return err
		}
		active, err := archived.isNil(ctx)
		if err != nil || !active {
			return err
		}
		project, err := issueHotField(ctx, issue, "Project")
		if err != nil {
			return err
		}
		if err := project.Children(ctx, func(_ reflect.Value, value metadataLogicalNode) error {
			return legacyImageCountID(ctx, s, value, func(id string) error {
				return counts.increment(ctx, "projects", id)
			})
		}); err != nil {
			return err
		}
		if err := beginLabels(); err != nil {
			return err
		}
		labels, err := issueHotField(ctx, issue, "Labels")
		if err != nil {
			return err
		}
		return labels.Children(ctx, func(_ reflect.Value, label metadataLogicalNode) error {
			return legacyImageCountID(ctx, s, label, func(id string) error {
				return addLabel(id)
			})
		})
	})
}

func legacyImageCountID(ctx context.Context, store *SQLiteStore, record metadataLogicalNode, visit func(string) error) error {
	id, err := issueHotField(ctx, record, "ID")
	if err != nil {
		return err
	}
	work := &ResourceRead{store: store}
	defer work.Close()
	value, err := work.node(ctx, id, false)
	if err != nil {
		return err
	}
	return visit(value.String())
}
