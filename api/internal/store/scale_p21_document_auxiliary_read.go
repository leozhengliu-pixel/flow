package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"

	"flow/api/internal/domain"
)

type DocumentAuxiliaryReadRequest struct {
	Ref     string
	Drafts  bool
	History bool
}

// DocumentAuxiliaryRead owns the installed reference matches and auxiliary
// values used by the legacy drafts/history handlers. Matching occurrences keep
// source order so projection still precedes the handler's document lookup.
type DocumentAuxiliaryRead struct {
	Data     domain.Bootstrap
	document *DocumentRead
	once     sync.Once
}

func (r *DocumentAuxiliaryRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Data = domain.Bootstrap{}
			if r.document != nil {
				r.document.Close()
				r.document = nil
			}
		})
	}
}

func (r *DocumentAuxiliaryRead) TransferOutput(ctx context.Context) error {
	if r == nil || !registerMetadataOutputClose(ctx, r.Close) {
		r.Close()
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	return nil
}

// LoadRevisionBodies shares the established owned directory body loader, but
// receives only the page selected by the legacy history handler.
func (r *DocumentAuxiliaryRead) LoadRevisionBodies(ctx context.Context, revisions []domain.DocumentRevision) error {
	if r == nil || r.document == nil {
		return errMetadataLogicalClosed
	}
	if len(revisions) > 200 {
		return ErrMetadataRecordQuery
	}
	value := &DocumentDirectoryValue{
		Document: domain.Document{Revisions: revisions},
		owner:    r.document.owner,
	}
	return value.revisions(ctx, r.Data.Workspace.URLKey)
}

func (s *SQLiteStore) BeginDocumentAuxiliaryRead(ctx context.Context, workspace, userID string, key *domain.APIKey, request DocumentAuxiliaryReadRequest) (_ *DocumentAuxiliaryRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	if request.Drafts == request.History {
		return nil, ErrMetadataRecordQuery
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	document := &DocumentRead{owner: &ResourceRead{store: s}}
	read := &DocumentAuxiliaryRead{document: document}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err = document.owner.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	held, borrowed := ctx.Value(workspaceReadKey{}).(workspaceRead)
	borrowed = borrowed && userID != "" && held.database == s.db && (workspace == "" || held.workspace == workspace)
	if borrowed {
		if held.reader == nil {
			return nil, ErrMetadataRecordQuery
		}
		workspace = held.workspace
	} else {
		installed, found, beginErr := s.BeginInstalledMetadataRead(ctx, workspace)
		if beginErr != nil || !found {
			if beginErr == nil {
				beginErr = ErrAuthForbidden
			}
			return nil, beginErr
		}
		defer installed.Close()
		workspace = installed.workspace
		document.owner.source, err = installed.retainSource(ctx, workspace)
		if err != nil {
			return nil, err
		}
	}
	tx := held.reader
	snapshot := document.owner.metadataOutputContext(ctx)
	var stamp copyStamp
	var epoch string
	if userID != "" {
		if !borrowed {
			tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
			if err != nil {
				return nil, err
			}
			defer tx.Rollback()
		}
		snapshot = context.WithValue(snapshot, workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
		stamp, epoch, err = s.accessStampAndEpoch(snapshot, workspace)
		if err != nil {
			return nil, err
		}
	}
	if borrowed {
		document.owner.source, err = document.documentAuxiliaryCallerSource(snapshot, held.metadata, stamp, userID, request)
		if err != nil {
			return nil, err
		}
	}
	for _, field := range append([]string{"workspace", "viewer"}, documentReadPolicyFields...) {
		if err = document.documentNativeField(snapshot, field, true); err != nil {
			return nil, err
		}
	}
	excluded := []string{"Content", "ContentState", "ContentData", "Attachments"}
	if request.Drafts {
		excluded = append(excluded, "Revisions")
	}
	directory := &DocumentDirectoryRead{owner: document.owner}
	if err = document.owner.visitNative(snapshot, "documents", func(_ int, node metadataLogicalNode) error {
		return document.owner.withHeader(snapshot, node, []string{"ID", "SlugID", "PreviousSlugIDs"}, func(header reflect.Value) error {
			if !documentAuxiliaryMatches(header.Interface().(domain.Document), request.Ref) {
				return nil
			}
			value, err := directory.header(snapshot, node, directoryOutputFields(reflect.TypeFor[domain.Document](), excluded...))
			if err == nil {
				document.Data.Documents = append(document.Data.Documents, value.Interface().(domain.Document))
			}
			return err
		})
	}); err != nil {
		return nil, err
	}
	if err = document.documentProjects(snapshot, nil, true); err != nil {
		return nil, err
	}
	if err = document.documentFavorites(snapshot); err != nil {
		return nil, err
	}
	if request.Drafts {
		if err = document.documentAuxiliaryDrafts(snapshot, request.Ref, userID); err != nil {
			return nil, err
		}
	}
	if request.History {
		err = document.owner.visitNative(snapshot, "trash", func(_ int, node metadataLogicalNode) error {
			matched := false
			if err := document.owner.withHeader(snapshot, node, []string{"ResourceType"}, func(header reflect.Value) error {
				if header.Interface().(domain.TrashEntry).ResourceType != "document" {
					return nil
				}
				err := document.documentPayloadHeader(snapshot, node, func(header domain.Document) error {
					matched = documentAuxiliaryTrashMatches(header, request.Ref)
					return nil
				})
				if documentAuxiliaryInvalidJSON(err) {
					return nil
				}
				return err
			}); err != nil || !matched {
				return err
			}
			value, err := directory.header(snapshot, node, directoryOutputFields(reflect.TypeFor[domain.TrashEntry](), "Links"))
			if err != nil {
				return err
			}
			entry := value.Interface().(domain.TrashEntry)
			work, err := resourceWindowTypedJSONWork(snapshot, entry.Payload, reflect.TypeFor[domain.Document]())
			if err != nil {
				if documentAuxiliaryInvalidJSON(err) {
					return nil
				}
				return err
			}
			if err := document.owner.reserve(snapshot, work*3+4096); err != nil {
				return err
			}
			document.Data.Trash = append(document.Data.Trash, entry)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	// workspaceData's metadata accessor crosses cloneBootstrap, rather than
	// the document API's JSON-equivalent selection boundary.
	document.Data = cloneBootstrap(document.Data)
	if userID != "" {
		if err = document.documentProjectionWork(snapshot, stamp.workspaceID, userID); err == nil {
			memoContext, closeMemo := withResourcePolicyMemo(snapshot, document.owner)
			func() {
				defer closeMemo()
				document.Data, err = s.projectDocumentRecordMetadata(memoContext, workspace, userID, key, document.Data)
			}()
		}
		if outputErr := metadataOutputError(snapshot); outputErr != nil {
			err = outputErr
		}
	} else {
		document.Data.ViewerRole = "admin"
	}
	if err != nil {
		return nil, err
	}
	if !borrowed && userID != "" {
		if err = tx.Rollback(); err != nil {
			return nil, err
		}
		if s.db.journalReady.Load() {
			current, err := s.AuthzEpoch(ctx, stamp.workspaceID)
			if err != nil {
				return nil, err
			}
			if current != epoch {
				return nil, errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	read.Data = document.Data
	return read, nil
}

func (r *DocumentRead) documentAuxiliaryDrafts(ctx context.Context, ref, userID string) error {
	index, _, _ := metadataLogicalField("documentContentDrafts")
	drafts := r.owner.source.fields[index]
	absent, err := drafts.isNil(ctx)
	if err != nil {
		return err
	}
	if !absent {
		r.Data.DocumentContentDrafts = []domain.DocumentContentDraft{}
	}
	return r.owner.nativeArray(ctx, drafts, func(_ int, node metadataLogicalNode) error {
		return r.owner.withHeader(ctx, node, []string{"DocumentID", "UserID"}, func(header reflect.Value) error {
			item := header.Interface().(domain.DocumentContentDraft)
			if item.DocumentID != ref || userID != "" && item.UserID != userID {
				return nil
			}
			value, err := r.owner.node(ctx, node, true)
			if err == nil {
				r.Data.DocumentContentDrafts = append(r.Data.DocumentContentDrafts, value.Interface().(domain.DocumentContentDraft))
			}
			return err
		})
	})
}

func (r *DocumentRead) documentAuxiliaryCallerSource(ctx context.Context, data domain.Bootstrap, stamp copyStamp, userID string, request DocumentAuxiliaryReadRequest) (*metadataLogicalSource, error) {
	candidate := domain.Bootstrap{
		Workspace: data.Workspace, Viewer: data.Viewer, Teams: data.Teams,
		TeamSettings: data.TeamSettings, TeamParents: data.TeamParents, WorkspaceSettings: data.WorkspaceSettings,
	}
	ids, projects := []string{}, []string{}
	for _, item := range data.Documents {
		if documentAuxiliaryMatches(item, request.Ref) {
			if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Document]().Size())*2+uint64(len(item.ProjectIDs))*32+4096); err != nil {
				return nil, err
			}
			candidate.Documents = append(candidate.Documents, item)
			ids, projects = append(ids, item.ID), append(projects, item.ProjectIDs...)
		}
	}
	for _, item := range data.Projects {
		if slices.Contains(projects, item.ID) || slices.ContainsFunc(item.Resources, func(resource domain.ProjectResource) bool { return slices.Contains(ids, resource.ID) }) {
			if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Project]().Size())*2+2048); err != nil {
				return nil, err
			}
			candidate.Projects = append(candidate.Projects, item)
		}
	}
	for _, item := range data.Favorites {
		if item.ResourceType == "document" && slices.Contains(ids, item.ResourceID) {
			if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Favorite]().Size())*2+2048); err != nil {
				return nil, err
			}
			candidate.Favorites = append(candidate.Favorites, item)
		}
	}
	if request.Drafts {
		if data.DocumentContentDrafts != nil {
			candidate.DocumentContentDrafts = []domain.DocumentContentDraft{}
		}
		for _, item := range data.DocumentContentDrafts {
			if item.DocumentID == request.Ref && item.UserID == userID {
				if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.DocumentContentDraft]().Size())*2+2048); err != nil {
					return nil, err
				}
				candidate.DocumentContentDrafts = append(candidate.DocumentContentDrafts, item)
			}
		}
	}
	if request.History {
		for _, item := range data.Trash {
			if item.ResourceType != "document" {
				continue
			}
			matched := false
			if err := r.documentJSONHeader(ctx, bytes.NewReader(item.Payload), 0, int64(len(item.Payload)), func(header domain.Document) error {
				matched = documentAuxiliaryTrashMatches(header, request.Ref)
				return nil
			}); err != nil {
				if !documentAuxiliaryInvalidJSON(err) {
					return nil, err
				}
			}
			if matched {
				if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.TrashEntry]().Size())*2+2048); err != nil {
					return nil, err
				}
				item.Links = nil
				candidate.Trash = append(candidate.Trash, item)
			}
		}
	}
	return captureMetadataLogicalSource(ctx, candidate, stamp)
}

func documentAuxiliaryMatches(document domain.Document, ref string) bool {
	return document.ID == ref || document.SlugID == ref || ref != "" && slices.Contains(document.PreviousSlugIDs, ref)
}

func documentAuxiliaryTrashMatches(document domain.Document, ref string) bool {
	return document.ID == ref || document.SlugID == ref || slices.Contains(document.PreviousSlugIDs, ref)
}

func documentAuxiliaryInvalidJSON(err error) bool {
	var typed *json.UnmarshalTypeError
	var syntax *json.SyntaxError
	return errors.As(err, &typed) || errors.As(err, &syntax)
}
