package main

import (
	"context"
	"net/http"
	"strings"

	"flow/api/internal/store"
)

const localStoreOutboxCapability = "local-store-outbox-v1"

// The ordinary issue editor still authorizes and validates the target on
// every attempt, including when the issue transaction finds a receipt.
func (s *server) localStoreIssueWriteContext(w http.ResponseWriter, r *http.Request, workspace string) (context.Context, bool) {
	ctx := r.Context()
	key := r.Header.Get("X-Flow-Local-Mutation")
	if key == "" {
		return ctx, true
	}
	if _, ready := s.syncV2Journal(); !ready || !localStoreIssueResumeAllowed(workspace) {
		http.NotFound(w, r)
		return ctx, false
	}
	if !uuidV4Pattern.MatchString(key) {
		writeError(w, http.StatusBadRequest, "Invalid local mutation ID")
		return ctx, false
	}
	viewer := localStoreBrowserViewer(r)
	if viewer == "" || viewer != r.Header.Get(localStoreHeader) {
		writeError(w, http.StatusForbidden, "Sync viewer changed")
		return ctx, false
	}
	access, err := s.resolveSyncAccess(r)
	if err != nil {
		writeSyncError(w, err)
		return ctx, false
	}
	if access.AuthzEpoch != r.Header.Get("X-Flow-Local-Authz-Epoch") {
		writeError(w, http.StatusForbidden, "Sync authorization changed")
		return ctx, false
	}
	return store.WithChatMutationKey(ctx, "browser:"+strings.ToLower(key)), true
}
