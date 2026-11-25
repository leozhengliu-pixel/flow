package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// largeRowSearchNames are metadataSearchText's members in order.
var largeRowSearchNames = []string{"name", "title", "displayName", "email", "summary", "description", "content", "domains", "version", "status", "tier", "scope", "resource"}

// largeRowHookMembers are the members the row-side indexes decode from a
// large entity row; the typed decoders read nothing else.
var largeRowHookMembers = map[string][]string{
	"apiKeys":  {"secretHash"},
	"projects": {"comments"},
	"releases": {"pipelineId", "stage", "status", "releasedAt", "issueIds"},
}

// writeLargeRow is writeArray's row write (writeMetadataRecordRow after the
// display-equality skip) for one large entity row. Both images are encoded
// to admitted spools and compared member by member without loading either;
// a changed row is NUL-stripped on disk and written by the literal row
// statement. Index hooks receive the exact members they decode, and the
// search document follows metadataSearchText's member rules and character
// cap without decoding the record into an interface map.
func (p *legacyMetadataWritePlan) writeLargeRow(ctx context.Context, f *legacyMetadataWriteField, row *legacyMetadataWriteRow, order int) (bool, error) {
	if _, ok := largeRowKind(f); !ok || row.next.typ == nil || !largeRowSelected(row.before, row.next) {
		return false, nil
	}
	name := f.field.name
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 512<<10); err != nil {
		return true, err
	}
	next, err := legacySessionNodeSpool(ctx, owner, row.next)
	if err != nil {
		return true, err
	}
	defer next.Close()
	var previous *RecordSpool
	if row.before.typ != nil {
		previous, err = legacySessionNodeSpool(ctx, owner, row.before)
		if err != nil {
			return true, err
		}
		defer previous.Close()
		var same bool
		if err := largeRowPhase(ctx, p.store, func(scratch *ResourceRead) error {
			var decided bool
			var err error
			if same, decided, err = largeRowDisplayEqual(ctx, scratch, p.actual, legacySessionSpoolPart(previous), legacySessionSpoolPart(next)); err != nil || decided {
				return err
			}
			same, err = p.largeRowDisplayEqualInMemory(ctx, scratch, previous, next)
			return err
		}); err != nil {
			return true, err
		}
		if same {
			return true, nil
		}
	}
	stripped, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return true, err
	}
	defer stripped.Close()
	if _, err := next.Seek(0, io.SeekStart); err != nil {
		return true, err
	}
	if err := legacyProjectCopyStoredJSON(ctx, stripped, next); err != nil {
		return true, err
	}
	data := legacySessionSpoolPart(stripped)
	fields, err := legacySessionJSONObject(ctx, owner, data)
	if err != nil {
		return true, err
	}
	// The row itself is one literal statement: chunked SQL appends rewrite
	// the growing value per chunk (quadratic in the row). Its bytes, the
	// offload rewrite and the dialect text and driver bind copies of the
	// stored bytes are admitted for this statement only.
	if err := largeRowPhase(ctx, p.store, func(scratch *ResourceRead) error {
		raw, err := largeRowPartBytes(ctx, scratch, data, 1)
		if err != nil {
			return err
		}
		stored := raw
		if offloadFields[name] && p.tx.database != nil && p.tx.database.content != nil {
			// Offload rewrites version bodies against the record's own
			// content; it decodes the member map and the versions.
			if err := scratch.reserve(ctx, uint64(len(raw))*3+4096); err != nil {
				return err
			}
			if stored, err = offloadRecord(ctx, p.tx, p.workspace, name, raw); err != nil {
				return err
			}
		}
		if err := scratch.reserve(ctx, uint64(len(stored))*2+4096); err != nil {
			return err
		}
		p.tx.metadataWritten = true
		_, err = p.tx.ExecContext(ctx, metadataSQLStageLegacyRowSQL, p.workspace, name, row.id, order, stored)
		return err
	}); err != nil {
		return true, err
	}
	if err := largeRowPhase(ctx, p.store, func(scratch *ResourceRead) error {
		return p.largeRowHooks(ctx, scratch, name, row.id, fields, data, previous)
	}); err != nil {
		return true, err
	}
	if metadataSearchFieldSet[name] {
		if err := largeRowPhase(ctx, p.store, func(scratch *ResourceRead) error {
			content := &legacyProjectAdmittedBuffer{ctx: ctx, owner: scratch}
			if err := largeRowSearchContent(ctx, scratch, fields, content); err != nil {
				return err
			}
			_, err := p.tx.ExecContext(ctx, metadataSQLStageLegacySearchSQL(p.tx.dialect), p.workspace, name, row.id, content.data.String())
			return err
		}); err != nil {
			return true, err
		}
	}
	return true, ctx.Err()
}

// largeRowDisplayEqualInMemory is the original equalDisplayData on the two
// complete encodings, for the shapes the streamed comparison leaves open.
func (p *legacyMetadataWritePlan) largeRowDisplayEqualInMemory(ctx context.Context, owner *ResourceRead, previous, next *RecordSpool) (bool, error) {
	before, err := largeRowPartBytes(ctx, owner, legacySessionSpoolPart(previous), 8)
	if err != nil {
		return false, err
	}
	after, err := largeRowPartBytes(ctx, owner, legacySessionSpoolPart(next), 8)
	if err != nil {
		return false, err
	}
	refs, err := p.references(ctx, p.actual, owner, nil, nil, before, after)
	if err != nil {
		return false, err
	}
	return refs.equalDisplayData(before, after), nil
}

