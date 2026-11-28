package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type metadataEncodingFixture struct {
	name  string
	value any
}

func metadataEncodingFixtures(body string) []metadataEncodingFixture {
	now := time.Date(2026, 11, 27, 1, 2, 3, 0, time.UTC)
	user := domain.User{ID: "user-1", Name: "Example User"}
	issue := domain.Issue{
		ID: "issue-1", Identifier: "ENG-42", Number: 42, Version: 1, Title: "Example issue", Description: body,
		Team: domain.Team{ID: "team-1", Name: "Engineering"}, State: domain.WorkflowState{ID: "backlog", Type: "backlog"},
		Creator: user, CreatedAt: now, UpdatedAt: now,
	}
	return []metadataEncodingFixture{
		{"issue", issue},
		{"document", domain.Document{ID: "document-1", Title: "Example document", Content: body, Creator: user, CreatedAt: now, UpdatedAt: now}},
		{"comment", domain.Comment{ID: "comment-1", Body: body, User: user, CreatedAt: now, Reactions: map[string][]string{"like": {user.ID}}}},
		{"project", domain.Project{ID: "project-1", Name: "Example project", Description: body, Lead: &user, CreatedAt: now, UpdatedAt: now}},
		{"project-update", domain.ProjectUpdate{ID: "update-1", ProjectID: "project-1", Body: body, User: user, CreatedAt: now}},
		{"activity", domain.ActivityEvent{ID: "activity-1", CreatedAt: now, Actor: user, Metadata: domain.ActivityMetadata{"detail": body}}},
		{"notification", domain.Notification{ID: "notification-1", Text: body, Actor: user, CreatedAt: now, UpdatedAt: now}},
		{"raw-compaction", json.RawMessage(`{ "body" : "` + body + `" }`)},
		{"document-revision", domain.DocumentRevision{ID: "revision-1", DocumentID: "document-1", Content: body, Author: user, CreatedAt: now}},
		{"document-input", domain.DocumentInput{Content: &body}},
		{"initiative", domain.Initiative{ID: "initiative-1", Name: "Example initiative", Description: body, Creator: user, CreatedAt: now, UpdatedAt: now}},
		{"initiative-update", domain.InitiativeUpdate{ID: "update-1", InitiativeID: "initiative-1", Body: body, User: user, CreatedAt: now}},
		{"loop", domain.Loop{ID: "loop-1", Name: "Example loop", Instructions: body, Creator: user, CreatedAt: now, UpdatedAt: now}},
		{"release", domain.Release{ID: "release-1", Name: "Example release", Version: "1.0.0", Description: body, Creator: user, CreatedAt: now, UpdatedAt: now}},
		{"issue-create-input", domain.IssueCreateInput{Title: "Example issue", Description: body, TeamID: "team-1"}},
		{"document-content", domain.DocumentContent{ID: "document-1", Version: 1, Content: body, UpdatedAt: now}},
		{"notification-delivery", domain.NotificationDelivery{ID: "delivery-1", NotificationID: "notification-1", RecipientID: user.ID, Error: body, CreatedAt: now, UpdatedAt: now}},
		{"issue-update-input", domain.IssueUpdateInput{Description: &body}},
		{"issue-with-document-content", domain.Issue{ID: issue.ID, Title: issue.Title, Description: body,
			DocumentContent: &domain.DocumentContent{ID: "content-1", Version: 1, Content: body, UpdatedAt: now}}},
	}
}

func (f metadataEncodingFixture) baseline() ([]byte, error) {
	value := f.value
	if issue, ok := value.(domain.Issue); ok {
		value = issueRecordStored(issue)
	}
	raw, err := json.Marshal(value)
	return StripJSONNUL(raw), err
}

func (f metadataEncodingFixture) encode() ([]byte, error) {
	if issue, ok := f.value.(domain.Issue); ok {
		return issueRecordDetail(issue)
	}
	if raw, ok := f.value.(json.RawMessage); ok {
		return marshalMetadataRawJSON(raw)
	}
	return marshalMetadataRecord(f.value)
}

