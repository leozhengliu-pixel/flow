package main

import (
	"context"
	"errors"
	"log"
	"os"
	"slices"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// trashedUploads lists the uploaded files that the trash entries matching
// keep attach (issue, document, customer request and update attachments).
// Deleted items keep their files while they can be restored; purging the
// entry releases them (releaseTrashedUploads).
func trashedUploads(entries []domain.TrashEntry, keep func(domain.TrashEntry) bool) []string {
	names := []string{}
	for _, entry := range entries {
		if keep(entry) {
			names = append(names, store.AttachmentUploads(entry.Payload)...)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// releaseTrashedUploads deletes the files of purged trash entries that
// nothing else uses: no issue of the workspace attaches them, no remaining
// trash entry does, and no other workspace owns or attaches them.
func (s *server) releaseTrashedUploads(ctx context.Context, workspace string, names []string) {
	if len(names) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	if data, ok := s.store.WorkspaceMetadataFields(workspace, "trash"); ok {
		remaining := trashedUploads(data.Trash, func(domain.TrashEntry) bool { return true })
		names = slices.DeleteFunc(slices.Clone(names), func(name string) bool {
			_, found := slices.BinarySearch(remaining, name)
			return found
		})
	}
	releasable, err := s.store.ReleasableUploads(ctx, workspace, names)
	if err != nil || len(releasable) == 0 {
		if err != nil {
			log.Printf("Trash purge workspace=%s: check attachment files: %v", workspace, err)
		}
		return
	}
	storage, err := s.storage()
	if err != nil {
		log.Printf("Trash purge workspace=%s: object storage: %v", workspace, err)
		return
	}
	deleted := make([]string, 0, len(releasable))
	failed := 0
	for _, name := range releasable {
		if err := storage.Delete(ctx, name); err != nil && !errors.Is(err, os.ErrNotExist) {
			failed++
			continue
		}
		deleted = append(deleted, name)
	}
	if failed > 0 {
		log.Printf("Trash purge workspace=%s: %d attachment files could not be deleted", workspace, failed)
	}
	if err := s.store.DeleteWorkspaceFileUploads(ctx, workspace, deleted); err != nil {
		log.Printf("Trash purge workspace=%s: upload records: %v", workspace, err)
	}
}
