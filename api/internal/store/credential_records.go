package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

const sw17CredentialFixedWork = 256 << 10

// VisitCanonicalCredentialSettings visits trusted canonical storage, before
// replica load normalization. It does not reproduce a warm installed snapshot
// and does not authorize. Callers must finish processing each owned record
// under its callback lease and must not retain records.
func (s *SQLiteStore) VisitCanonicalCredentialSettings(ctx context.Context, workspace, kind string, ref *string, visit func(context.Context, domain.Bootstrap) error) (MetadataArrayShape, error) {
	recordType, err := sw17CredentialType(kind)
	if err != nil || visit == nil || ref != nil && kind != "oauthApplications" {
		return MetadataArrayShape{}, ErrMetadataRecordQuery
	}
	read, err := s.BeginMetadataArrayRead(ctx, workspace, kind)
	if err != nil {
		return MetadataArrayShape{}, err
	}
	defer read.Close()
	err = read.Visit(func(recordCtx context.Context, record MetadataArrayRecord) error {
		input, ok := record.Reader.(io.ReaderAt)
		if !ok {
			return fmt.Errorf("%w: credential source needs detached random access", ErrMetadataRecordQuery)
		}
		if ref != nil {
			match, err := s.sw17CredentialIDMatches(recordCtx, input, record.EncodedBytes, *ref)
			if err != nil || !match {
				return err
			}
		}
		cost, err := sw17CredentialPreflight(recordCtx, input, record.EncodedBytes, recordType)
		if err != nil {
			return err
		}
		typedCtx, release, err := s.admitMetadataWork(recordCtx, cost)
		if err != nil {
			return err
		}
		defer release()
		value, err := sw17DecodeCredential(typedCtx, input, record.EncodedBytes, recordType)
		if err != nil {
			return err
		}
		var data domain.Bootstrap
		switch item := value.Interface().(type) {
		case domain.APIKey:
			data.APIKeys = []domain.APIKey{item}
		case domain.OAuthApplication:
			data.OAuthApplications = []domain.OAuthApplication{item}
		case domain.IntegrationConnection:
			data.IntegrationConnections = []domain.IntegrationConnection{item}
		}
		if err := visit(typedCtx, data); err != nil {
			return err
		}
		if ref != nil {
			return ErrMetadataIterationDone
		}
		return nil
	})
	return read.Shape, errors.Join(err, read.Close())
}

func sw17CredentialType(kind string) (reflect.Type, error) {
	var recordType reflect.Type
	switch kind {
	case "apiKeys":
		recordType = reflect.TypeFor[domain.APIKey]()
	case "oauthApplications":
		recordType = reflect.TypeFor[domain.OAuthApplication]()
	case "integrationConnections":
		recordType = reflect.TypeFor[domain.IntegrationConnection]()
	default:
		return nil, ErrMetadataRecordQuery
	}
	for index := range recordType.NumField() {
		field := recordType.Field(index)
		if field.Tag.Get("json") == "-" {
			continue
		}
		value := field.Type
		supported := value.Kind() == reflect.String || value.Kind() == reflect.Bool ||
			value.Kind() >= reflect.Int && value.Kind() <= reflect.Float64 ||
			value == reflect.TypeFor[time.Time]() || value == reflect.TypeFor[*time.Time]() ||
			value.Kind() == reflect.Slice && value.Elem().Kind() == reflect.String ||
			value.Kind() == reflect.Map && value.Key().Kind() == reflect.String && value.Elem().Kind() == reflect.String
		if !supported {
			return nil, fmt.Errorf("%w: credential admission needs review for %s.%s", ErrBoundedMetadataCoverage, kind, field.Name)
		}
	}
	return recordType, nil
}

func sw17CredentialScanner(ctx context.Context, input io.ReaderAt, offset, size int64) *metadataCollectionScanner {
	return &metadataCollectionScanner{
		ctx: ctx, offset: offset,
		input: bufio.NewReaderSize(io.NewSectionReader(input, offset, size), metadataRecordChunkBytes),
	}
}

func sw17CredentialField(input io.ReaderAt, key metadataCollectionRange, recordType reflect.Type) (int, error) {
	if key.size > 4096 {
		return -1, nil
	}
	var name string
	if err := json.NewDecoder(io.NewSectionReader(input, key.offset, key.size)).Decode(&name); err != nil {
		return -1, err
	}
	for index := range recordType.NumField() {
		field := recordType.Field(index)
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag != "-" && (name == tag || strings.EqualFold(name, tag)) {
			return index, nil
		}
	}
	return -1, nil
}

func sw17VisitCredentialFields(ctx context.Context, input io.ReaderAt, size int64, recordType reflect.Type, visit func(int, metadataCollectionRange) error) error {
	scanner := sw17CredentialScanner(ctx, input, 0, size)
	if err := scanner.spaces(); err != nil {
		return err
	}
	if value, err := scanner.peek(); err != nil {
		return err
	} else if value == 'n' {
		if err := scanner.literal("null"); err != nil {
			return err
		}
		return scanner.end()
	}
	if err := scanner.object(func(key, value metadataCollectionRange) error {
		index, err := sw17CredentialField(input, key, recordType)
		if err != nil || index < 0 {
			return err
		}
		return visit(index, value)
	}, 0); err != nil {
		return err
	}
	return scanner.end()
}

