package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW241Issue() domain.Issue {
	at := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	return domain.Issue{
		ID: "sw241-issue", Version: 1, Identifier: "SW241-1", Number: 1,
		Title: "Before", Description: "Retained body", Priority: 4, PriorityLabel: "Low",
		CreatedAt: at, UpdatedAt: at,
		Team:     domain.Team{ID: "team", Name: "Stored team"},
		State:    domain.WorkflowState{ID: "state", TeamID: "team", Name: "Stored state", Type: "started"},
		Creator:  domain.User{ID: "person", Name: "Stored creator"},
		Assignee: &domain.User{ID: "assignee", Name: "Stored assignee"},
		Delegate: &domain.User{ID: "delegate", Name: "Stored delegate"},
		Project:  &domain.ProjectSummary{ID: "project", Name: "Stored project"},
		Labels:   []domain.IssueLabel{{ID: "label", Name: "Stored label", IssueCount: 17}},
	}
}

func scaleP21SW241JSON(t *testing.T, issue domain.Issue) []byte {
	t.Helper()
	raw, err := json.Marshal(issue)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func scaleP21SW241Oracle(t *testing.T, previous []byte, next domain.Issue, data domain.Bootstrap) bool {
	t.Helper()
	values, err := issueRecordValues("", next)
	if err != nil {
		t.Fatal("valid native Issue oracle failed", err)
	}
	return equalIssueRecordData(previous, values[len(values)-1].([]byte)) ||
		newIssueReferences(data).equalOwned(previous, next)
}

func scaleP21SW241Frames(t *testing.T, repo *SQLiteStore, next domain.Issue, data domain.Bootstrap) (*metadataLogicalBuilder, metadataLogicalNode, *metadataLogicalBuilder, *metadataLogicalSource) {
	t.Helper()
	disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
	input, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	output, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		input.Close()
		t.Fatal(err)
	}
	node, err := input.writeValue(reflect.ValueOf(next), 0, false)
	if err != nil {
		input.Close()
		output.Close()
		t.Fatal(err)
	}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		input.Close()
		output.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		source.Release()
		output.Close()
		output.Close()
		input.Close()
		input.Close()
		if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 ||
			metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors ||
			recordSpoolBytes.Load() != spools {
			t.Error("Issue frame comparison retained work/disk/descriptors/spools or decoded whole metadata")
		}
	})
	return input, node, output, source
}

