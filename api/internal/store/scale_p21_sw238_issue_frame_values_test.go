package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"

	"github.com/Deln0r/ygo"
)

func scaleP21SW238Issue() domain.Issue {
	at := time.Date(2026, 11, 16, 1, 2, 3, 456, time.UTC)
	parent, cycle := "parent", "cycle"
	return domain.Issue{
		ID: "sw238-issue", Version: 7, Identifier: "SW238-17", Number: 17,
		Title: "Native issue", Description: "Retained body", Priority: 3,
		PriorityLabel: "Medium", SortOrder: -12.25, CreatedAt: at, UpdatedAt: at,
		Team:     domain.Team{ID: "team", Key: "SW238", Name: "Stored team"},
		State:    domain.WorkflowState{ID: "state", TeamID: "team", Name: "Started", Type: "started"},
		Creator:  domain.User{ID: "creator", Name: "Stored creator", Active: true},
		Assignee: &domain.User{ID: "assignee", Name: "Stored assignee"},
		Project:  &domain.ProjectSummary{ID: "project", Name: "Stored project"},
		ParentID: &parent, CycleID: &cycle,
		Labels:         []domain.IssueLabel{{ID: "label", Name: "Stored label", IssueCount: 91}},
		AgentSessionID: "session", AgentSessionState: "active",
		DocumentContent: &domain.DocumentContent{
			ID: "document", Version: 4, Content: "Document body",
			ContentState: "Stored state", UpdatedAt: at,
			ContentData: map[string]any{"type": "doc", "content": []any{}},
		},
	}
}

func scaleP21SW238Frames(t *testing.T, repo *SQLiteStore, issue domain.Issue, auditedJSON bool) (*metadataLogicalBuilder, metadataLogicalNode) {
	t.Helper()
	disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
	frames, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		frames.Close()
		frames.Close()
		if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 ||
			metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors ||
			recordSpoolBytes.Load() != spools {
			t.Error("issue frame values retained work/disk/descriptors/spools or decoded workspace metadata")
		}
	})
	if auditedJSON {
		if err := frames.auditJSONLocations(reflect.ValueOf(issue)); err != nil {
			t.Fatal("fresh standard JSON timestamp provenance", err)
		}
	}
	node, err := frames.writeValue(reflect.ValueOf(issue), 0, false)
	if err != nil {
		t.Fatal("capture typed Issue fixture", err)
	}
	return frames, node
}

func scaleP21SW238CompareColumns(t *testing.T, expected, actual []any) {
	t.Helper()
	if len(expected) != 20 || len(actual) != len(expected) {
		t.Fatal("issue SQL column count changed", len(expected), len(actual))
	}
	for index := range expected {
		if reflect.TypeOf(actual[index]) != reflect.TypeOf(expected[index]) {
			t.Fatalf("column %d concrete type=%T want=%T", index, actual[index], expected[index])
		}
		if !reflect.DeepEqual(expected[index], actual[index]) {
			if want, ok := expected[index].([]byte); ok {
				got := actual[index].([]byte)
				first := 0
				for first < len(want) && first < len(got) && want[first] == got[first] {
					first++
				}
				t.Fatalf("column %d raw JSON differs at byte %d: len=%d want=%d", index, first, len(got), len(want))
			}
			t.Fatalf("column %d value=%#v want=%#v", index, actual[index], expected[index])
		}
	}
}

