package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21AskStatusProjection(data domain.Bootstrap, id string) (found bool, status, note string) {
	for _, ask := range data.Asks {
		if ask.ID != id {
			continue
		}
		found, status = true, ask.Status
		if status == "rejected" {
			for index := len(ask.Approvals) - 1; index >= 0; index-- {
				if ask.Approvals[index].Decision == "rejected" {
					note = ask.Approvals[index].Note
					break
				}
			}
		}
		break
	}
	return
}

func scaleP21AskStatusEqual(t *testing.T, read *AskStatusRead, data domain.Bootstrap, id string) {
	t.Helper()
	found, status, note := scaleP21AskStatusProjection(data, id)
	if read.AskFound != found || read.Status != status || read.Note != note {
		t.Fatal("first Ask status or final rejected note changed", id, read.AskFound, read.Status)
	}
}

func TestScaleP21AskStatusNativeOwnership(t *testing.T) {
	large := strings.Repeat("unused body and note ", 50000)
	data := domain.Bootstrap{Asks: []domain.Ask{
		{ID: "duplicate", Status: "accepted", Body: large, Approvals: []domain.AskApproval{{Decision: "rejected", Note: large}}},
		{ID: "duplicate", Status: "rejected", Approvals: []domain.AskApproval{{Decision: "rejected", Note: "wrong later Ask"}}},
		{ID: "", Status: "rejected", Approvals: []domain.AskApproval{{Decision: "rejected", Note: "empty ID"}}},
		{ID: "", Status: "accepted"},
		{ID: "ask\x00id", Status: "rejected", Body: large, Approvals: []domain.AskApproval{
			{Decision: "approved", Note: large}, {Decision: "rejected", Note: large},
			{Decision: "rejected", Note: "\t\u2003Final note \x00 "}, {Decision: "REJECTED", Note: large}, {Decision: "approved", Note: large},
		}},
		{ID: "empty-note", Status: "rejected", Approvals: []domain.AskApproval{{Decision: "rejected", Note: large}, {Decision: "rejected"}}},
		{ID: "nil-approvals", Status: "rejected"},
		{ID: "empty-approvals", Status: "rejected", Approvals: []domain.AskApproval{}},
		{ID: "case-sensitive", Status: "Rejected", Approvals: []domain.AskApproval{{Decision: "rejected", Note: large}}},
	}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	view.lastWorkspaceKey = "physical"
	for _, id := range []string{"duplicate", "", "ask\x00id", "empty-note", "nil-approvals", "empty-approvals", "case-sensitive", "missing"} {
		for _, key := range []string{"physical", ""} {
			statements := 0
			read, found, err := view.BeginAskStatusRead(WithSQLStatementObserver(t.Context(), func(string) { statements++ }), key, id)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			scaleP21AskStatusEqual(t, read, data, id)
			if statements != 0 || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used == 0 {
				t.Fatal("status output hydrated metadata or lost ownership", statements)
			}
			read.Note = "caller replacement"
			read.Close()
			read.Close()
			if read.AskFound || read.Status != "" || read.Note != "" || view.metadataResidency.work.used != 0 {
				t.Fatal("status close did not clear and release")
			}
		}
	}
	for _, key := range []string{"missing", "physical "} {
		if read, found, err := view.BeginAskStatusRead(t.Context(), key, ""); err != nil || found || read != nil {
			t.Fatal("missing workspace changed", key, found, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, _, err := view.BeginAskStatusRead(cancelled, "", "ask\x00id"); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("status cancellation ignored", err)
	}
	limit := view.metadataResidency.work.limit
	for _, size := range []int{1, 128<<10 + 1024} {
		view.metadataResidency.work.limit = size
		if read, _, err := view.BeginAskStatusRead(t.Context(), "", "ask\x00id"); read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != 0 {
			t.Fatal("partial status read escaped budget or leaked", size, err)
		}
	}
	view.metadataResidency.work.limit = limit
	retained, cancel := context.WithCancel(t.Context())
	read, found, err := view.BeginAskStatusRead(retained, "", "ask\x00id")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	cancel()
	if _, err := json.Marshal([]string{read.Status, read.Note}); err != nil || view.metadataResidency.work.used == 0 {
		t.Fatal("cancellation freed output before encoding", err)
	}
	read.Close()
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range 3 {
				read, found, err := view.BeginAskStatusRead(t.Context(), "", "ask\x00id")
				if err != nil || !found {
					t.Error(found, err)
					return
				}
				scaleP21AskStatusEqual(t, read, data, "ask\x00id")
				read.Close()
			}
		})
	}
	readers.Wait()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("concurrent status reads leaked")
	}
}

