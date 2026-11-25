package store

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW198StreamStore(t *testing.T) *SQLiteStore {
	t.Helper()
	return scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {}})
}

func TestScaleP21SW198SessionCanonicalFrames(t *testing.T) {
	repo := scaleP21SW198StreamStore(t)
	for _, raw := range []string{
		`{"id":"session","messages":null}`,
		`{"id":"session","messages":[]}`,
		`{"ID":"session","messages":[null,{"id":"same","content":"old"},{"id":"same","content":"new"}]}`,
		`{"id":"session","messages":[{"id":"m","content":"first","CONTENT":null,"content":"last\u0000","durationMs":9007199254740993}]}`,
		`{"id":"session","messages":[{"id":"m","role":"assistant","parts":[{"id":"p","type":"toolCall","toolCall":{"id":"call","name":"save","arguments":{"x":1,"x":2},"result":{"ok":true}}}]}]}`,
		`{"id":"session","messages":[{"id":"m","content":"` + strings.Repeat("large user input ", 180000) + `","role":"user"}]}`,
		`{"id":"session","messages":[{"id":"old","content":"old"}],"MESSAGES":[{"id":"new","content":"new"}]}`,
		`{"id":"session","messages":[{"id":"old","content":"old","role":"user"}],"MESSAGES":[{"id":"new"}]}`,
		`{"id":"session","messages":[{"id":"a","content":"old a"},{"id":"b","content":"old b"},{"id":"c","content":"old c"}],"messages":[{"id":"short"}],"messages":[null,{"id":"reused"},null]}`,
		`{"id":"session","messages":[{"id":"a","content":"old"}],"messages":[],"messages":[{"id":"fresh"}]}`,
		`{"id":"session","messages":[{"id":"a","content":"old"}],"messages":null,"messages":[{"id":"fresh"}]}`,
		`{"id":"session","messages":[{"id":"m","content":"` + strings.Repeat("large retained ", 180000) + `","role":"user"}],"messages":[{"id":"same-body","content":null}]}`,
	} {
		t.Run(string(rune(len(raw)%26+'a')), func(t *testing.T) {
			var oracle domain.AgentSession
			if err := json.Unmarshal([]byte(raw), &oracle); err != nil {
				t.Fatal(err)
			}
			expected, err := json.Marshal(oracle)
			if err != nil {
				t.Fatal(err)
			}
			frames, err := newMetadataLogicalBuilder(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer frames.Close()
			node, err := legacySessionCanonicalRecord(t.Context(), repo, frames, legacySessionJSONPart{strings.NewReader(raw), 0, int64(len(raw))})
			if err != nil {
				t.Fatal(err)
			}
			owner := &ResourceRead{store: repo}
			var got bytes.Buffer
			err = legacyProjectStreamNode(t.Context(), owner, node, &got)
			owner.Close()
			if err != nil || !bytes.Equal(got.Bytes(), expected) {
				t.Fatal("canonical framing changed typed JSON", err)
			}
			field, ok := metadataFieldByName("agentSessions")
			if !ok {
				t.Fatal("agentSessions field missing")
			}
			loaded, err := normalizedMetadataRecord(field, oracle.ID, reflect.ValueOf(oracle))
			if err != nil || !bytes.Equal(loaded, expected) {
				t.Fatal("session load normalization changed; extend streamed row closure", err)
			}
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("canonical message framing leaked")
			}
		})
	}
}