func TestMetadataJSONEncodingAllocation(t *testing.T) {
	for _, size := range []struct{ name, body string }{
		{"small", "A small record."},
		{"typical", strings.Repeat("Text <>& with more detail. ", 152)},
		{"near-limit", strings.Repeat("<>&", 700<<10)},
	} {
		t.Run(size.name, func(t *testing.T) {
			for _, fixture := range metadataEncodingFixtures(size.body) {
				t.Run(fixture.name, func(t *testing.T) {
					work, err := metadataRecordEncodingWork(t.Context(), reflect.ValueOf(fixture.value))
					if err != nil {
						t.Fatal(err)
					}
					want, baseline, baselineLive := measureMetadataEncoding(t, fixture.baseline)
					raw, total, live := measureMetadataEncoding(t, fixture.encode)
					t.Logf("encoded=%d charge=%d total=%d retained=%d baseline_total=%d baseline_retained=%d",
						len(raw), work, total, live, baseline, baselineLive)
					if !bytes.Equal(raw, want) {
						t.Fatal("encoded bytes differ from encoding/json")
					}
					if total > uint64(work) {
						t.Fatalf("allocated %d bytes with a %d byte charge", total, work)
					}
				})
			}
		})
	}
}

func measureMetadataEncoding(t *testing.T, encode func() ([]byte, error)) (raw []byte, total, live uint64) {
	t.Helper()
	gc := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(gc)
	var err error
	total = allocatedBy(func() { raw, err = encode() })
	if err != nil {
		t.Fatal(err)
	}
	// allocatedBy forces GC before encoding. With GC disabled TotalAlloc
	// also bounds the heap increase at any point in the encode. Retained
	// output capacity is the directly attributable live memory afterward.
	return raw, total, uint64(cap(raw))
}

func TestMetadataJSONEncodingParity(t *testing.T) {
	fragments := "<>&\x00\x1f\b\f\n\r\t\\\"\u2028\u2029\u754c\xff" + `\u0000`
	for _, size := range []int{metadataJSONLargeBody - 1, metadataJSONLargeBody, metadataJSONLargeBody + 1} {
		body := strings.Repeat(fragments, size/len(fragments)) + strings.Repeat("x", size%len(fragments))
		raw := json.RawMessage(`{ "description" : "<>&`)
		raw = append(raw, strings.Repeat("x", size)...)
		raw = append(raw, "\u2028\u2029\xff"+`", "escaped" : "\u003c\\", "n" : 1.0e-6 }`...)
		for _, value := range []any{
			domain.Issue{Description: body, DescriptionState: body, Title: `{"description":"-"}`},
			domain.Issue{Description: body, DocumentContent: &domain.DocumentContent{Content: body, ContentState: body}},
			domain.IssueCreateInput{Description: body, DescriptionState: &body, ContentState: &body},
			domain.IssueUpdateInput{Description: &body, DescriptionState: &body, ContentState: &body},
			domain.Document{Content: body, ContentState: body, Revisions: []domain.DocumentRevision{{Content: "", ContentState: body}, {Content: body}}},
			domain.Document{Revisions: []domain.DocumentRevision{{Content: body}}},
			domain.Document{Content: body, Revisions: []domain.DocumentRevision{}},
			domain.Comment{Body: body, BodyData: map[string]any{"body": "-"}},
			raw, []json.RawMessage{nil, raw, json.RawMessage(` true `)},
			map[string]json.RawMessage{"": nil, "\xff": raw, "<": json.RawMessage(`true`)},
		} {
			want, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			got, err := marshalMetadataJSON(value)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("%T at size %d differs from encoding/json: got length %d, want %d, err=%v", value, size, len(got), len(want), err)
			}
		}
		update := metadataUpdate{Parent: body, Item: raw, Null: true}
		wantUpdate, err := json.Marshal(update)
		if err != nil {
			t.Fatal(err)
		}
		gotUpdate, err := marshalMetadataUpdate(update)
		if err != nil || !bytes.Equal(gotUpdate, wantUpdate) {
			t.Fatalf("logical update envelope changed at size %d: %v", size, err)
		}
		issue := domain.Issue{Description: body, DescriptionState: body}
		want, err := json.Marshal(issueRecordStored(issue))
		if err != nil {
			t.Fatal(err)
		}
		got, err := issueRecordDetail(issue)
		if err != nil || !bytes.Equal(got, StripJSONNUL(want)) {
			t.Fatal("SQL JSON changed NUL handling", err)
		}
	}
	broken := json.RawMessage(`"` + strings.Repeat("x", metadataJSONLargeBody))
	if _, err := marshalMetadataJSON(broken); err == nil {
		t.Fatal("invalid raw JSON accepted")
	}
}

