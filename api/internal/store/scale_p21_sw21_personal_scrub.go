package store

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"unicode/utf8"

	"flow/api/internal/domain"
)

const scaleP21SW21ScrubFixedWork = 2 << 20
const scaleP21SW21ScrubHeaderKeys = 8 << 10
const scaleP21SW21ScrubRecordWork = metadataWorkBytes - scaleP21SW21ScrubFixedWork

type scaleP21SW21ScrubHeader struct {
	keys      []string
	size      int64
	aggregate string
}

type scaleP21SW21ScrubReader struct {
	ctx    context.Context
	tx     *sqlTx
	target int
	column string
	scope  any
	header scaleP21SW21ScrubHeader
}

type scaleP21SW21ScrubReadError struct{ error }

func (e *scaleP21SW21ScrubReadError) Unwrap() error { return e.error }

func (r *scaleP21SW21ScrubReader) ReadAt(p []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, ErrMetadataRecordQuery
	}
	if len(p) == 0 {
		return 0, nil
	}
	if offset >= r.header.size {
		return 0, io.EOF
	}
	target := personScrubTargets[r.target]
	column := r.column
	switch r.tx.dialect {
	case "sqlite":
		column = "CAST(" + column + " AS BLOB)"
	case "mysql":
		column = "CAST(" + column + " AS BINARY)"
	case "postgres":
		if personTextColumns[column] {
			column = "convert_to(" + column + ",'UTF8')"
		}
	}
	where := target.scope + "=?"
	for _, key := range target.keys {
		where += " AND " + key + "=?"
	}
	n := 0
	for n < len(p) && offset+int64(n) < r.header.size {
		size := min(int64(metadataRecordChunkBytes), int64(len(p)-n), r.header.size-offset-int64(n))
		args := []any{offset + int64(n) + 1, size, r.scope}
		for _, key := range r.header.keys {
			args = append(args, key)
		}
		var chunk []byte
		if err := r.tx.QueryRowContext(r.ctx, "SELECT SUBSTR("+column+",?,?) FROM "+target.table+" WHERE "+where, args...).Scan(&chunk); err != nil {
			return n, &scaleP21SW21ScrubReadError{err}
		}
		if int64(len(chunk)) != size {
			return n, &scaleP21SW21ScrubReadError{io.ErrUnexpectedEOF}
		}
		n += copy(p[n:], chunk)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func scaleP21SW21ScrubByteLength(dialect, column string) string {
	if dialect == "sqlite" {
		return "LENGTH(CAST(" + column + " AS BLOB))"
	}
	if dialect == "postgres" {
		return "OCTET_LENGTH(" + column + ")"
	}
	return "LENGTH(" + column + ")"
}

// scrubPersonCopiesBounded retains the existing native SQL sources and exact
// ScrubPersonJSON codec. A repeatable-read write transaction makes chunk reads
// coherent and publishes every rewrite atomically; no rewritten body outlives
// its one-record admission or accumulates in an outer update slice.
func (s *SQLiteStore) scrubPersonCopiesBounded(ctx context.Context, workspaceKey, workspaceID string, anonymous domain.User) (int, error) {
	ctx, release, err := s.admitMetadataWork(ctx, scaleP21SW21ScrubFixedWork)
	if err != nil {
		return 0, err
	}
	defer release()
	options := *metadataReadOptions(s.dialect)
	options.ReadOnly = false
	tx, err := s.db.BeginTx(ctx, &options)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	changed := 0
	for index, target := range personScrubTargets {
		scope := any(workspaceKey)
		if target.byID {
			scope = workspaceID
		}
		for _, column := range target.columns {
			last, started := []string(nil), false
			for {
				page, err := s.scaleP21SW21ScrubHeaders(ctx, tx, index, column, scope, anonymous.ID, last, started)
				if err != nil {
					return 0, err
				}
				for _, header := range page {
					reader := &scaleP21SW21ScrubReader{ctx: ctx, tx: tx, target: index, column: column, scope: scope, header: header}
					part, cost, valid, err := scaleP21SW21ScrubPreflight(ctx, reader, header.size)
					if err != nil {
						return 0, err
					}
					if !valid {
						continue
					}
					_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, cost)
					if err != nil {
						return 0, err
					}
					rewritten, err := func() (bool, error) {
						defer release()
						raw := make([]byte, int(part.size))
						if _, err := io.ReadFull(io.NewSectionReader(reader, part.offset, part.size), raw); err != nil {
							return false, err
						}
						next, changed := ScrubPersonJSON(raw, anonymous, target.table == "domain_events" && header.aggregate == anonymous.ID)
						if !changed {
							return false, nil
						}
						value := any(next)
						if personTextColumns[column] {
							value = string(next)
						}
						args := []any{value, scope}
						for _, key := range header.keys {
							args = append(args, key)
						}
						if _, err := tx.ExecContext(ctx, target.updates[column], args...); err != nil {
							return false, err
						}
						return true, nil
					}()
					if err != nil {
						return 0, err
					}
					if rewritten {
						changed++
					}
				}
				if len(page) == 0 || len(target.keys) == 0 {
					break
				}
				last, started = page[len(page)-1].keys, true
			}
		}
	}
	if changed == 0 {
		return 0, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	slog.Info("scrubbed stored copies of an erased person", "workspace", workspaceKey, "values", changed)
	return changed, nil
}

func (s *SQLiteStore) scaleP21SW21ScrubHeaders(ctx context.Context, tx *sqlTx, index int, column string, scope any, person string, last []string, started bool) ([]scaleP21SW21ScrubHeader, error) {
	target := personScrubTargets[index]
	selected := make([]string, 0, len(target.keys)*2+3)
	for _, key := range target.keys {
		length := scaleP21SW21ScrubByteLength(s.dialect, key)
		selected = append(selected, length, fmt.Sprintf("CASE WHEN %s<=%d THEN %s ELSE NULL END", length, scaleP21SW21ScrubHeaderKeys, key))
	}
	selected = append(selected, scaleP21SW21ScrubByteLength(s.dialect, column))
	where := target.scope + "=? AND " + s.textCast(column) + " LIKE ? ESCAPE '!'"
	args := []any{scope, labelReferencePattern(person)}
	if target.table == "domain_events" {
		length := scaleP21SW21ScrubByteLength(s.dialect, "aggregate_id")
		selected = append(selected, length, fmt.Sprintf("CASE WHEN %s<=%d THEN aggregate_id ELSE NULL END", length, scaleP21SW21ScrubHeaderKeys))
		where = target.scope + "=? AND " + column + " IS NOT NULL AND (aggregate_id=? OR " + s.textCast(column) + " LIKE ? ESCAPE '!')"
		args = []any{scope, person, labelReferencePattern(person)}
	}
	if started {
		var terms []string
		for i, key := range target.keys {
			term := ""
			for j := 0; j < i; j++ {
				term += target.keys[j] + "=? AND "
				args = append(args, last[j])
			}
			terms = append(terms, "("+term+key+">?)")
			args = append(args, last[i])
		}
		where += " AND (" + strings.Join(terms, " OR ") + ")"
	}
	order := ""
	if len(target.keys) != 0 {
		order = " ORDER BY " + strings.Join(target.keys, ",")
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+strings.Join(selected, ",")+" FROM "+target.table+" WHERE "+where+order+" LIMIT 50", args...)
	if err != nil {
		return nil, fmt.Errorf("scan %s.%s: %w", target.table, column, err)
	}
	defer rows.Close()
	page := make([]scaleP21SW21ScrubHeader, 0, 50)
	for rows.Next() {
		lengths, keys := make([]int64, len(target.keys)), make([]sql.NullString, len(target.keys))
		var aggregateSize int64
		var aggregate sql.NullString
		item := scaleP21SW21ScrubHeader{keys: make([]string, len(target.keys))}
		dest := make([]any, 0, len(target.keys)*2+3)
		for i := range keys {
			dest = append(dest, &lengths[i], &keys[i])
		}
		dest = append(dest, &item.size)
		if target.table == "domain_events" {
			dest = append(dest, &aggregateSize, &aggregate)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		for i, key := range keys {
			if lengths[i] < 0 || lengths[i] > scaleP21SW21ScrubHeaderKeys || !key.Valid {
				return nil, fmt.Errorf("%w: personal scrub key exceeds header allowance", ErrMetadataWorkBudget)
			}
			item.keys[i] = key.String
		}
		if target.table == "domain_events" {
			if aggregateSize < 0 || aggregateSize > scaleP21SW21ScrubHeaderKeys || !aggregate.Valid {
				return nil, fmt.Errorf("%w: personal scrub aggregate exceeds header allowance", ErrMetadataWorkBudget)
			}
			item.aggregate = aggregate.String
		}
		if item.size < 0 {
			return nil, ErrMetadataRecordQuery
		}
		page = append(page, item)
	}
	return page, rows.Err()
}

// Generic JSON admission charges decoder/raw storage, structural containers,
// encoder growth, returned output, NUL stripping and text/SQL copies. It
// preserves Decoder's first-value behavior: trailing bytes are not validated,
// and an invalid first value remains unchanged rather than aborting the job.
func scaleP21SW21ScrubPreflight(ctx context.Context, input io.ReaderAt, size int64) (metadataCollectionRange, int, bool, error) {
	scanner := metadataCollectionScanner{ctx: ctx, input: bufio.NewReaderSize(io.NewSectionReader(input, 0, size), metadataRecordChunkBytes)}
	part, err := scanner.value(0)
	if err != nil {
		if ctx.Err() != nil {
			return part, 0, false, ctx.Err()
		}
		var readError *scaleP21SW21ScrubReadError
		if errors.As(err, &readError) {
			return part, 0, false, err
		}
		if errors.Is(err, errMetadataCollectionJSON) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return part, 0, false, nil
		}
		return part, 0, false, err
	}
	var first [1]byte
	if _, err := input.ReadAt(first[:], part.offset); err != nil {
		return part, 0, false, err
	}
	if first[0] != '{' && first[0] != '[' {
		return part, 0, false, nil
	}
	cost, encoded := int64(256<<10), part.size
	add := func(value int64) error {
		if value < 0 || value > int64(scaleP21SW21ScrubRecordWork)-cost {
			return ErrMetadataWorkBudget
		}
		cost += value
		return nil
	}
	if part.size > int64(scaleP21SW21ScrubRecordWork)/4 {
		return part, 0, false, ErrMetadataWorkBudget
	}
	if err := add(part.size * 4); err != nil {
		return part, 0, false, err
	}
	reader := bufio.NewReaderSize(io.NewSectionReader(input, part.offset, part.size), metadataRecordChunkBytes)
	quoted, escaped := false, false
	for {
		if err := ctx.Err(); err != nil {
			return part, 0, false, err
		}
		char, width, err := reader.ReadRune()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return part, 0, false, err
		}
		if !quoted {
			switch char {
			case '"':
				quoted = true
				err = add(128)
			case '{', '[':
				err = add(1024)
			case ':', ',':
				err = add(128)
			}
			if err != nil {
				return part, 0, false, err
			}
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		switch char {
		case '\\':
			escaped = true
		case '"':
			quoted = false
		case '<', '>', '&':
			encoded += 5
		case '\u2028', '\u2029':
			encoded += 3
		case utf8.RuneError:
			if width == 1 {
				encoded += 2
			}
		}
		if encoded > int64(scaleP21SW21ScrubRecordWork)/6 {
			return part, 0, false, ErrMetadataWorkBudget
		}
	}
	if encoded > int64(scaleP21SW21ScrubRecordWork)/6 {
		return part, 0, false, ErrMetadataWorkBudget
	}
	if err := add(encoded * 6); err != nil {
		return part, 0, false, err
	}
	return part, int(cost), true, nil
}
