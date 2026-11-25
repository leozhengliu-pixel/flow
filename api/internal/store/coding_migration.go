package store

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW17JSONRange struct{ start, size int64 }

// The detached input preserves one read snapshot while each native environment
// insert keeps its original autocommit behavior, including on one SQL connection.
func (s *SQLiteStore) scaleP21SW17MigrateCodingEnvironments(ctx context.Context, workspace string) (int, error) {
	scanCtx, scanRelease, err := s.admitMetadataWork(ctx, metadataPolicyBytes+(8<<20))
	if err != nil {
		return 0, err
	}
	defer scanRelease()
	ctx = scanCtx
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if !s.HasWorkspace(workspace) {
		return 0, nil
	}
	input, err := NewRecordSpool()
	if err != nil {
		return 0, err
	}
	defer input.Close()
	size, createdAt, err := s.scaleP21SW17DetachCodingSettings(scanCtx, workspace, input)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	read := func(part scaleP21SW17JSONRange) io.Reader {
		return io.NewSectionReader(input, part.start, part.size)
	}
	scanner := scaleP21SW17JSONScanner{ctx: ctx, reader: bufio.NewReaderSize(read(scaleP21SW17JSONRange{size: size}), metadataRecordChunkBytes), readRange: read}
	migrated := 0
	_, count, err := scanner.settings(func(index int, part scaleP21SW17JSONRange) error {
		if part.size > (metadataPolicyBytes+(8<<20)-(2<<20))/3 {
			return ErrMetadataWorkBudget
		}
		// This type has only strings and a boolean: charge decoder-buffer
		// growth plus owned strings before validating imported large values.
		workCtx, release, err := s.admitMetadataWork(ctx, max(2<<20, int(part.size)*3+(2<<20)))
		if err != nil {
			return err
		}
		defer release()
		var legacy domain.CodingAgentEnvironment
		decoder := json.NewDecoder(read(part))
		if err := decoder.Decode(&legacy); err != nil {
			return err
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return fmt.Errorf("legacy coding environment has trailing JSON: %w", err)
		}
		environment, ok := legacyCodingEnvironment(workspace, index, legacy, createdAt)
		if !ok {
			slog.Warn("skipped a legacy coding environment with an unusable repository", "workspace", workspace, "environment", legacy.ID, "name", legacy.Name, "repository", legacy.Repository)
			return nil
		}
		var exists int
		if err := s.db.QueryRowContext(workCtx, `SELECT COUNT(*) FROM coding_environments WHERE id=?`, environment.ID).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			return nil
		}
		err = s.insertCodingEnvironment(workCtx, s.db, environment)
		if errors.Is(err, domain.ErrCodingRepoInUse) && environment.ArchivedAt == nil {
			archived := createdAt
			environment.ArchivedAt = &archived
			err = s.insertCodingEnvironment(workCtx, s.db, environment)
		}
		if err == nil {
			migrated++
		}
		return err
	}, nil)
	if err != nil || count == 0 {
		return migrated, err
	}
	workCtx, release, err := s.admitMetadataWork(ctx, metadataPolicyBytes+(8<<20))
	if err != nil {
		return migrated, err
	}
	defer release()
	if err := s.scaleP21SW17ClearCodingEnvironments(workCtx, workspace, read, size); err != nil {
		return migrated, err
	}
	s.dropMetadataCache(workCtx, workspace)
	s.mu.Lock()
	if !slices.Contains(s.startupCacheDrops, workspace) {
		s.startupCacheDrops = append(s.startupCacheDrops, workspace)
	}
	s.mu.Unlock()
	return migrated, nil
}