func TestMetadataJSONEncodingInvalidRawAllocation(t *testing.T) {
	raw := json.RawMessage(strings.Repeat(" ", metadataJSONLargeBody) + strings.Repeat("[", 9999) + "!")
	work, err := metadataRecordEncodingWork(t.Context(), reflect.ValueOf(raw))
	if err != nil {
		t.Fatal(err)
	}
	total := allocatedBy(func() {
		_, err = marshalMetadataRawJSON(raw)
	})
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) {
		t.Fatalf("invalid raw JSON returned %v", err)
	}
	t.Logf("charge=%d total=%d", work, total)
	if total > uint64(work) {
		t.Fatalf("invalid raw JSON allocated %d bytes with a %d byte charge", total, work)
	}
}

func TestMetadataJSONEncodingLegacyArrayShape(t *testing.T) {
	for _, ids := range [][]string{
		nil, {}, {"", "ok"}, {"ok", "ok"}, {"<&\x00", "ok"},
		{"\xff\xfe", "\ufffd"}, {"\xff\xfe", "\ufffd\ufffd"},
		{strings.Repeat("a", 191)}, {strings.Repeat("a", 192)},
		{strings.Repeat("\xff", 63)}, {strings.Repeat("\xff", 64)},
	} {
		check := func(value any, field string, got bool) {
			t.Helper()
			raw, err := json.Marshal(map[string]any{field: value})
			if err != nil {
				t.Fatal(err)
			}
			_, shapes, _, err := splitWorkspaceMetadata(raw)
			if want := shapes[field] == "array"; err != nil || got != want {
				t.Fatalf("shape mismatch for %q (%s): got %t, want %t, err=%v", ids, field, got, want, err)
			}
		}
		var users []domain.User
		if ids != nil {
			users = make([]domain.User, len(ids))
		}
		counts := legacyCountWriteField{name: "projects", before: metadataLogicalNode{typ: reflect.TypeFor[[]domain.Project]()}}
		for i, id := range ids {
			users[i] = domain.User{ID: id, DisplayName: "Ignored body <>&"}
			counts.headers = append(counts.headers, legacyCountWriteHeader{id: id})
		}
		counts.headers = append(counts.headers, legacyCountWriteHeader{deleted: true})
		check(users, "users", legacyActorWriteSplits(users))
		check(counts.skeleton().Interface(), counts.name, counts.splits())
	}
}

func TestMetadataJSONEncodingPageSizes(t *testing.T) {
	key := RecordKey{Kind: "projects", ID: "<&\xff"}
	for _, record := range []MetadataRecord{
		{},
		{Key: key, Version: "\u2028", Data: json.RawMessage(` { "name" : "<&" } `), Omitted: true, Detail: &key},
		{Key: key, Missing: true, Data: json.RawMessage{}},
	} {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		size, err := record.recordJSONBytes()
		if err != nil || size != int64(len(raw)) {
			t.Fatalf("record size %d, want %d, err=%v", size, len(raw), err)
		}
	}
	if _, err := (MetadataRecord{Data: json.RawMessage(`{"invalid"}`)}).recordJSONBytes(); err == nil {
		t.Fatal("invalid raw record accepted")
	}
	for _, change := range []CommittedChange{
		{},
		{Records: []RecordDelta{}, Invalidations: []string{}},
		{TxID: "\u2029", Seq: "5", AuthzEpoch: "one", Records: []RecordDelta{{Key: key, Version: "1"}, {Key: key, Deleted: true}}, Invalidations: []string{"projects", "&"}},
	} {
		raw, err := json.Marshal(change)
		if size := committedChangeJSONBytes(change); err != nil || size != int64(len(raw)) {
			t.Fatalf("journal change size %d, want %d, err=%v", size, len(raw), err)
		}
	}
}

