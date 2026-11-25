package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"

	"github.com/Deln0r/ygo"
)

type scaleP21SW234ReaderAt func([]byte, int64) (int, error)

func (read scaleP21SW234ReaderAt) ReadAt(dst []byte, offset int64) (int, error) {
	return read(dst, offset)
}

func scaleP21SW234ExpectWire(t *testing.T, raw []byte) (domain.Issue, []byte) {
	t.Helper()
	var oracle domain.Issue
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal("valid canonical Issue fixture does not unmarshal", err)
	}
	normalizeIssueRecord(&oracle)
	wire, err := json.Marshal(oracle)
	if err != nil {
		t.Fatal("valid normalized Issue fixture does not marshal", err)
	}
	return oracle, wire
}

func scaleP21SW234CompareFrame(t *testing.T, repo *SQLiteStore, node metadataLogicalNode, oracle domain.Issue, expected []byte, native bool) {
	t.Helper()
	if node.typ != reflect.TypeFor[domain.Issue]() {
		t.Fatal("canonical frame changed its exact domain type")
	}
	owner := &ResourceRead{store: repo}
	defer owner.Close()
	spool, err := legacySessionNodeSpool(t.Context(), owner, node)
	if err != nil {
		t.Fatal("stream canonical Issue", err)
	}
	defer spool.Close()
	if spool.bytes != int64(len(expected)) {
		t.Fatal("canonical normalized wire length differs", spool.bytes, len(expected))
	}
	var actual [32 << 10]byte
	for offset := int64(0); offset < spool.bytes; {
		size := int(min(int64(len(actual)), spool.bytes-offset))
		count, err := spool.ReadAt(actual[:size], offset)
		if err != nil || count != size ||
			!bytes.Equal(actual[:size], expected[int(offset):int(offset)+size]) {
			t.Fatal("streamed canonical Issue differs from normalized standard JSON", offset, err)
		}
		offset += int64(size)
	}
	if native {
		// Small-table oracle only. The large body success never materializes
		// a complete native Issue from the bounded canonical frame.
		value, err := node.Value(t.Context(), metadataWorkBytes)
		if err != nil || !reflect.DeepEqual(value.Interface(), oracle) {
			t.Fatal("typed native Issue differs, including null/default/container state", err)
		}
	}
}

