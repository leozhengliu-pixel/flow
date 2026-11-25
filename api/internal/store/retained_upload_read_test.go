package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW132Gzip(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func scaleP21SW132Expected(repo *SQLiteStore, workspace string, names []string) []string {
	data, found := repo.WorkspaceMetadataFields(workspace, "trash")
	if !found {
		return slices.Clone(names)
	}
	retained := map[string]bool{}
	for _, entry := range data.Trash {
		for _, name := range AttachmentUploads(entry.Payload) {
			retained[name] = true
		}
	}
	return slices.DeleteFunc(slices.Clone(names), func(name string) bool { return retained[name] })
}

func TestScaleP21SW132RetainedUploadNativeDifferential(t *testing.T) {
	raws := [][]byte{
		nil, {}, []byte(`null`), []byte(`"/uploads/direct.pdf?token=yes#page"`),
		[]byte(`{"a":"/uploads/first.pdf","a":"/uploads/second.pdf"}`),
		[]byte(`{"escaped":"\u002fuploads\u002fescaped.pdf"}`),
		[]byte(`{"marker":"/uploads/marker.pdf","escaped":"\u002fuploads\u002fescaped.pdf"}`),
		[]byte(`{"text":"embedded /uploads/text.pdf","url":"https://example.test/uploads/absolute.pdf"}`),
		[]byte(`["/uploads/valid.pdf","/uploads/../invalid","/uploads/path/slash","/uploads/back\\slash"]`),
		[]byte(`{"pulseSummary":{"text":"/uploads/pulse.pdf"}}`),
		[]byte(`{"url":"/uploads/trailing.pdf"} trailing ignored`),
		[]byte(`{"url":"/uploads/malformed.pdf",`),
		scaleP21SW132Gzip(t, []byte(`{"url":"/uploads/gzip.pdf"}`)),
		{0x1f, 0x8b, 0},
	}
	broken := scaleP21SW132Gzip(t, []byte(`{"url":"/uploads/broken.pdf"}`))
	broken[len(broken)-1] ^= 1
	raws = append(raws, broken)
	data := domain.Bootstrap{Trash: []domain.TrashEntry{}}
	names := []string{"free.pdf", "first.pdf", "second.pdf", "direct.pdf", "escaped.pdf", "marker.pdf", "text.pdf",
		"absolute.pdf", "valid.pdf", "../invalid", "path/slash", "back\\slash", "pulse.pdf", "trailing.pdf",
		"malformed.pdf", "gzip.pdf", "broken.pdf", "", "free.pdf", "second.pdf"}
	for i, raw := range raws {
		data.Trash = append(data.Trash, domain.TrashEntry{ID: fmt.Sprint(i), Payload: raw, Title: strings.Repeat("ignored header", 500)})
	}
	literal := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"physical": data}, lastWorkspaceKey: "physical"}
	view := scaleP21HotLogoStore(t, literal.workspaces)
	view.lastWorkspaceKey = "physical"
	for _, workspace := range []string{"physical", "", "missing"} {
		for _, input := range [][]string{nil, {}, names} {
			want := scaleP21SW132Expected(literal, workspace, input)
			before := slices.Clone(input)
			read, err := view.BeginRetainedUploadRead(t.Context(), workspace, input)
			if err != nil || !reflect.DeepEqual(read.Names, want) {
				t.Fatal(workspace, read, want, err)
			}
			if !reflect.DeepEqual(input, before) {
				t.Fatal("candidate names changed")
			}
			if len(read.Names) != 0 {
				read.Names[0] = "caller mutation"
				if !reflect.DeepEqual(input, before) {
					t.Fatal("owned result aliases candidates")
				}
			}
			read.Close()
			read.Close()
			if read.Names != nil || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("owned names/work leaked")
			}
		}
	}
	if view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("full metadata decoded")
	}
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			read, err := view.BeginRetainedUploadRead(t.Context(), "", names)
			if err != nil {
				t.Error(err)
				return
			}
			defer read.Close()
			if !reflect.DeepEqual(read.Names, scaleP21SW132Expected(literal, "", names)) {
				t.Error("concurrent candidate filtering differs")
			}
		})
	}
	workers.Wait()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("concurrent filtering leaked")
	}
}

