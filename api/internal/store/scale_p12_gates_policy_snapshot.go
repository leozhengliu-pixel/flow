package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

type resourcePolicySnapshotKey struct{}
type resourcePolicySnapshot struct {
	reader    *sqlTx
	workspace string
	stamp     copyStamp
	policy    domain.Bootstrap
}

const legacyTeamPolicyPrefixBytes = metadataRecordChunkBytes / 4

// Only BeginWorkspaceRead publishes this witness for complete metadata or a
// pre-SQL installed policy, validated at the same repeatable SQL stamp.
func (s *SQLiteStore) ownedResourcePolicy(ctx context.Context, workspace, workspaceID string) (domain.Bootstrap, bool) {
	snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead)
	witness, complete := ctx.Value(resourcePolicySnapshotKey{}).(resourcePolicySnapshot)
	if !ok || !complete || s.metadataResidency != nil || snapshot.database != s.db || snapshot.reader == nil ||
		snapshot.reader != witness.reader || snapshot.workspace != workspace || witness.workspace != workspace ||
		witness.stamp.workspaceID != workspaceID || snapshot.metadata.Workspace.ID != workspaceID ||
		witness.policy.Workspace.ID != workspaceID || snapshot.reader.writtenBytes != 0 || snapshot.reader.metadataWritten {
		return domain.Bootstrap{}, false
	}
	current, err := readMetadataStamp(ctx, snapshot.reader, workspace)
	if err != nil || current != witness.stamp {
		return domain.Bootstrap{}, false
	}
	return resourcePolicyMetadata(witness.policy), true
}