func (s *SQLiteStore) scaleP21SW17DetachCodingSettings(ctx context.Context, workspace string, dst io.Writer) (int64, time.Time, error) {
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return 0, time.Time{}, err
	}
	defer tx.Rollback()
	var size int64
	if err := tx.QueryRowContext(ctx, `SELECT LENGTH(data) FROM workspace_metadata_records WHERE workspace_key=? AND field='workspaceSettings' AND record_key='codingAgentSettings'`, workspace).Scan(&size); err != nil {
		return 0, time.Time{}, err
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field='workspaceSettings' AND record_key='updatedAt'`, MetadataRecordPageMaxBytes, workspace).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, time.Time{}, err
	}
	if err == nil && raw == nil {
		return 0, time.Time{}, ErrMetadataPageBudget
	}
	var createdAt time.Time
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &createdAt); err != nil {
			return 0, time.Time{}, err
		}
	}
	reader := &canonicalMetadataReader{ctx: ctx, tx: tx, workspace: workspace, key: RecordKey{Kind: "workspaceSettings", ID: "codingAgentSettings"}, size: size}
	_, err = io.CopyBuffer(dst, reader, make([]byte, metadataRecordChunkBytes))
	if err != nil {
		return 0, time.Time{}, err
	}
	return size, createdAt, tx.Rollback()
}

func (s *SQLiteStore) scaleP21SW17ClearCodingEnvironments(ctx context.Context, workspace string, baseRead func(scaleP21SW17JSONRange) io.Reader, baseSize int64) error {
	tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lock := ""
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var root []byte
	if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?`+lock, MetadataRecordPageMaxBytes, workspace).Scan(&root); err != nil {
		return err
	}
	if _, _, err := residentMetadataRoot(root); err != nil {
		return err
	}
	var order int
	if err := tx.QueryRowContext(ctx, `SELECT collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field='workspaceSettings' AND record_key='codingAgentSettings'`+lock, workspace).Scan(&order); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var remaining bytes.Buffer
	remaining.WriteByte('{')
	fields := 0
	scanner := scaleP21SW17JSONScanner{ctx: ctx, reader: bufio.NewReaderSize(baseRead(scaleP21SW17JSONRange{size: baseSize}), metadataRecordChunkBytes), readRange: baseRead}
	_, _, err = scanner.settings(nil, func(name string, part scaleP21SW17JSONRange) error {
		encoded, _ := json.Marshal(name)
		if int64(remaining.Len()+len(encoded)+3)+part.size > MetadataRecordPageMaxBytes {
			return ErrMetadataPageBudget
		}
		if fields > 0 {
			remaining.WriteByte(',')
		}
		fields++
		remaining.Write(encoded)
		remaining.WriteByte(':')
		_, err := io.CopyN(&remaining, baseRead(part), part.size)
		return err
	})
	if err != nil {
		return err
	}
	remaining.WriteByte('}')
	var settings domain.CodingAgentSettings
	if err := json.Unmarshal(remaining.Bytes(), &settings); err != nil {
		return err
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	budget := newMetadataHydrationBudget()
	if err := budget.consume(encoded); err != nil {
		return err
	}
	// The legacy delta treats WorkspaceSettings as a struct, not a rebaseable
	// collection. Its changed codingAgentSettings property replaces the
	// original property wholesale, while untouched properties stay stored.
	if err := writeMetadataRecordRow(ctx, tx, workspace, metadataRecordKey{field: "workspaceSettings", key: "codingAgentSettings"}, metadataRecord{data: encoded, order: order}, nil); err != nil {
		return err
	}
	probe := &SQLiteStore{db: s.db, dialect: s.dialect, metadataResidency: newMetadataResidency()}
	probe.metadataResidency.requireCoverage = false
	policy, stamp, cost, err := probe.readResidentWorkspace(ctx, tx, workspace)
	if err != nil {
		return err
	}
	s.metadataResidency.mu.Lock()
	total := cost
	for key, bytes := range s.metadataResidency.policy {
		if key != workspace {
			total += bytes
		}
	}
	limit := s.metadataResidency.policyLimit
	s.metadataResidency.mu.Unlock()
	if total > limit {
		return ErrMetadataWorkBudget
	}
	head, err := residentJournalHead(ctx, tx, policy.Workspace.ID)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.installResidentPolicy(workspace, policy, stamp, head, cost)
}

type scaleP21SW17JSONScanner struct {
	ctx       context.Context
	reader    *bufio.Reader
	readRange func(scaleP21SW17JSONRange) io.Reader
	offset    int64
}

func (s *scaleP21SW17JSONScanner) byte() (byte, error) {
	if s.offset&((4<<10)-1) == 0 && s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			return 0, err
		}
	}
	c, err := s.reader.ReadByte()
	if err == nil {
		s.offset++
	}
	return c, err
}

func (s *scaleP21SW17JSONScanner) peek() (byte, error) {
	p, err := s.reader.Peek(1)
	if err != nil {
		return 0, err
	}
	return p[0], nil
}

func (s *scaleP21SW17JSONScanner) spaces() error {
	for {
		c, err := s.peek()
		if err != nil {
			return err
		}
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			return nil
		}
		if _, err := s.byte(); err != nil {
			return err
		}
	}
}

func (s *scaleP21SW17JSONScanner) expect(want byte) error {
	if err := s.spaces(); err != nil {
		return err
	}
	c, err := s.byte()
	if err != nil {
		return err
	}
	if c != want {
		return fmt.Errorf("%w: unexpected coding settings token", ErrMetadataRecordQuery)
	}
	return nil
}