func TestScaleP21SW198SessionStreamedOffload(t *testing.T) {
	repo := scaleP21SW198StreamStore(t)
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	cases := []domain.AgentSession{
		{ID: "session", Messages: nil},
		{ID: "session", Messages: []domain.AgentMessage{}},
		{ID: "session", Messages: []domain.AgentMessage{{ID: "user", Role: "user", Content: strings.Repeat("inline user ", 230000), CreatedAt: at}}},
		{ID: "session", Messages: []domain.AgentMessage{
			{ID: "user", Role: "user", Content: strings.Repeat("inline user ", 230000), CreatedAt: at},
			{ID: "reply", Role: "assistant", Content: strings.Repeat("<>& reply ", 1000), Parts: []domain.AgentMessagePart{
				{ID: "text", Text: strings.Repeat("part text ", 1000), Type: "text"},
				{ID: "call", Type: "toolCall", ToolCall: &domain.AgentToolCall{ID: "call", Name: "save", Arguments: json.RawMessage(`{"x":1,"x":2}`), Result: json.RawMessage(`{"body":"` + strings.Repeat("large result ", 1000) + `"}`)}},
			}, CreatedAt: at},
		}},
		{ID: "session", Messages: []domain.AgentMessage{
			{ID: "same", Role: "assistant", Content: "", Offloaded: domain.OffloadedContent{"content": {Key: "existing", Size: 3000}},
				Parts: []domain.AgentMessagePart{{ID: "p", Text: "", Offloaded: domain.OffloadedContent{"text": {Key: "part", Size: 2000}}}}},
			{ID: "same", Role: "assistant", Content: "small"},
		}},
	}
	tx := &sqlTx{database: &sqlDatabase{content: &contentOffload{agentBytes: 256}}}
	for index, session := range cases {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			raw, err := json.Marshal(session)
			if err != nil {
				t.Fatal(err)
			}
			expected, _, err := tx.database.content.storedForm("agentSessions", raw)
			if err != nil {
				t.Fatal(err)
			}
			owner := &ResourceRead{store: repo}
			output, err := newMetadataLogicalRecordSpool(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			err = legacySessionStoredForm(t.Context(), owner, tx, "", legacySessionJSONPart{bytes.NewReader(raw), 0, int64(len(raw))}, output, false)
			actual := make([]byte, int(output.bytes))
			_, readErr := output.ReadAt(actual, 0)
			owner.Close()
			if err != nil || readErr != nil || !bytes.Equal(actual, expected) {
				t.Fatal("streamed offload changed canonical bytes", index, err, readErr)
			}
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("streamed offload leaked")
			}
		})
	}
}

