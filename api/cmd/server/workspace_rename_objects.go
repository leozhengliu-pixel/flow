package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"flow/api/internal/coding"
	"flow/api/internal/objectstore"
	"flow/api/internal/store"
)

// codingHarnessKeyObjects moves coding sessions' saved harness state, whose
// object names embed the workspace URL key (coding.HarnessStateObjectKey),
// when a workspace is renamed. The store copies before the rename commits and
// deletes the old copies after (store.WorkspaceKeyObjects).
type codingHarnessKeyObjects struct {
	objects objectstore.Store
}

func (o codingHarnessKeyObjects) CopyWorkspaceKeyObjects(ctx context.Context, move store.WorkspaceKeyMove) error {
	if o.objects == nil {
		return nil
	}
	for _, session := range move.CodingSessionIDs {
		target := coding.HarnessStateObjectKey(move.NewKey, session)
		if reader, _, _, err := o.objects.Open(ctx, target); err == nil {
			// A state saved under the new key is newer: keep it.
			reader.Close()
			continue
		} else if !objectMissing(err) {
			return err
		}
		reader, _, _, err := o.objects.Open(ctx, coding.HarnessStateObjectKey(move.OldKey, session))
		if objectMissing(err) {
			continue
		}
		if err != nil {
			return err
		}
		_, err = o.objects.Put(ctx, target, reader, "application/zstd")
		reader.Close()
		if err != nil {
			return fmt.Errorf("copy harness state: %w", err)
		}
	}
	return nil
}

func (o codingHarnessKeyObjects) DeleteWorkspaceKeyObjects(ctx context.Context, workspaceKey string, sessionIDs []string) error {
	if o.objects == nil {
		return nil
	}
	failed := 0
	for _, session := range sessionIDs {
		if err := o.objects.Delete(ctx, coding.HarnessStateObjectKey(workspaceKey, session)); err != nil && !objectMissing(err) {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d harness state objects could not be deleted", failed)
	}
	return nil
}

// objectMissing reports an object store error for an object that does not
// exist (local file system or S3).
func objectMissing(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "nosuchkey") || strings.Contains(message, "not found") || strings.Contains(message, "statuscode: 404")
}