// largeRowPartBytes loads one encoded part with its decode work admitted.
func largeRowPartBytes(ctx context.Context, owner *ResourceRead, part legacySessionJSONPart, copies uint64) ([]byte, error) {
	if part.size < 0 || uint64(part.size) > (metadataWorkBytes-4096)/copies {
		return nil, ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, uint64(part.size)*copies+4096); err != nil {
		return nil, err
	}
	raw := make([]byte, int(part.size))
	if _, err := part.input.ReadAt(raw, part.start); err != nil {
		return nil, err
	}
	return raw, nil
}

// largeRowMembers is a JSON object of the named members of fields, each with
// its exact encoded bytes; the typed hook decoders read no other member.
func largeRowMembers(ctx context.Context, owner *ResourceRead, fields map[string]legacySessionJSONPart, names []string) ([]byte, error) {
	var output bytes.Buffer
	output.WriteByte('{')
	first := true
	for _, name := range names {
		part, found := fields[name]
		if !found {
			continue
		}
		raw, err := largeRowPartBytes(ctx, owner, part, 8)
		if err != nil {
			return nil, err
		}
		key, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		if !first {
			output.WriteByte(',')
		}
		first = false
		output.Write(key)
		output.WriteByte(':')
		output.Write(raw)
	}
	output.WriteByte('}')
	return output.Bytes(), nil
}

// largeRowHooks runs writeMetadataRecordRow's index hooks in its order.
func (p *legacyMetadataWritePlan) largeRowHooks(ctx context.Context, owner *ResourceRead, name, key string, fields map[string]legacySessionJSONPart, data legacySessionJSONPart, previous *RecordSpool) error {
	if name == "apiKeys" {
		raw, err := largeRowMembers(ctx, owner, fields, largeRowHookMembers[name])
		if err != nil {
			return err
		}
		if err := upsertAPIKeyLookup(ctx, p.tx, p.workspace, key, apiKeyLookupHashFromRecord(raw)); err != nil {
			return err
		}
	}
	if name == "projects" {
		raw, err := largeRowMembers(ctx, owner, fields, largeRowHookMembers[name])
		if err != nil {
			return err
		}
		var before []byte
		if previous != nil {
			// The original passes the previous image unstripped.
			prior, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(previous))
			if err != nil {
				return err
			}
			if before, err = largeRowMembers(ctx, owner, prior, largeRowHookMembers[name]); err != nil {
				return err
			}
		}
		if err := syncNewCommentMentions(ctx, p.tx, p.workspace, key, raw, before); err != nil {
			return err
		}
	}
	var customer []byte
	if name == "customers" || name == "customerRequests" {
		var err error
		if customer, err = largeRowPartBytes(ctx, owner, data, 8); err != nil {
			return err
		}
	}
	if err := writeCustomerFilterRecord(ctx, p.tx, p.workspace, name, key, customer); err != nil {
		return err
	}
	var release []byte
	if name == "releases" {
		var err error
		if release, err = largeRowMembers(ctx, owner, fields, largeRowHookMembers[name]); err != nil {
			return err
		}
	}
	return writeReleaseIssueRecords(ctx, p.tx, p.workspace, name, key, release)
}

// largeRowSearchContent writes capSearchText(metadataSearchText(data)) for a
// stored entity row from its member map: strings are decoded in chunks with
// encoding/json's replacement rules, other values take the original
// interface round trip.
func largeRowSearchContent(ctx context.Context, owner *ResourceRead, fields map[string]legacySessionJSONPart, output io.Writer) error {
	limit, used := owner.store.db.searchMaxChars, 0
	emit := func(part []byte) error {
		if limit > 0 {
			remaining := limit - used
			if remaining <= 0 {
				return ctx.Err()
			}
			count := utf8.RuneCount(part)
			if count > remaining {
				count, cut := 0, 0
				for cut < len(part) && count < remaining {
					_, size := utf8.DecodeRune(part[cut:])
					cut += size
					count++
				}
				part = part[:cut]
				used = limit
			} else {
				used += count
			}
		}
		_, err := output.Write(part)
		return err
	}
	for index, name := range largeRowSearchNames {
		if index > 0 {
			if err := emit([]byte(" ")); err != nil {
				return err
			}
		}
		part, present := fields[name]
		if !present {
			continue
		}
		reader := bufio.NewReader(part.reader())
		var prefix byte
		for {
			b, err := reader.ReadByte()
			if err != nil {
				return err
			}
			if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
				prefix = b
				break
			}
		}
		switch prefix {
		case 'n':
			continue
		case '"':
			if _, err := VisitMetadataJSONStringChunks(ctx, part.reader(), emit); err != nil {
				return err
			}
			continue
		}
		raw, err := largeRowPartBytes(ctx, owner, part, 16)
		if err != nil {
			return err
		}
		var item any
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return err
		}
		var text string
		if json.Unmarshal(encoded, &text) == nil {
			err = emit([]byte(text))
		} else {
			err = emit(encoded)
		}
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

// largeRowPhase owns one phase's work: it is released as soon as the phase
// ends, so a large row's phases never hold their allowances together.
func largeRowPhase(ctx context.Context, store *SQLiteStore, phase func(*ResourceRead) error) error {
	scratch := &ResourceRead{store: store}
	defer scratch.Close()
	return phase(scratch)
}
