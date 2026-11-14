package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// defaultWorkspaceDeletionGrace is how long a workspace scheduled for
// deletion stays restorable before it is purged.
const defaultWorkspaceDeletionGrace = 30 * 24 * time.Hour

// workspaceDeletionGrace is the configured grace period
// (FLOW_WORKSPACE_DELETION_GRACE_DAYS, set at startup).
var workspaceDeletionGrace = defaultWorkspaceDeletionGrace

// purgeDeletedWorkspaces finishes purges an earlier pass left unfinished,
// then purges every workspace whose deletion was requested more than the
// grace period ago. It runs in the storage maintenance pass, which one
// replica runs at a time.
func (s *server) purgeDeletedWorkspaces(ctx context.Context, now time.Time) {
	pending, err := s.store.WorkspaceDeletionReceipts(ctx, store.WorkspacePurgePurging, 100)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("Workspace purge: list unfinished purges: %v", err)
		}
		return
	}
	for _, receipt := range pending {
		if ctx.Err() != nil {
			return
		}
		s.purgeWorkspace(ctx, receipt)
	}
	cutoff := now.Add(-workspaceDeletionGrace)
	for _, workspace := range s.store.WorkspacesDueForDeletion(cutoff) {
		if ctx.Err() != nil {
			return
		}
		receipt, started, err := s.store.BeginWorkspacePurge(ctx, workspace.URLKey, cutoff)
		if err != nil {
			log.Printf("Workspace purge workspace_id=%s: start: %v", workspace.ID, err)
			continue
		}
		if !started || receipt.Status != store.WorkspacePurgePurging {
			continue
		}
		log.Printf("Workspace purge started workspace_id=%s requested_at=%s request=%s", receipt.WorkspaceID, receipt.RequestedAt.Format(time.RFC3339), receipt.RequestRef)
		s.purgeWorkspace(ctx, receipt)
	}
}

// purgeWorkspace runs (or resumes) one purge: what the rows point to outside
// the database goes first, while the rows still say what that is, then the
// catalog entry, then the rows. Every step can run again, so a purge that
// stopped half way is finished by the next pass. It reports whether the
// purge completed.
func (s *server) purgeWorkspace(ctx context.Context, receipt store.WorkspaceDeletionReceipt) bool {
	target := receipt.Target()
	fail := func(step string, err error) bool {
		if ctx.Err() != nil {
			return false
		}
		// The receipt keeps the step and a short error; the log has the rest.
		log.Printf("Workspace purge workspace_id=%s: %s: %v", target.ID, step, err)
		message := step + ": " + err.Error()
		if len(message) > 300 {
			message = message[:300]
		}
		receipt.LastError = message
		_ = s.store.SaveWorkspaceDeletionReceipt(context.WithoutCancel(ctx), receipt)
		return false
	}
	inCatalog := false
	var identity domain.Bootstrap
	var catalogExists bool
	if s.store.BoundedMetadataEnabled() {
		var err error
		identity, catalogExists, err = s.store.WorkspaceMetadataScoped(ctx, target.Key, store.MetadataRecordScope{})
		if err != nil {
			return fail("catalog", err)
		}
	} else {
		identity, catalogExists = s.store.BootstrapFor(target.Key)
	}
	if catalogExists {
		if identity.Workspace.ID != target.ID {
			return fail("catalog", store.ErrWorkspaceKeyReused)
		}
		inCatalog = true
	}
	if inCatalog {
		// Per-user and provider credentials are named after the workspace's
		// connectors, which only the catalog copy still lists.
		deleted, err := s.deleteWorkspaceCredentials(ctx, target)
		receipt.AddCount("connector_secrets", deleted)
		if err != nil {
			return fail("credentials", err)
		}
	}
	if err := s.deleteWorkspaceObjects(ctx, target, &receipt); err != nil {
		return fail("objects", err)
	}
	// "Download your data" archives of the workspace's people hold its data.
	// The people are read while the membership rows still name them; their
	// archives go now and again once the workspace left the catalog, for an
	// export requested in between.
	people, err := s.store.WorkspaceUserIDs(ctx, target.ID)
	if err != nil {
		return fail("data exports", err)
	}
	expireExports := func() bool {
		deleted, err := s.expirePersonalDataExports(ctx, people)
		receipt.AddCount("objects.personal_data_exports", deleted)
		return err == nil || fail("data exports", err)
	}
	if !expireExports() {
		return false
	}
	if inCatalog {
		counts := map[string]int64{}
		err := s.store.RemoveWorkspaceFromCatalog(ctx, target.Key, counts)
		for name, n := range counts {
			receipt.AddCount(name, n)
		}
		if err != nil {
			return fail("catalog", err)
		}
		// Files uploaded while the objects above were being deleted.
		if err := s.deleteWorkspaceObjects(ctx, target, &receipt); err != nil {
			return fail("objects", err)
		}
		if !expireExports() {
			return false
		}
	}
	counts := map[string]int64{}
	err = s.store.PurgeWorkspaceRows(ctx, target, counts)
	for name, n := range counts {
		receipt.AddCount(name, n)
	}
	if err != nil {
		return fail("rows", err)
	}
	now := time.Now().UTC()
	receipt.Status, receipt.CompletedAt, receipt.LastError = store.WorkspacePurgeCompleted, &now, ""
	if err := s.store.SaveWorkspaceDeletionReceipt(context.WithoutCancel(ctx), receipt); err != nil {
		log.Printf("Workspace purge workspace_id=%s: save receipt: %v", target.ID, err)
		return false
	}
	var rows, objects int64
	for name, n := range receipt.Counts {
		if strings.HasPrefix(name, "objects.") {
			objects += n
		} else {
			rows += n
		}
	}
	log.Printf("Workspace purged workspace_id=%s requested_at=%s request=%s rows=%d objects=%d", target.ID, receipt.RequestedAt.Format(time.RFC3339), receipt.RequestRef, rows, objects)
	return true
}