func TestScaleP21SW241IssueFrameCompareDifferential(t *testing.T) {
	type cell struct {
		name     string
		previous []byte
		next     domain.Issue
		data     domain.Bootstrap
		want     bool
	}
	base := scaleP21SW241Issue()
	cells := []cell{
		{name: "absent-preimage", next: base},
		{name: "missing-defaults", previous: []byte(`{}`), want: true},
		{name: "null-preimage-defaults", previous: []byte(`null`), want: true},
		{name: "valid-native-row", previous: scaleP21SW241JSON(t, base), next: base, want: true},
	}
	values, err := issueRecordValues("", base)
	if err != nil {
		t.Fatal(err)
	}
	cells = append(cells, cell{name: "literal-raw-equal", previous: bytes.Clone(values[len(values)-1].([]byte)), next: base, want: true})
	add := func(name string, old, next domain.Issue, data domain.Bootstrap, want bool) {
		cells = append(cells, cell{name, scaleP21SW241JSON(t, old), next, cloneBootstrap(data), want})
	}
	normalized := base
	normalized.AgentSessionState = "active"
	normalized.Labels = []domain.IssueLabel{{ID: "label", Name: "Stored label", IssueCount: 123}}
	add("read-decorations-and-label-counts", normalized, base, domain.Bootstrap{}, true)
	defaulted, empty := domain.Issue{}, domain.Issue{
		Version: 1, Labels: []domain.IssueLabel{}, Reactions: map[string][]string{},
		SubscriberIDs: []string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{},
	}
	add("nil-empty-and-zero-version-defaults", defaulted, empty, domain.Bootstrap{}, true)
	changed := base
	changed.Title = "After!"
	add("changed-title", base, changed, domain.Bootstrap{}, false)
	changed = base
	changed.Version = 2
	add("changed-version", base, changed, domain.Bootstrap{}, false)
	directory := domain.Bootstrap{
		Teams:  []domain.Team{{ID: "team", Name: "Directory team"}},
		States: []domain.WorkflowState{{ID: "state", TeamID: "team", Name: "Directory state", Type: "started"}},
		Users: []domain.User{
			{ID: "person", Name: "Directory creator"}, {ID: "assignee", Name: "Directory assignee"}, {ID: "delegate", Name: "Directory delegate"},
		},
		Projects: []domain.Project{{ID: "project", Name: "Directory project"}},
		Labels:   []domain.IssueLabel{{ID: "label", Name: "Directory label", IssueCount: 99}},
	}
	refChanged := base
	refChanged.Team.Name, refChanged.State.Name, refChanged.Creator.Name = "New team snapshot", "New state snapshot", "New creator snapshot"
	refChanged.Assignee = &domain.User{ID: "assignee", Name: "New assignee snapshot"}
	refChanged.Delegate = &domain.User{ID: "delegate", Name: "New delegate snapshot"}
	refChanged.Project = &domain.ProjectSummary{ID: "project", Name: "New project snapshot"}
	refChanged.Labels = []domain.IssueLabel{{ID: "label", Name: "New label snapshot", IssueCount: 7}}
	add("all-directory-reference-displays-refresh", base, refChanged, directory, true)
	add("missing-directory-preserves-reference-differences", base, refChanged, domain.Bootstrap{}, false)
	typeChanged := refChanged
	typeChanged.State.Type = "completed"
	add("state-type-difference-is-indexed-before-refresh", base, typeChanged, directory, false)
	oldAlias, nextAlias := base, base
	oldAlias.Creator = domain.User{ID: "alias\x00", Name: "Stored old"}
	nextAlias.Creator = domain.User{ID: "alias", Name: "Stored new"}
	duplicateDirectory := domain.Bootstrap{Users: []domain.User{
		{ID: "alias\x00", Name: "First loses"}, {ID: "alias\x00", Name: "Last wins"}, {ID: "alias", Name: "Last wins"},
	}}
	add("last-duplicate-directory-wins-over-native-NUL-alias", oldAlias, nextAlias, duplicateDirectory, true)
	duplicateDirectory.Users[1].Name = "Last is different"
	add("last-duplicate-directory-difference-is-visible", oldAlias, nextAlias, duplicateDirectory, false)
	changed = base
	changed.Labels = []domain.IssueLabel{{ID: "label", Name: "Stored label", IssueCount: -10}, {ID: "label", Name: "Stored label"}}
	add("duplicate-issue-label-occurrences-not-deduplicated", base, changed, directory, false)
	for _, pair := range []struct {
		name, old, next string
		want            bool
	}{
		{"description-body-equal", "body <>&", "body <>&", true},
		{"description-body-changed", "before", "after", false},
		{"native-NUL-is-stored-lossily", "before\x00after", "beforeafter", true},
		{"literal-backslash-u0000-is-retained", `before\u0000after`, "beforeafter", false},
		{"invalid-UTF8-matches-standard-replacement", string([]byte{'x', 255, 'y'}), "x\ufffdy", true},
	} {
		old, next := base, base
		old.Description, next.Description = pair.old, pair.next
		add(pair.name, old, next, domain.Bootstrap{}, pair.want)
	}
	for _, pair := range []struct {
		name      string
		old, next *domain.DocumentContent
		want      bool
	}{
		{"nil-document-versus-empty-pointer", nil, &domain.DocumentContent{}, false},
		{"document-body-changed", &domain.DocumentContent{Content: "before"}, &domain.DocumentContent{Content: "after"}, false},
		{"document-state-changed", &domain.DocumentContent{ContentState: "state-a"}, &domain.DocumentContent{ContentState: "state-b"}, false},
		{"document-native-NUL-is-stored-lossily", &domain.DocumentContent{Content: "a\x00b"}, &domain.DocumentContent{Content: "ab"}, true},
		{"document-content-data-NUL-value-is-stored-lossily", &domain.DocumentContent{ContentData: map[string]any{"key": "a\x00b"}}, &domain.DocumentContent{ContentData: map[string]any{"key": "ab"}}, true},
		{"document-content-data-literal-backslash-u0000-is-retained", &domain.DocumentContent{ContentData: map[string]any{"key": `a\u0000b`}}, &domain.DocumentContent{ContentData: map[string]any{"key": "ab"}}, false},
		{"document-content-data-NUL-key-is-stored-lossily", &domain.DocumentContent{ContentData: map[string]any{"nul\x00key": 1.}}, &domain.DocumentContent{ContentData: map[string]any{"nulkey": 1.}}, true},
		{"document-content-data-nil-empty-remains-distinct", &domain.DocumentContent{}, &domain.DocumentContent{ContentData: map[string]any{}}, false},
		{"document-content-data-shape", &domain.DocumentContent{ContentData: map[string]any{"items": []any{}}}, &domain.DocumentContent{ContentData: map[string]any{"items": nil}}, false},
		{"document-content-data-map-order", &domain.DocumentContent{ContentData: map[string]any{"a": 1., "b": true}}, &domain.DocumentContent{ContentData: map[string]any{"b": true, "a": 1.}}, true},
		{"raw-json-producer-generic-rounding-is-literal",
			&domain.DocumentContent{ContentData: map[string]any{"n": json.RawMessage(`9007199254740993`)}},
			&domain.DocumentContent{ContentData: map[string]any{"n": json.RawMessage(`9007199254740993`)}}, false},
	} {
		old, next := base, base
		old.DocumentContent, next.DocumentContent = pair.old, pair.next
		add(pair.name, old, next, domain.Bootstrap{}, pair.want)
	}
	rawProducer := base
	rawProducer.DocumentContent = &domain.DocumentContent{ContentData: map[string]any{"n": json.RawMessage(`9007199254740993`)}}
	values, err = issueRecordValues("", rawProducer)
	if err != nil {
		t.Fatal(err)
	}
	cells = append(cells, cell{name: "raw-equal-precedes-generic-number-normalization",
		previous: bytes.Clone(values[len(values)-1].([]byte)), next: rawProducer, want: true})
	for _, test := range cells {
		t.Run(test.name, func(t *testing.T) {
			if oracle := scaleP21SW241Oracle(t, test.previous, test.next, test.data); oracle != test.want {
				t.Fatal("fixture did not establish its literal equality branch", oracle, test.want)
			}
			repo := &SQLiteStore{metadataResidency: newMetadataResidency(), maxStateBytes: 1 << 20}
			_, node, frames, source := scaleP21SW241Frames(t, repo, test.next, test.data)
			got, err := legacyIssueRecordFrameEqual(t.Context(), repo, frames,
				legacySessionJSONPart{bytes.NewReader(test.previous), 0, int64(len(test.previous))}, node, source)
			if err != nil || got != test.want {
				t.Fatal("framed equality differs from exact valid-preimage oracle", got, test.want, err)
			}
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("completed framed equality retained its temporary work owner")
			}
		})
	}
}

