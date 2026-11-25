package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW302Codec func(context.Context, *SQLiteStore, *metadataLogicalBuilder, legacySessionJSONPart) (metadataLogicalNode, error)

func scaleP21SW302Oracle(t *testing.T, kind reflect.Type, raw []byte) (reflect.Value, []byte) {
	t.Helper()
	value := reflect.New(kind)
	if err := json.Unmarshal(raw, value.Interface()); err != nil {
		t.Fatal("valid typed codec oracle failed", err)
	}
	wire, err := json.Marshal(value.Elem().Interface())
	if err != nil {
		t.Fatal(err)
	}
	return value.Elem(), wire
}

func scaleP21SW302Compare(t *testing.T, repo *SQLiteStore, node metadataLogicalNode, want reflect.Value, wire []byte) {
	t.Helper()
	if node.Type() != want.Type() {
		t.Fatal("canonical codec changed its declared domain type")
	}
	owner := &ResourceRead{store: repo}
	defer owner.Close()
	if err := owner.reserve(t.Context(), node.cost*2+64<<10); err != nil {
		t.Fatal(err)
	}
	actual, err := node.Value(t.Context(), metadataWorkBytes)
	if err != nil || !reflect.DeepEqual(actual.Interface(), want.Interface()) {
		t.Fatal("canonical native output differs from standard typed JSON decode", err)
	}
	actual = reflect.Value{}
	spool, err := legacySessionNodeSpool(t.Context(), owner, node)
	if err != nil {
		t.Fatal("stream canonical domain record", err)
	}
	defer spool.Close()
	got, err := io.ReadAll(io.NewSectionReader(spool, 0, spool.bytes))
	if err != nil || !bytes.Equal(got, wire) {
		t.Fatal("canonical streamed wire differs from standard typed JSON encode", err)
	}
}

func scaleP21SW302Success(t *testing.T, codec scaleP21SW302Codec, kind reflect.Type, raw string) {
	t.Helper()
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
	frames, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		frames.Close()
		frames.Close()
		if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 ||
			metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors ||
			recordSpoolBytes.Load() != spools {
			t.Error("pure canonical codec retained work/disk/descriptors/spools or decoded full metadata")
		}
	}()
	input := []byte(raw)
	want, wire := scaleP21SW302Oracle(t, kind, input)
	node, err := codec(t.Context(), repo, frames, legacySessionJSONPart{bytes.NewReader(input), 0, int64(len(input))})
	if err != nil {
		t.Fatal("valid supported typed record must succeed", err)
	}
	if repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("finished canonical frame retained its temporary native owner")
	}
	scaleP21SW302Compare(t, repo, node, want, wire)
}

