package store

import "context"

// P19 cursors use the same shared encryption key and principal/epoch binding
// as record pages. The caller includes the query schema and collection head.
func (s *SQLiteStore) SealScaleP19Cursor(ctx context.Context, access AccessContext, kind, query, position string) (string, error) {
	if err := s.ValidateAccess(ctx, access); err != nil {
		return "", err
	}
	return sealRecordCursor(access, PageRequest{Kind: kind, Filter: query, Order: "scale-p19-v1"}, position)
}

func (s *SQLiteStore) OpenScaleP19Cursor(ctx context.Context, access AccessContext, kind, query, cursor string) (string, error) {
	if err := s.ValidateAccess(ctx, access); err != nil {
		return "", err
	}
	return openRecordCursor(access, PageRequest{Kind: kind, Filter: query, Order: "scale-p19-v1", Cursor: cursor})
}
