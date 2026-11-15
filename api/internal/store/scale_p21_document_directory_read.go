package store

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// DocumentDirectoryEntry identifies an occurrence, including duplicate IDs.
// Document is a body-free header in the installed collection's original order.
type DocumentDirectoryEntry struct {
	Document domain.Document
	Ordinal  int
}

// DocumentDirectoryRead owns policy, headers and their immutable source.
// Its caller owns final authority validation when lending a SQL snapshot.
type DocumentDirectoryRead struct {
	Data      domain.Bootstrap
	Entries   []DocumentDirectoryEntry
	owner     *ResourceRead
	nodes     []metadataLogicalNode
	visible   map[int]bool
	workspace string
	stamp     copyStamp
	epoch     string
	borrowed  bool
	principal bool
}

func (r *DocumentDirectoryRead) Close() {
	if r == nil {
		return
	}
	r.Data, r.Entries, r.nodes, r.visible = domain.Bootstrap{}, nil, nil, nil
	r.owner.Close()
}

// BeginDocumentDirectoryRead preserves the installed directory source and its
// literal full-list fallback. An explicit caller snapshot wins only when the
// old directory would fall back (an uncertified source or duplicate IDs).
func (s *SQLiteStore) BeginDocumentDirectoryRead(ctx context.Context, workspace, userID string, key *domain.APIKey) (_ *DocumentDirectoryRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	installed, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		if err == nil {
			err = ErrAuthForbidden
		}
		return nil, err
	}
	defer installed.Close()
	source, err := installed.retainSource(ctx, installed.workspace)
	if err != nil {
		return nil, err
	}
	r := &DocumentDirectoryRead{owner: &ResourceRead{store: s, source: source}, workspace: installed.workspace, principal: userID != ""}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if err = r.owner.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	var caller domain.Bootstrap
	var tx *sqlTx
	if held, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && held.database == s.db && held.workspace == r.workspace {
		tx, r.borrowed, caller = held.reader, true, held.metadata
		if tx == nil {
			return nil, ErrMetadataRecordQuery
		}
	} else {
		tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
	}
	snapshot := context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: r.workspace, reader: tx})
	snapshot = r.owner.metadataOutputContext(snapshot)
	r.stamp, r.epoch, err = s.accessStampAndEpoch(snapshot, r.workspace)
	if err != nil {
		return nil, err
	}
	callerSelected := caller.Workspace.ID != "" && source.Stamp() != r.stamp
	if callerSelected {
		if err = r.callerSource(snapshot, caller); err != nil {
			return nil, err
		}
	}
	duplicate, err := r.headers(snapshot)
	if err != nil {
		return nil, err
	}
	if duplicate && caller.Workspace.ID != "" && !callerSelected {
		if err = r.callerSource(snapshot, caller); err != nil {
			return nil, err
		}
		if _, err = r.headers(snapshot); err != nil {
			return nil, err
		}
	}
	document := &DocumentRead{Data: r.Data, owner: r.owner}
	for _, field := range append([]string{"workspace", "viewer", "favorites"}, documentReadPolicyFields...) {
		if err = document.documentNativeField(snapshot, field, true); err != nil {
			return nil, err
		}
	}
	r.Data = document.Data
	if userID != "" {
		if err = document.documentProjectionWork(snapshot, r.stamp.workspaceID, userID); err != nil {
			return nil, err
		}
		r.Data, err = s.projectDocumentRecordMetadata(snapshot, r.workspace, userID, key, r.Data)
		if err != nil {
			return nil, err
		}
	} else {
		r.Data.ViewerRole = "admin"
	}
	if err = r.projectedEntries(ctx); err != nil {
		return nil, err
	}
	if !r.borrowed {
		if err = tx.Rollback(); err != nil {
			return nil, err
		}
	}
	if err = r.Validate(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *DocumentDirectoryRead) callerSource(ctx context.Context, data domain.Bootstrap) error {
	candidate := domain.Bootstrap{
		Workspace: data.Workspace, Viewer: data.Viewer, Teams: data.Teams,
		TeamSettings: data.TeamSettings, TeamParents: data.TeamParents,
		WorkspaceSettings: data.WorkspaceSettings, Favorites: data.Favorites,
		Documents: data.Documents,
	}
	source, err := captureMetadataLogicalSource(ctx, candidate, r.stamp)
	if err != nil {
		return err
	}
	r.owner.source.Release()
	r.owner.source = source
	return nil
}