func TestScaleP21SW302DocumentCanonicalTypedOracle(t *testing.T) {
	for _, test := range []struct{ name, raw string }{
		{"null-root", `null`},
		{"no-resident-defaults", `{"version":0,"contentVersion":0}`},
		{"negative-versions", `{"version":-7,"contentVersion":-9}`},
		{"folded-header-and-body", `{"id":"first","ID":null,"title":"first","TITLE":"last","content":"first","CONTENT":null,"Content":"last","contentState":"first","CONTENTSTATE":null}`},
		{"unicode-folded-state", `{"contentState":"first","content\u017ftate":"last"}`},
		{"body-escapes", `{"content":"nul\u0000<>&\u2028\u2029","contentState":"quoted \" \\ \n \ud800"}`},
		{"invalid-utf8-replacement", "{\"content\":\"before" + string([]byte{255}) + "after\",\"title\":\"title" + string([]byte{255}) + "\"}"},
		{"typed-map-union", `{"contentData":{"keep":[1,2],"same":{"old":true},"n":1},"CONTENTDATA":{"same":{"new":true},"n":9007199254740993},"contentData":{}}`},
		{"typed-map-null-reset", `{"contentData":{"old":true},"CONTENTDATA":null,"contentData":{"fresh":[]}}`},
		{"generic-normalization", `{"contentData":{"a":1,"\u0061":2,"n":9007199254740993,"zero":-0,"small":1e-7,"nil":null,"emptyObject":{},"emptyArray":[]}}`},
		{"header-struct-pointer-reuse", `{"creator":{"id":"person","name":"first"},"CREATOR":{"name":null,"email":"last"},"botActor":{"id":"bot","name":"first"},"BOTACTOR":{"name":"last"}}`},
		{"header-container-backing", `{"projectIds":["a","b","c"],"PROJECTIDS":["short"],"projectIds":[null,null,null],"subscriberIds":["a"],"SUBSCRIBERIDS":null,"subscriberIds":[]}`},
		{"revision-fields-fold-and-union", `{"revisions":[{"id":"r","content":"first","CONTENT":null,"Content":"last","contentState":"old","CONTENTSTATE":"new","contentData":{"keep":1,"same":{"a":1}},"CONTENTDATA":{"same":{"b":2}}}]}`},
		{"revision-shrink-regrow-backing", `{"revisions":[{"id":"r0","title":"t0","content":"c0","contentData":{"keep":0}},{"id":"r1","title":"t1","content":"c1","contentData":{"keep":1}},{"id":"r2","title":"t2","content":"c2"}],"REVISIONS":[{"title":"short"}],"revisions":[null,{"contentState":"added"},null]}`},
		{"revision-header-slice-backing-across-arrays", `{"revisions":[{"id":"r","authorIds":["a","b","c"]}],"revisions":[{"authorIds":["short"]}],"revisions":[{"authorIds":[null,null,null]}]}`},
		{"revision-regrow-beyond-previous-capacity", `{"revisions":[{"id":"r0","content":"c0"},{"id":"r1","content":"c1"},{"id":"r2","content":"c2"}],"revisions":[{}],"revisions":[null,null,null,{"id":"r3"},null,{"id":"r5"}]}`},
		{"revision-null-discards-backing", `{"revisions":[{"id":"old","content":"old","contentData":{"old":true}}],"revisions":null,"revisions":[null,{"id":"fresh"}]}`},
		{"revision-empty-discards-backing", `{"revisions":[{"id":"old","content":"old"}],"revisions":[],"revisions":[null]}`},
		{"revision-empty-versus-nil", `{"revisions":[],"attachments":[],"permissions":null}`},
		{"revision-member-null-retains", `{"revisions":[{"id":"r","content":"old","contentState":"old","contentData":{"old":true}}],"revisions":[{"content":null,"contentState":null,"contentData":null}]}`},
		{"times-retain-offsets", `{"createdAt":"2026-01-02T03:04:05.000000006+07:00","CREATEDAT":null,"updatedAt":"2026-01-02T03:04:05Z","archivedAt":"2026-01-03T01:02:03.4-03:00","revisions":[{"id":"r","createdAt":"2026-01-02T03:04:05+07:00","startedAt":"2026-01-01T00:00:00Z"}],"revisions":[null]}`},
		{"offloaded-remains-typed", `{"revisions":[{"id":"r","_offloaded":{"body":{"key":"some-key","size":123},"other":{"key":"kept","size":1,"summary":"retained"}}}]}`},
		{"unknown-syntax-only", `{"unknown":{"number":1e400,"array":[{"name":"ignored"}]},"content":"kept","revisions":[{"id":"r","unknown":1e400}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			scaleP21SW302Success(t, metadataRetryDocumentCanonicalRecord, reflect.TypeFor[domain.Document](), test.raw)
		})
	}
}

func TestScaleP21SW302ProjectCanonicalTypedOracle(t *testing.T) {
	for _, test := range []struct{ name, raw string }{
		{"null-root", `null`},
		{"missing-defaults", `{}`},
		{"folded-description", `{"id":"first","ID":null,"name":"first","NAME":"last","description":"first","DESCRIPTION":null,"Description":"last"}`},
		{"unicode-folded-description", `{"description":"first","de\u017fcription":"last"}`},
		{"description-escapes", `{"description":"nul\u0000<>&\u2028\u2029","summary":"kept"}`},
		{"invalid-utf8-replacement", "{\"description\":\"a" + string([]byte{255}) + "b\"}"},
		{"container-backing", `{"teamIds":["a","b","c"],"TEAMIDS":["short"],"teamIds":[null,null,null],"memberIds":null,"labelIds":[]}`},
		{"pointer-reuse-reset", `{"lead":{"id":"old","name":"first"},"LEAD":{"name":"second"},"lead":null,"lead":{"id":"fresh"},"creator":{"id":"person","name":"first"},"CREATOR":{"email":"last"}}`},
		{"nested-struct-backing", `{"milestones":[{"id":"m0","description":"body0"},{"id":"m1","description":"body1"}],"MILESTONES":[{"id":"short"}],"milestones":[null,{"name":"reused"}]}`},
		{"fresh-times", `{"createdAt":"2026-01-02T03:04:05.000000006+07:00","updatedAt":"2026-01-02T03:04:05Z","archivedAt":"2026-01-03T01:02:03.4-03:00"}`},
		{"unknown-syntax-only", `{"description":"kept","unknown":{"number":1e400,"nested":[true,null]}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			scaleP21SW302Success(t, metadataRetryProjectCanonicalRecord, reflect.TypeFor[domain.Project](), test.raw)
		})
	}
}

