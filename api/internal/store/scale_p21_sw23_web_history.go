package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go"
)

var errSW23HistoryJSON = errors.New("invalid historical settings JSON")

// Only string-token type and short field names matter to this projection.
// Validate tokens before shortening them so the existing JSON parser can skip
// arbitrary ignored bodies without retaining their strings or numeric digits.
type sw23HistoryTokens struct {
	input   *bufio.Reader
	pending []byte
	err     error
}

func (r *sw23HistoryTokens) Read(output []byte) (int, error) {
	written := 0
	for written < len(output) {
		if len(r.pending) > 0 {
			n := copy(output[written:], r.pending)
			written += n
			r.pending = r.pending[n:]
			continue
		}
		if r.err != nil {
			return written, r.err
		}
		c, err := r.input.ReadByte()
		if err != nil {
			r.err = err
			continue
		}
		switch {
		case c == '"':
			r.pending, r.err = r.stringToken()
		case c == '-' || c >= '0' && c <= '9':
			r.err = r.numberToken(c)
			if r.err == nil {
				r.pending = []byte("0")
			}
		default:
			r.pending = []byte{c}
		}
	}
	return written, nil
}

func (r *sw23HistoryTokens) tokenByte() (byte, error) {
	c, err := r.input.ReadByte()
	if errors.Is(err, io.EOF) {
		err = errSW23HistoryJSON
	}
	return c, err
}

func (r *sw23HistoryTokens) stringToken() ([]byte, error) {
	// Either recognized field name is far shorter than this even when every
	// character uses a JSON Unicode escape. Longer keys cannot select a flag.
	raw := make([]byte, 1, 512)
	raw[0] = '"'
	long := false
	appendByte := func(c byte) {
		if len(raw) < 512 {
			raw = append(raw, c)
		} else {
			long = true
		}
	}
	for {
		c, err := r.tokenByte()
		if err != nil {
			return nil, err
		}
		appendByte(c)
		if c == '"' {
			if long {
				return []byte(`""`), nil
			}
			return raw, nil
		}
		if c < 0x20 {
			return nil, errSW23HistoryJSON
		}
		if c != '\\' {
			continue
		}
		c, err = r.tokenByte()
		if err != nil {
			return nil, err
		}
		appendByte(c)
		switch c {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		case 'u':
			for range 4 {
				c, err = r.tokenByte()
				if err != nil {
					return nil, err
				}
				appendByte(c)
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
					return nil, errSW23HistoryJSON
				}
			}
		default:
			return nil, errSW23HistoryJSON
		}
	}
}

func (r *sw23HistoryTokens) numberToken(first byte) error {
	c := first
	eof := false
	next := func() error {
		var err error
		c, err = r.input.ReadByte()
		if errors.Is(err, io.EOF) {
			c, eof = 0, true
			return nil
		}
		return err
	}
	if c == '-' {
		if err := next(); err != nil {
			return err
		}
	}
	if c == '0' {
		if err := next(); err != nil {
			return err
		}
		if c >= '0' && c <= '9' {
			return errSW23HistoryJSON
		}
	} else {
		if c < '1' || c > '9' {
			return errSW23HistoryJSON
		}
		for c >= '0' && c <= '9' {
			if err := next(); err != nil {
				return err
			}
		}
	}
	if c == '.' {
		if err := next(); err != nil {
			return err
		}
		if c < '0' || c > '9' {
			return errSW23HistoryJSON
		}
		for c >= '0' && c <= '9' {
			if err := next(); err != nil {
				return err
			}
		}
	}
	if c == 'e' || c == 'E' {
		if err := next(); err != nil {
			return err
		}
		if c == '+' || c == '-' {
			if err := next(); err != nil {
				return err
			}
		}
		if c < '0' || c > '9' {
			return errSW23HistoryJSON
		}
		for c >= '0' && c <= '9' {
			if err := next(); err != nil {
				return err
			}
		}
	}
	switch c {
	case 0:
		if eof {
			return nil
		}
		return errSW23HistoryJSON
	case ' ', '\t', '\r', '\n', ',', ']', '}':
		return r.input.UnreadByte()
	default:
		return errSW23HistoryJSON
	}
}

