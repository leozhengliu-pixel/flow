package store

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// issueEditorLookups owns the lazy, read-only callback services. It shares the
// mutation transaction and retains its installed native source through commit.
// The editor validator borrows independent, live allocation leases from it.
type issueEditorLookups struct {
	ctx                    context.Context
	store                  *SQLiteStore
	tx                     *sqlTx
	reader                 metadataReader
	legacy                 bool
	source                 *metadataLogicalSource
	workspace, workspaceID string
	documentID             string
	// documents are further documents whose queued updates the callback
	// may read (a batch update's other issues), each loaded and admitted on
	// first use like documentID's.
	documents     map[string]*issueEditorDocument
	former        map[string]bool
	updates       [][]byte
	legacyUpdates [][][]byte
	updateBytes   uint64
	formerLoaded  bool
	updatesLoaded bool
	// liveMembers mirrors a legacy write whose member snapshot is the
	// live membership list (loadMemberAccess): without memberships that
	// list is empty, so no snapshot member counts as former.
	liveMembers bool
	err         error
	releases    []func()
	closed      bool
}

type issueEditorDocument struct {
	updates [][]byte
	bytes   uint64
	loaded  bool
}

// allowDocuments lets collaborationUpdates read the queued updates of the
// document generation of each issue besides the first target's.
func (r *issueEditorLookups) allowDocuments(issues []domain.Issue) {
	for _, issue := range issues {
		id := "document_content_" + issue.ID
		if issue.DocumentContent != nil && issue.DocumentContent.ID != "" {
			id = issue.DocumentContent.ID
		}
		if id == r.documentID {
			continue
		}
		if r.documents == nil {
			r.documents = map[string]*issueEditorDocument{}
		}
		if r.documents[id] == nil {
			r.documents[id] = &issueEditorDocument{}
		}
	}
}

func newIssueEditorLookups(ctx context.Context, inputs *issueHotInputs, tx *sqlTx, workspace, workspaceID string, target domain.Issue) (_ *issueEditorLookups, err error) {
	if inputs == nil || inputs.store == nil || inputs.source == nil || tx == nil || tx.database != inputs.store.db {
		return nil, ErrMetadataRecordQuery
	}
	read := &issueEditorLookups{
		ctx: ctx, store: inputs.store, tx: tx, source: inputs.source,
		workspace: workspace, workspaceID: workspaceID, documentID: "document_content_" + target.ID,
	}
	if target.DocumentContent != nil && target.DocumentContent.ID != "" {
		read.documentID = target.DocumentContent.ID
	}
	if err := read.reserve(4096); err != nil {
		return nil, err
	}
	if err := read.source.Retain(); err != nil {
		read.source = nil
		read.close()
		return nil, err
	}
	return read, nil
}

func (r *issueEditorLookups) close() {
	if r == nil || r.closed {
		return
	}
	r.closed = true
	r.former, r.updates, r.legacyUpdates, r.documents = nil, nil, nil, nil
	if r.source != nil {
		r.source.Release()
		r.source = nil
	}
	for _, release := range r.releases {
		release()
	}
	r.releases = nil
}

func (r *issueEditorLookups) reserve(cost uint64) error {
	if cost > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(r.ctx, int(cost))
	if err == nil {
		r.releases = append(r.releases, release)
	}
	return err
}

func (r *issueEditorLookups) install(data *domain.Bootstrap) {
	data.FormerMembers = r.formerMembers
	data.CollaborationUpdates = r.collaborationUpdates
	data.CollaborationWork = r.collaborationWork
}

func (r *issueEditorLookups) collaborationWork() domain.CollaborationWork {
	return domain.CollaborationWork{Context: r.ctx, Admit: r.admitCollaboration}
}

