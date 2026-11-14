package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"flow/api/internal/domain"
)

const scaleP21SW16FormerMapBytes = 16 << 20
const scaleP21SW16FormerWorkBytes = 64 << 20

// FormerMembersRead owns the returned ID map and its output admission. Members
// is read-only and must not be retained after Close. Always close a nonnil
// handle, including when BeginFormerMembersRead also returns an error.
type FormerMembersRead struct {
	members  map[string]bool
	releases []func()
	once     sync.Once
}

func (r *FormerMembersRead) Members() map[string]bool { return r.members }

func (r *FormerMembersRead) Close() error {
	if r != nil {
		r.once.Do(func() {
			r.members = nil
			for _, release := range r.releases {
				release()
			}
			r.releases = nil
		})
	}
	return nil
}

// This trusted directory read keeps the membership/departure authority ahead
// of the development-mode metadata fallback. Only IDs survive the operation.
func (s *SQLiteStore) BeginFormerMembersRead(ctx context.Context, workspace string) (*FormerMembersRead, error) {
	if !s.BoundedMetadataEnabled() {
		members, err := s.FormerMembers(ctx, workspace)
		return &FormerMembersRead{members: members}, err
	}
	read := &FormerMembersRead{members: map[string]bool{}}
	former := read.members
	s.mu.RLock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, found := s.workspaces[workspace]
	workspaceID := data.Workspace.ID
	s.mu.RUnlock()
	if !found || workspaceID == "" {
		return read, nil
	}
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 2<<20)
	if err != nil {
		return read, err
	}
	defer release()
	var tx *sqlTx
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db {
		if snapshot.workspace != workspace || snapshot.reader == nil {
			return read, ErrMetadataRecordQuery
		}
		tx = snapshot.reader
	} else {
		tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return read, err
		}
		defer tx.Rollback()
	}
	remaining := scaleP21SW16FormerMapBytes
	add := func(id string) error {
		if former[id] {
			return nil
		}
		cost := len(id) + 128
		if cost > remaining {
			return ErrMetadataWorkBudget
		}
		remaining -= cost
		former[id] = true
		return nil
	}
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM workspace_memberships WHERE workspace_id=? LIMIT 1`, workspaceID).Scan(&exists)
	if err == nil {
		length := s.scaleP21SW16FormerIDLength()
		var count, bytes int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM("+length+"),0) FROM ("+formerMembersQuery+") sw16_former", workspaceID, workspaceID).Scan(&count, &bytes); err != nil {
			return read, err
		}
		if count < 0 || bytes < 0 || count > scaleP21SW16FormerMapBytes/128 || bytes > scaleP21SW16FormerMapBytes-count*128 {
			return read, ErrMetadataWorkBudget
		}
		// Owned output, map buckets and one driver's largest page remain
		// charged together. Ordinary small directories reserve small work.
		_, releaseOutput, err := s.tryAdmitAdditionalMetadataWork(ctx, int(count)*384+int(bytes)*4+128<<10)
		if err != nil {
			return read, err
		}
		read.releases = append(read.releases, releaseOutput)
		return read, s.scaleP21SW16FormerMembershipRows(ctx, tx, workspaceID, &remaining, add)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return read, err
	}
	return s.scaleP21SW16FallbackFormerMembers(ctx, tx, workspace, read, add, false)
}

func (s *SQLiteStore) scaleP21SW16FallbackFormerMembers(ctx context.Context, tx *sqlTx, workspace string, read *FormerMembersRead, add func(string) error, canonicalSource bool) (*FormerMembersRead, error) {
	collection, err := s.MetadataCollectionsInSnapshot(ctx, tx, workspace, "members")
	if err != nil {
		return read, err
	}
	defer collection.Close()
	shape, err := collection.Shape("members")
	if err != nil {
		return read, err
	}
	if !canonicalSource {
		if shape.RequiresTypedMerge {
			return read, fmt.Errorf("%w: former-member fallback needs logical source provenance", ErrBoundedMetadataCoverage)
		}
		present := false
		err := collection.VisitArray("members", func(context.Context, MetadataArrayRecord) error {
			present = true
			return ErrMetadataIterationDone
		})
		if err != nil {
			return read, err
		}
		if present {
			// Full workspace writes can sanitize a canonical inline value
			// while the legacy warm copy retains the callback's value.
			return read, fmt.Errorf("%w: former-member fallback needs logical source provenance", ErrBoundedMetadataCoverage)
		}
		return read, nil
	}
	ctx, releaseFallback, err := s.tryAdmitAdditionalMetadataWork(ctx, scaleP21SW16FormerWorkBytes)
	if err != nil {
		return read, err
	}
	read.releases = append(read.releases, releaseFallback)
	if shape.RequiresTypedMerge {
		return read, s.scaleP21SW16MergedFormerMembers(ctx, collection, add)
	}
	err = collection.VisitArray("members", func(recordCtx context.Context, record MetadataArrayRecord) error {
		cost, err := scaleP21SW16MemberDecodeCost(record)
		if err != nil {
			return err
		}
		recordCtx, release, err := s.tryAdmitAdditionalMetadataWork(recordCtx, cost)
		if err != nil {
			return err
		}
		defer release()
		var member domain.WorkspaceMember
		if err := scaleP21SW16DecodeMember(recordCtx, record, &member); err != nil {
			return err
		}
		if member.Status == "suspended" {
			return add(member.User.ID)
		}
		return nil
	})
	return read, err
}

func (s *SQLiteStore) scaleP21SW16FormerIDLength() string {
	length := "LENGTH(user_id)"
	if s.dialect == "sqlite" {
		length = "LENGTH(CAST(user_id AS BLOB))"
	} else if s.dialect == "postgres" {
		length = "OCTET_LENGTH(user_id)"
	}
	return length
}

func (s *SQLiteStore) scaleP21SW16FormerMembershipRows(ctx context.Context, tx *sqlTx, workspaceID string, remaining *int, add func(string) error) error {
	length, order := s.scaleP21SW16FormerIDLength(), "CAST(user_id AS BLOB)"
	if s.dialect == "mysql" {
		order = "CAST(user_id AS BINARY)"
	} else if s.dialect == "postgres" {
		order = `user_id COLLATE "C"`
	}
	source := " FROM (" + formerMembersQuery + ") sw16_former ORDER BY " + order
	for offset := 0; ; {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT "+length+source+" LIMIT 64 OFFSET ?", workspaceID, workspaceID, offset)
		if err != nil {
			return err
		}
		count, pageBytes := 0, 0
		for rows.Next() {
			var size int
			if err := rows.Scan(&size); err != nil {
				rows.Close()
				return err
			}
			if size < 0 || size+128 > *remaining-pageBytes {
				rows.Close()
				return ErrMetadataWorkBudget
			}
			if count > 0 && size > (1<<20)-pageBytes {
				break
			}
			count++
			pageBytes += size + 128
			if pageBytes >= 1<<20 {
				break
			}
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		if count == 0 {
			return nil
		}
		rows, err = tx.QueryContext(ctx, "SELECT user_id"+source+" LIMIT ? OFFSET ?", workspaceID, workspaceID, count, offset)
		if err != nil {
			return err
		}
		actual := 0
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if err := add(id); err != nil {
				rows.Close()
				return err
			}
			actual++
		}
		err = rows.Err()
		closeErr = rows.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		if actual != count {
			return ErrMetadataRecordQuery
		}
		offset += count
	}
}

func scaleP21SW16MemberDecodeCost(record MetadataArrayRecord) (int, error) {
	if record.EncodedBytes < 0 || record.EncodedBytes > (metadataWorkBytes-(128<<10))/8 {
		return 0, ErrMetadataWorkBudget
	}
	reader, err := ReplayMetadataReader(record.Reader, record.EncodedBytes)
	if err != nil {
		return 0, err
	}
	cost := int(record.EncodedBytes)*8 + 128<<10
	quoted, escaped := false, false
	var buffer [metadataRecordChunkBytes]byte
	for {
		n, err := reader.Read(buffer[:])
		for _, c := range buffer[:n] {
			if quoted {
				if escaped {
					escaped = false
				} else if c == '\\' {
					escaped = true
				} else if c == '"' {
					quoted = false
				}
				continue
			}
			switch c {
			case '"':
				quoted = true
				cost += 128
			case '{', '[':
				cost += 1024
			case ':', ',':
				cost += 128
			}
			if cost > metadataWorkBytes {
				return 0, ErrMetadataWorkBudget
			}
		}
		if err == io.EOF {
			return cost, nil
		}
		if err != nil {
			return 0, err
		}
	}
}

func scaleP21SW16DecodeMember(ctx context.Context, record MetadataArrayRecord, member *domain.WorkspaceMember) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reader, err := ReplayMetadataReader(record.Reader, record.EncodedBytes)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(member); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = ErrMetadataRecordQuery
		}
		return err
	}
	return nil
}

func (s *SQLiteStore) scaleP21SW16MergedFormerMembers(ctx context.Context, read *MetadataCollectionRead, add func(string) error) error {
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, scaleP21SW16FormerWorkBytes)
	if err != nil {
		return err
	}
	defer release()
	remaining := scaleP21SW16FormerWorkBytes
	var members []domain.WorkspaceMember
	err = read.VisitArrayOccurrences("members", func(_ context.Context, occurrence MetadataArrayOccurrence) error {
		switch {
		case occurrence.Null:
			members = nil
			return nil
		case occurrence.Begin:
			members = members[:0]
			return nil
		case occurrence.End:
			if occurrence.Ordinal == 0 {
				members = []domain.WorkspaceMember{}
			} else if occurrence.Ordinal != int64(len(members)) {
				return ErrMetadataRecordQuery
			}
			return nil
		}
		cost, err := scaleP21SW16MemberDecodeCost(occurrence.MetadataArrayRecord)
		if err != nil || cost > remaining {
			if err != nil {
				return err
			}
			return fmt.Errorf("%w: repeated member fields exceed typed merge allowance", ErrMetadataWorkBudget)
		}
		remaining -= cost
		index := int(occurrence.Ordinal)
		if occurrence.Append {
			index = len(members)
			members = append(members, domain.WorkspaceMember{})
		} else if index < cap(members) {
			members = members[:index+1]
		} else if index == len(members) {
			members = append(members, domain.WorkspaceMember{})
		} else {
			return ErrMetadataRecordQuery
		}
		return scaleP21SW16DecodeMember(ctx, occurrence.MetadataArrayRecord, &members[index])
	})
	if err != nil {
		return err
	}
	for _, member := range members {
		if member.Status == "suspended" {
			if err := add(member.User.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
