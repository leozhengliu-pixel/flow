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
	if input > uint64(metadataWorkBytes())-(64<<10) || before > (uint64(metadataWorkBytes())-input-(64<<10))/3 {
		return nil, ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, before*3+input+64<<10); err != nil {
		return nil, err
	}
	recipients := uint64(len(data.Users))
	var issueBytes uint64
	for _, issue := range data.Issues {
		recipients += uint64(len(issue.SubscriberIDs))
		if uint64(len(issue.ID)) > uint64(metadataWorkBytes())-issueBytes {
			return nil, ErrMetadataWorkBudget
		}
		issueBytes += uint64(len(issue.ID))
	}
	fixed := uint64(reflect.TypeFor[domain.Notification]().Size())*2 +
		uint64(reflect.TypeFor[domain.NotificationDelivery]().Size())*8 + 8192
	if issueBytes > (uint64(metadataWorkBytes())-fixed)/32 {
		return nil, ErrMetadataWorkBudget
	}
	fixed += issueBytes * 32
	if recipients > (uint64(metadataWorkBytes())-(64<<10))/fixed {
		return nil, ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, recipients*fixed+64<<10); err != nil {
		return nil, err
	}
	return reserveIssueBatchCallbackEncoding(ctx, r.owner.store, data, payload)
}

// reserveIssueCreateEvent admits the event and realtime work of one attempt.
// The returned release belongs to the writer holding that attempt's event and
// realtime payload: those bytes end when the writer has published them (the
// sinks account for whatever they keep), not when the containing response
// closes. Holding them through the response would leave work that follows
// the creation in the same request (triage suggestions) without the room
// the creation no longer uses.
func (r *boundedDiscussionAttempt) reserveIssueCreateEvent(ctx context.Context, payload any, entity domain.Issue) (func(), error) {
	input, err := metadataRecordEncodingWork(ctx, reflect.ValueOf(payload))
	if err != nil {
		return nil, err
	}
	output, err := metadataRecordEncodingWork(ctx, reflect.ValueOf(entity))
	if err != nil {
		return nil, err
	}
	// The event keeps the encoded input. Realtime decodes that input,
	// encodes its members and the created entity, then assembles the
	// envelope in a single allocation. Each record allowance covers
	// copies into the envelope. The second input allowance covers decode.
	if uint64(output) > uint64(metadataWorkBytes())-(64<<10) || uint64(input) > (uint64(metadataWorkBytes())-uint64(output)-(64<<10))/2 {
		return nil, ErrMetadataWorkBudget
	}
	_, release, err := r.owner.store.tryAdmitAdditionalMetadataWork(ctx, int(uint64(input)*2+uint64(output)+64<<10))
	if err != nil {
		return nil, err
	}
	return release, nil
}