func TestScaleP21SW234IssueCanonicalDifferential(t *testing.T) {
	rawProducer := domain.Issue{
		ID: "raw-producer",
		DocumentContent: &domain.DocumentContent{ContentData: map[string]any{
			"raw":  json.RawMessage(` { "n": 9007199254740993, "same": 1, "same": 2, "html": "<>&" } `),
			"list": []any{json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`{"ok":true}`)},
		}},
	}
	rawProducerJSON, err := json.Marshal(rawProducer)
	if err != nil {
		t.Fatal(err)
	}
	valid := []struct{ name, raw string }{
		{"null-root-defaults", `null`},
		{"missing-defaults", `{}`},
		{"null-defaults", `{"version":0,"labels":null,"reactions":null,"subscriberIds":null,"subIssueIds":null,"relations":null,"attachments":null}`},
		{"empty-defaults", `{"version":0,"labels":[],"reactions":{},"subscriberIds":[],"subIssueIds":[],"relations":[],"attachments":[]}`},
		{"negative-version-is-not-zero", `{"version":-7}`},
		{"native-integers", `{"version":9223372036854775807,"number":-9223372036854775808,"sortOrder":-0,"estimate":1e-7}`},
		{"folded-body-null-retention", `{"id":"first","ID":null,"title":"first","TITLE":null,"description":"first","DESCRIPTION":null,"descriptionState":"first-state","DESCRIPTIONSTATE":null}`},
		{"folded-body-last", `{"DESCRIPTION":"first","description":"last\u0000<>&\u2028","DescriptionState":"first","descriptionState":"last"}`},
		{"unicode-folded-body-name", `{"de\u017fcription":"long-s","DeScRiPtIoN":null,"documentContent":{"content\u017ftate":"folded-state","CONTENTSTATE":null}}`},
		{"invalid-utf8-is-standard-replacement", "{\"description\":\"a" + string([]byte{255}) + "b\"}"},
		{"load-is-not-column-rewrite", `{"title":"x\u0000y","description":"before\u0000after","agentSessionState":"active","hasContent":true,"reactionCounts":{"heart":3},"affectedIssues":[{"id":"peer","version":9}],"labels":[{"id":"label","issueCount":11}]}`},
		{"document-pointer-reuse", `{"documentContent":{"id":"doc","version":7,"content":"first","contentState":"first-state","contentData":{"keep":1}},"DOCUMENTCONTENT":{"CONTENT":null,"CONTENTSTATE":"last-state"}}`},
		{"document-pointer-reset", `{"documentContent":{"id":"old","content":"old","contentState":"old","contentData":{"old":true}},"documentContent":null,"DocumentContent":{"content":"new"}}`},
		{"nested-folded-body-retention", `{"documentContent":{"content":"first","CONTENT":null,"contentState":"first","CONTENTSTATE":null,"Content":"last","contentState":"last"}}`},
		{"content-data-map-merge", `{"documentContent":{"contentData":{"keep":{"a":1},"replace":{"old":true},"same":1}},"DOCUMENTCONTENT":{"CONTENTDATA":{"replace":{"new":true},"same":2,"new":false}}}`},
		{"content-data-empty-object-retains", `{"documentContent":{"contentData":{"keep":[1,2],"nested":{"before":true}}},"documentContent":{"contentData":{}}}`},
		{"content-data-null-clear", `{"documentContent":{"contentData":{"old":true}},"documentContent":{"contentData":null},"documentContent":{"contentData":{"fresh":[]}}}`},
		{"content-data-escaped-key-union", `{"documentContent":{"contentData":{"a":{"old":true},"keep":1}},"DOCUMENTCONTENT":{"CONTENTDATA":{"\u0061":{"new":true,"same":1,"same":2},"nul\u0000key":null}}}`},
		{"content-data-nested-values-replace", `{"documentContent":{"contentData":{"object":{"old":true},"array":[1,2,3]}},"documentContent":{"contentData":{"object":{},"array":[null]}}}`},
		{"generic-normalization", `{"documentContent":{"contentData":{"n":9007199254740993,"small":1e-7,"negativeZero":-0,"same":1,"same":2,"nil":null,"emptyMap":{},"emptyList":[],"list":[1,true,null,{"x":"<>&\u2028"}]}}}`},
		{"raw-message-producer-normalizes", string(rawProducerJSON)},
		{"string-slice-backing-reuse", `{"previousIdentifiers":["old-a","old-b","old-c"],"PREVIOUSIDENTIFIERS":["short"],"previousIdentifiers":[null,null,null]}`},
		{"string-slice-reset", `{"subscriberIds":["discard"],"SUBSCRIBERIDS":[],"subscriberIds":[null],"subIssueIds":["discard"],"SUBISSUEIDS":null,"subIssueIds":[null]}`},
		{"label-slice-backing-reuse", `{"labels":[{"id":"a","name":"first"},{"id":"b","name":"second"}],"LABELS":[{"id":"short"}],"labels":[null,{"name":"reused"}]}`},
		{"reaction-map-merge", `{"reactions":{"keep":["a"],"same":["before"]},"REACTIONS":{"same":null,"new":[]},"reactions":{"last":["z"]}}`},
		{"reaction-map-reset", `{"reactions":{"old":["x"]},"REACTIONS":null,"reactions":{"fresh":null}}`},
		{"pointer-and-struct-null", `{"assignee":{"id":"first","name":"first"},"ASSIGNEE":{"name":null,"email":"last"},"team":{"id":"team","name":"before"},"TEAM":null,"estimate":1,"ESTIMATE":null}`},
		{"pointer-replacement", `{"assignee":{"id":"old","name":"old"},"assignee":null,"assignee":{"id":"new"},"project":{"id":"old","name":"old"},"PROJECT":null,"project":{"id":"new"}}`},
		{"times", `{"createdAt":"2026-01-02T03:04:05.000000006+07:00","CREATEDAT":null,"updatedAt":"2026-01-02T03:04:05Z","completedAt":"2026-01-03T01:02:03.4Z","COMPLETEDAT":null,"documentContent":{"updatedAt":"2026-01-02T03:04:05.000000006-03:00"},"DOCUMENTCONTENT":{"UPDATEDAT":null}}`},
		{"nested-time-and-generic-attachment", `{"attachments":[{"id":"a","createdAt":"2026-01-02T03:04:05Z","metadata":{"n":9007199254740993,"same":1,"same":2},"creator":{"id":"person","via":{"ignored":1e400}}}],"permissions":[{"id":"p","createdAt":"2026-01-02T03:04:05+07:00"}]}`},
		{"unknown-validates-without-float-conversion", `{"id":"issue","unknown":{"n":1e400,"deep":[{"body":"ignored"}]},"documentContent":{"unknown":{"n":1e400},"content":"kept"}}`},
	}
	for _, test := range valid {
		t.Run("valid/"+test.name, func(t *testing.T) {
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
					t.Error("canonical Issue codec retained work/disk/descriptors or decoded whole metadata")
				}
			}()
			raw := []byte(test.raw)
			oracle, expected := scaleP21SW234ExpectWire(t, raw)
			node, err := legacyIssueCanonicalRecord(t.Context(), repo, frames, legacySessionJSONPart{bytes.NewReader(raw), 0, int64(len(raw))})
			if err != nil {
				t.Fatal("valid typed Issue must succeed, not match an error outcome", err)
			}
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("completed canonical frame retained a temporary native owner")
			}
			scaleP21SW234CompareFrame(t, repo, node, oracle, expected, true)
		})
	}
	for _, test := range []struct{ name, raw string }{
		{"array-root-is-not-issue", `[]`},
		{"trailing-value", `{"description":"ok"} {"id":"extra"}`},
		{"unknown-broken-array", `{"unknown":[true,]}`},
		{"unknown-truncated-object", `{"documentContent":{"unknown":{"nested":`},
		{"bad-string-type", `{"description":[]}`},
		{"bad-nested-string-type", `{"documentContent":{"contentState":{}}}`},
		{"bad-content-data-type", `{"documentContent":{"contentData":[]}}`},
		{"integer-overflow", `{"number":9223372036854775808}`},
		{"generic-number-overflow", `{"documentContent":{"contentData":{"n":1e400}}}`},
		{"generic-overflow-even-if-overwritten", `{"documentContent":{"contentData":{"n":1e400,"n":2}}}`},
		{"bad-time", `{"documentContent":{"updatedAt":"not-a-time"}}`},
	} {
		t.Run("invalid/"+test.name, func(t *testing.T) {
			var oracle domain.Issue
			literalErr := json.Unmarshal([]byte(test.raw), &oracle)
			if literalErr == nil {
				t.Fatal("invalid fixture unexpectedly succeeds with encoding/json")
			}
			repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
			disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
			frames, err := newMetadataLogicalBuilder(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			node, err := legacyIssueCanonicalRecord(t.Context(), repo, frames, legacySessionJSONPart{strings.NewReader(test.raw), 0, int64(len(test.raw))})
			frames.Close()
			if err == nil || errors.Is(err, ErrMetadataWorkBudget) || errors.Is(err, context.Canceled) ||
				node != (metadataLogicalNode{}) {
				t.Fatal("invalid JSON was accepted, exposed a partial node or failed for the wrong reason", literalErr, err)
			}
			var wantType, actualType *json.UnmarshalTypeError
			if errors.As(literalErr, &wantType) &&
				(!errors.As(err, &actualType) || actualType.Type != wantType.Type || actualType.Value != wantType.Value) {
				t.Fatal("typed canonical decode changed its type-error family", literalErr, err)
			}
			var wantTime, actualTime *time.ParseError
			if errors.As(literalErr, &wantTime) &&
				(!errors.As(err, &actualTime) || actualTime.Layout != wantTime.Layout || actualTime.Value != wantTime.Value) {
				t.Fatal("typed canonical decode changed its time-error family", literalErr, err)
			}
			if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 ||
				metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors ||
				recordSpoolBytes.Load() != spools {
				t.Fatal("failed canonical Issue retained work/disk/descriptors or decoded whole metadata")
			}
		})
	}
}