// Strings are decoded one field at a time: allow two encoded decoder buffers
// and the unescaped bytes plus owned string. Count HTML/Unicode escaping too,
// then allow encoder growth and its returned copy. String slices add 64 bytes
// per element for growth and headers; string maps add 128 bytes per occurrence
// for bucket growth, key/value headers and the encoder's sorted keys.
// Repeated field occurrences are all charged, although their values may merge
// or replace earlier typed state. Unknown fields never enter typed storage.
func sw17CredentialPreflight(ctx context.Context, input io.ReaderAt, size int64, recordType reflect.Type) (int, error) {
	cost := int64(sw17CredentialFixedWork)
	add := func(bytes int64) error {
		if bytes < 0 || bytes > int64(metadataWorkBytes)-cost {
			return ErrMetadataWorkBudget
		}
		cost += bytes
		return nil
	}
	err := sw17VisitCredentialFields(ctx, input, size, recordType, func(index int, part metadataCollectionRange) error {
		if part.size > int64(metadataWorkBytes)/4 {
			return ErrMetadataWorkBudget
		}
		if err := add(part.size*4 + 512); err != nil {
			return err
		}
		encoded, err := sw17CredentialEncodedBound(ctx, input, part)
		if err != nil {
			return err
		}
		if encoded > int64(metadataWorkBytes)/3 {
			return ErrMetadataWorkBudget
		}
		if err := add(encoded * 3); err != nil {
			return err
		}
		field := recordType.Field(index).Type
		scanner := sw17CredentialScanner(ctx, input, part.offset, part.size)
		first, err := scanner.peek()
		if err != nil {
			return err
		}
		if field.Kind() == reflect.Slice && first == '[' {
			return scanner.array(func(metadataCollectionRange) error { return add(64) }, 0)
		}
		if field.Kind() == reflect.Map && first == '{' {
			return scanner.object(func(metadataCollectionRange, metadataCollectionRange) error { return add(128) }, 0)
		}
		return nil
	})
	return int(cost), err
}

func sw17CredentialEncodedBound(ctx context.Context, input io.ReaderAt, part metadataCollectionRange) (int64, error) {
	reader := bufio.NewReaderSize(io.NewSectionReader(input, part.offset, part.size), metadataRecordChunkBytes)
	encoded, quoted, escaped := part.size, false, false
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		char, size, err := reader.ReadRune()
		if errors.Is(err, io.EOF) {
			return encoded, nil
		}
		if err != nil {
			return 0, err
		}
		if !quoted {
			quoted = char == '"'
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
			if size == 1 {
				encoded += 2
			}
		}
		if encoded > int64(metadataWorkBytes) {
			return 0, ErrMetadataWorkBudget
		}
	}
}

func sw17DecodeCredential(ctx context.Context, input io.ReaderAt, size int64, recordType reflect.Type) (reflect.Value, error) {
	value := reflect.New(recordType).Elem()
	err := sw17VisitCredentialFields(ctx, input, size, recordType, func(index int, part metadataCollectionRange) error {
		decoder := json.NewDecoder(io.NewSectionReader(input, part.offset, part.size))
		// Decode into existing fields: duplicate map members merge, array
		// elements reuse backing state, and null strings preserve prior text.
		if err := decoder.Decode(value.Field(index).Addr().Interface()); err != nil {
			return err
		}
		var tail any
		if err := decoder.Decode(&tail); !errors.Is(err, io.EOF) {
			return ErrMetadataRecordQuery
		}
		return nil
	})
	return value, err
}

func (s *SQLiteStore) sw17CredentialIDMatches(ctx context.Context, input io.ReaderAt, size int64, ref string) (bool, error) {
	match := ref == ""
	recordType := reflect.TypeFor[domain.OAuthApplication]()
	err := sw17VisitCredentialFields(ctx, input, size, recordType, func(index int, part metadataCollectionRange) error {
		if recordType.Field(index).Name != "ID" {
			return nil
		}
		scanner := sw17CredentialScanner(ctx, input, part.offset, part.size)
		first, err := scanner.peek()
		if err != nil {
			return err
		}
		if first == 'n' {
			return nil
		}
		if first != '"' {
			return ErrMetadataRecordQuery
		}
		// A string equal to ref cannot need more than six JSON bytes per
		// UTF-8 byte plus its quotes. Oversized unrelated IDs need no decode.
		if part.size > 6*int64(len(ref))+2 {
			match = false
			return nil
		}
		if part.size > (int64(metadataWorkBytes)-sw17CredentialFixedWork)/4 {
			return ErrMetadataWorkBudget
		}
		_, release, err := s.admitMetadataWork(ctx, int(part.size*4)+sw17CredentialFixedWork)
		if err != nil {
			return err
		}
		defer release()
		var id string
		if err := json.NewDecoder(io.NewSectionReader(input, part.offset, part.size)).Decode(&id); err != nil {
			return err
		}
		match = id == ref
		return nil
	})
	return match, err
}