// deleteWorkspaceObjects deletes the uploads, coding objects and cached
// pulse audio the workspace's rows point to. A missing object is fine.
func (s *server) deleteWorkspaceObjects(ctx context.Context, target store.WorkspacePurgeTarget, receipt *store.WorkspaceDeletionReceipt) error {
	objects, err := s.store.WorkspaceObjectReferences(ctx, target)
	if err != nil {
		return err
	}
	coded := append([]string{}, objects.CodingObjects...)
	for _, session := range objects.CodingSessionIDs {
		coded = append(coded, coding.HarnessStateObjectKey(target.Key, session))
	}
	var audio []string
	for _, text := range objects.PulseSummaryTexts {
		audio = append(audio, pulseAudioKey(s.tts.Model, s.tts.Voice, text))
	}
	if len(objects.Uploads) == 0 && len(coded) == 0 && len(audio) == 0 {
		return nil
	}
	storage, err := s.storage()
	if err != nil {
		return fmt.Errorf("open object storage: %w", err)
	}
	remove := func(kind string, keys []string) error {
		failed := 0
		for _, key := range keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := storage.Delete(ctx, key); err != nil && !errors.Is(err, os.ErrNotExist) {
				failed++
				continue
			}
			receipt.AddCount("objects."+kind, 1)
		}
		if failed > 0 {
			// Object names can carry file names: count them, do not list them.
			return fmt.Errorf("%d %s objects could not be deleted", failed, kind)
		}
		return nil
	}
	if err := remove("uploads", objects.Uploads); err != nil {
		return err
	}
	if err := remove("coding", coded); err != nil {
		return err
	}
	return remove("pulse_audio", audio)
}

// deleteWorkspaceCredentials deletes the sealed credentials of the
// workspace's integrations and connectors: provider credentials, integration
// OAuth tokens, GitLab member sign-ins and each member's MCP connector tokens.
func (s *server) deleteWorkspaceCredentials(ctx context.Context, target store.WorkspacePurgeTarget) (int64, error) {
	// The same enumeration a rename moves credentials with.
	ids, err := s.store.WorkspaceCredentialSecretIDs(ctx, target.Key)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	return s.store.DeleteConnectorSecrets(ctx, ids)
}

// workspaceDeletionView is what the workspace deletion endpoints return:
// the workspace and, while deletion is scheduled, when it will be purged.
type workspaceDeletionView struct {
	domain.Workspace
	DeletionScheduledFor *time.Time `json:"deletionScheduledFor,omitempty"`
}

func newWorkspaceDeletionView(workspace domain.Workspace) workspaceDeletionView {
	view := workspaceDeletionView{Workspace: workspace}
	if at := workspaceDeletionScheduledFor(workspace); at != nil {
		view.DeletionScheduledFor = at
	}
	return view
}

// workspaceDeletionScheduledFor is when a workspace scheduled for deletion
// will be purged (nil when no deletion is scheduled).
func workspaceDeletionScheduledFor(workspace domain.Workspace) *time.Time {
	if workspace.DeletionRequestedAt == nil {
		return nil
	}
	at := workspace.DeletionRequestedAt.Add(workspaceDeletionGrace).UTC()
	return &at
}
