package store

import "context"

type commentCreationKeyContext struct{}

// WithCommentCreationKey makes a retried integration command reuse its comment
// ID inside the existing discussion transaction.
func WithCommentCreationKey(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, commentCreationKeyContext{}, id)
}

func CommentCreationKey(ctx context.Context) string {
	id, _ := ctx.Value(commentCreationKeyContext{}).(string)
	return id
}