func TestScaleP21SW238IssueFrameValuesDifferential(t *testing.T) {
	type fixture struct {
		name        string
		issue       domain.Issue
		auditedJSON bool
		dialect     string
	}
	tests := []fixture{
		{name: "zero-and-nil-defaults"},
		{name: "empty-containers", issue: domain.Issue{
			Labels: []domain.IssueLabel{}, Reactions: map[string][]string{},
			SubscriberIDs: []string{}, SubIssueIDs: []string{},
			Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{},
		}},
		{name: "rich-native", issue: scaleP21SW238Issue()},
	}
	add := func(name string, change func(*domain.Issue)) {
		issue := scaleP21SW238Issue()
		change(&issue)
		tests = append(tests, fixture{name: name, issue: issue})
	}
	add("negative-version-retained", func(issue *domain.Issue) { issue.Version = -7 })
	add("max-version-and-negative-zero", func(issue *domain.Issue) {
		issue.Version, issue.SortOrder = int64(1<<63-1), math.Copysign(0, -1)
	})
	add("read-decorations-and-label-counts", func(issue *domain.Issue) {
		issue.AgentSessionState = "awaitingInput"
		issue.Labels = []domain.IssueLabel{
			{ID: "first", IssueCount: 91}, {ID: "second", IssueCount: -4},
		}
		issue.AffectedIssues = []domain.IssueVersion{{ID: "peer", Version: 19}}
	})
	for _, reaction := range []struct {
		name   string
		users  map[string][]string
		counts map[string]int
	}{
		{"nil-retains-existing-counts", nil, map[string]int{"old": 17}},
		{"empty-retains-existing-counts", map[string][]string{}, map[string]int{"old": 17}},
		{"nonempty-recomputes-counts", map[string][]string{"heart": {"a", "a", ""}, "zero": nil}, map[string]int{"old": 17}},
		{"allzero-clears-existing-counts", map[string][]string{"nil": nil, "empty": {}}, map[string]int{"old": 17}},
		{"nil-counts-stay-nil", nil, nil},
		{"empty-counts-stay-empty", map[string][]string{}, map[string]int{}},
	} {
		issue := scaleP21SW238Issue()
		issue.Reactions, issue.ReactionCounts = reaction.users, reaction.counts
		tests = append(tests, fixture{name: "reactions/" + reaction.name, issue: issue})
	}
	for _, content := range []struct {
		name, description, document, state string
		already                            bool
	}{
		{"unicode-whitespace-only", "\t\n\r\v\f \u0085\u00a0\u1680\u2000\u2007\u2028\u2029\u202f\u205f\u3000", "\u00a0\u2003", "", false},
		{"description-content", "\u2003 text \u3000", "", "", false},
		{"document-content", "\u3000", "\u2003text\u00a0", "", false},
		{"zero-width-is-content", "\u200b", "", "", false},
		{"state-only-is-not-content", "", "", "state-only", false},
		{"computed-true-retained", "", "", "", true},
		{"nul-is-content-before-strip", "\x00", "", "", false},
		{"invalid-utf8-is-content", string([]byte{0xff}), "", "", false},
	} {
		issue := scaleP21SW238Issue()
		issue.Description, issue.DescriptionState, issue.HasContent = content.description, content.state, content.already
		issue.DocumentContent.Content, issue.DocumentContent.ContentState = content.document, content.state
		tests = append(tests, fixture{name: "content/" + content.name, issue: issue})
	}
	add("native-nul-invalid-utf8-and-json-escaping", func(issue *domain.Issue) {
		text := "before\x00" + string([]byte{0xff, 0xc0, 0xaf}) + "<>&\u2028\u2029\n\\u0000"
		issue.ID, issue.Identifier, issue.Title = text, text, text
		issue.Description, issue.DescriptionState = text, text
		issue.Team.ID, issue.Team.Name, issue.State.ID, issue.Creator.ID = text, text, text, text
		issue.Project.ID, issue.Assignee.ID, *issue.CycleID, *issue.ParentID = text, text, text, text
		issue.DocumentContent.Content, issue.DocumentContent.ContentState = text, text
		issue.Labels[0].Name = text
		issue.Reactions = map[string][]string{"key\x00": {"person\x00", string([]byte{0xff})}, "key": {}}
	})
	add("typed-generic-editor-data", func(issue *domain.Issue) {
		issue.DocumentContent.ContentData = map[string]any{
			"large": int64(9007199254740993), "number": json.Number("1e-7"),
			"raw":   json.RawMessage(` { "html": "<>&", "same": 1, "same": 2 } `),
			"empty": []string{}, "nil": []string(nil),
			"bytes": []byte{0, 255, 128}, "nul\x00key": "body\x00",
		}
	})
	add("archived-and-empty-pointer-values", func(issue *domain.Issue) {
		at := issue.UpdatedAt
		issue.ArchivedAt = &at
		issue.Project.ID, issue.Assignee.ID, *issue.CycleID, *issue.ParentID = "", "", "", ""
	})
	var offset domain.Issue
	if err := json.Unmarshal([]byte(`{"id":"offset","createdAt":"2026-01-02T03:04:05.000000006+05:45","updatedAt":"2026-01-02T03:04:05.999999999-03:30","archivedAt":"2026-01-02T03:04:05+07:00","completedAt":"2026-01-02T03:04:05+04:00","documentContent":{"updatedAt":"2026-01-02T03:04:05-07:00"}}`), &offset); err != nil {
		t.Fatal(err)
	}
	tests = append(tests, fixture{name: "audited-offset-times", issue: offset, auditedJSON: true})
	for _, dialect := range []string{"sqlite", "postgres", "mysql"} {
		tests = append(tests, fixture{name: "dialect/" + dialect, dialect: dialect, issue: scaleP21SW238Issue()})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &SQLiteStore{dialect: test.dialect, metadataResidency: newMetadataResidency()}
			workspace := "workspace\x00" + string([]byte{0xff})
			before, beforeErr := json.Marshal(test.issue)
			if beforeErr != nil {
				t.Fatal(beforeErr)
			}
			expected, err := repo.issueRecordColumns(workspace, test.issue)
			if err != nil {
				t.Fatal("valid fixture literal oracle", err)
			}
			frames, node := scaleP21SW238Frames(t, repo, test.issue, test.auditedJSON)
			actual, release, err := legacyIssueRecordFrameValues(t.Context(), repo, frames, node, workspace)
			if err != nil || release == nil {
				t.Fatal("valid framed columns must succeed", err)
			}
			defer release()
			scaleP21SW238CompareColumns(t, expected, actual)
			var detail, list domain.Issue
			if err := json.Unmarshal(actual[19].([]byte), &detail); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(actual[18].([]byte), &list); err != nil {
				t.Fatal(err)
			}
			version := test.issue.Version
			if version == 0 {
				version = 1
			}
			if detail.Version != version || detail.AgentSessionState != "" ||
				list.AgentSessionState != "" || !list.IsSummary ||
				list.Description != "" || list.DescriptionState != "" || list.DocumentContent != nil {
				t.Fatal("stored/list normalization or body exclusion changed")
			}
			for _, label := range detail.Labels {
				if label.IssueCount != 0 {
					t.Fatal("read-model label count leaked into stored issue")
				}
			}
			after, afterErr := json.Marshal(test.issue)
			if afterErr != nil || !bytes.Equal(before, after) {
				t.Fatal("column construction changed the caller's native Issue", afterErr)
			}
			held := repo.MetadataWorkUsedForTesting()
			if held < len(actual[18].([]byte))+len(actual[19].([]byte)) {
				t.Fatal("returned SQL byte buffers have no containing work owner", held)
			}
			frames.Close()
			scaleP21SW238CompareColumns(t, expected, actual)
			if repo.MetadataWorkUsedForTesting() != held {
				t.Fatal("closing frame input released live SQL argument bytes")
			}
			release()
			release()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("column release is incomplete or non-idempotent")
			}
		})
	}
}

