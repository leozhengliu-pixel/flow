package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"

	"github.com/Deln0r/ygo"
)

func scaleP21SW232ActionFixture(t *testing.T, size int) domain.Bootstrap {
	t.Helper()
	body := strings.Repeat("x", size)
	document := map[string]any{
		"type": "doc", "content": []any{
			map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": body}}},
		},
	}
	documentJSON, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	doc := ygo.NewDocWithOptions(ygo.Options{ClientID: 232})
	fragment := ygo.NewXmlFragment(doc, "prosemirror")
	tx := doc.WriteTxn()
	paragraph := fragment.InsertXmlElement(tx, 0, "paragraph")
	if err := paragraph.InsertXmlText(tx, 0).Insert(tx, 0, body); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	state := base64.StdEncoding.EncodeToString(ygo.EncodeStateAsUpdate(doc))
	at := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	before := domain.User{ID: "before", Name: "Original embedded snapshot"}
	after := domain.User{ID: "after", Name: "Assigned user"}
	label := domain.IssueLabel{ID: "new-label", Name: "New label", Color: "green"}
	return domain.Bootstrap{
		Workspace: domain.Workspace{ID: "sw232-workspace"},
		Viewer:    domain.User{ID: "viewer"}, ViewerRole: "member",
		Users: []domain.User{{ID: "viewer"}, after}, Labels: []domain.IssueLabel{label},
		Teams:  []domain.Team{{ID: "team"}},
		States: []domain.WorkflowState{{ID: "state", TeamID: "team", Type: "started"}},
		Issues: []domain.Issue{
			{ID: "other", Number: 9, Title: "Earlier unrelated issue"},
			{
				ID: "target", Number: 17, Title: "Unloaded title", Description: body,
				DescriptionState: string(documentJSON), Priority: 4, PriorityLabel: "Low", Assignee: &before,
				Labels: []domain.IssueLabel{{ID: "old-label", Name: "Existing label", Color: "red"}},
				DocumentContent: &domain.DocumentContent{
					ID: "document_content_target", Version: 1, Content: body, ContentState: state, UpdatedAt: at,
				},
				Team: domain.Team{ID: "team"}, State: domain.WorkflowState{ID: "state"}, UpdatedAt: at,
			},
			{ID: "target", Number: 40, Title: "Later duplicate stays unchanged", Description: "later body"},
			{ID: "last", Number: 99, Title: "Number fold includes unselected rows"},
		},
		NextIssueNumber: 100,
		WorkflowDefinitions: []domain.WorkflowDefinition{{
			ID: "workflow", Enabled: true, CreatorID: "viewer",
			Actions: []domain.WorkflowAction{
				{Type: "setIssuePriority", Config: map[string]string{"priority": "1"}},
				{Type: "assignIssue", Config: map[string]string{"userId": "after"}},
				{Type: "addIssueLabel", Config: map[string]string{"labelId": "new-label"}},
			},
		}},
	}
}