func TestScaleP21SW241IssueFrameCompareAboveWorkspaceRootCap(t *testing.T) {
	before := scaleP21SW241Issue()
	before.Description = strings.Repeat("x", 2<<20)
	previous := scaleP21SW241JSON(t, before)
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "changed"}[changed], func(t *testing.T) {
			next := before
			if changed {
				next.Description = "y" + next.Description[1:]
			}
			repo := &SQLiteStore{metadataResidency: newMetadataResidency(), maxStateBytes: 1 << 20}
			_, node, frames, source := scaleP21SW241Frames(t, repo, next, domain.Bootstrap{})
			got, err := legacyIssueRecordFrameEqual(t.Context(), repo, frames,
				legacySessionJSONPart{bytes.NewReader(previous), 0, int64(len(previous))}, node, source)
			want := scaleP21SW241Oracle(t, previous, next, domain.Bootstrap{})
			if len(previous) <= repo.maxStateBytes || want == changed || err != nil || got != want {
				t.Fatal("valid Issue row was capped by workspace-root bytes or changed equality", len(previous), got, want, err)
			}
		})
	}
}

func TestScaleP21SW241IssueFrameCompareLargeEditor(t *testing.T) {
	raw, requestBytes, stateBytes := scaleP21SW234EditorProducerRow(t)
	var before domain.Issue
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	values, err := issueRecordValues("", before)
	if err != nil {
		t.Fatal(err)
	}
	previous := values[len(values)-1].([]byte)
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "one-byte-changed"}[changed], func(t *testing.T) {
			next := before
			if changed {
				next.Description = "y" + next.Description[1:]
			}
			repo := &SQLiteStore{metadataResidency: newMetadataResidency(), maxStateBytes: 1 << 20}
			repo.metadataResidency.work.limit = 16 << 20
			input, node, frames, source := scaleP21SW241Frames(t, repo, next, domain.Bootstrap{})
			node.cost = uint64(metadataWorkBytes) + 1
			if _, err := node.Value(t.Context(), metadataWorkBytes); !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("whole native Issue decode witness did not refuse", err)
			}
			poisoned := 0
			if err := node.Children(t.Context(), func(key reflect.Value, child metadataLogicalNode) error {
				if key.String() != "Description" && key.String() != "DescriptionState" && key.String() != "DocumentContent" {
					return nil
				}
				child.cost = uint64(metadataWorkBytes) + 1
				var cost [8]byte
				binary.LittleEndian.PutUint64(cost[:], child.cost)
				if _, err := input.storage.file.WriteAt(cost[:], child.offset+8); err != nil {
					return err
				}
				if _, err := child.Value(t.Context(), metadataWorkBytes); !errors.Is(err, ErrMetadataWorkBudget) {
					t.Fatal("native body decode witness did not refuse", key.String(), err)
				}
				poisoned++
				return nil
			}); err != nil || poisoned != 3 {
				t.Fatal("large editor did not retain three framed-body witnesses", poisoned, err)
			}
			want := scaleP21SW241Oracle(t, previous, next, domain.Bootstrap{})
			got, err := legacyIssueRecordFrameEqual(t.Context(), repo, frames,
				legacySessionJSONPart{bytes.NewReader(previous), 0, int64(len(previous))}, node, source)
			if len(previous) <= 17<<20 || requestBytes >= 16<<20 || want == changed || err != nil || got != want {
				t.Fatal("real legal editor equality refused or differs from literal", len(previous), requestBytes, stateBytes, got, want, err)
			}
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("large editor comparison retained work")
			}
		})
	}
}