func TestScaleP21SW238IssueFrameValuesErrors(t *testing.T) {
	for _, field := range []string{"createdAt", "completedAt", "documentUpdatedAt", "labelCreatedAt", "title"} {
		t.Run(field, func(t *testing.T) {
			issue := scaleP21SW238Issue()
			invalid := time.Date(10000, 1, 2, 3, 4, 5, 0, time.UTC)
			switch field {
			case "createdAt":
				issue.CreatedAt = invalid
			case "completedAt":
				issue.CompletedAt = &invalid
			case "documentUpdatedAt":
				issue.DocumentContent.UpdatedAt = invalid
			case "labelCreatedAt":
				issue.Labels[0].CreatedAt = invalid
			case "title":
				issue.Title = strings.Repeat("x", domain.IssueTitleMaxLength+1)
			}
			repo := &SQLiteStore{dialect: "postgres", metadataResidency: newMetadataResidency()}
			_, expectedErr := repo.issueRecordColumns("workspace", issue)
			if expectedErr == nil {
				t.Fatal("invalid fixture literal oracle succeeded")
			}
			frames, node := scaleP21SW238Frames(t, repo, issue, false)
			actual, release, err := legacyIssueRecordFrameValues(t.Context(), repo, frames, node, "workspace")
			if release != nil {
				release()
			}
			if err == nil || actual != nil || errors.Is(err, ErrMetadataWorkBudget) || errors.Is(err, context.Canceled) {
				t.Fatal("invalid issue returned partial columns or wrong failure family", expectedErr, err)
			}
			var wantTime, gotTime *json.MarshalerError
			if errors.As(expectedErr, &wantTime) &&
				(!errors.As(err, &gotTime) || gotTime.Type != wantTime.Type) {
				t.Fatal("framed columns changed the timestamp marshal error family", expectedErr, err)
			}
			var wantLong, gotLong *ValueTooLongError
			if errors.As(expectedErr, &wantLong) &&
				(!errors.As(err, &gotLong) || *gotLong != *wantLong) {
				t.Fatal("framed columns changed the dialect column-length error", expectedErr, err)
			}
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("invalid columns retained their output owner")
			}
		})
	}
}

