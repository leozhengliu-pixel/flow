package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

type discussionThreadTargetKey struct{}

// The legacy scoped callback edits native occurrences. Canonical rows still
// merge by ID, but that must not collapse duplicate installed occurrences.
func (s *SQLiteStore) prepareDiscussionThreadPublication(ctx context.Context, tx *sqlTx, workspace string, source *metadataLogicalSource, before, next []domain.ThreadSubscription, issueID string, baseStamp copyStamp, exact bool) (*metadataLogicalPublication, error) {
	candidate, err := s.discussionThreadPostimage(ctx, source, before, next, issueID)
	if err != nil {
		return nil, err
	}
	merged, err := s.persistLegacyMetadataImage(ctx, tx, workspace, source, candidate)
	candidate.Release()
	if err != nil {
		return nil, err
	}
	exact = exact && source.Stamp().revision >= 0 && source.Stamp() == baseStamp
	publication, err := s.prepareMetadataLogicalPublication(ctx, tx, workspace, merged, baseStamp, exact, false)
	if err != nil {
		merged.Release()
	}
	return publication, err
}

func (s *SQLiteStore) discussionThreadPostimage(ctx context.Context, source *metadataLogicalSource, before, next []domain.ThreadSubscription, issueID string) (*metadataLogicalSource, error) {
	if source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	if reflect.DeepEqual(before, next) {
		return source.Transform(ctx, func(_ string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
			return metadataAggregateStoredNode{value: original}, false, nil
		})
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	input := &issueHotInputs{store: s, source: source}
	defer input.close()
	if err := input.reserve(ctx, uint64(len(before))*16+4096); err != nil {
		return nil, err
	}
	read := &legacyDiscussionInputs{input: input, ResourceKey: issueID}
	index, _, _ := metadataLogicalField("threadSubscriptions")
	base := source.fields[index]
	err := base.Children(ctx, func(ordinal reflect.Value, record metadataLogicalNode) error {
		value, err := issueHotField(ctx, record, "IssueID")
		if err != nil {
			return err
		}
		selected, err := value.StringEqual(ctx, issueID)
		if err != nil || !selected {
			return err
		}
		if len(read.ThreadSubscriptionOrdinals) >= len(before) {
			return ErrBoundedMetadataCoverage
		}
		read.ThreadSubscriptionOrdinals = append(read.ThreadSubscriptionOrdinals, int(ordinal.Int()))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(read.ThreadSubscriptionOrdinals) != len(before) {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = metadataLogicalSourceScratchContext(metadataLogicalUnpinnedContext(ctx), source)
	frames, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer frames.Close()
	for _, storage := range source.stores {
		if err := frames.inheritLocations(storage); err != nil {
			return nil, err
		}
	}
	if err := frames.inheritTimestampInputs(ctx); err != nil {
		return nil, err
	}
	patch, err := read.mergeFlat(ctx, frames, "threadSubscriptions", base, reflect.ValueOf(before), reflect.ValueOf(next))
	if err != nil {
		return nil, err
	}
	return source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == "threadSubscriptions" {
			return patch, true, nil
		}
		return metadataAggregateStoredNode{value: original}, false, nil
	})
}