func scaleP21SW234EditorProducerRow(t *testing.T) ([]byte, int, int) {
	t.Helper()
	body := strings.Repeat("x", 4<<20)
	documentJSON, err := json.Marshal(map[string]any{
		"type": "doc", "content": []any{
			map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": body}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc := ygo.NewDocWithOptions(ygo.Options{ClientID: 234})
	fragment := ygo.NewXmlFragment(doc, "prosemirror")
	tx := doc.WriteTxn()
	paragraph := fragment.InsertXmlElement(tx, 0, "paragraph")
	if err := paragraph.InsertXmlText(tx, 0).Insert(tx, 0, body); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	state, descriptionState := base64.StdEncoding.EncodeToString(ygo.EncodeStateAsUpdate(doc)), string(documentJSON)
	request, err := json.Marshal(domain.IssueUpdateInput{
		Description: &body, DescriptionState: &descriptionState, ContentState: &state,
	})
	if err != nil || len(request) >= 16<<20 {
		t.Fatal("supported optional-field editor request exceeds HTTP limit", err, len(request))
	}
	// These are the exact fields retained by server.applyUpdate, already
	// exercised through that real producer in SW201 legal-large-editor-state.
	at := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	raw, err := json.Marshal(domain.Issue{
		ID: "sw234-editor", Version: 1, Identifier: "SW234-1", Number: 1,
		Title: "Supported editor state", Description: body, DescriptionState: descriptionState,
		Priority: 4, PriorityLabel: "Low", CreatedAt: at, UpdatedAt: at,
		Team:    domain.Team{ID: "sw234-team", Key: "SW234", Name: "Canonical fixture"},
		State:   domain.WorkflowState{ID: "sw234-state", TeamID: "sw234-team", Name: "Started", Type: "started"},
		Creator: domain.User{ID: "sw234-owner", Name: "Canonical owner", Active: true},
		DocumentContent: &domain.DocumentContent{
			ID: "document_content_sw234-editor", Version: 1, Content: body, ContentState: state, UpdatedAt: at,
		},
	})
	if err != nil || len(raw) <= 17<<20 {
		t.Fatal("supported editor row did not exceed the old whole-row bound", err, len(raw))
	}
	return raw, len(request), len(state)
}

func TestScaleP21SW234IssueCanonicalLargeAndRelease(t *testing.T) {
	raw, requestBytes, stateBytes := scaleP21SW234EditorProducerRow(t)
	oracle, expected := scaleP21SW234ExpectWire(t, raw)
	sentinel := errors.New("sw234 backing read failed")
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	// An 18-MiB row must not require owning its complete raw/native image.
	// This smaller fixture allowance is not a production limit change.
	repo.metadataResidency.work.limit = 16 << 20
	for _, mode := range []string{"success", "cancel-before", "cancel-during-read", "backing-io", "short-backing", "budget-denial", "recovery"} {
		t.Run(mode, func(t *testing.T) {
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
					t.Error("large/failing canonical Issue retained ownership or decoded whole metadata")
				}
			}()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancel-before" {
				cancel()
			}
			input := bytes.NewReader(raw)
			if mode == "short-backing" {
				input = bytes.NewReader(raw[:len(raw)-5])
			}
			readBytes, observedWork := int64(0), 0
			reader := scaleP21SW234ReaderAt(func(dst []byte, offset int64) (int, error) {
				if mode == "backing-io" && readBytes >= 64<<10 {
					return 0, sentinel
				}
				count, err := input.ReadAt(dst, offset)
				readBytes += int64(count)
				observedWork = max(observedWork, repo.MetadataWorkUsedForTesting())
				if mode == "cancel-during-read" && readBytes >= 64<<10 {
					cancel()
				}
				return count, err
			})
			if mode == "budget-denial" {
				repo.metadataResidency.work.limit = 1
			}
			node, err := legacyIssueCanonicalRecord(ctx, repo, frames, legacySessionJSONPart{reader, 0, int64(len(raw))})
			repo.metadataResidency.work.limit = 16 << 20
			switch mode {
			case "success", "recovery":
				if err != nil {
					t.Fatal("supported large editor record must succeed exactly", err)
				}
				if repo.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("finished large frame retained a whole native decode")
				}
				scaleP21SW234CompareFrame(t, repo, node, oracle, expected, false)
				t.Logf("request_bytes=%d row_bytes=%d content_state_bytes=%d observed_IO_work=%d",
					requestBytes, len(raw), stateBytes, observedWork)
			case "cancel-before", "cancel-during-read":
				if !errors.Is(err, context.Canceled) || node != (metadataLogicalNode{}) ||
					mode == "cancel-during-read" && readBytes < 64<<10 {
					t.Fatal("canonical cancellation returned partial state or missed real IO", err, readBytes)
				}
			case "backing-io":
				if !errors.Is(err, sentinel) || node != (metadataLogicalNode{}) || readBytes < 64<<10 {
					t.Fatal("canonical reader swallowed backing IO or returned partial state", err, readBytes)
				}
			case "short-backing":
				if (!errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF)) || node != (metadataLogicalNode{}) {
					t.Fatal("short backing source became accepted JSON or a partial record", err)
				}
			case "budget-denial":
				if !errors.Is(err, ErrMetadataWorkBudget) || node != (metadataLogicalNode{}) {
					t.Fatal("admission refusal returned a node or changed its typed error", err)
				}
			}
		})
	}
}
