package store

import "context"

type sqlStatementObserverKey struct{}

// WithSQLStatementObserver observes executed statement text without argument
// values. The observer must support concurrent calls if the context is shared.
func WithSQLStatementObserver(ctx context.Context, observe func(string)) context.Context {
	return context.WithValue(ctx, sqlStatementObserverKey{}, observe)
}

func observeSQLStatement(ctx context.Context, query string) {
	if observe, ok := ctx.Value(sqlStatementObserverKey{}).(func(string)); ok && observe != nil {
		observe(query)
	}
}