func (r *DocumentDirectoryRead) headers(ctx context.Context) (bool, error) {
	r.Data.Documents, r.nodes, r.Entries = nil, nil, nil
	fields := []string{}
	typ := reflect.TypeFor[domain.Document]()
	for index := 0; index < typ.NumField(); index++ {
		name := typ.Field(index).Name
		if !slices.Contains([]string{"Content", "ContentState", "ContentData", "Revisions", "Attachments"}, name) {
			fields = append(fields, name)
		}
	}
	seen, duplicate := map[string]bool{}, false
	err := r.owner.visitNative(ctx, "documents", func(index int, node metadataLogicalNode) error {
		value, err := r.header(ctx, node, fields)
		if err != nil {
			return err
		}
		header := value.Interface().(domain.Document)
		duplicate = duplicate || seen[header.ID]
		seen[header.ID] = true
		r.Data.Documents = append(r.Data.Documents, header)
		r.Entries = append(r.Entries, DocumentDirectoryEntry{header, index})
		r.nodes = append(r.nodes, node)
		return nil
	})
	return duplicate, err
}

func (r *DocumentDirectoryRead) header(ctx context.Context, node metadataLogicalNode, fields []string) (reflect.Value, error) {
	cost := uint64(2048)
	if err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if slices.Contains(fields, key.String()) {
			if child.cost > (metadataWorkBytes-cost)/8 {
				return ErrMetadataWorkBudget
			}
			cost += child.cost * 8
		}
		return nil
	}); err != nil {
		return reflect.Value{}, err
	}
	if err := r.owner.reserve(ctx, cost); err != nil {
		return reflect.Value{}, err
	}
	value := reflect.New(node.typ).Elem()
	err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if !slices.Contains(fields, key.String()) {
			return nil
		}
		var field reflect.Value
		var err error
		if child.unavailable {
			// Headers are read-only. Native timestamps remain native when
			// available; wire-only locations support JSON/time comparisons.
			field, err = child.WireValue(ctx, int(cost))
		} else {
			field, err = child.Value(ctx, int(cost))
		}
		if err == nil {
			value.FieldByName(key.String()).Set(field)
		}
		return err
	})
	return value, err
}

// Projection keeps document headers intact except Favorite. Match occurrences
// within each ID bucket so a hidden duplicate cannot select another body.
func (r *DocumentDirectoryRead) projectedEntries(ctx context.Context) error {
	if err := r.owner.reserve(ctx, uint64(len(r.Entries))*1024+4096); err != nil {
		return err
	}
	byID := map[string][]int{}
	for index, entry := range r.Entries {
		byID[entry.Document.ID] = append(byID[entry.Document.ID], index)
	}
	entries := make([]DocumentDirectoryEntry, 0, len(r.Data.Documents))
	r.visible = map[int]bool{}
	for _, header := range r.Data.Documents {
		found := false
		for position, index := range byID[header.ID] {
			before := r.Entries[index].Document
			before.Favorite = header.Favorite
			if !reflect.DeepEqual(before, header) {
				continue
			}
			entry := DocumentDirectoryEntry{header, r.Entries[index].Ordinal}
			entries = append(entries, entry)
			r.visible[entry.Ordinal] = header.Favorite
			byID[header.ID] = slices.Delete(byID[header.ID], position, position+1)
			found = true
			break
		}
		if !found {
			return ErrBoundedMetadataCoverage
		}
	}
	r.Entries = entries
	return nil
}