func TestMetadataJSONEncodingOversizeCatalogMilestones(t *testing.T) {
	const item = `{"unknown":0}`
	input := []byte(`{"milestones":[` + strings.Repeat(item+",", metadataSummaryMaxBytes/3) + item + `],"id":"project"}`)
	row, err := projectCatalogRecord(t.Context(), &canonicalMetadataReader{
		ctx: t.Context(), key: RecordKey{Kind: "projects", ID: "project"}, size: int64(len(input)), offset: int64(len(input)), buffer: input,
	})
	if err != nil || !row.omitted || string(row.summary) != `{"id":"project"}` {
		t.Fatalf("oversize summary: omitted=%t len=%d err=%v", row.omitted, len(row.summary), err)
	}
	if row.version != metadataFingerprint(input) {
		t.Fatal("omission did not fingerprint the complete input")
	}
}

func TestMetadataJSONEncodingIssueUpdateEvent(t *testing.T) {
	view := scaleHotLogoStore(t, nil)
	oldText := strings.Repeat("<>&", 64<<10)
	newText := oldText + " more"
	original := domain.Issue{ID: "issue", Description: oldText, Icon: "old", Version: 1}
	updated := domain.Issue{ID: original.ID, Description: newText, Version: 2,
		DocumentContent: &domain.DocumentContent{Content: newText}}
	input := domain.IssueUpdateInput{Description: &newText}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	wantRealtime := enrichRealtimePayload(payload, updated, "issue.updated")
	wantBefore, err := json.Marshal(map[string]any{
		"description": oldText, "documentContent": nil, "icon": original.Icon, "version": original.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	owner := &ResourceRead{store: view}
	defer owner.Close()
	gotPayload, gotBefore, err := issueUpdateEventFields(ctx, owner, original, updated, input, true)
	if err != nil || !bytes.Equal(gotPayload, payload) || !bytes.Equal(gotBefore, wantBefore) {
		t.Fatalf("stored event bytes changed: %v", err)
	}
	gotRealtime, err := view.encodeMetadataRealtimePayload(ctx, owner, gotPayload, updated, "issue.updated")
	if err != nil || !bytes.Equal(gotRealtime, wantRealtime) {
		t.Fatalf("realtime event bytes changed: %v", err)
	}
	if view.MetadataWorkUsedForTesting() < len(gotPayload)+len(gotBefore)+len(gotRealtime) {
		t.Fatal("event buffers lost their allowance before publication")
	}
	owner.Close()
	held, err := view.HoldMetadataWorkForTesting(view.MetadataWorkLimitForTesting() - (1 << 20))
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	refused := &ResourceRead{store: view}
	defer refused.Close()
	if _, _, err := issueUpdateEventFields(ctx, refused, original, updated, input, true); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("event encoding under budget pressure returned %v", err)
	}
	refused.Close()
	held()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("event encoding work leaked")
	}
}