// Find value boundaries without decoding skipped arrays or strings. Canonical
// typed settings have bounded field names; each selected environment is decoded
// separately after its complete encoded size is known.
func (s *scaleP21SW17JSONScanner) value() (scaleP21SW17JSONRange, error) {
	if err := s.spaces(); err != nil {
		return scaleP21SW17JSONRange{}, err
	}
	part := scaleP21SW17JSONRange{start: s.offset}
	first, err := s.peek()
	if err != nil {
		return part, err
	}
	quoted, escaped, depth := false, false, 0
	composite := first == '{' || first == '[' || first == '"'
	for {
		c, err := s.peek()
		if errors.Is(err, io.EOF) && !composite {
			break
		}
		if err != nil {
			return part, err
		}
		if !composite && (c == ',' || c == ']' || c == '}' || c == ' ' || c == '\n' || c == '\r' || c == '\t') {
			break
		}
		if _, err := s.byte(); err != nil {
			return part, err
		}
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
				if first == '"' && depth == 0 {
					break
				}
			}
		} else {
			switch c {
			case '"':
				quoted = true
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					part.size = s.offset - part.start
					return part, nil
				}
			}
		}
	}
	part.size = s.offset - part.start
	if part.size == 0 {
		return part, ErrMetadataRecordQuery
	}
	return part, nil
}

func (s *scaleP21SW17JSONScanner) settings(environment func(int, scaleP21SW17JSONRange) error, field func(string, scaleP21SW17JSONRange) error) (scaleP21SW17JSONRange, int, error) {
	var environments scaleP21SW17JSONRange
	count := 0
	seen := map[string]bool{}
	if err := s.expect('{'); err != nil {
		return environments, count, err
	}
	for {
		if err := s.spaces(); err != nil {
			return environments, count, err
		}
		if c, _ := s.peek(); c == '}' {
			if _, err := s.byte(); err != nil {
				return environments, count, err
			}
			break
		}
		namePart, err := s.value()
		if err != nil || namePart.size > 1024 {
			if err == nil {
				err = ErrMetadataRecordQuery
			}
			return environments, count, err
		}
		nameRaw, err := io.ReadAll(s.readRange(namePart))
		var name string
		if err == nil {
			err = json.Unmarshal(nameRaw, &name)
		}
		if err != nil || seen[name] || len(seen) >= 256 {
			if err == nil {
				err = ErrMetadataRecordQuery
			}
			return environments, count, err
		}
		seen[name] = true
		if err := s.expect(':'); err != nil {
			return environments, count, err
		}
		if name != "environments" {
			part, err := s.value()
			if err == nil && field != nil {
				err = field(name, part)
			}
			if err != nil {
				return environments, count, err
			}
		} else {
			if err := s.spaces(); err != nil {
				return environments, count, err
			}
			environments.start = s.offset
			c, _ := s.peek()
			if c == '[' {
				if _, err := s.byte(); err != nil {
					return environments, count, err
				}
				for {
					if err := s.spaces(); err != nil {
						return environments, count, err
					}
					if c, _ := s.peek(); c == ']' {
						if _, err := s.byte(); err != nil {
							return environments, count, err
						}
						break
					}
					part, err := s.value()
					if err == nil && environment != nil {
						err = environment(count, part)
					}
					if err != nil {
						return environments, count, err
					}
					count++
					if err := s.spaces(); err != nil {
						return environments, count, err
					}
					c, _ := s.peek()
					if c == ']' {
						if _, err := s.byte(); err != nil {
							return environments, count, err
						}
						break
					}
					if err := s.expect(','); err != nil {
						return environments, count, err
					}
				}
			} else {
				part, err := s.value()
				if err != nil || part.size != 4 {
					if err == nil {
						err = ErrMetadataRecordQuery
					}
					return environments, count, err
				}
				raw, err := io.ReadAll(s.readRange(part))
				if err != nil || string(raw) != "null" {
					if err == nil {
						err = ErrMetadataRecordQuery
					}
					return environments, count, err
				}
			}
			environments.size = s.offset - environments.start
		}
		if err := s.spaces(); err != nil {
			return environments, count, err
		}
		if c, _ := s.peek(); c == '}' {
			if _, err := s.byte(); err != nil {
				return environments, count, err
			}
			break
		}
		if err := s.expect(','); err != nil {
			return environments, count, err
		}
	}
	if err := s.spaces(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = ErrMetadataRecordQuery
		}
		return environments, count, err
	}
	return environments, count, nil
}