type scaleP21SW238CancelContext struct {
	context.Context
	cancel context.CancelFunc
	after  int64
	calls  atomic.Int64
}

func (ctx *scaleP21SW238CancelContext) Err() error {
	if ctx.calls.Add(1) >= ctx.after {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestScaleP21SW238IssueFrameValuesFailureRelease(t *testing.T) {
	for _, mode := range []string{"cancel-before", "cancel-during", "input-io", "short-input", "denial", "late-denial"} {
		t.Run(mode, func(t *testing.T) {
			repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
			issue := scaleP21SW238Issue()
			issue.Description = strings.Repeat("body ", 32<<10)
			input, node := scaleP21SW238Frames(t, repo, issue, false)
			output, err := newMetadataLogicalBuilder(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			_, parentRelease, err := repo.tryAdmitAdditionalMetadataWork(t.Context(), 4096)
			if err != nil {
				t.Fatal(err)
			}
			defer parentRelease()
			baseline := repo.MetadataWorkUsedForTesting()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var callContext context.Context = ctx
			var during *scaleP21SW238CancelContext
			switch mode {
			case "cancel-before":
				cancel()
			case "cancel-during":
				during = &scaleP21SW238CancelContext{Context: ctx, cancel: cancel, after: 128}
				callContext = during
			case "input-io":
				if err := input.storage.file.Close(); err != nil {
					t.Fatal(err)
				}
			case "short-input":
				if err := input.storage.file.Truncate(node.offset + node.size - 1); err != nil {
					t.Fatal(err)
				}
			case "denial":
				repo.metadataResidency.work.limit = baseline + 1
			case "late-denial":
				_, warmRelease, warmErr := legacyIssueRecordFrameValues(t.Context(), repo, output, node, "workspace")
				if warmErr != nil || warmRelease == nil {
					t.Fatal("establish complete output ownership before late refusal", warmErr)
				}
				held := repo.MetadataWorkUsedForTesting()
				warmRelease()
				if held <= baseline || repo.MetadataWorkUsedForTesting() != baseline {
					t.Fatal("warm columns had no independent output owner")
				}
				repo.metadataResidency.work.limit = held - 1
			}
			output.ctx = callContext
			actual, release, err := legacyIssueRecordFrameValues(callContext, repo, output, node, "workspace")
			if release != nil {
				defer release()
			}
			if actual != nil || err == nil {
				t.Fatal("failed framed encoding exposed partial SQL arguments", mode, err)
			}
			switch mode {
			case "cancel-before", "cancel-during":
				if !errors.Is(err, context.Canceled) || during != nil && (during.calls.Load() < during.after || output.offset == 0) {
					t.Fatal("cancellation did not interrupt an entered framed operation", err)
				}
			case "input-io":
				if !errors.Is(err, os.ErrClosed) {
					t.Fatal("backing-file IO error was swallowed or reclassified", err)
				}
			case "short-input":
				if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatal("truncated frame was accepted or changed failure family", err)
				}
			case "denial", "late-denial":
				if !errors.Is(err, ErrMetadataWorkBudget) {
					t.Fatal("output admission changed its refusal", err)
				}
			}
			if release != nil {
				release()
			}
			if repo.MetadataWorkUsedForTesting() != baseline {
				t.Fatal("failure/release consumed the independent parent owner", mode)
			}
			repo.metadataResidency.work.limit = metadataWorkBytes
			fresh, err := newMetadataLogicalBuilder(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			recovered, err := fresh.writeValue(reflect.ValueOf(issue), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			expected, oracleErr := repo.issueRecordColumns("workspace", issue)
			values, done, recoveryErr := legacyIssueRecordFrameValues(t.Context(), repo, fresh, recovered, "workspace")
			if recoveryErr != nil || oracleErr != nil || done == nil {
				t.Fatal("same store did not recover after failed encoding", recoveryErr, oracleErr)
			}
			defer done()
			scaleP21SW238CompareColumns(t, expected, values)
			done()
			if repo.MetadataWorkUsedForTesting() != baseline {
				t.Fatal("same-store recovery consumed the retained parent owner")
			}
			parentRelease()
		})
	}
}

func TestScaleP21SW238IssueFrameValuesFinalCancellationDropsColumns(t *testing.T) {
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	_, node := scaleP21SW238Frames(t, repo, domain.Issue{}, false)
	run := func(ctx context.Context) ([]any, func(), error) {
		output, err := newMetadataLogicalBuilder(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer output.Close()
		output.ctx = ctx
		return legacyIssueRecordFrameValues(ctx, repo, output, node, "workspace")
	}
	measure, stopMeasure := context.WithCancel(t.Context())
	defer stopMeasure()
	counter := &scaleP21SW238CancelContext{Context: measure, cancel: stopMeasure, after: 1<<63 - 1}
	columns, warmRelease, err := run(counter)
	if err != nil || warmRelease == nil || len(columns) != 20 {
		t.Fatal("measure a completed minimal deterministic encoding", err)
	}
	last := counter.calls.Load()
	warmRelease()
	if last <= 1 || repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("completed encoding did not provide a clean final context-check witness", last)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	defer cancel()
	atLastCheck := &scaleP21SW238CancelContext{Context: cancelled, cancel: cancel, after: last}
	actual, release, err := run(atLastCheck)
	if release != nil {
		release()
	}
	if !errors.Is(err, context.Canceled) || atLastCheck.calls.Load() != last ||
		actual != nil || repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("final cancellation exposed columns after releasing their owner", err, atLastCheck.calls.Load(), last)
	}
}

func TestScaleP21SW238IssueFrameValuesUnauditedNativeTimeRefuses(t *testing.T) {
	issue := scaleP21SW238Issue()
	issue.CreatedAt = issue.CreatedAt.In(time.FixedZone("caller-owned-zone", 5*60*60+45*60))
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	if _, err := repo.issueRecordColumns("workspace", issue); err != nil {
		t.Fatal("native timestamp itself is valid; the refused input is its unaudited frame", err)
	}
	frames, node := scaleP21SW238Frames(t, repo, issue, false)
	if !node.unavailable {
		t.Fatal("unaudited location fixture no longer creates a wire-only native frame")
	}
	actual, release, err := legacyIssueRecordFrameValues(t.Context(), repo, frames, node, "workspace")
	if release != nil {
		release()
	}
	if !errors.Is(err, ErrBoundedMetadataCoverage) || actual != nil || repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("unaudited native timestamp was admitted or leaked output ownership", err)
	}
}

func scaleP21SW238EditorIssue(t *testing.T) (domain.Issue, int) {
	t.Helper()
	body := strings.Repeat("x", 4<<20)
	document := map[string]any{
		"type": "doc", "content": []any{
			map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": body}}},
		},
	}
	rawDocument, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	doc := ygo.NewDocWithOptions(ygo.Options{ClientID: 238})
	fragment := ygo.NewXmlFragment(doc, "prosemirror")
	tx := doc.WriteTxn()
	paragraph := fragment.InsertXmlElement(tx, 0, "paragraph")
	if err := paragraph.InsertXmlText(tx, 0).Insert(tx, 0, body); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	state, descriptionState := base64.StdEncoding.EncodeToString(ygo.EncodeStateAsUpdate(doc)), string(rawDocument)
	request, err := json.Marshal(domain.IssueUpdateInput{
		Description: &body, DescriptionState: &descriptionState, ContentState: &state,
	})
	if err != nil || len(request) >= 16<<20 {
		t.Fatal("supported optional-field editor request exceeds its HTTP bound", len(request), err)
	}
	issue := scaleP21SW238Issue()
	issue.Description, issue.DescriptionState = body, descriptionState
	issue.DocumentContent.Content, issue.DocumentContent.ContentState = body, state
	issue.DocumentContent.ContentData = nil
	return issue, len(request)
}

func TestScaleP21SW238IssueFrameValuesLargeEditorNoNativeBody(t *testing.T) {
	issue, requestBytes := scaleP21SW238EditorIssue(t)
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	expected, err := repo.issueRecordColumns("workspace", issue)
	if err != nil || len(expected[19].([]byte)) <= 17<<20 {
		t.Fatal("supported editor issue is not the approximately 18-MiB row witness", err)
	}
	frames, node := scaleP21SW238Frames(t, repo, issue, false)
	// The frame bytes remain valid. A bounded full-root Value call and any
	// bounded native body Value call must fail under these poisoned receipts.
	node.cost = uint64(metadataWorkBytes) + 1
	if _, err := node.Value(t.Context(), metadataWorkBytes); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("whole-Issue native decode witness did not refuse", err)
	}
	poisoned := 0
	if err := node.Children(t.Context(), func(key reflect.Value, child metadataLogicalNode) error {
		if key.String() != "Description" && key.String() != "DescriptionState" && key.String() != "DocumentContent" {
			return nil
		}
		child.cost = uint64(metadataWorkBytes) + 1
		var cost [8]byte
		binary.LittleEndian.PutUint64(cost[:], child.cost)
		if _, err := frames.storage.file.WriteAt(cost[:], child.offset+8); err != nil {
			return err
		}
		if _, err := child.Value(t.Context(), metadataWorkBytes); !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("native body decode witness did not refuse", key.String(), err)
		}
		poisoned++
		return nil
	}); err != nil || poisoned != 3 {
		t.Fatal("failed to establish all editor-body frame witnesses", poisoned, err)
	}
	actual, release, err := legacyIssueRecordFrameValues(t.Context(), repo, frames, node, "workspace")
	if err != nil || release == nil {
		t.Fatal("supported large editor columns must succeed without native body decoding", err)
	}
	defer release()
	scaleP21SW238CompareColumns(t, expected, actual)
	held := repo.MetadataWorkUsedForTesting()
	if held < len(actual[18].([]byte))+len(actual[19].([]byte)) {
		t.Fatal("large returned SQL argument bytes lost their lease", held)
	}
	frames.Close()
	scaleP21SW238CompareColumns(t, expected, actual)
	release()
	release()
	if repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("large editor argument release leaked admission")
	}
	t.Logf("supported_request_bytes=%d detail_bytes=%d list_bytes=%d held_work=%d",
		requestBytes, len(expected[19].([]byte)), len(expected[18].([]byte)), held)
}