func TestMetadataJSONEncodingNotificationUpdateBudget(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := journalScaleDatabase(t)
	ctx := t.Context()
	workspace, recipient := f.data.Workspace.URLKey, f.data.Viewer.ID
	item := domain.Notification{
		ID: "encoding-budget-notification", RecipientID: recipient, Text: strings.Repeat("<>&", 128<<10),
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := writeContentRecord(ctx, tx, workspace, "notification", recipient, item); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency()}
	release, err := view.HoldMetadataWorkForTesting(view.MetadataWorkLimitForTesting() - (1 << 20))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	update := func(mutate func(*domain.Notification)) (domain.Notification, error) {
		return view.UpdateNotificationRecord(ctx, workspace, recipient, item.ID, "notification.updated", nil, mutate)
	}
	_, err = update(func(*domain.Notification) { t.Error("mutation ran before encoding admission") })
	release()
	if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatalf("under work pressure: error=%v, used=%d", err, view.MetadataWorkUsedForTesting())
	}
	got, err := update(func(next *domain.Notification) { next.Favorite = true })
	if err != nil || !got.Favorite || got.Text != item.Text || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatalf("bounded notification update: favorite=%t, error=%v, used=%d", got.Favorite, err, view.MetadataWorkUsedForTesting())
	}
}

