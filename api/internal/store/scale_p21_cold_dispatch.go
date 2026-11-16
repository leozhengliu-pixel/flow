package store

import (
	"context"
	"fmt"
	"strings"
)

func validateColdMetadataDispatch(consumer string) error {
	inventory, err := metadataConsumerReviews()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBoundedMetadataCoverage, err)
	}
	review, found := inventory.Consumers[consumer]
	if !found || review.Temperature != "cold" || strings.TrimSpace(review.TemperatureReason) == "" {
		return fmt.Errorf("%w: %s is not a registered cold consumer", ErrBoundedMetadataCoverage, consumer)
	}
	return nil
}

// WithColdMetadataRead lends the literal legacy store to one registered cold
// consumer. The callback owns its graph through projection and response output;
// it must not retain the store or its values, Close it, or start async work with
// it. Nested calls carrying the returned context borrow the same replica slot.
// Off mode forwards the original context and receiver literally.
func (s *SQLiteStore) WithColdMetadataRead(ctx context.Context, consumer string, run func(context.Context, *SQLiteStore) error) error {
	if run == nil {
		return ErrMetadataRecordQuery
	}
	if s.coldParent != nil {
		return s.coldParent.WithColdMetadataRead(ctx, consumer, run)
	}
	if !s.BoundedMetadataEnabled() {
		return run(ctx, s)
	}
	if err := validateColdMetadataDispatch(consumer); err != nil {
		return err
	}
	return s.withColdMetadataRead(ctx, run)
}

// WithColdMetadataMutation also lends the integrated legacy publication path.
// Event/payload select the same supported dispatch as withColdMutation; run
// must execute that dispatch on the borrowed store. Arbitrary content, scoped
// and issue writers require their own publication integration before admission.
func (s *SQLiteStore) WithColdMetadataMutation(ctx context.Context, consumer, workspace, event string, payload any, run func(context.Context, *SQLiteStore) error) error {
	if run == nil {
		return ErrMetadataRecordQuery
	}
	if s.coldParent != nil {
		return s.coldParent.WithColdMetadataMutation(ctx, consumer, workspace, event, payload, run)
	}
	if !s.BoundedMetadataEnabled() {
		return run(ctx, s)
	}
	if err := validateColdMetadataDispatch(consumer); err != nil {
		return err
	}
	if err := validateColdMetadataMutation(ctx, event, payload); err != nil {
		return err
	}
	return s.withColdMetadataRead(ctx, func(ctx context.Context, facade *SQLiteStore) error {
		if facade.coldWrite != nil {
			return run(ctx, facade)
		}
		return s.withColdMutationStore(ctx, workspace, run)
	})
}

func validateColdMetadataMutation(ctx context.Context, event string, payload any) error {
	_, scoped := mutationScopeFromContext(ctx)
	loopContract := event == "loop.output_contract" && !fullMutationsForced.Load()
	if scoped || UsesIssueRecordMutations(ctx) || projectRecordSelectionEnabled(ctx) ||
		metadataRecordVerificationEnabled(ctx) || !metadataOnlyMutation(event, payload) && !loopContract && !metadataTeamMutation(event, payload) {
		return fmt.Errorf("%w: cold mutation writer is not integrated", ErrBoundedMetadataCoverage)
	}
	return nil
}