// SQL JSON paths do not reproduce encoding/json's folded aliases, duplicate
// order or null no-op. Stream the original tokens, retaining only policy
// strings; even very large unrelated guidance stays in fixed-size chunks.
func (s *SQLiteStore) readLegacyTeamPolicySettings(ctx context.Context, workspace string, target map[string]domain.TeamSettings) error {
	length, storage, body := "LENGTH(r.data)", "''", "r.data"
	if s.dialect == "sqlite" {
		length, storage, body = "LENGTH(CAST(r.data AS BLOB))", "typeof(r.record_key)", "CAST(r.data AS BLOB)"
	} else if s.dialect == "postgres" {
		length = "OCTET_LENGTH(r.data)"
	}
	snapshot, pinned := ctx.Value(workspaceReadKey{}).(workspaceRead)
	pinned = pinned && snapshot.database == s.db && snapshot.reader != nil
	last, lastOrder, started := any(""), 0, false
	for {
		writtenBytes, metadataWrite := 0, false
		if pinned {
			writtenBytes, metadataWrite = snapshot.reader.writtenBytes, snapshot.reader.metadataWritten
		}
		where := "r.workspace_key=? AND r.field='teamSettings'"
		args := []any{legacyTeamPolicyPrefixBytes, workspace}
		if started {
			where += " AND (r.collection_order>? OR (r.collection_order=? AND r.record_key>?))"
			args = append(args, lastOrder, lastOrder, last)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT r.record_key,`+storage+`,r.collection_order,`+length+`,SUBSTR(`+body+`,1,?) FROM workspace_metadata_records r WHERE `+where+` ORDER BY r.collection_order,r.record_key LIMIT 32`, args...)
		if err != nil {
			return err
		}
		type header struct {
			key, storage string
			order        int
			size         int64
			prefix       []byte
		}
		page := make([]header, 0, 32)
		for rows.Next() {
			var item header
			if err := rows.Scan(&item.key, &item.storage, &item.order, &item.size, &item.prefix); err != nil {
				rows.Close()
				return err
			}
			if item.size < 0 {
				rows.Close()
				return ErrMetadataRecordQuery
			}
			page = append(page, item)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, item := range page {
			input := &legacyTeamPolicyReaderAt{
				ctx: ctx, store: s, workspace: workspace, key: item.key, size: item.size,
				blob: s.dialect == "mysql" || item.storage == "blob", chunk: item.prefix,
				writtenBytes: writtenBytes, metadataWrite: metadataWrite,
			}
			settings, err := readLegacyTeamPolicyJSON(ctx, io.NewSectionReader(input, 0, item.size))
			if err != nil {
				return err
			}
			target[item.key] = settings
		}
		if len(page) < 32 {
			return nil
		}
		tail := page[len(page)-1]
		last, lastOrder, started = tail.key, tail.order, true
		if s.dialect == "mysql" || tail.storage == "blob" {
			last = []byte(tail.key)
		}
	}
}

type legacyTeamPolicyReaderAt struct {
	ctx            context.Context
	store          *SQLiteStore
	workspace, key string
	size           int64
	blob           bool
	mu             sync.Mutex
	chunk          []byte
	chunkOffset    int64
	writtenBytes   int
	metadataWrite  bool
}

func (r *legacyTeamPolicyReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, ErrMetadataRecordQuery
	}
	if len(p) == 0 {
		return 0, nil
	}
	if offset >= r.size {
		return 0, io.EOF
	}
	key, body := any(r.key), "data"
	if r.blob {
		key = []byte(r.key)
	}
	if r.store.dialect == "sqlite" {
		body = "CAST(data AS BLOB)"
	}
	snapshot, pinned := r.ctx.Value(workspaceReadKey{}).(workspaceRead)
	pinned = pinned && snapshot.database == r.store.db && snapshot.reader != nil
	if pinned && (snapshot.reader.writtenBytes != r.writtenBytes || snapshot.reader.metadataWritten != r.metadataWrite) {
		r.chunk = nil
	}
	n := 0
	for n < len(p) && offset+int64(n) < r.size {
		position := offset + int64(n)
		if pinned && position >= r.chunkOffset && position < r.chunkOffset+int64(len(r.chunk)) {
			n += copy(p[n:], r.chunk[position-r.chunkOffset:])
			continue
		}
		start := position
		size := min(int64(metadataRecordChunkBytes), int64(len(p)-n), r.size-position)
		if pinned {
			// Token ranges revisit keys and values in the original JSON.
			// Share one bounded page within the owned SQL snapshot.
			start = position / int64(metadataRecordChunkBytes) * int64(metadataRecordChunkBytes)
			size = min(int64(metadataRecordChunkBytes), r.size-start)
		}
		var chunk []byte
		if err := r.store.db.QueryRowContext(r.ctx, `SELECT SUBSTR(`+body+`,?,?) FROM workspace_metadata_records WHERE workspace_key=? AND field='teamSettings' AND record_key=?`, start+1, size, r.workspace, key).Scan(&chunk); err != nil {
			return n, err
		}
		if int64(len(chunk)) != size {
			return n, io.ErrUnexpectedEOF
		}
		if pinned {
			r.chunk, r.chunkOffset = chunk, start
			r.writtenBytes, r.metadataWrite = snapshot.reader.writtenBytes, snapshot.reader.metadataWritten
		}
		n += copy(p[n:], chunk[position-start:])
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func readLegacyTeamPolicyJSON(ctx context.Context, input *io.SectionReader) (domain.TeamSettings, error) {
	var result domain.TeamSettings
	scanner := metadataCollectionScanner{ctx: ctx, input: bufio.NewReaderSize(input, metadataRecordChunkBytes)}
	if err := scanner.spaces(); err != nil {
		return result, err
	}
	first, err := scanner.peek()
	if err != nil {
		return result, err
	}
	if first == 'n' {
		if err := scanner.literal("null"); err != nil {
			return result, err
		}
		return result, scanner.end()
	}
	if first != '{' {
		return result, ErrMetadataRecordQuery
	}
	fields := []struct {
		name   string
		target *string
	}{
		{"parentTeamId", &result.ParentTeamID},
		{"access", &result.Access},
		{"membershipRestriction", &result.MembershipRestriction},
		{"settingsPermission", &result.SettingsPermission},
		{"labelPermission", &result.LabelPermission},
		{"memberPermission", &result.MemberPermission},
	}
	err = scanner.object(func(key, value metadataCollectionRange) error {
		if key.size > 6*int64(len("membershipRestriction"))+2 {
			return nil
		}
		var name string
		if err := json.NewDecoder(io.NewSectionReader(input, key.offset, key.size)).Decode(&name); err != nil {
			return err
		}
		for _, field := range fields {
			if !strings.EqualFold(field.name, name) {
				continue
			}
			var selected strings.Builder
			null, err := VisitMetadataJSONStringChunks(ctx, io.NewSectionReader(input, value.offset, value.size), func(chunk []byte) error {
				_, err := selected.Write(chunk)
				return err
			})
			if err != nil {
				return err
			}
			if !null {
				*field.target = selected.String()
			}
			break
		}
		return nil
	}, 0)
	if err != nil {
		return result, err
	}
	return result, scanner.end()
}