func TestScaleP21SW232WorkflowActionFieldsKeepEditorFrames(t *testing.T) {
	bounds := map[string]int{}
	for _, size := range []int{32, 4 << 20} {
		data := scaleP21SW232ActionFixture(t, size)
		repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
		source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
		if err != nil {
			t.Fatal(err)
		}
		defer source.Release()
		for _, action := range []string{"priority", "assignee", "label", "detached-after-prepend"} {
			t.Run(fmt.Sprintf("body-%d/%s", size, action), func(t *testing.T) {
				inputs, err := repo.loadWorkflowRunInputs(t.Context(), source, "workflow", "issue", "target")
				if err != nil {
					t.Fatal("legal editor body entered action input decoding", err)
				}
				defer inputs.close()
				if inputs.resourceOrdinal != 1 || inputs.placeholders != 1 ||
					len(inputs.data.Issues) != 2 || inputs.data.Issues[0].Number != 17 ||
					scaleP21SW201NextIssueNumber(inputs.data.Issues) != scaleP21SW201NextIssueNumber(data.Issues) {
					t.Fatal("original ordinal, first duplicate or number-only placeholders changed")
				}
				for _, view := range []domain.Bootstrap{inputs.data, inputs.before} {
					if view.Issues[0].Title != "" || view.Issues[0].Description != "" ||
						view.Issues[0].DescriptionState != "" || view.Issues[0].DocumentContent != nil {
						t.Fatal("unread editor/body fields entered action-owned native data")
					}
				}
				expected := append([]domain.Issue(nil), data.Issues...)
				at := data.Issues[1].UpdatedAt.Add(time.Minute)
				switch action {
				case "priority":
					inputs.data.Issues[0].Priority, inputs.data.Issues[0].PriorityLabel = 1, "Urgent"
					inputs.data.Issues[0].UpdatedAt = at
					expected[1].Priority, expected[1].PriorityLabel, expected[1].UpdatedAt = 1, "Urgent", at
				case "assignee":
					user := data.Users[1]
					inputs.data.Issues[0].Assignee, inputs.data.Issues[0].UpdatedAt = &user, at
					expected[1].Assignee, expected[1].UpdatedAt = &user, at
				case "label":
					inputs.data.Issues[0].Labels = append(inputs.data.Issues[0].Labels, data.Labels[0])
					expected[1].Labels = append(append([]domain.IssueLabel(nil), expected[1].Labels...), data.Labels[0])
				case "detached-after-prepend":
					target := &inputs.data.Issues[0]
					created := domain.Issue{ID: "created", Number: 100, Title: "Created workflow issue"}
					inputs.data.Issues = append([]domain.Issue{created}, inputs.data.Issues...)
					// This is the literal callback's detached pointer after
					// createIssue prepends into a new backing array.
					target.Priority, target.PriorityLabel, target.UpdatedAt = 1, "Urgent", at
					expected = append([]domain.Issue{created}, expected...)
				}
				inputs.data.WorkflowRuns = append(inputs.data.WorkflowRuns, domain.WorkflowRun{
					ID: "new-run", WorkflowID: "workflow", Status: "succeeded",
				})
				image, err := inputs.image(t.Context())
				if err != nil {
					t.Fatal("closed action delta materialized the editor/body", err)
				}
				defer image.Release()
				held := repo.MetadataWorkUsedForTesting()
				if size == 32 {
					bounds[action] = held
				} else if held > bounds[action]+(64<<10) {
					t.Fatal("action native ownership grew with unrelated editor bytes", held, bounds[action])
				}
				issues, _, _ := metadataLogicalField("issues")
				// This is the test oracle after measuring production-held
				// work; the adapter above never materializes these bodies.
				actual, err := image.fields[issues].Value(t.Context(), metadataWorkBytes)
				if err != nil || !reflect.DeepEqual(actual.Interface().([]domain.Issue), expected) {
					t.Fatal("original body frames, duplicate order or closed action output changed", err)
				}
				t.Logf("body_bytes=%d description_state_bytes=%d content_state_bytes=%d held_work=%d",
					size, len(data.Issues[1].DescriptionState), len(data.Issues[1].DocumentContent.ContentState), held)
				inputs.close()
				if repo.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(inputs.before, domain.Bootstrap{}) ||
					inputs.resourceNode != (metadataLogicalNode{}) {
					t.Fatal("closed action input retained native work or its source node")
				}
			})
		}
	}
}

func TestScaleP21SW232WorkflowActionDeltaRejectsUnrelatedChanges(t *testing.T) {
	data := scaleP21SW232ActionFixture(t, 32)
	for _, field := range []string{"Title", "Description", "DescriptionState", "DocumentContent", "ID", "Number"} {
		t.Run(field, func(t *testing.T) {
			repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			inputs, err := repo.loadWorkflowRunInputs(t.Context(), source, "workflow", "issue", "target")
			if err != nil {
				t.Fatal(err)
			}
			defer inputs.close()
			issue := &inputs.data.Issues[0]
			switch field {
			case "Title":
				issue.Title = "undeclared"
			case "Description":
				issue.Description = "undeclared"
			case "DescriptionState":
				issue.DescriptionState = `{"type":"doc"}`
			case "DocumentContent":
				issue.DocumentContent = &domain.DocumentContent{ContentState: "undeclared"}
			case "ID":
				issue.ID = "undeclared"
			case "Number":
				issue.Number++
			}
			inputs.data.WorkflowRuns = append(inputs.data.WorkflowRuns, domain.WorkflowRun{ID: "new-run", WorkflowID: "workflow"})
			image, err := inputs.image(t.Context())
			if image != nil {
				image.Release()
			}
			if !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("unrelated action edit escaped the closed frame delta", field, err)
			}
			inputs.close()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("refused action edit retained native admission")
			}
		})
	}
}
