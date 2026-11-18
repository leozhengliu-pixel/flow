package store

import "context"

// BorrowedWorkspaceReadHasIssues is a source discriminator, not an access
// check. A nonempty matching caller source must retain its existing path.
func (s *SQLiteStore) BorrowedWorkspaceReadHasIssues(ctx context.Context, workspace string) bool {
	snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead)
	return ok && snapshot.database == s.db &&
		(workspace == "" || workspace == snapshot.workspace) && len(snapshot.metadata.Issues) != 0
}
