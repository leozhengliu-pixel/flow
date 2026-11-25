package store

import (
	"fmt"
	"slices"
)

// Captures become readable after every commit hook and successful SQL commit.
// A guarded capture proves that the candidate includes every metadata write.
type metadataCommitStamp struct {
	value      copyStamp
	ready      bool
	base       *copyStamp
	ownAdvance int64
}

func (capture *metadataCommitStamp) result() (copyStamp, bool) {
	if !capture.ready {
		return copyStamp{}, false
	}
	return capture.value, true
}

func (tx *sqlTx) captureMetadataCommitStamp(workspace string) *metadataCommitStamp {
	if tx.metadataCommitStamps == nil {
		tx.metadataCommitStamps = map[string]*metadataCommitStamp{}
	}
	if capture := tx.metadataCommitStamps[workspace]; capture != nil {
		return capture
	}
	capture := &metadataCommitStamp{}
	tx.metadataCommitStamps[workspace] = capture
	return capture
}

func (tx *sqlTx) captureMetadataCommitStampFrom(workspace string, base copyStamp) *metadataCommitStamp {
	capture := tx.captureMetadataCommitStamp(workspace)
	capture.base = &base
	return capture
}

func (tx *sqlTx) readMetadataCommitStamps() error {
	workspaces := make([]string, 0, len(tx.metadataCommitStamps))
	for workspace := range tx.metadataCommitStamps {
		workspaces = append(workspaces, workspace)
	}
	slices.Sort(workspaces)
	for _, workspace := range workspaces {
		stamp, err := readMetadataStamp(tx.context(), tx, workspace)
		if err != nil {
			return err
		}
		capture := tx.metadataCommitStamps[workspace]
		if capture.base != nil && (stamp.workspaceID != capture.base.workspaceID || stamp.revision != capture.base.revision+capture.ownAdvance) {
			return fmt.Errorf("%w: logical base %d, final queued advance %d, observed %d", ErrConcurrentWrite, capture.base.revision, capture.ownAdvance, stamp.revision)
		}
		capture.value = stamp
	}
	return nil
}