func (r *issueEditorLookups) admitCollaboration(cost uint64) (func(), error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	if cost > metadataWorkBytes {
		return nil, ErrMetadataWorkBudget
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(r.ctx, int(cost))
	if err != nil {
		return nil, err
	}
	return release, nil
}

func (r *issueEditorLookups) failure(err error) error {
	if r.err == nil {
		r.err = err
	}
	return err
}

func (r *issueEditorLookups) check() error {
	if r.closed || r.store == nil || r.source == nil || r.legacy && r.reader == nil ||
		!r.legacy && (r.tx == nil || r.tx.database != r.store.db) {
		return r.failure(ErrMetadataRecordQuery)
	}
	if err := r.ctx.Err(); err != nil {
		return r.failure(err)
	}
	return r.err
}

func (r *issueEditorLookups) bytes(column string) string {
	if r.store.dialect == "sqlite" {
		return "LENGTH(CAST(" + column + " AS BLOB))"
	}
	return "OCTET_LENGTH(" + column + ")"
}

// Ordinary SQL errors preserve the legacy warning and partial nonnil map.
// Ownership, coverage and cancellation failures must abort the bounded write.
func (r *issueEditorLookups) formerMembers() map[string]bool {
	if r.former == nil {
		r.former = map[string]bool{}
	}
	if r.check() != nil || r.formerLoaded {
		return r.former
	}
	r.formerLoaded = true
	if err := r.loadFormer(); err != nil {
		if r.err != nil || errors.Is(err, ErrMetadataWorkBudget) || errors.Is(err, ErrMetadataRecordQuery) ||
			errors.Is(err, ErrBoundedMetadataCoverage) || r.ctx.Err() != nil {
			if r.ctx.Err() != nil {
				err = r.ctx.Err()
			}
			r.failure(err)
		} else {
			slog.Warn("read former workspace members", "workspace", r.workspaceID, "error", err)
		}
	}
	return r.former
}

func (r *issueEditorLookups) loadFormer() error {
	if r.legacy {
		return r.loadLegacyFormer()
	}
	if r.workspaceID == "" {
		return nil
	}
	rows, err := r.tx.QueryContext(r.ctx, `SELECT 1 FROM workspace_memberships WHERE workspace_id=? LIMIT 1`, r.workspaceID)
	if err != nil {
		return err
	}
	hasMemberships := rows.Next()
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasMemberships {
		if r.liveMembers {
			return nil
		}
		if err := r.loadNativeFormer(); err != nil {
			return r.failure(err)
		}
		return nil
	}
	var count, size int64
	err = r.tx.QueryRowContext(r.ctx, "SELECT COUNT(*),COALESCE(SUM("+r.bytes("user_id")+"),0) FROM ("+formerMembersQuery+") issue_former", r.workspaceID, r.workspaceID).Scan(&count, &size)
	if err != nil {
		return err
	}
	if count < 0 || size < 0 || uint64(count) > (metadataWorkBytes-(64<<10))/512 ||
		uint64(size) > (metadataWorkBytes-(64<<10)-uint64(count)*512)/8 {
		return ErrMetadataWorkBudget
	}
	// Buckets, retained strings, driver copies and transient result frames are
	// admitted together before any user_id value leaves the SQL driver.
	if err := r.reserve(uint64(count)*512 + uint64(size)*8 + 64<<10); err != nil {
		return err
	}
	rows, err = r.tx.QueryContext(r.ctx, formerMembersQuery, r.workspaceID, r.workspaceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var actual, actualBytes uint64
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		actual++
		actualBytes += uint64(len(id))
		if actual > uint64(count) || actualBytes > uint64(size) {
			return ErrMetadataRecordQuery
		}
		r.former[id] = true
	}
	return rows.Err()
}

func (r *issueEditorLookups) loadNativeFormer() error {
	index, _, _ := metadataLogicalField("members")
	return r.source.fields[index].Children(r.ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		status, err := issueHotField(r.ctx, record, "Status")
		if err != nil {
			return err
		}
		suspended, err := status.StringEqual(r.ctx, "suspended")
		if err != nil || !suspended {
			return err
		}
		user, err := issueHotField(r.ctx, record, "User")
		if err != nil {
			return err
		}
		idNode, err := issueHotField(r.ctx, user, "ID")
		if err != nil {
			return err
		}
		if idNode.cost > (metadataWorkBytes-512)/4 {
			return ErrMetadataWorkBudget
		}
		cost := idNode.cost*4 + 512
		_, release, err := r.store.tryAdmitAdditionalMetadataWork(r.ctx, int(cost))
		if err != nil {
			return err
		}
		defer release()
		value, err := idNode.Value(r.ctx, int(cost))
		if err != nil {
			return err
		}
		id := value.String()
		if !r.former[id] {
			if err := r.reserve(cost); err != nil {
				return err
			}
			r.former[id] = true
		}
		return nil
	})
}

func (r *issueEditorLookups) collaborationUpdates(documentID string) (_ [][]byte, err error) {
	if r.legacy {
		return r.legacyCollaborationUpdates(documentID)
	}
	if err := r.check(); err != nil {
		return nil, err
	}
	var updates [][]byte
	var size uint64
	if documentID == r.documentID {
		if !r.updatesLoaded {
			if r.updates, r.updateBytes, err = r.loadUpdates(documentID); err != nil {
				return nil, r.failure(err)
			}
			r.updatesLoaded = true
		}
		updates, size = r.updates, r.updateBytes
	} else if document := r.documents[documentID]; document != nil {
		if !document.loaded {
			if document.updates, document.bytes, err = r.loadUpdates(documentID); err != nil {
				return nil, r.failure(err)
			}
			document.loaded = true
		}
		updates, size = document.updates, document.bytes
	} else {
		return nil, r.failure(ErrMetadataRecordQuery)
	}
	if err := r.reserve(uint64(len(updates))*128 + size*2 + 4096); err != nil {
		return nil, r.failure(err)
	}
	// P04 executes independent callbacks. Give each its own mutable blobs,
	// while keeping the retained transaction result private.
	result := slices.Clone(updates)
	for index := range result {
		result[index] = slices.Clone(result[index])
	}
	return result, nil
}

func (r *issueEditorLookups) loadUpdates(documentID string) (updates [][]byte, total uint64, err error) {
	const from = " FROM document_collaboration_updates WHERE workspace_key=? AND document_id=?"
	var count, size int64
	err = r.tx.QueryRowContext(r.ctx, "SELECT COUNT(*),COALESCE(SUM("+r.bytes("update_data")+"),0)"+from, r.workspace, documentID).Scan(&count, &size)
	if err != nil {
		return nil, 0, err
	}
	if count < 0 || size < 0 || uint64(count) > (metadataWorkBytes-(64<<10))/256 ||
		uint64(size) > (metadataWorkBytes-(64<<10)-uint64(count)*256)/4 {
		return nil, 0, ErrMetadataWorkBudget
	}
	if err := r.reserve(uint64(count)*256 + uint64(size)*4 + 64<<10); err != nil {
		return nil, 0, err
	}
	rows, err := r.tx.QueryContext(r.ctx, "SELECT update_data"+from+" ORDER BY created_at,update_id", r.workspace, documentID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var actual uint64
	for rows.Next() {
		var update []byte
		if err := rows.Scan(&update); err != nil {
			return nil, 0, err
		}
		actual++
		total += uint64(len(update))
		if actual > uint64(count) || total > uint64(size) {
			return nil, 0, ErrMetadataRecordQuery
		}
		updates = append(updates, update)
	}
	return updates, total, rows.Err()
}
