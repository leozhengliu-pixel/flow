package store

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

const teamProjectionStrings = 256 << 10
const teamProjectionWork = 4 << 20

// Team and its selected strings/timestamps remain owned by this handle until
// Close. Generic encoded responses need their own separately sized allowance.
type teamProjectionRead struct {
	Team    domain.Team
	release func()
	once    sync.Once
}

func (r *teamProjectionRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Team = domain.Team{}
			if r.release != nil {
				r.release()
			}
		})
	}
}

type teamField struct {
	name, kind string
	retain     bool
}

var teamFields = []teamField{
	{"id", "string", true},
	{"name", "string", false},
	{"key", "string", true},
	{"color", "string", false},
	{"icon", "string", false},
	{"private", "bool", true},
	{"externalSource", "string", true},
	{"retiredAt", "time", true},
	{"archivedAt", "time", true},
	{"archivedById", "string", false},
	{"createdAt", "time", false},
	{"updatedAt", "time", false},
}

var teamSchema = sync.OnceValue(func() error {
	kind := reflect.TypeFor[domain.Team]()
	if kind.NumField() != len(teamFields) || reflect.PointerTo(kind).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return fmt.Errorf("%w: Team projection schema changed", ErrBoundedMetadataCoverage)
	}
	for index, expected := range teamFields {
		field := kind.Field(index)
		tag := expected.name
		if index >= 4 {
			tag += ",omitempty"
		}
		validType := expected.kind == "string" && field.Type == reflect.TypeFor[string]() ||
			expected.kind == "bool" && field.Type == reflect.TypeFor[bool]() ||
			expected.kind == "time" && field.Type == reflect.TypeFor[*time.Time]()
		if field.Anonymous || field.PkgPath != "" || field.Tag.Get("json") != tag || !validType {
			return fmt.Errorf("%w: Team projection field %d changed", ErrBoundedMetadataCoverage, index)
		}
	}
	return nil
})

// Project one trusted canonical Team row without selecting a lifecycle source
// or authorizing a caller.
func (s *SQLiteStore) beginTeamProjection(ctx context.Context, record MetadataArrayRecord) (_ *teamProjectionRead, err error) {
	if err := teamSchema(); err != nil {
		return nil, err
	}
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, teamProjectionWork)
	if err != nil {
		return nil, err
	}
	result := &teamProjectionRead{release: release}
	defer func() {
		if err != nil {
			result.Close()
		}
	}()
	reader, err := ReplayMetadataReader(record.Reader, record.EncodedBytes)
	if err != nil {
		return nil, err
	}
	scanner := metadataCollectionScanner{ctx: ctx, input: bufio.NewReaderSize(reader, metadataRecordChunkBytes)}
	if err := scanner.spaces(); err != nil {
		return nil, err
	}
	first, err := scanner.peek()
	if err != nil {
		return nil, err
	}
	if first == 'n' {
		if err := scanner.literal("null"); err != nil {
			return nil, err
		}
		return result, scanner.end()
	}
	if first != '{' {
		return nil, ErrMetadataRecordQuery
	}
	remaining := teamProjectionStrings
	err = scanner.object(func(keyRange, valueRange metadataCollectionRange) error {
		// Every spelling folding to a known ASCII tag fits six JSON bytes
		// per rune. A giant unknown property never becomes a Go string.
		if keyRange.size > 6*int64(len("externalSource"))+2 {
			return nil
		}
		var name string
		if err := json.NewDecoder(io.NewSectionReader(reader, keyRange.offset, keyRange.size)).Decode(&name); err != nil {
			return err
		}
		index := -1
		for candidate, field := range teamFields {
			if field.name == name {
				index = candidate
				break
			}
		}
		if index < 0 {
			for candidate, field := range teamFields {
				if strings.EqualFold(field.name, name) {
					index = candidate
					break
				}
			}
		}
		if index < 0 {
			return nil
		}
		field := teamFields[index]
		part := io.NewSectionReader(reader, valueRange.offset, valueRange.size)
		switch field.kind {
		case "string":
			var selected strings.Builder
			null, err := VisitMetadataJSONStringChunks(ctx, part, func(chunk []byte) error {
				if !field.retain {
					return nil
				}
				if len(chunk) > remaining {
					return ErrMetadataWorkBudget
				}
				remaining -= len(chunk)
				_, err := selected.Write(chunk)
				return err
			})
			if err != nil {
				return fmt.Errorf("Team.%s: %w", field.name, err)
			}
			if null || !field.retain {
				return nil
			}
			switch field.name {
			case "id":
				result.Team.ID = selected.String()
			case "key":
				result.Team.Key = selected.String()
			case "externalSource":
				result.Team.ExternalSource = selected.String()
			}
		case "bool":
			if valueRange.size > 5 {
				return ErrMetadataRecordQuery
			}
			// Unmarshal into the existing bool: null is a no-op.
			if err := json.NewDecoder(part).Decode(&result.Team.Private); err != nil {
				return err
			}
		case "time":
			value, err := teamTime(ctx, part)
			if err != nil {
				return fmt.Errorf("Team.%s: %w", field.name, err)
			}
			switch field.name {
			case "archivedAt":
				result.Team.ArchivedAt = value
			case "retiredAt":
				result.Team.RetiredAt = value
			}
		default:
			return ErrBoundedMetadataCoverage
		}
		return nil
	}, 0)
	if err != nil {
		return nil, err
	}
	if err := scanner.end(); err != nil {
		return nil, err
	}
	return result, nil
}

// time.Time's decoder accepts an arbitrarily long fraction and truncates it
// after nine digits. Preserve its raw quoting/escape behavior, retaining only
// those digits before delegating all remaining RFC3339 rules to that decoder.
// The input is an exact scanner token range, excluding surrounding whitespace.
func teamTime(ctx context.Context, input io.Reader) (*time.Time, error) {
	reader := bufio.NewReaderSize(input, 4096)
	var raw [128]byte
	used, fractionDigits := 0, 0
	fractionSeen, inFraction := false, false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, err := reader.ReadByte()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if !fractionSeen && (value == '.' || value == ',') {
			fractionSeen, inFraction = true, true
		} else if inFraction && value >= '0' && value <= '9' {
			fractionDigits++
			if fractionDigits > 9 {
				continue
			}
		} else if inFraction {
			inFraction = false
		}
		if used == len(raw) {
			return nil, ErrMetadataRecordQuery
		}
		raw[used] = value
		used++
	}
	var value *time.Time
	if err := json.Unmarshal(raw[:used], &value); err != nil {
		return nil, err
	}
	return value, nil
}