func TestScaleP21SW302RetryPrivateOriginalCarry(t *testing.T) {
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
	frames, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		frames.Close()
		if repo.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk ||
			metadataLogicalDescriptorsUsed.Load() != descriptors || recordSpoolBytes.Load() != spools {
			t.Error("private retry carry retained work or frame storage")
		}
	}()
	// Use a real domain field with json:"-"; no custom types or marshalers.
	previous := domain.IntegrationConnection{
		ID: "same", Name: "old", SecretHash: "native-private",
		Config: map[string]string{"old": "old"}, Scopes: []string{"old"},
	}
	raw := []byte(`{"id":"same","name":"fresh","config":null,"scopes":[]}`)
	kind := reflect.TypeFor[domain.IntegrationConnection]()
	want, err := decodeMetadataRecord(kind, raw, reflect.ValueOf(previous))
	if err != nil {
		t.Fatal(err)
	}
	var decoded domain.IntegrationConnection
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	fresh, err := frames.writeValue(reflect.ValueOf(decoded), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := metadataLogicalRetryFramedRecord(t.Context(), fresh, kind, metadataAggregateNativeNode{value: reflect.ValueOf(previous)})
	if err != nil {
		t.Fatal(err)
	}
	node, err := frames.writeAggregateNode(retained, 0)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(want.Interface())
	if err != nil {
		t.Fatal(err)
	}
	scaleP21SW302Compare(t, repo, node, want, wire)
	owner := &ResourceRead{store: repo}
	defer owner.Close()
	if err := owner.reserve(t.Context(), node.cost*2+64<<10); err != nil {
		t.Fatal(err)
	}
	actual, err := node.Value(t.Context(), metadataWorkBytes)
	if err != nil || actual.Interface().(domain.IntegrationConnection).SecretHash != previous.SecretHash {
		t.Fatal("retry lost its original direct private field", err)
	}
	if actual.Interface().(domain.IntegrationConnection).Config != nil || actual.Interface().(domain.IntegrationConnection).Scopes == nil {
		t.Fatal("retry carried public fields instead of the fresh nil/empty typed values")
	}
}

