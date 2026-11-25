package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

type issueRecordWriteAdmissionKey struct{}

// withIssueRecordWriteAdmission scopes temporary encoding work to the row or
// SQL argument batch that owns it. Callback inputs and outputs remain charged
// by their attempt owner independently.
func withIssueRecordWriteAdmission(ctx context.Context, s *SQLiteStore) context.Context {
	return context.WithValue(ctx, issueRecordWriteAdmissionKey{}, s)
}

func issueRecordWriteAdmission(ctx context.Context) *SQLiteStore {
	s, _ := ctx.Value(issueRecordWriteAdmissionKey{}).(*SQLiteStore)
	return s
}

func reserveIssueRecordEncoding(ctx context.Context, s *SQLiteStore, issue domain.Issue, previous []byte) (func(), error) {
	return reserveIssueRecordEncodingEnvelope(ctx, s, issue, previous, false)
}

// The row still owns its initial column envelope. Admit only the additional
// comparison work; both owners together retain the original complete bound.
func reserveIssueRecordComparisonEncoding(ctx context.Context, s *SQLiteStore, issue domain.Issue, previous []byte) (func(), error) {
	return reserveIssueRecordEncodingEnvelope(ctx, s, issue, previous, true)
}

func reserveIssueRecordEncodingEnvelope(ctx context.Context, s *SQLiteStore, issue domain.Issue, previous []byte, columnsHeld bool) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	work, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(issue), 0)
	if err != nil {
		return nil, err
	}
	// Column construction owns its detail and list projections. Without an
	// old row there is no decoded equality/refresh phase. Callers supplying
	// nil must separately own any previous row they later discover.
	cost := uint64(work) * 2
	if len(previous) > 0 {
		// Equality encodes the stored and refreshed display projections
		// while the new columns remain live.
		cost = uint64(work) * 4
		before, err := metadataAggregatePayloadJSONWork(ctx, previous)
		if err != nil {
			return nil, err
		}
		if before > metadataWorkBytes || cost > metadataWorkBytes-before {
			return nil, ErrMetadataWorkBudget
		}
		cost += before
	}
	if cost > metadataWorkBytes {
		return nil, ErrMetadataWorkBudget
	}
	if columnsHeld {
		cost -= uint64(work) * 2
	}
	if cost == 0 {
		return func() {}, nil
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	return release, err
}

// A batch callback marshals one before/after issue pair at a time. Its
// mutable containers are already owned by the callback reservation; encoding
// the whole issue slice here would retain every issue's temporary buffers.
func reserveIssueBatchCallbackEncoding(ctx context.Context, s *SQLiteStore, data domain.Bootstrap, payload any) (func(), error) {
	payloadWork, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(payload), 0)
	if err != nil {
		return nil, err
	}
	maximum := uint64(0)
	for _, issue := range data.Issues {
		work, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(issue), 0)
		if err != nil {
			return nil, err
		}
		maximum = max(maximum, uint64(work))
	}
	// Every edited field comes from payload. Include it in both sides of
	// the live pair so an update increasing a description is admitted too.
	if uint64(payloadWork) > metadataWorkBytes-maximum ||
		maximum+uint64(payloadWork) > metadataWorkBytes/2 {
		return nil, ErrMetadataWorkBudget
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int((maximum+uint64(payloadWork))*2))
	return release, err
}

func reserveIssueBatchContentEncoding(ctx context.Context, s *SQLiteStore, data domain.Bootstrap) (func(), error) {
	maximum := 0
	visit := func(value any) error {
		work, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
		if err == nil {
			maximum = max(maximum, work)
		}
		return err
	}
	for _, items := range data.Comments {
		for _, item := range items {
			if err := visit(item); err != nil {
				return nil, err
			}
		}
	}
	for _, items := range data.Activities {
		for _, item := range items {
			if err := visit(item); err != nil {
				return nil, err
			}
		}
	}
	for _, item := range data.Notifications {
		if err := visit(item); err != nil {
			return nil, err
		}
	}
	for _, item := range data.NotificationDeliveries {
		if err := visit(item); err != nil {
			return nil, err
		}
	}
	// syncContentRecords writes one row at a time; row construction also
	// encodes the actor/identity envelope while its initial JSON is live.
	if maximum > metadataWorkBytes/4 {
		return nil, ErrMetadataWorkBudget
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, max(maximum*4, 64<<10))
	return release, err
}
