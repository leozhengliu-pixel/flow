package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

func (r *boundedDiscussionAttempt) reserveIssueCreateCallback(ctx context.Context, data domain.Bootstrap, payload any) (func(), error) {
	before, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(data), 0)
	if err != nil {
		return nil, err
	}
	input, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(payload), 0)
	if err != nil {
		return nil, err
	}
	// The callback clones its metadata input; the payload's immutable strings
	// and content data are shared with the newly created issue. They remain
	// owned after temporary callback encoding work has been released.
	if input > metadataWorkBytes-(64<<10) || before > (metadataWorkBytes-input-(64<<10))/3 {
		return nil, ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, before*3+input+64<<10); err != nil {
		return nil, err
	}
	recipients := uint64(len(data.Users))
	var issueBytes uint64
	for _, issue := range data.Issues {
		recipients += uint64(len(issue.SubscriberIDs))
		if uint64(len(issue.ID)) > metadataWorkBytes-issueBytes {
			return nil, ErrMetadataWorkBudget
		}
		issueBytes += uint64(len(issue.ID))
	}
	fixed := uint64(reflect.TypeFor[domain.Notification]().Size())*2 +
		uint64(reflect.TypeFor[domain.NotificationDelivery]().Size())*8 + 8192
	if issueBytes > (metadataWorkBytes-fixed)/32 {
		return nil, ErrMetadataWorkBudget
	}
	fixed += issueBytes * 32
	if recipients > (metadataWorkBytes-(64<<10))/fixed {
		return nil, ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, recipients*fixed+64<<10); err != nil {
		return nil, err
	}
	return reserveIssueBatchCallbackEncoding(ctx, r.owner.store, data, payload)
}

func (r *boundedDiscussionAttempt) reserveIssueCreateEvent(ctx context.Context, payload any, entity domain.Issue) error {
	input, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(payload), 0)
	if err != nil {
		return err
	}
	output, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(entity), 0)
	if err != nil {
		return err
	}
	// The literal event retains the encoded input. Realtime decodes that
	// input and encodes it again alongside the created entity. Two input
	// encoding allowances cover the retained raw bytes and decoded tree as
	// well as that input's part of the enriched encoding; the entity owns
	// the other part of the encoding until the containing response closes.
	if uint64(output) > metadataWorkBytes-(64<<10) || uint64(input) > (metadataWorkBytes-uint64(output)-(64<<10))/2 {
		return ErrMetadataWorkBudget
	}
	return r.owner.reserve(ctx, uint64(input)*2+uint64(output)+64<<10)
}
