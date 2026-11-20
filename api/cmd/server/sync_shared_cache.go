package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"flow/api/internal/store"
)

var errSyncPageUncacheable = errors.New("sync page could not be cached")

const syncIssueHistoryKind = "issue_field_history"

func cacheableSyncPage(page store.CommittedPage) bool {
	if len(page.Transactions) == 0 {
		return false
	}
	for _, change := range page.Transactions {
		if len(change.Records) == 0 || len(change.Invalidations) != 0 {
			return false
		}
		for _, record := range change.Records {
			// Issue upserts are projected from canonical SQL. Metadata reads
			// can depend on a local installed view, and deletion events can
			// be pruned separately from the record journal.
			if record.Deleted || record.Key.Kind != "issues" && record.Key.Kind != syncIssueHistoryKind {
				return false
			}
		}
	}
	return true
}

func (s *server) sharedSyncPage(ctx context.Context, access store.EventAccess, page store.CommittedPage, after string, limit int, project func(context.Context) (syncV2ChangesResponse, error)) (syncV2ChangesResponse, error) {
	fingerprint := access.PermissionFingerprint()
	pageInput, err := json.Marshal(page)
	if err != nil || fingerprint == "" {
		return project(ctx)
	}
	version := access.SourceVersion()
	stamp := fmt.Sprintf("1.%s.%d.%d", page.Head, version.Metadata, version.Issues)
	key := s.coordinator.SharedCacheKey(access.WorkspaceKey(), "sync-page", stamp, version.WorkspaceID,
		access.AuthzEpoch, access.PrincipalID, access.CredentialScope, fingerprint, after, strconv.Itoa(limit), string(pageInput))
	var result syncV2ChangesResponse
	computed := false
	raw, _, err := s.coordinator.SharedCacheDo(ctx, key, func(ctx context.Context) ([]byte, error) {
		var err error
		result, err = project(ctx)
		computed = true
		if err != nil {
			return nil, err
		}
		for _, transaction := range result.Transactions {
			// Issue field history always produces an invalidation. Any other
			// invalidation could be a fallback for transient budget pressure.
			if len(transaction.Invalidations) != 0 && !slices.Contains(transaction.Invalidations, syncIssueHistoryKind) {
				return nil, errSyncPageUncacheable
			}
		}
		return json.Marshal(result)
	})
	if errors.Is(err, errSyncPageUncacheable) {
		if computed {
			return result, nil
		}
		return project(ctx)
	}
	if err != nil {
		return result, err
	}
	if !computed && json.Unmarshal(raw, &result) != nil {
		return project(ctx)
	}
	// Check again at delivery, including after a leader's Redis write.
	return result, s.store.ValidateEventAccess(ctx, access)
}