func TestScaleP21AskStatusSelectedNoteBudget(t *testing.T) {
	note := strings.Repeat("complete selected note ", 15000)
	data := domain.Bootstrap{Asks: []domain.Ask{{ID: "large", Status: "rejected", Approvals: []domain.AskApproval{{Decision: "rejected", Note: note}}}}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	read, found, err := view.BeginAskStatusRead(t.Context(), "physical", "large")
	if err != nil || !found || read.Note != note {
		t.Fatal("complete large selected note changed", found, err)
	}
	held := view.metadataResidency.work.used
	read.Close()
	view.metadataResidency.work.limit = held - 1
	if read, _, err := view.BeginAskStatusRead(t.Context(), "physical", "large"); read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != 0 {
		t.Fatal("selected note escaped dynamic admission or leaked", err)
	}
}

func TestScaleP21AskStatusWarmReloadAndPin(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, askID := f.data.Workspace.URLKey, "ask-status-source"
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	seed := func(size int, replacement bool) {
		t.Helper()
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "asks"), workspace, "scale_p21.ask_status", "", nil, func(data *domain.Bootstrap) error {
			unused := strings.Repeat("unused body or approval ", size)
			data.Asks = []domain.Ask{
				{ID: askID + "-unrelated", Body: unused, Approvals: []domain.AskApproval{{Decision: "rejected", Note: unused}}},
				{ID: askID, Status: "rejected", Body: unused, Requester: domain.User{JobTitle: unused}, Approvals: []domain.AskApproval{
					{Decision: "rejected", Note: unused}, {Decision: "approved", Note: unused},
					{Decision: "rejected", Note: " \tExact final note\x00 "}, {Decision: "approved", Note: unused, Approver: domain.User{JobTitle: unused}},
				}},
			}
			if replacement {
				data.Asks[1].Status, data.Asks[1].Approvals[2].Note = "accepted", "replacement note"
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	expected := map[bool]int{}
	for _, size := range []int{5000, 50000} {
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
			data, _ := f.WorkspaceMetadataFields(workspace, "asks")
			read, found, err := view.BeginAskStatusRead(t.Context(), workspace, askID)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			scaleP21AskStatusEqual(t, read, data, askID)
			held := view.metadataResidency.work.used
			if expected[reload] == 0 {
				expected[reload] = held
			}
			// String framing charges 16 admitted bytes per retained byte.
			// Allow four scalar bytes across native/canonical normalization;
			// exact source-relative output is checked independently above.
			if held > expected[reload]+64 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("unused body/approval growth increased retained admission", held, expected)
			}
			read.Close()
			if view.metadataResidency.work.used != 0 {
				t.Fatal("warm/reloaded status read leaked")
			}
			t.Logf("unused_repetitions=%d reload=%t held_work=%d", size, reload, held)
		}
	}
	old, _ := f.WorkspaceMetadataFields(workspace, "asks")
	parent, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	bound, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	seed(5000, true)
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	read, found, err := view.BeginAskStatusRead(bound, workspace, askID)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	parent.Close()
	scaleP21AskStatusEqual(t, read, old, askID)
	read.Close()
	fresh, _, err := view.BeginAskStatusRead(t.Context(), workspace, askID)
	if err != nil || fresh.Status != "accepted" || fresh.Note != "" {
		t.Fatal("fresh status source did not observe replacement", err)
	}
	fresh.Close()
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("pinned status read leaked or hydrated complete metadata")
	}
}
