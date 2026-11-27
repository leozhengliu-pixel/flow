package store

import (
	"context"
	"math"
)

// Request bodies larger than the general JSON limit (document content) are
// decoded under the metadata work budget, as the records they become are
// normalized under it: the server reserves the decode before it reads the
// body and releases it once the body is decoded.

// MetadataRecordBytesLimit is about the largest record, in bytes of its
// strings, one metadata operation admits: the per-record work limit (the
// process work budget, metadataRecordWorkLimit) over the work a record
// reserves per byte, eight times a decode cost of about twice its bytes.
// The server derives its default document request limit from it.
func (s *SQLiteStore) MetadataRecordBytesLimit() int64 {
	return int64(s.metadataRecordWorkLimit()) / int64(2*metadataRecordWorkFactor())
}

// AdmitRequestBodyWork reserves bytes of the process metadata work budget
// for decoding one request body and returns the release. It waits for room
// as other admitted operations do; a reservation the budget has no room for
// while other work holds it is MetadataWorkContended (retryable), one larger
// than the whole budget the plain ErrMetadataWorkBudget. The reservation is
// independent: it does not become the lease of the request's later metadata
// operations, which admit their own work. Without bounded metadata nothing is
// reserved.
func (s *SQLiteStore) AdmitRequestBodyWork(ctx context.Context, bytes int64) (func(), error) {
	if s == nil || s.metadataResidency == nil {
		return func() {}, nil
	}
	if bytes <= 0 || bytes > math.MaxInt {
		return nil, ErrMetadataWorkBudget
	}
	// The context the budget returns carries the lease; it is dropped so a
	// later operation of this request is not nested under the body's lease.
	_, release, err := s.metadataResidency.work.acquire(ctx, int(bytes))
	if err != nil {
		return nil, err
	}
	return release, nil
}