func TestMetadataJSONEncodingResidentRecordPublication(t *testing.T) {
	_, view, workspace := coldIncrementalFixture(t)
	const id = "ci-project-05"
	ctx := WithMetadataRecords(t.Context(), MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: id}}})
	input := strings.Repeat("<>&", 6<<10)
	published := 0
	view.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		published++
		var payload struct {
			Input  string         `json:"input"`
			Entity domain.Project `json:"entity"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.Input != input || payload.Entity.Name != "Edited" {
			t.Errorf("unexpected resident event: name=%q, input length=%d, error=%v", payload.Entity.Name, len(payload.Input), err)
		}
		if view.MetadataWorkUsedForTesting() < len(event.Payload) {
			t.Error("released publication encoding before delivery")
		}
	})
	edit := func() error {
		return view.mutateResidentRecords(ctx, workspace, "project.updated", input, func(next *domain.Bootstrap) (string, error) {
			for i := range next.Projects {
				if next.Projects[i].ID == id {
					if next.Projects[i].Name != "Project 5" {
						t.Error("budget refusal committed an edit")
					}
					next.Projects[i].Name = "Edited"
					return id, nil
				}
			}
			return "", ErrBoundedMetadataCoverage
		})
	}
	release, err := view.HoldMetadataWorkForTesting(view.MetadataWorkLimitForTesting() - (8<<20 + 128<<10))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	err = edit()
	release()
	if !errors.Is(err, ErrMetadataWorkBudget) || published != 0 || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatalf("resident edit under work pressure: error=%v, published=%d, used=%d", err, published, view.MetadataWorkUsedForTesting())
	}
	if err := edit(); err != nil || published != 1 || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatalf("resident edit: error=%v, published=%d, used=%d", err, published, view.MetadataWorkUsedForTesting())
	}
}

func TestMetadataJSONEncodingIssueCASPresence(t *testing.T) {
	body := strings.Repeat("<>&", 64<<10)
	input := domain.IssueUpdateInput{
		ExpectedVersion: new(int64(2)),
		ContentState:    &body,
		AssigneeID:      new(""),
		LabelIDs:        new([]string{}),
	}
	fields, ok := issueCASFields(input)
	if want := []string{"assigneeId", "delegateId", "description", "labelIds"}; !ok || !reflect.DeepEqual(fields, want) {
		t.Fatalf("CAS fields=%v valid=%t, want %v", fields, ok, want)
	}
}

func TestMetadataJSONEncodingSplitUpdateParentBudget(t *testing.T) {
	view := scaleHotLogoStore(t, nil)
	owner := &ResourceRead{store: view}
	defer owner.Close()
	parent := strings.Repeat("<", 256<<10)
	items := map[string]json.RawMessage{parent: json.RawMessage("[" +
		strings.Repeat(`{"id":"before"},`, 24) + `{"id":"last"}]`)}
	// The input includes one copy of the parent, while each output row
	// includes it again. Admission precedes constructing those rows.
	if _, _, err := splitUpdateMetadataOwned(t.Context(), owner, "projectUpdates", items); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("split update encoding returned %v, want work refusal", err)
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("refused split held work")
	}
}

func TestMetadataJSONEncodingLogicalSelectionBudget(t *testing.T) {
	view := &SQLiteStore{metadataResidency: newMetadataResidency()}
	ctx := t.Context()
	scope := MetadataRecordScope{Fields: []string{"settings"}}
	data := domain.Bootstrap{Settings: map[string]any{"text": strings.Repeat("<", 32<<10)}}
	if err := view.validateMetadataLogicalSelection(ctx, data, scope); err != nil {
		t.Fatal(err)
	}
	release, err := view.HoldMetadataWorkForTesting(view.MetadataWorkLimitForTesting() - (2 << 20))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := view.validateMetadataLogicalSelection(ctx, data, scope); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("selection under budget pressure returned %v", err)
	}
	release()
	data.Settings["text"] = strings.Repeat("<", 64<<10)
	if err := view.validateMetadataLogicalSelection(ctx, data, scope); !errors.Is(err, ErrMetadataPageBudget) {
		t.Fatalf("oversized selection returned %v", err)
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("selection work leaked")
	}
}

func TestMetadataJSONEncodingNestedAllocation(t *testing.T) {
	body := strings.Repeat("<>&", 700<<10)
	value := map[string]any{"descriptionData": []any{map[string]any{"text": body}}}
	work, err := metadataAggregatePayloadEncodingWork(t.Context(), reflect.ValueOf(value), 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, total, _ := measureMetadataEncoding(t, func() ([]byte, error) { return json.Marshal(value) })
	t.Logf("encoded=%d charge=%d total=%d", len(raw), work, total)
	if total > uint64(work) {
		t.Fatalf("ordinary Marshal allocated %d bytes with a %d byte charge", total, work)
	}
}

func TestMetadataJSONEncodingCustomAndRawCollections(t *testing.T) {
	for _, fixture := range []metadataEncodingFixture{
		{"label-deltas", domain.ActivityMetadata{
			"addedLabelIds": `["` + strings.Repeat(`<>&\u0000","`, 16<<10) + `"]`,
		}},
		{"partial-tool-call", domain.AgentToolCall{
			Arguments: json.RawMessage(`{"missing":`),
			Result:    json.RawMessage(strings.Repeat("<>&\xff", 64<<10)),
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			work, err := MetadataJSONEncodingWork(t.Context(), fixture.value)
			if err != nil {
				t.Fatal(err)
			}
			raw, total, retained := measureMetadataEncoding(t, func() ([]byte, error) { return json.Marshal(fixture.value) })
			t.Logf("encoded=%d charge=%d total=%d retained=%d", len(raw), work, total, retained)
			if total > uint64(work) {
				t.Fatalf("custom codec allocated %d with a %d byte charge", total, work)
			}
		})
	}
	t.Run("offload-members", func(t *testing.T) {
		items := make([]map[string]json.RawMessage, 24<<10)
		for i := range items {
			switch i % 3 {
			case 0:
				items[i] = map[string]json.RawMessage{"x": json.RawMessage(` [ "<>&", 1.0 ] `), "y": nil}
			case 1:
				items[i] = map[string]json.RawMessage{}
			}
		}
		want, baseline, _ := measureMetadataEncoding(t, func() ([]byte, error) { return json.Marshal(items) })
		raw, total, retained := measureMetadataEncoding(t, func() ([]byte, error) { return marshalMetadataRawObjects(items) })
		t.Logf("encoded=%d total=%d retained=%d baseline_total=%d", len(raw), total, retained, baseline)
		if !bytes.Equal(raw, want) || total >= baseline {
			t.Fatalf("offload list changed bytes or allocated %d, baseline %d", total, baseline)
		}
	})
}

func TestMetadataOffloadAssemblyAllocation(t *testing.T) {
	view := scaleHotLogoStore(t, nil)
	offload := newContentOffload(DatabaseConfig{AgentOffloadBytes: 4096})
	for _, size := range []struct {
		name  string
		count int
	}{
		{"small", 5},
		{"typical", 512},
		{"near-limit", 350 << 10},
	} {
		body := strings.Repeat("<>&", size.count)
		document := domain.Document{ID: "doc", Content: body, Revisions: []domain.DocumentRevision{
			{ID: "current", Content: body},
			{ID: "older", Content: body + " before edit"},
		}}
		rawDocument, err := marshalMetadataJSON(document)
		if err != nil {
			t.Fatal(err)
		}
		rawTrash, err := marshalMetadataRawObject(map[string]json.RawMessage{
			"resourceType": json.RawMessage(`"document"`), "payload": rawDocument,
		})
		if err != nil {
			t.Fatal(err)
		}
		transcript := map[string]any{"id": "session", "messages": []any{
			map[string]any{"role": "assistant", "content": body, "parts": []any{
				map[string]any{"toolCall": map[string]any{"result": map[string]any{"body": body}}},
			}},
		}}
		rawTranscript, err := json.Marshal(transcript)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range []struct {
			field string
			raw   []byte
		}{
			{"documents", rawDocument}, {"trash", rawTrash}, {"agentSessions", rawTranscript},
		} {
			t.Run(size.name+"/"+record.field, func(t *testing.T) {
				owner := &ResourceRead{store: view}
				defer owner.Close()
				before := view.MetadataWorkUsedForTesting()
				if err := reserveOffloadRecordWork(t.Context(), owner, record.field, record.raw); err != nil {
					t.Fatal(err)
				}
				charge := view.MetadataWorkUsedForTesting() - before
				var retainedBlobs uint64
				_, total, retained := measureMetadataEncoding(t, func() ([]byte, error) {
					raw, blobs, err := offload.storedForm(record.field, record.raw)
					for _, blob := range blobs {
						retainedBlobs += uint64(cap(blob.body))
					}
					return raw, err
				})
				t.Logf("input=%d charge=%d total=%d retained_row_and_bodies=%d",
					len(record.raw), charge, total, retained+retainedBlobs)
				if total > uint64(charge) {
					t.Fatalf("offload allocated %d with a %d byte charge", total, charge)
				}
			})
		}
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("offload encoding work leaked")
	}
}

func TestIntegrationDeliveryEncodingAllocation(t *testing.T) {
	view := scaleHotLogoStore(t, nil)
	for _, fixture := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"compact", json.RawMessage(`{"a":"` + strings.Repeat(`\u003c`, 1<<20) + `"}`)},
		{"whitespace", json.RawMessage(`[` + strings.Repeat(" ", 4<<20) + `true]`)},
		{"html-expansion", json.RawMessage(`{"a":"` + strings.Repeat("<>&", 512<<10) + `"}`)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			read := scaleJSONOwner(t, view, t.Context())
			defer read.Close()
			value := domain.IntegrationDelivery{ID: "delivery", Payload: fixture.raw}
			before := view.MetadataWorkUsedForTesting()
			if err := read.ReserveIntegrationDeliveryEncoding(t.Context(), value); err != nil {
				t.Fatal(err)
			}
			work := view.MetadataWorkUsedForTesting() - before
			total := allocatedBy(func() {
				if err := json.NewEncoder(io.Discard).Encode(value); err != nil {
					t.Fatal(err)
				}
			})
			t.Logf("input=%d charge=%d total=%d", len(fixture.raw), work, total)
			if total > uint64(work) {
				t.Fatalf("delivery encode allocated %d with a %d byte charge", total, work)
			}
		})
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("delivery encoding work leaked")
	}
}

func BenchmarkMetadataJSONEncoding(b *testing.B) {
	for _, size := range []struct{ name, body string }{
		{"small", "A small record."},
		{"typical", strings.Repeat("Text <>& with more detail. ", 152)},
	} {
		for _, fixture := range metadataEncodingFixtures(size.body)[:4] {
			b.Run(size.name+"/"+fixture.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := metadataRecordEncodingWork(b.Context(), reflect.ValueOf(fixture.value)); err != nil {
						b.Fatal(err)
					}
					if _, err := fixture.encode(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