func sw23HistoryEnabled(input io.Reader) (bool, error) {
	tokens := &sw23HistoryTokens{input: bufio.NewReaderSize(input, 4096)}
	iter := jsoniter.Parse(jsoniter.ConfigCompatibleWithStandardLibrary, tokens, 4096)
	var search, access *bool
	if iter.WhatIsNext() == jsoniter.ObjectValue {
		iter.ReadObjectCB(func(iter *jsoniter.Iterator, key string) bool {
			var target **bool
			switch {
			case strings.EqualFold(key, "agentWebSearch"):
				target = &search
			case strings.EqualFold(key, "agentWebAccess"):
				target = &access
			}
			if target == nil {
				iter.Skip()
			} else if iter.ReadNil() {
				*target = nil
			} else if iter.WhatIsNext() == jsoniter.BoolValue {
				value := iter.ReadBool()
				*target = &value
			} else {
				iter.ReportError("historical settings", "boolean or null required")
			}
			return iter.Error == nil
		})
	} else {
		iter.Skip()
	}
	parsed := iter.Error == nil
	trailing := iter.WhatIsNext()
	if tokens.err != nil && !errors.Is(tokens.err, io.EOF) && !errors.Is(tokens.err, errSW23HistoryJSON) {
		return false, tokens.err
	}
	if !parsed || trailing != jsoniter.InvalidValue || !errors.Is(iter.Error, io.EOF) {
		return false, nil
	}
	return search != nil && *search || access != nil && *access, nil
}

type sw23HistoryPayload struct {
	ctx          context.Context
	tx           *sqlTx
	id           string
	size, offset int64
	buffer       []byte
}

func (r *sw23HistoryPayload) Read(output []byte) (int, error) {
	if len(r.buffer) == 0 {
		if r.offset >= r.size {
			return 0, io.EOF
		}
		column := "payload"
		if r.tx.dialect == "sqlite" {
			column = "CAST(payload AS BLOB)"
		}
		n := min(int64(metadataRecordChunkBytes), r.size-r.offset)
		if err := r.tx.QueryRowContext(r.ctx, `SELECT SUBSTR(`+column+`,?,?) FROM domain_events WHERE id=?`, r.offset+1, n, r.id).Scan(&r.buffer); err != nil {
			return 0, err
		}
		if len(r.buffer) == 0 {
			return 0, io.ErrUnexpectedEOF
		}
		r.offset += int64(len(r.buffer))
	}
	n := copy(output, r.buffer)
	r.buffer = r.buffer[n:]
	return n, nil
}

func (s *SQLiteStore) agentWebSearchFirstEnabledBounded(ctx context.Context) (time.Time, error) {
	ctx, release, err := s.admitMetadataWork(ctx, 16<<20)
	if err != nil {
		return time.Time{}, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	spool, err := NewRecordSpool()
	if err != nil {
		return time.Time{}, err
	}
	defer spool.Close()
	type header struct {
		ID, Created string
		Size        int64
	}
	// Preserve the legacy query's iteration order, including its zero-time
	// sentinel behavior, without keeping result rows open during chunk reads.
	rows, err := tx.QueryContext(ctx, `SELECT CASE WHEN LENGTH(id)<=191 THEN id ELSE NULL END,CASE WHEN LENGTH(created_at)<=128 THEN created_at ELSE NULL END,LENGTH(payload) FROM domain_events WHERE event_type=?`, "workspace_preferences.updated")
	if err != nil {
		return time.Time{}, err
	}
	encoder := json.NewEncoder(spool)
	for rows.Next() {
		var item header
		var id, created []byte
		if err := rows.Scan(&id, &created, &item.Size); err != nil {
			rows.Close()
			return time.Time{}, err
		}
		if id == nil || created == nil || item.Size < 0 {
			rows.Close()
			return time.Time{}, ErrMetadataPageBudget
		}
		item.ID, item.Created = string(id), string(created)
		if err := encoder.Encode(item); err != nil {
			rows.Close()
			return time.Time{}, err
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return time.Time{}, rowErr
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return time.Time{}, err
	}
	decoder := json.NewDecoder(spool)
	var first time.Time
	for {
		if err := ctx.Err(); err != nil {
			return time.Time{}, err
		}
		var item header
		if err := decoder.Decode(&item); err != nil {
			if errors.Is(err, io.EOF) {
				return first, nil
			}
			return time.Time{}, err
		}
		enabled, err := sw23HistoryEnabled(&sw23HistoryPayload{ctx: ctx, tx: tx, id: item.ID, size: item.Size})
		if err != nil {
			return time.Time{}, err
		}
		if !enabled {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, item.Created)
		if err == nil && (first.IsZero() || at.Before(first)) {
			first = at
		}
	}
}
