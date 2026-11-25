package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21ChatEventPolicyProjection(data domain.Bootstrap, request ChatEventPolicyRequest) (bool, string, []ChatEventTemplateHeader) {
	found, issueID := false, ""
	if request.AskID != nil {
		for _, ask := range data.Asks {
			if ask.ID == *request.AskID {
				found, issueID = true, ask.IssueID
				break
			}
		}
	}
	var templates []ChatEventTemplateHeader
	for _, template := range data.IssueTemplates {
		if slices.Contains(request.TemplateIDs, template.ID) {
			templates = append(templates, ChatEventTemplateHeader{ID: template.ID, TemplateType: template.TemplateType, TeamID: template.TeamID, VisibilityTeamID: template.VisibilityTeamID, ProjectID: template.ProjectID, FormFields: len(template.FormFields)})
		}
	}
	return found, issueID, templates
}

func scaleP21ChatEventPolicyEqual(t *testing.T, read *ChatEventPolicyRead, data domain.Bootstrap, request ChatEventPolicyRequest) {
	t.Helper()
	found, issueID, templates := scaleP21ChatEventPolicyProjection(data, request)
	if read.AskFound != found || read.AskIssueID != issueID || !reflect.DeepEqual(read.Templates, templates) {
		t.Fatal("installed event policy projection changed", read.AskFound, read.AskIssueID, read.Templates)
	}
}

func TestScaleP21ChatEventPolicyNativeOwnership(t *testing.T) {
	data := domain.Bootstrap{
		Asks: []domain.Ask{
			{ID: "duplicate", IssueID: ""}, {ID: "duplicate", IssueID: "wrong-later"},
			{ID: "", IssueID: "empty-first"}, {ID: "", IssueID: "empty-later"},
			{ID: "ask\x00id", IssueID: "issue\x00id"},
		},
		IssueTemplates: []domain.IssueTemplate{
			{ID: "duplicate", TemplateType: "issue", TeamID: "team"},
			{ID: "unselected", TemplateType: "form"},
			{ID: "duplicate", VisibilityTeamID: "private", FormFields: []domain.TemplateFormField{{}, {}}},
			{ID: "", ProjectID: "project"},
			{ID: "template\x00id", TemplateType: "form"},
		},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	view.lastWorkspaceKey = "physical"
	for _, id := range []string{"duplicate", "", "ask\x00id", "missing"} {
		request := ChatEventPolicyRequest{AskID: &id, TemplateIDs: []string{"duplicate", "", "template\x00id", "duplicate"}}
		for _, key := range []string{"physical", ""} {
			statements := 0
			read, found, err := view.BeginChatEventPolicyRead(WithSQLStatementObserver(t.Context(), func(string) { statements++ }), key, request)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			scaleP21ChatEventPolicyEqual(t, read, data, request)
			if statements != 0 || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used == 0 {
				t.Fatal("event headers hydrated metadata or lost ownership", statements)
			}
			read.Templates[0].TeamID = "caller mutation"
			read.Close()
			read.Close()
			if read.AskFound || read.AskIssueID != "" || read.Templates != nil || view.metadataResidency.work.used != 0 {
				t.Fatal("close leaked event headers")
			}
		}
	}
	read, _, err := view.BeginChatEventPolicyRead(t.Context(), "", ChatEventPolicyRequest{})
	if err != nil || read.AskFound || read.Templates != nil {
		t.Fatal("absent selections loaded fields", err)
	}
	read.Close()
	if _, found, err := view.BeginChatEventPolicyRead(t.Context(), "missing", ChatEventPolicyRequest{}); err != nil || found {
		t.Fatal("missing workspace changed", found, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := view.BeginChatEventPolicyRead(cancelled, "", ChatEventPolicyRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	limit := view.metadataResidency.work.limit
	for _, size := range []int{1, 130 << 10} {
		view.metadataResidency.work.limit = size
		if _, _, err := view.BeginChatEventPolicyRead(t.Context(), "", ChatEventPolicyRequest{TemplateIDs: []string{"duplicate"}}); !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != 0 {
			t.Fatal("partial header read escaped budget or leaked", size, err)
		}
	}
	view.metadataResidency.work.limit = limit
	var jobs sync.WaitGroup
	for range 4 {
		jobs.Go(func() {
			request := ChatEventPolicyRequest{TemplateIDs: []string{"duplicate"}}
			for range 3 {
				read, found, err := view.BeginChatEventPolicyRead(t.Context(), "", request)
				if err != nil || !found {
					t.Error(found, err)
					return
				}
				foundAsk, issueID, templates := scaleP21ChatEventPolicyProjection(data, request)
				if read.AskFound != foundAsk || read.AskIssueID != issueID || !reflect.DeepEqual(read.Templates, templates) {
					t.Error("concurrent event policy projection changed")
				}
				read.Close()
			}
		})
	}
	jobs.Wait()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("concurrent event header reads leaked")
	}
}

func TestScaleP21ChatEventPolicyWarmReloadAndPin(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, askID := f.data.Workspace.URLKey, "event-policy-ask"
	request := ChatEventPolicyRequest{AskID: &askID, TemplateIDs: []string{"event-policy-template"}}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	seed := func(size int, changed bool) {
		t.Helper()
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "asks", "issueTemplates"), workspace, "scale_p21.chat_event_policy", "", nil, func(data *domain.Bootstrap) error {
			data.Asks = []domain.Ask{{ID: askID, IssueID: "event-policy-issue", Title: strings.Repeat("unused ask title ", size), Body: strings.Repeat("unused ask body ", size), Approvals: []domain.AskApproval{{Note: strings.Repeat("unused approval ", size)}}}}
			data.IssueTemplates = []domain.IssueTemplate{{ID: "event-policy-template", TeamID: "event-policy-team", TemplateType: "issue", Body: strings.Repeat("unused template body ", size), FormFields: []domain.TemplateFormField{{Label: strings.Repeat("unused field label ", size)}, {Options: []string{strings.Repeat("unused field option ", size)}}}}}
			if changed {
				data.Asks[0].IssueID = "replacement-issue"
				data.IssueTemplates[0].TemplateType = "form"
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	expectedWork := map[bool]int{}
	for _, size := range []int{10000, 100000} {
		seed(size, false)
		for _, reload := range []bool{false, true} {
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			data, _ := f.WorkspaceMetadataFields(workspace, "asks", "issueTemplates")
			read, found, err := view.BeginChatEventPolicyRead(t.Context(), workspace, request)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			scaleP21ChatEventPolicyEqual(t, read, data, request)
			held := view.metadataResidency.work.used
			if expectedWork[reload] == 0 {
				expectedWork[reload] = held
			}
			if held != expectedWork[reload] || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("unused bodies increased event header admission", held, expectedWork)
			}
			read.Close()
			if view.metadataResidency.work.used != 0 {
				t.Fatal("warm/reloaded event policy leaked")
			}
			t.Logf("body_repetitions=%d reload=%t held_work=%d", size, reload, held)
		}
	}
	old, _ := f.WorkspaceMetadataFields(workspace, "asks", "issueTemplates")
	parent, _, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	seed(10000, true)
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	read, found, err := view.BeginChatEventPolicyRead(bound, workspace, request)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	parent.Close()
	scaleP21ChatEventPolicyEqual(t, read, old, request)
	read.Close()
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("pinned child leaked event policy work")
	}
}