func TestScaleP21SW302CanonicalInvalidKnownFields(t *testing.T) {
	for _, family := range []struct {
		name  string
		kind  reflect.Type
		codec scaleP21SW302Codec
		rows  []string
	}{
		{"document", reflect.TypeFor[domain.Document](), metadataRetryDocumentCanonicalRecord, []string{
			`{"content":[]}`, `{"contentState":{}}`, `{"contentData":[]}`,
			`{"contentData":{"n":1e400}}`, `{"contentData":{"n":1e400,"n":1}}`,
			`{"version":9223372036854775808}`, `{"updatedAt":"broken"}`,
			`{"revisions":{}}`, `{"revisions":[42]}`, `{"revisions":[{"content":false}]}`,
			`{"revisions":[{"contentData":[]}]}`, `{"revisions":[{"createdAt":"broken"}]}`,
			`{"revisions":[{"_offloaded":{"body":[]}}]}`,
			`{"unknown":[true,]}`, `{"content":"first"} {"title":"trailing"}`,
		}},
		{"project", reflect.TypeFor[domain.Project](), metadataRetryProjectCanonicalRecord, []string{
			`{"description":[]}`, `{"priority":"bad"}`, `{"teamIds":{}}`,
			`{"createdAt":"broken"}`, `{"lead":42}`, `{"milestones":[false]}`,
			`{"unknown":{"nested":`, `{"description":"first"} false`,
		}},
	} {
		for index, raw := range family.rows {
			t.Run(family.name+"/"+string(rune('a'+index)), func(t *testing.T) {
				oracle := reflect.New(family.kind)
				literalErr := json.Unmarshal([]byte(raw), oracle.Interface())
				if literalErr == nil {
					t.Fatal("invalid typed fixture unexpectedly succeeds")
				}
				repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
				disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
				frames, err := newMetadataLogicalBuilder(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				node, err := family.codec(t.Context(), repo, frames, legacySessionJSONPart{strings.NewReader(raw), 0, int64(len(raw))})
				frames.Close()
				if err == nil || node != (metadataLogicalNode{}) || errors.Is(err, ErrMetadataWorkBudget) || errors.Is(err, context.Canceled) {
					t.Fatal("invalid known field exposed a node or failed for an unrelated reason", literalErr, err)
				}
				var wantType, actualType *json.UnmarshalTypeError
				if errors.As(literalErr, &wantType) &&
					(!errors.As(err, &actualType) || actualType.Type != wantType.Type || actualType.Value != wantType.Value) {
					t.Fatal("canonical codec changed the standard typed error family", literalErr, err)
				}
				var wantTime, actualTime *time.ParseError
				if errors.As(literalErr, &wantTime) &&
					(!errors.As(err, &actualTime) || actualTime.Layout != wantTime.Layout || actualTime.Value != wantTime.Value) {
					t.Fatal("canonical codec changed timestamp rejection", literalErr, err)
				}
				if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 ||
					metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors ||
					recordSpoolBytes.Load() != spools {
					t.Fatal("failed typed codec retained owners or decoded full metadata")
				}
			})
		}
	}
}

type scaleP21SW302ReadAt func([]byte, int64) (int, error)

func (read scaleP21SW302ReadAt) ReadAt(output []byte, offset int64) (int, error) {
	return read(output, offset)
}

func TestScaleP21SW302CanonicalCancellationRelease(t *testing.T) {
	for _, family := range []struct {
		name, raw string
		kind      reflect.Type
		codec     scaleP21SW302Codec
	}{
		{"document-revision", `{"id":"doc","content":"kept","revisions":[{"id":"r","content":"stop-here","contentData":{"keep":true}}]}`, reflect.TypeFor[domain.Document](), metadataRetryDocumentCanonicalRecord},
		{"project", `{"id":"project","description":"stop-here","name":"kept"}`, reflect.TypeFor[domain.Project](), metadataRetryProjectCanonicalRecord},
	} {
		for _, during := range []bool{false, true} {
			phase := "before"
			if during {
				phase = "body"
			}
			t.Run(family.name+"/"+phase, func(t *testing.T) {
				repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
				disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
				frames, err := newMetadataLogicalBuilder(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				prior, err := frames.writeValue(reflect.ValueOf("prior"), 0, false)
				if err != nil {
					frames.Close()
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				reader := strings.NewReader(family.raw)
				target := int64(strings.Index(family.raw, `"stop-here"`))
				canceledAtBody := false
				input := scaleP21SW302ReadAt(func(output []byte, offset int64) (int, error) {
					count, err := reader.ReadAt(output, offset)
					if during && offset == target {
						canceledAtBody = true
						cancel()
					}
					return count, err
				})
				if !during {
					cancel()
				}
				node, err := family.codec(ctx, repo, frames, legacySessionJSONPart{input, 0, int64(len(family.raw))})
				if !errors.Is(err, context.Canceled) || node != (metadataLogicalNode{}) || during && !canceledAtBody {
					frames.Close()
					t.Fatal("canonical cancellation boundary changed or exposed partial output", phase, err, canceledAtBody)
				}
				priorOwner := &ResourceRead{store: repo}
				if err := priorOwner.reserve(t.Context(), prior.cost); err != nil {
					frames.Close()
					t.Fatal(err)
				}
				old, err := prior.Value(t.Context(), 4096)
				if err != nil || old.String() != "prior" {
					priorOwner.Close()
					frames.Close()
					t.Fatal("failed canonical append corrupted the earlier immutable frame", err)
				}
				old = reflect.Value{}
				priorOwner.Close()
				frames.Close()
				if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 ||
					metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors ||
					recordSpoolBytes.Load() != spools {
					t.Fatal("cancelled canonical codec retained partial owners")
				}
				recovery, err := newMetadataLogicalBuilder(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				want, wire := scaleP21SW302Oracle(t, family.kind, []byte(family.raw))
				node, err = family.codec(t.Context(), repo, recovery, legacySessionJSONPart{strings.NewReader(family.raw), 0, int64(len(family.raw))})
				if err != nil {
					recovery.Close()
					t.Fatal("same-store canonical codec did not recover after cancellation", err)
				}
				scaleP21SW302Compare(t, repo, node, want, wire)
				recovery.Close()
				if repo.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk ||
					metadataLogicalDescriptorsUsed.Load() != descriptors || recordSpoolBytes.Load() != spools {
					t.Fatal("canonical recovery retained work or storage")
				}
			})
		}
	}
}