func TestScaleP21SW132RetainedUploadWarmReloadBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	names := []string{"selected.pdf", "second.pdf", "free.pdf", "free.pdf"}
	held := 0
	for _, count := range []int{20, 200} {
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "trash"), workspace, "scale_p21.sw132_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Trash = make([]domain.TrashEntry, count)
			for i := range data.Trash {
				data.Trash[i] = domain.TrashEntry{ID: fmt.Sprintf("sw132-%d", i), Title: strings.Repeat("Unselected title. ", 1000),
					Payload: json.RawMessage(`{"title":"` + strings.Repeat("one payload ", 1000) + `","url":"/uploads/selected.pdf"}`)}
			}
			data.Trash[0].Payload = json.RawMessage(`{"url":"/uploads/selected.pdf","url":"/uploads/second.pdf"}`)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			budget := view.metadataResidency.work
			limit := budget.limit
			budget.limit = 2 << 20
			queries := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
			read, err := view.BeginRetainedUploadRead(ctx, workspace, names)
			budget.limit = limit
			if err != nil {
				t.Fatal("per-row scan exceeded fixed work bound", count, reload, err)
			}
			want := scaleP21SW132Expected(f.SQLiteStore, workspace, names)
			if !reflect.DeepEqual(read.Names, want) {
				t.Fatal("warm/reloaded installed payload differs", count, reload, read.Names, want)
			}
			work := view.MetadataWorkUsedForTesting()
			if held == 0 {
				held = work
			}
			if work != held || queries != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("10x trash grew retained work or queried/hydrated SQL", count, reload, work, held, queries)
			}
			t.Log("trash rows / retained work / SQL", count, work, queries)
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("warm/reloaded scan leaked")
			}
		}
	}
	want := scaleP21SW132Expected(f.SQLiteStore, workspace, names)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "trash"), workspace, "scale_p21.sw132_drift", "", nil, func(data *domain.Bootstrap) error {
		data.Trash = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginRetainedUploadRead(t.Context(), workspace, names)
	if err != nil || !reflect.DeepEqual(read.Names, want) {
		t.Fatal("canonical SQL drift replaced the frozen installed trash", err)
	}
	read.Close()
}

func TestScaleP21SW132RetainedUploadCurrentGeneration(t *testing.T) {
	before := domain.Bootstrap{Trash: []domain.TrashEntry{{Payload: json.RawMessage(`"/uploads/old.pdf"`)}}}
	after := domain.Bootstrap{Trash: []domain.TrashEntry{{Payload: json.RawMessage(`"/uploads/new.pdf"`)}}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": before})
	parent, found, err := view.BeginInstalledMetadataRead(t.Context(), "physical")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer parent.Close()
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	source, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("physical", source)
	for _, closed := range []bool{false, true} {
		if closed {
			parent.Close()
		}
		read, err := view.BeginRetainedUploadRead(pinned, "physical", []string{"old.pdf", "new.pdf"})
		if err != nil || !reflect.DeepEqual(read.Names, []string{"old.pdf"}) {
			t.Fatal("contextless original inherited caller's installed generation", closed, err)
		}
		read.Close()
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("current-generation lookup leaked")
	}
}

func TestScaleP21SW132RetainedUploadLargeAndFailures(t *testing.T) {
	large := []byte(`{"url":"/uploads/large.pdf?query=` + strings.Repeat("x", 500000) + `","body":"` + strings.Repeat("legal large payload ", 100000) + `"}`)
	for _, raw := range [][]byte{large, scaleP21SW132Gzip(t, large)} {
		view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {Trash: []domain.TrashEntry{{Payload: raw}}}})
		read, err := view.BeginRetainedUploadRead(t.Context(), "physical", []string{"large.pdf", "free.pdf"})
		if err != nil || !reflect.DeepEqual(read.Names, []string{"free.pdf"}) {
			t.Fatal("legal large payload rejected or differs", len(raw), err)
		}
		read.Close()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if read, err := view.BeginRetainedUploadRead(ctx, "physical", []string{"free.pdf"}); read != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled lookup escaped", read, err)
		}
		budget := view.metadataResidency.work
		limit := budget.limit
		budget.limit = 512 << 10
		if read, err := view.BeginRetainedUploadRead(t.Context(), "physical", []string{"free.pdf"}); read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("partial admission did not fail closed", read, err)
		}
		budget.limit = limit
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("partial admission/cancellation leaked")
		}
	}
	raw := scaleP21SW132Gzip(t, []byte(strings.Repeat("x", metadataWorkBytes/16+1)))
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {Trash: []domain.TrashEntry{{Payload: raw}}}})
	if read, err := view.BeginRetainedUploadRead(t.Context(), "physical", []string{"free.pdf"}); read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("gzip expansion exceeded work bound", read, err)
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("gzip work rejection leaked")
	}
	missing := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {}})
	missing.releaseMetadataLogicalSources()
	if read, err := missing.BeginRetainedUploadRead(t.Context(), "physical", []string{"free.pdf"}); read != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("missing installed generation did not fail closed", read, err)
	}
	if missing.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("missing installed generation leaked candidate work")
	}
}
