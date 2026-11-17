package store

import (
	"context"
	"reflect"
)

// InstalledViewerRole reads the installed snapshot's own viewerRole, the raw
// role a development-mode WorkspaceMetadata read presents to its caller.
func (s *SQLiteStore) InstalledViewerRole(ctx context.Context, workspace string) (string, bool, error) {
	if !s.BoundedMetadataEnabled() {
		return "", false, ErrBoundedMetadataCoverage
	}
	installed, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return "", found, err
	}
	defer installed.Close()
	source, err := installed.retainSource(ctx, installed.workspace)
	if err != nil {
		return "", true, err
	}
	defer source.Release()
	owner := &ResourceRead{store: s}
	defer owner.Close()
	index, _, _ := metadataLogicalField("viewerRole")
	value, err := cycleScheduleNativeValue(ctx, owner, source.fields[index])
	if err != nil {
		return "", true, err
	}
	return value.String(), true, ctx.Err()
}

// AdmitMetadataOutputValue charges value's encoding work to the caller's
// metadata output owner until that owner closes. Background producers use it
// for values they build from bounded inputs and hold across slow work.
func (s *SQLiteStore) AdmitMetadataOutputValue(ctx context.Context, value any) error {
	if !s.BoundedMetadataEnabled() {
		return ErrBoundedMetadataCoverage
	}
	cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
	if err != nil {
		return err
	}
	if uint64(cost) > (metadataWorkBytes-4096)/2 {
		return ErrMetadataWorkBudget
	}
	owner := &ResourceRead{store: s}
	if err := owner.reserve(ctx, uint64(cost)*2+4096); err != nil {
		owner.Close()
		return err
	}
	if !registerMetadataOutputClose(ctx, owner.Close) {
		owner.Close()
		return ErrBoundedMetadataCoverage
	}
	return nil
}