func (r *DocumentDirectoryRead) Validate(ctx context.Context) error {
	if r == nil || r.owner == nil || r.owner.source == nil {
		return errMetadataLogicalClosed
	}
	if !r.borrowed && r.principal && r.owner.store.db.journalReady.Load() {
		epoch, err := r.owner.store.AuthzEpoch(ctx, r.stamp.workspaceID)
		if err != nil {
			return err
		}
		if epoch != r.epoch {
			return errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
		}
	}
	stamp, err := readMetadataStamp(ctx, r.owner.store.db, r.workspace)
	if err != nil {
		return err
	}
	if stamp != r.stamp {
		return errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
	}
	return ctx.Err()
}

// MatchSearch streams strings.ToLower(Title+" "+Content), including matches
// crossing the separator or a UTF-8/chunk boundary.
func (r *DocumentDirectoryRead) MatchSearch(ctx context.Context, ordinal int, search string) (bool, error) {
	if _, ok := r.visible[ordinal]; !ok || ordinal < 0 || ordinal >= len(r.nodes) {
		return false, ErrMetadataRecordQuery
	}
	if search == "" {
		return true, ctx.Err()
	}
	if len(search) > (metadataWorkBytes-(64<<10))/16 {
		return false, ErrMetadataWorkBudget
	}
	scratch := &ResourceRead{store: r.owner.store}
	defer scratch.Close()
	if err := scratch.reserve(ctx, uint64(len(search))*16+64<<10); err != nil {
		return false, err
	}
	prefix := make([]int, len(search))
	for i, j := 1, 0; i < len(search); i++ {
		for j > 0 && search[i] != search[j] {
			j = prefix[j-1]
		}
		if search[i] == search[j] {
			j++
		}
		prefix[i] = j
	}
	matched, position := false, 0
	accept := func(value byte) {
		for position > 0 && value != search[position] {
			position = prefix[position-1]
		}
		if value == search[position] {
			position++
		}
		matched = position == len(search)
	}
	nodes := [2]metadataLogicalNode{}
	err := r.nodes[ordinal].Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		switch key.String() {
		case "Title":
			nodes[0] = child
		case "Content":
			nodes[1] = child
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	for index, node := range nodes {
		if index != 0 {
			accept(' ')
			if matched {
				return true, nil
			}
		}
		err := node.StreamBytes(ctx, func(input io.Reader) error {
			reader := bufio.NewReaderSize(input, 32<<10)
			var encoded [utf8.UTFMax]byte
			for {
				value, _, err := reader.ReadRune()
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
				for _, value := range encoded[:utf8.EncodeRune(encoded[:], unicode.ToLower(value))] {
					accept(value)
					if matched {
						return nil
					}
				}
			}
		})
		if err != nil || matched {
			return matched, err
		}
	}
	return false, nil
}

// DocumentDirectoryValue owns one complete output through its encoder. Close
// it before loading the next occurrence; RevisionError retains legacy partial
// hydration/logging behavior without hiding admission or cancellation failures.
type DocumentDirectoryValue struct {
	Document      domain.Document
	RevisionError error
	owner         *ResourceRead
}

func (r *DocumentDirectoryValue) Close() {
	if r != nil {
		r.Document, r.RevisionError = domain.Document{}, nil
		r.owner.Close()
	}
}

func (r *DocumentDirectoryRead) Load(ctx context.Context, ordinal int) (_ *DocumentDirectoryValue, err error) {
	favorite, ok := r.visible[ordinal]
	if !ok || ordinal < 0 || ordinal >= len(r.nodes) {
		return nil, ErrMetadataRecordQuery
	}
	if err := r.Validate(ctx); err != nil {
		return nil, err
	}
	value := &DocumentDirectoryValue{owner: &ResourceRead{store: r.owner.store}}
	defer func() {
		if err != nil {
			value.Close()
		}
	}()
	decoded, err := value.owner.node(ctx, r.nodes[ordinal], true)
	if err != nil {
		return nil, err
	}
	value.Document = decoded.Interface().(domain.Document)
	value.Document.Favorite = favorite
	err = value.revisions(ctx, r.workspace)
	if errors.Is(err, ErrMetadataWorkBudget) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	value.RevisionError, err = err, nil
	return value, nil
}

type documentDirectoryBodyBuffer struct {
	bytes.Buffer
	ctx   context.Context
	owner *ResourceRead
}

func (b *documentDirectoryBodyBuffer) Write(data []byte) (int, error) {
	if len(data) != 0 {
		if err := b.owner.reserve(b.ctx, uint64(len(data))*32); err != nil {
			return 0, err
		}
	}
	return b.Buffer.Write(data)
}

func (r *DocumentDirectoryValue) revisions(ctx context.Context, workspace string) error {
	bodies := map[string][]byte{}
	seen := map[string]bool{}
	for _, revision := range r.Document.Revisions {
		ref, found := revision.Offloaded["body"]
		if !found {
			continue
		}
		if seen[ref.Key] {
			continue
		}
		seen[ref.Key] = true
		if body, found := r.owner.store.db.content.cache.get(workspace + "/" + ref.Key); found {
			if err := r.owner.reserve(ctx, uint64(len(body))*32+4096); err != nil {
				return err
			}
			bodies[ref.Key] = body
			continue
		}
		buffer := &documentDirectoryBodyBuffer{ctx: ctx, owner: r.owner}
		err := r.streamRevisionBody(ctx, workspace, ref, buffer)
		if errors.Is(err, errOffloadedObjectMissing) || errors.Is(err, fs.ErrNotExist) || err != nil && (strings.Contains(strings.ToLower(err.Error()), "nosuchkey") || strings.Contains(strings.ToLower(err.Error()), "not found")) {
			continue
		}
		if err != nil {
			return err
		}
		body := buffer.Bytes()
		bodies[ref.Key] = body
		r.owner.store.db.content.cache.put(workspace+"/"+ref.Key, body)
	}
	for index := range r.Document.Revisions {
		revision := &r.Document.Revisions[index]
		ref, found := revision.Offloaded["body"]
		body, loaded := bodies[ref.Key]
		if !found || !loaded {
			continue
		}
		work, err := metadataAggregatePayloadJSONWork(ctx, body)
		if err != nil {
			return fmt.Errorf("document version %s: %w", revision.ID, err)
		}
		if work != 0 {
			if err := r.owner.reserve(ctx, work); err != nil {
				return err
			}
		}
		var fields struct {
			Content      string         `json:"content"`
			ContentState string         `json:"contentState"`
			ContentData  map[string]any `json:"contentData"`
		}
		if err := json.Unmarshal(body, &fields); err != nil {
			return fmt.Errorf("document version %s: %w", revision.ID, err)
		}
		revision.Content, revision.ContentState, revision.ContentData = fields.Content, fields.ContentState, fields.ContentData
		revision.Offloaded = nil
	}
	return nil
}

// The general streaming API rejects an empty key. The old typed revision
// loader still looks it up, including SQL-created rows with that literal key.
func (r *DocumentDirectoryValue) streamRevisionBody(ctx context.Context, workspace string, ref domain.ContentRef, dst io.Writer) error {
	s := r.owner.store
	if ref.Key != "" {
		return s.StreamOffloadedBody(ctx, workspace, ref, dst)
	}
	var size sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT LENGTH(data) FROM metadata_blobs WHERE workspace_key=? AND blob_key=?`, workspace, ref.Key).Scan(&size); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errOffloadedObjectMissing
		}
		return err
	}
	var source io.Reader
	if size.Valid {
		source = &offloadedChunkReader{ctx: ctx, reader: s.db, workspace: workspace, key: ref.Key, size: size.Int64}
	} else {
		if s.db.content.objects == nil {
			return errOffloadedObjectMissing
		}
		body, _, _, err := s.db.content.objects.Open(ctx, offloadedObjectKey(workspace, ref.Key))
		if err != nil {
			return err
		}
		defer body.Close()
		source = body
	}
	zipped, err := gzip.NewReader(offloadedContextReader{ctx, source})
	if err != nil {
		return err
	}
	defer zipped.Close()
	_, err = io.CopyBuffer(dst, offloadedContextReader{ctx, zipped}, make([]byte, 32<<10))
	return err
}
