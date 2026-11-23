package store

import (
	"context"
	"strconv"
)

// PeerWorkspaceState is a SQL authority check, including writes that committed
// before their event was sequenced or their Redis notification was published.
type PeerWorkspaceState struct {
	WorkspaceID         string
	Head                int64
	EventHead           int64
	MetadataRevision    int64
	InstalledRevision   int64 // -1 if unknown
	InstalledPolicyHead int64 // -1 if unknown
	Current             bool
	AuthzEpoch          string
	Pending             bool
}

func (s *SQLiteStore) WorkspacePeerState(ctx context.Context, workspace string) (PeerWorkspaceState, error) {
	var result PeerWorkspaceState
	if _, pinned := ctx.Value(workspaceReadKey{}).(workspaceRead); pinned {
		// This context routes SQL through an older read transaction. Its
		// caller must release it before making a new authorization decision.
		return result, ErrPeerSnapshotRequired
	}
	var stamp copyStamp
	var epoch int64
	err := s.db.QueryRowContext(ctx, `SELECT w.workspace_id,(SELECT COALESCE(SUM(r.revision),0) FROM workspace_cache_revisions r WHERE r.workspace_key=w.workspace_key AND r.kind=?),COALESCE(j.head,0),COALESCE(e.head,0),COALESCE(a.epoch,0),EXISTS(SELECT 1 FROM domain_event_sequencing q WHERE q.workspace_id=w.workspace_id) FROM workspace_states w LEFT JOIN workspace_record_journal_heads j ON j.workspace_id=w.workspace_id LEFT JOIN workspace_event_seq e ON e.workspace_id=w.workspace_id LEFT JOIN workspace_authz_epochs a ON a.workspace_id=w.workspace_id WHERE w.workspace_key=?`, cacheKindMetadata, workspace).Scan(&stamp.workspaceID, &stamp.revision, &result.Head, &result.EventHead, &epoch, &result.Pending)
	if err != nil {
		return result, err
	}
	result.WorkspaceID = stamp.workspaceID
	result.AuthzEpoch = strconv.FormatInt(epoch, 10)
	result.MetadataRevision, result.InstalledRevision, result.InstalledPolicyHead = stamp.revision, -1, -1
	copy, ok := s.copyRevisionOf(workspace)
	if ok && copy.workspaceID == stamp.workspaceID {
		result.InstalledRevision = copy.revision
	}
	result.Current = ok && copy.workspaceID == stamp.workspaceID && copy.revision == stamp.revision
	if s.metadataResidency != nil {
		policy, head, known := s.residentStamp(workspace)
		if known && policy.workspaceID == stamp.workspaceID {
			result.InstalledRevision, result.InstalledPolicyHead = policy.revision, head
		}
		result.Current = known && policy == stamp
		if !result.Current {
			// HOT bounded writers can install an exact logical generation
			// without rebuilding the resident policy projection. The
			// logical source is still the installed metadata that bounded
			// readers use, so its exact stamp is sufficient for freshness.
			s.metadataResidency.mu.Lock()
			source := s.metadataResidency.logical[workspace]
			if source != nil && !source.behind.Load() {
				logical := source.Stamp()
				if logical.workspaceID == stamp.workspaceID {
					result.InstalledRevision = max(result.InstalledRevision, logical.revision)
				}
				result.Current = logical == stamp
			}
			s.metadataResidency.mu.Unlock()
		}
	}
	return result, nil
}