func TestScaleP21SW241IssueFrameCompareFailureAndRelease(t *testing.T) {
	for _, mode := range []string{"cancel-before", "cancel-during-preimage", "denial-and-recovery", "closed-input", "closed-output", "closed-directory-source"} {
		t.Run(mode, func(t *testing.T) {
			before, next := scaleP21SW241Issue(), scaleP21SW241Issue()
			before.Description, next.Description = strings.Repeat("x", 128<<10), strings.Repeat("x", 128<<10)
			next.Creator.Name = "New display"
			data := domain.Bootstrap{Users: []domain.User{{ID: "person", Name: "Current display"}}}
			previous := scaleP21SW241JSON(t, before)
			if !scaleP21SW241Oracle(t, previous, next, data) {
				t.Fatal("failure fixture must require directory equality")
			}
			repo := &SQLiteStore{metadataResidency: newMetadataResidency(), maxStateBytes: 1 << 20}
			input, node, output, source := scaleP21SW241Frames(t, repo, next, data)
			_, releaseParent, err := repo.tryAdmitAdditionalMetadataWork(t.Context(), 4096)
			if err != nil {
				t.Fatal(err)
			}
			defer releaseParent()
			parent := repo.MetadataWorkUsedForTesting()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			part := legacySessionJSONPart{bytes.NewReader(previous), 0, int64(len(previous))}
			reads := 0
			switch mode {
			case "cancel-before":
				cancel()
			case "cancel-during-preimage":
				backing := bytes.NewReader(previous)
				part.input = scaleP21SW234ReaderAt(func(dst []byte, offset int64) (int, error) {
					count, err := backing.ReadAt(dst, offset)
					reads++
					cancel()
					return count, err
				})
			case "denial-and-recovery":
				repo.metadataResidency.work.limit = parent + logicalJSONStreamWork - 1
			case "closed-input":
				input.Close()
			case "closed-output":
				output.Close()
			case "closed-directory-source":
				source.Release()
			}
			got, err := legacyIssueRecordFrameEqual(ctx, repo, output, part, node, source)
			if err == nil || got || repo.MetadataWorkUsedForTesting() != parent {
				t.Fatal("failed comparison exposed equality or released the independent parent", got, err, repo.MetadataWorkUsedForTesting(), parent)
			}
			if strings.HasPrefix(mode, "cancel-") && !errors.Is(err, context.Canceled) ||
				mode == "cancel-during-preimage" && reads == 0 ||
				mode == "denial-and-recovery" && !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("failure occurred at the wrong boundary", mode, reads, err)
			}
			if mode == "denial-and-recovery" {
				repo.metadataResidency.work.limit = metadataWorkBytes
				got, err = legacyIssueRecordFrameEqual(t.Context(), repo, output, part, node, source)
				if err != nil || !got || repo.MetadataWorkUsedForTesting() != parent {
					t.Fatal("same-store recovery after denial failed", got, err)
				}
			}
			releaseParent()
			releaseParent()
		})
	}
}
