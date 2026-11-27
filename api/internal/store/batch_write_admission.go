package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// reserveIssueRecordColumns admits issueRecordValues: the data column's
// encoding and then, while it is live, the list projection's. Each is
// measured on the value encoded (the stored form, and its projection without
// the description and document content).
func reserveIssueRecordColumns(ctx context.Context, s *SQLiteStore, issue domain.Issue) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	stored := issueRecordStored(issue)
	detail, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(stored), 0)
	if err != nil {
		return nil, err
	}
	list, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(issueListProjection(stored)), 0)
	if err != nil {
		return nil, err
	}
	if detail > metadataWorkBytes()-list {
		return nil, ErrMetadataWorkBudget
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, detail+list)
	return release, err
}

// issueRecordDecodeWork bounds what json.Unmarshal allocates decoding a stored
// issue payload into typed values (domain.Issue or a narrower struct), the
// garbage it leaves included. Outside strings it charges as
// metadataAggregatePayloadJSONWork. A quoted string decodes into at most an
// unquoted copy (made only when it has escapes: its quoted length plus
// 2*utf8.UTFMax) and the resulting string or []byte, which is no longer than
// the quoted text; a json.RawMessage copies its text once. So a quoted byte
// is charged twice and each string's 128-byte opening charge covers the
// copy's slack and the string header. The generic estimate charges quoted
// bytes 16 times for the maps and interfaces of an untyped decode, which a
// typed row decode never builds from a string's bytes.
func issueRecordDecodeWork(ctx context.Context, raw []byte) (uint64, error) {
	if len(raw) > metadataWorkBytes()/2 {
		return 0, ErrMetadataWorkBudget
	}
	cost := uint64(64 << 10)
	quoted, escaped := false, false
	for i, c := range raw {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			if cost > uint64(metadataWorkBytes()) {
				return 0, ErrMetadataWorkBudget
			}
		}
		if quoted {
			cost += 2
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		cost += 16
		switch c {
		case '"':
			quoted = true
			cost += 128
		case '{', '[':
			cost += 1024
		case ':', ',':
			cost += 128
		}
	}
	if cost > uint64(metadataWorkBytes()) {
		return 0, ErrMetadataWorkBudget
	}
	return cost, ctx.Err()
}

// reserveIssueRecordDecoding admits one typed decode of a stored payload.
func reserveIssueRecordDecoding(ctx context.Context, s *SQLiteStore, raw []byte) (func(), error) {
	if s == nil || len(raw) == 0 {
		return func() {}, nil
	}
	work, err := issueRecordDecodeWork(ctx, raw)
	if err != nil {
		return nil, err
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(work))
	return release, err
}

// issueRecordComparisonAdmission owns the data-column encodings a stored-row
// comparison makes, one at a time: an encoding is admitted by its encoding
// work while it is made, and an encoding the comparison keeps is then
// charged by its own capacity. A nil admission or store admits nothing.
type issueRecordComparisonAdmission struct {
	ctx      context.Context
	store    *SQLiteStore
	encoding func()
	retained []func()
}

func (a *issueRecordComparisonAdmission) encode(issue domain.Issue) error {
	if a == nil || a.store == nil {
		return nil
	}
	a.endEncoding()
	work, err := metadataAggregatePayloadEncodingWork(a.ctx, reflect.ValueOf(issueRecordStored(issue)), 0)
	if err != nil {
		return err
	}
	_, release, err := a.store.tryAdmitAdditionalMetadataWork(a.ctx, work)
	if err != nil {
		return err
	}
	a.encoding = release
	return nil
}

// retain keeps encoded live after its encoding work has ended.
func (a *issueRecordComparisonAdmission) retain(encoded []byte) error {
	if a == nil || a.store == nil {
		return nil
	}
	_, release, err := a.store.tryAdmitAdditionalMetadataWork(a.ctx, max(cap(encoded), 1))
	if err != nil {
		return err
	}
	a.retained = append(a.retained, release)
	a.endEncoding()
	return nil
}

func (a *issueRecordComparisonAdmission) endEncoding() {
	if a.encoding != nil {
		a.encoding()
		a.encoding = nil
	}
}

func (a *issueRecordComparisonAdmission) close() {
	if a == nil {
		return
	}
	a.endEncoding()
	for _, release := range a.retained {
		release()
	}
	a.retained = nil
}

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
		if before > uint64(metadataWorkBytes()) || cost > uint64(metadataWorkBytes())-before {
			return nil, ErrMetadataWorkBudget
		}
		cost += before
	}
	if cost > uint64(metadataWorkBytes()) {
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
	if uint64(payloadWork) > uint64(metadataWorkBytes())-maximum ||
		maximum+uint64(payloadWork) > uint64(metadataWorkBytes())/2 {
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
	if maximum > metadataWorkBytes()/4 {
		return nil, ErrMetadataWorkBudget
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, max(maximum*4, 64<<10))
	return release, err
}