func TestScaleP21SW198SessionMessageMerge(t *testing.T) {
	repo := scaleP21SW198StreamStore(t)
	large := strings.Repeat("unchanged user ", 170000)
	for _, test := range []struct{ name, base, ours, theirs string }{
		{"independent-fields", `[{"id":"m","content":"base","status":"streaming"}]`, `[{"id":"m","content":"base","status":""}]`, `[{"id":"m","content":"peer","status":"streaming"}]`},
		{"duplicate-last-value-first-order", `[{"id":"a","content":"first"},{"id":"b","content":"b"},{"id":"a","content":"last"}]`, `[{"id":"a","content":"changed first"},{"id":"b","content":"b"},{"id":"a","content":"last"}]`, `[{"id":"b","content":"peer b"},{"id":"a","content":"last"}]`},
		{"conflicting-leaf", `[{"id":"m","content":"base"}]`, `[{"id":"m","content":"ours"}]`, `[{"id":"m","content":"theirs"}]`},
		{"empty-id", `[{"id":"","content":"base"}]`, `[{"id":"","content":"ours"}]`, `[{"id":"","content":"theirs"}]`},
		{"large-user-frame", `[{"id":"user","content":"` + large + `"},{"id":"m","status":"streaming"}]`, `[{"id":"user","content":"` + large + `"},{"id":"m","status":""}]`, `[{"id":"user","content":"` + large + `"},{"id":"m","status":"streaming","content":"peer"}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected, valid := mergeMetadataLists([]byte(test.base), []byte(test.ours), []byte(test.theirs))
			owner := &ResourceRead{store: repo}
			output, err := newMetadataLogicalRecordSpool(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			parts := make([]legacySessionJSONPart, 0, 3)
			for _, value := range []string{test.base, test.ours, test.theirs} {
				parts = append(parts, legacySessionJSONPart{strings.NewReader(value), 0, int64(len(value))})
			}
			ok, err := legacySessionMergeMessages(t.Context(), owner, parts[0], parts[1], parts[2], output)
			actual := make([]byte, int(output.bytes))
			if output.bytes != 0 {
				if _, readErr := output.ReadAt(actual, 0); readErr != nil {
					t.Fatal(readErr)
				}
			}
			owner.Close()
			if err != nil || ok != valid || ok && !bytes.Equal(expected, actual) {
				t.Fatal("streamed message merge changed legacy result", valid, ok, err)
			}
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("streamed message merge leaked")
			}
		})
	}
}

func TestScaleP21SW198SessionRowHookContract(t *testing.T) {
	if metadataSearchFieldSet["agentSessions"] {
		t.Fatal("session search hook added; extend streamed session writer")
	}
	// These calls prove the row writer's non-search side-index hooks remain
	// no-ops for sessions; a new hook cannot silently consume header-only JSON.
	if err := writeCustomerFilterRecord(context.Background(), nil, "", "agentSessions", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := writeReleaseIssueRecords(context.Background(), nil, "", "agentSessions", "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW198SessionFullFieldSmallRowRebase(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	first := domain.AgentSession{ID: "session", Title: "before", UpdatedAt: at}
	duplicate := domain.AgentSession{ID: "session", Title: "later duplicate", UpdatedAt: at}
	before, target := f.data, f.data
	before.AgentSessions = []domain.AgentSession{first, duplicate}
	target.AgentSessions = []domain.AgentSession{first, duplicate}
	target.AgentSessions[0].LastReadAt = &at
	peer := first
	peer.Title = "canonical peer"
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	scaleP09Put(t, tx, f.data.Workspace.URLKey, "agentSessions", "session", peer)
	ctx := context.WithValue(t.Context(), agentSessionLifecycleWriteKey{}, true)
	stamp := copyStamp{workspaceID: f.data.Workspace.ID}
	left, err := captureMetadataLogicalSource(ctx, before, stamp)
	if err != nil {
		t.Fatal(err)
	}
	defer left.Release()
	right, err := captureMetadataLogicalSource(ctx, target, stamp)
	if err != nil {
		t.Fatal(err)
	}
	defer right.Release()
	frames, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer frames.Close()
	for _, source := range []*metadataLogicalSource{left, right} {
		for _, storage := range source.stores {
			if err := frames.inheritLocations(storage); err != nil {
				t.Fatal(err)
			}
		}
	}
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency()}
	owner := &ResourceRead{store: view}
	defer owner.Close()
	field, found := metadataFieldByName("agentSessions")
	if !found {
		t.Fatal("missing session field")
	}
	selected := &legacyMetadataWriteField{
		field: field, before: left.fields[field.index], next: right.fields[field.index],
		full: true, changed: true,
	}
	plan := &legacyMetadataWritePlan{
		store: view, tx: tx, workspace: f.data.Workspace.URLKey, workspaceID: f.data.Workspace.ID,
		work: owner, frames: frames, requested: right, actual: left,
	}
	// Run the literal complete-field rebaser as the oracle, then clear only
	// its transaction-local "already rebased" marker before the streamed path.
	oracleBefore, oracleNext := cloneBootstrap(before), cloneBootstrap(target)
	if _, _, err := f.rebaseMetadata(ctx, tx, f.data.Workspace.URLKey, &oracleBefore, &oracleNext, []metadataField{field}, nil); err != nil {
		t.Fatal("literal full-field rebase", err)
	}
	if len(oracleNext.AgentSessions) != 2 || oracleNext.AgentSessions[0].Title != peer.Title || oracleNext.AgentSessions[0].LastReadAt == nil || !reflect.DeepEqual(oracleNext.AgentSessions[1], duplicate) {
		t.Fatal("literal fixture did not merge the first occurrence and preserve the later duplicate")
	}
	tx.rebasedMetadata = nil
	handled, err := plan.rebaseAgentSessionLifecycleCollection(ctx, selected)
	if err != nil || !handled {
		t.Fatal("small full-field rebase", handled, err)
	}
	got, err := selected.next.Value(ctx, metadataWorkBytes)
	expected := oracleNext.AgentSessions
	if err != nil || !reflect.DeepEqual(got.Interface(), expected) {
		t.Fatal("small canonical row skipped generic rebase or changed later duplicate", err, got)
	}
	owner.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("small full-field rebase leaked")
	}
}

func TestScaleP21SW198SessionRawAndLoadedEquality(t *testing.T) {
	session := domain.AgentSession{ID: "session", Messages: []domain.AgentMessage{{ID: "user", Role: "user", Content: string([]byte{0xff})}}}
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {AgentSessions: []domain.AgentSession{session}}})
	source, err := repo.retainMetadataLogicalSource(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	index, _, _ := metadataLogicalField("agentSessions")
	var node metadataLogicalNode
	if err := source.fields[index].Children(t.Context(), func(_ reflect.Value, value metadataLogicalNode) error { node = value; return nil }); err != nil {
		t.Fatal(err)
	}
	owner := &ResourceRead{store: repo}
	defer owner.Close()
	native, err := legacySessionNodeSpool(t.Context(), owner, node)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	nativeFields, err := legacySessionJSONObject(t.Context(), owner, legacySessionSpoolPart(native))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	part := legacySessionJSONPart{bytes.NewReader(raw), 0, int64(len(raw))}
	rawFields, err := legacySessionJSONObject(t.Context(), owner, part)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := legacySessionObjectsEqual(t.Context(), owner, rawFields, nativeFields); err != nil || !same {
		t.Fatal("stored raw escape no longer matches native invalid UTF-8", err)
	}
	frames, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer frames.Close()
	decoded, err := legacySessionCanonicalRecord(t.Context(), repo, frames, part)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := legacySessionNodeSpool(t.Context(), owner, decoded)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	loadedFields, err := legacySessionJSONObject(t.Context(), owner, legacySessionSpoolPart(loaded))
	if err != nil {
		t.Fatal(err)
	}
	if same, err := legacySessionObjectsEqual(t.Context(), owner, loadedFields, nativeFields); err != nil || same {
		t.Fatal("typed loaded equality erased raw/native escape distinction", err)
	}
}

func TestScaleP21SW198SessionAggregateFrames(t *testing.T) {
	repo := scaleP21SW198StreamStore(t)
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	for _, test := range []struct {
		name, body, raw string
	}{
		{"native-generic-wire", "native\x00" + string([]byte{0xff}) + "<>&\u2028", `{"duplicate":1,"duplicate":2,"long":9007199254740993,"negative":-0.0}`},
		{"absent-nonfinite-generic-value", "body", `{"outsideFloat64":1e400}`},
		{"nine-megabyte-user-body", strings.Repeat("large raw user ", 600000), `{"value":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := domain.Bootstrap{AgentSessions: []domain.AgentSession{{
				ID: "session", UserID: "owner", UpdatedAt: at,
				Messages: []domain.AgentMessage{
					{ID: "user", Role: "user", Content: test.body, CreatedAt: at},
					{ID: "reply", Role: "assistant", Content: "old", Status: "streaming", DurationMS: 9007199254740993,
						Parts:     []domain.AgentMessagePart{{ID: "call", Type: "toolCall", ToolCall: &domain.AgentToolCall{ID: "call", Name: "save", Arguments: json.RawMessage(test.raw)}}},
						CreatedAt: at, UpdatedAt: &at},
				},
			}}}
			next := cloneBootstrap(before)
			next.AgentSessions[0].Messages[1].Content = "updated"
			next.AgentSessions[0].Messages[1].Status = ""
			left, err := captureMetadataLogicalSource(t.Context(), before, copyStamp{})
			if err != nil {
				t.Fatal(err)
			}
			defer left.Release()
			right, err := captureMetadataLogicalSource(t.Context(), next, copyStamp{})
			if err != nil {
				t.Fatal(err)
			}
			defer right.Release()
			for _, webhook := range []bool{false, true} {
				publication, err := repo.prepareMetadataAggregatePublication(t.Context(),
					metadataAggregateImage{Source: left}, metadataAggregateImage{Source: right},
					"session", webhook, json.RawMessage("null"), "agent.message_updated")
				if err != nil {
					t.Fatal(err)
				}
				previous, realtime := publication.PreviousValues, publication.RealtimePayload
				expectedPrevious := json.RawMessage(nil)
				if webhook {
					expectedPrevious = aggregatePreviousValues(before, next, "session")
				}
				expectedRealtime := enrichRealtimePayload(json.RawMessage("null"), aggregateJSONValue(next, "session"), "agent.message_updated")
				if !bytes.Equal(previous, expectedPrevious) || !bytes.Equal(realtime, expectedRealtime) {
					publication.Close()
					t.Fatal("owned session aggregate changed legacy envelope", webhook)
				}
				if repo.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("aggregate frames have no live owner")
				}
				publication.Close()
				if repo.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("aggregate frames leaked")
				}
			}
		})
	}
	for _, pair := range [][2]string{
		{`{"n":-0}`, `{"n":0}`},
		{`[{"n":-0}]`, `[{"n":0}]`},
		{`{"n":1}`, `{"n":2}`},
	} {
		var left, right any
		if err := json.Unmarshal([]byte(pair[0]), &left); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(pair[1]), &right); err != nil {
			t.Fatal(err)
		}
		work, err := beginMetadataAggregateTraversal(t.Context(), func(bytes uint64) (func(), error) {
			if bytes > metadataWorkBytes {
				return nil, ErrMetadataWorkBudget
			}
			_, release, err := repo.tryAdmitAdditionalMetadataWork(t.Context(), int(bytes))
			return release, err
		})
		if err != nil {
			t.Fatal(err)
		}
		same, err := metadataAggregateNormalizedEqual(t.Context(), left, right, work)
		work.Close()
		if err != nil || same != reflect.DeepEqual(left, right) {
			t.Fatal("generic signed-zero equality changed", same, err)
		}
		if repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("generic equality traversal leaked")
		}
	}
}
