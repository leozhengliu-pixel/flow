package store

import (
	"context"
	"reflect"
)

// The legacy write/install/refresh chain (metadata_copy_revision.go,
// refreshMetadataRecords) edits the whole resident copy of a workspace. On a
// bounded store that copy holds the core and policy fields only, so the
// remaining flag-on callers of the chain dispatch here before it: the copy is
// never patched with record collections, and the installed logical generation
// receives the change the legacy copy would have received, with the unknown
// stamp the legacy copy's revision takes (forgetCopyRevisionLocked).

// refreshConflictedLogicalRecords is refreshMetadataRecords on a bounded
// store: the named records of the installed logical generation are replaced
// by the stored rows (dropped when no longer stored), under writeMu, and the
// generation is installed with an unknown stamp. Agent sessions are framed
// from disk; every other field uses the generic retry adapter, which skips
// the fields the legacy refresh skips (metadataRecordType). A workspace this
// server has not installed is left alone, as the legacy refresh does.
func (s *SQLiteStore) refreshConflictedLogicalRecords(ctx context.Context, workspace string) func(error) error {
	refresh := s.refreshAgentSessionLifecycleConflict(ctx, workspace)
	return func(conflict error) error {
		keys, ok := conflictedMetadataKeys(conflict)
		if !ok {
			return nil
		}
		return s.refreshLogicalMetadataRecords(workspace, keys, refresh)
	}
}

func (s *SQLiteStore) refreshLogicalMetadataRecords(workspace string, keys []metadataRecordKey, refresh func(error) error) error {
	var selected []metadataRecordKey
	for _, key := range keys {
		if _, _, ok := metadataRecordType(key.field); ok {
			selected = append(selected, key)
		}
	}
	s.mu.RLock()
	_, installed := s.workspaces[workspace]
	s.mu.RUnlock()
	if !installed || len(selected) == 0 {
		return nil
	}
	return refresh(&metadataConflictError{keys: selected})
}

// publishBoundedAPIKeyUse is the installation of writeAPIKeyUse on a bounded
// store, after its SQL commit, under writeMu as the legacy patch runs: the
// first API key and the first OAuth authorization with the use's ids in the
// installed logical generation take the use's time as their last use unless
// they are revoked there, exactly the legacy patch of the resident copy
// (which may differ from the row when the row's predicate skipped the
// update). Nothing else is copied, decoded or re-encoded; the policy copy is
// unchanged. Without a change the generation and its stamp stay.
func (s *SQLiteStore) publishBoundedAPIKeyUse(ctx context.Context, use apiKeyUse) error {
	s.mu.RLock()
	_, installed := s.workspaces[use.workspace]
	s.mu.RUnlock()
	if !installed {
		return nil
	}
	ctx = metadataLogicalUnpinnedContext(ctx)
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 32<<20)
	if err != nil {
		return err
	}
	defer release()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	source, err := s.retainMetadataLogicalSource(ctx, use.workspace)
	if err != nil {
		return err
	}
	defer source.Release()
	ids := map[string]string{"apiKeys": use.keyID, "oauthAuthorizations": use.authorizationID}
	frames, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
	if err != nil {
		return err
	}
	defer frames.Close()
	at := use.at
	if err := frames.auditJSONLocations(reflect.ValueOf(at)); err != nil {
		return err
	}
	changed := false
	candidate, err := source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		current := metadataAggregateNode(metadataAggregateStoredNode{node})
		id := ids[name]
		if id == "" || node.Type().Kind() != reflect.Slice {
			return current, false, nil
		}
		old, err := metadataLogicalFirstRecord(ctx, current, id)
		if err != nil || old == nil {
			return current, false, err
		}
		value, err := old.nodeValue(ctx, 2<<20)
		if err != nil {
			return nil, false, err
		}
		record := reflect.New(value.Type()).Elem()
		record.Set(value)
		if !record.FieldByName("RevokedAt").IsNil() {
			return current, false, nil
		}
		used := at
		record.FieldByName("LastUsedAt").Set(reflect.ValueOf(&used))
		fresh, err := frames.writeValue(record, 0, false)
		if err != nil {
			return nil, false, err
		}
		changed = true
		return metadataLogicalPatchedCollection{current, node, id, metadataAggregateStoredNode{fresh}}, true, nil
	})
	if err != nil {
		return err
	}
	if !changed {
		candidate.Release()
		return nil
	}
	if err := candidate.setStamp(copyStamp{workspaceID: source.Stamp().workspaceID, revision: -1}); err != nil {
		candidate.Release()
		return err
	}
	s.installMetadataLogicalSource(use.workspace, candidate)
	s.mu.Lock()
	s.forgetCopyRevisionLocked(use.workspace)
	s.mu.Unlock()
	return nil
}
