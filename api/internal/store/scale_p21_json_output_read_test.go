package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21JSONOwner(t *testing.T, view *SQLiteStore, ctx context.Context) *MetadataJSONRead {
	t.Helper()
	read, err := view.BeginMetadataJSONRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(read.Close)
	return read
}

func TestScaleP21JSONOutputBeginAndIndependentLease(t *testing.T) {
	if read, err := (&SQLiteStore{}).BeginMetadataJSONRead(t.Context()); read != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("literal store accepted bounded JSON ownership", read, err)
	}
	var nilRead *MetadataJSONRead
	if !errors.Is(nilRead.Err(), ErrBoundedMetadataCoverage) ||
		!errors.Is(nilRead.ReserveEncoding(t.Context(), "value"), ErrBoundedMetadataCoverage) ||
		!errors.Is(nilRead.ReserveEncoded(t.Context(), 1), ErrBoundedMetadataCoverage) ||
		!errors.Is(nilRead.ReserveDecode(t.Context(), []byte(`{}`), nil), ErrBoundedMetadataCoverage) ||
		nilRead.Context(t.Context()) != t.Context() {
		t.Fatal("nil owner lost explicit coverage/default context behavior")
	}
	nilRead.Close()
	view := scaleP21HotLogoStore(t, nil)
	view.metadataResidency.work.limit = 128<<10 - 1
	if read, err := view.BeginMetadataJSONRead(t.Context()); read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("base admission failure leaked or returned an owner", read, err)
	}
	view.metadataResidency.work.limit = metadataWorkBytes
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := view.BeginMetadataJSONRead(cancelled); read != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cancelled begin retained work", read, err)
	}
	parentContext, release, err := view.admitMetadataWork(t.Context(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	read := scaleP21JSONOwner(t, view, parentContext)
	if view.MetadataWorkUsedForTesting() != 1<<20+128<<10 {
		t.Fatal("JSON owner borrowed the containing work lease", view.MetadataWorkUsedForTesting())
	}
	release()
	if view.MetadataWorkUsedForTesting() != 128<<10 {
		t.Fatal("containing lease release removed independent JSON work")
	}
	read.Close()
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("independent base work leaked")
	}
}

func TestScaleP21JSONOutputEncodedGrowth(t *testing.T) {
	view := scaleP21HotLogoStore(t, nil)
	read := scaleP21JSONOwner(t, view, t.Context())
	const page = 64 << 10
	for _, step := range []struct {
		size, pages int
	}{
		{0, 0}, {1, 1}, {page - 1, 1}, {page, 1}, {page + 1, 2},
		{2 * page, 2}, {page, 2}, {0, 2}, {2*page + 1, 3},
	} {
		if err := read.ReserveEncoded(t.Context(), step.size); err != nil {
			t.Fatal(step, err)
		}
		want := 128<<10 + 4*page*step.pages
		if got := view.MetadataWorkUsedForTesting(); got != want {
			t.Fatal("absolute-size admission duplicated or lost a buffer page", step, got, want)
		}
	}
	held := view.MetadataWorkUsedForTesting()
	view.metadataResidency.work.limit = held + 4*page - 1
	if err := read.ReserveEncoded(t.Context(), 3*page+1); !errors.Is(err, ErrMetadataWorkBudget) ||
		!errors.Is(read.Err(), ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != held {
		t.Fatal("growth failure lost the existing admitted buffer", err, read.Err(), view.MetadataWorkUsedForTesting())
	}
	if err := read.ReserveEncoded(t.Context(), 1); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("a smaller follow-up hid the failed growth", err)
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("failed growth owner leaked old buffer work")
	}
	for _, size := range []int{-1, metadataWorkBytes/4 + 1} {
		view.metadataResidency.work.limit = metadataWorkBytes
		read := scaleP21JSONOwner(t, view, t.Context())
		if err := read.ReserveEncoded(t.Context(), size); !errors.Is(err, ErrMetadataWorkBudget) || !errors.Is(read.Err(), err) {
			t.Fatal("invalid absolute size did not latch", size, err, read.Err())
		}
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("invalid size leaked base work")
		}
	}
}

func TestScaleP21JSONOutputLargePlainAndDenseContainers(t *testing.T) {
	view := scaleP21HotLogoStore(t, nil)
	plain := strings.Repeat("p", 1<<20)
	read := scaleP21JSONOwner(t, view, t.Context())
	if err := read.ReserveEncoding(t.Context(), plain); err != nil {
		t.Fatal("legal large text could not reserve encoding", err)
	}
	raw, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	for offset := 0; offset < len(raw); {
		end := min(offset+17<<10, len(raw))
		if err := read.ReserveEncoded(t.Context(), end); err != nil {
			t.Fatal("large output failed before buffer growth", err)
		}
		buffer.Write(raw[offset:end])
		offset = end
	}
	var decoded string
	if err := read.ReserveDecode(t.Context(), buffer.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(buffer.Bytes(), &decoded); err != nil || decoded != plain || read.Err() != nil {
		t.Fatal("large output encode/buffer/decode round trip changed", err, read.Err())
	}
	if view.MetadataWorkUsedForTesting() <= 32<<20 {
		t.Fatal("large encoding, buffer and decode work did not coexist", view.MetadataWorkUsedForTesting())
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("large round trip leaked work")
	}

	plainRaw := []byte(`"` + strings.Repeat("x", 128<<10) + `"`)
	denseRaw := []byte("[" + strings.Repeat("{},", (128<<10)/3-1) + "{}]")
	if len(denseRaw) > len(plainRaw) || !json.Valid(denseRaw) {
		t.Fatal("dense fixture is larger than its plain-text comparison")
	}
	var plainWork int
	for _, test := range []struct {
		name string
		raw  []byte
		want error
	}{
		{"plain", plainRaw, nil},
		{"dense", denseRaw, ErrMetadataWorkBudget},
	} {
		t.Run(test.name, func(t *testing.T) {
			view.metadataResidency.work.limit = 8 << 20
			read := scaleP21JSONOwner(t, view, t.Context())
			err := read.ReserveDecode(t.Context(), test.raw, nil)
			if !errors.Is(err, test.want) {
				t.Fatal("byte-only admission missed the container cost", err)
			}
			if test.want == nil {
				plainWork = view.MetadataWorkUsedForTesting()
				var value any
				if err := json.Unmarshal(test.raw, &value); err != nil {
					t.Fatal(err)
				}
			} else if view.MetadataWorkUsedForTesting() != 128<<10 || !errors.Is(read.Err(), test.want) {
				t.Fatal("failed dense preflight retained partial decoded work", read.Err(), view.MetadataWorkUsedForTesting())
			}
			read.Close()
		})
	}
	t.Logf("plain_bytes=%d dense_bytes=%d plain_held_work=%d quota=%d", len(plainRaw), len(denseRaw), plainWork, 8<<20)
}

func TestScaleP21JSONOutputPreflightQuotas(t *testing.T) {
	for _, stage := range []string{"encoding", "decode", "previous-graph"} {
		t.Run(stage, func(t *testing.T) {
			view := scaleP21HotLogoStore(t, nil)
			view.metadataResidency.work.limit = 1 << 20
			read := scaleP21JSONOwner(t, view, t.Context())
			previous := map[string]any{"unchanged": strings.Repeat("existing ", 300000)}
			before := maps.Clone(previous)
			var err error
			switch stage {
			case "encoding":
				err = read.ReserveEncoding(t.Context(), strings.Repeat("new text ", 100000))
			case "decode":
				err = read.ReserveDecode(t.Context(), []byte(`"`+strings.Repeat("new ", 100000)+`"`), nil)
			case "previous-graph":
				if err := read.ReserveDecode(t.Context(), []byte(`{"added":true}`), nil); err != nil {
					t.Fatal("small raw input alone could not fit", err)
				}
				err = read.ReserveDecode(t.Context(), []byte(`{"added":true}`), &previous)
			}
			if !errors.Is(err, ErrMetadataWorkBudget) || !errors.Is(read.Err(), ErrMetadataWorkBudget) ||
				!reflect.DeepEqual(before, previous) {
				t.Fatal("preflight failure changed the original graph or lost its latch", err, read.Err())
			}
			held := view.MetadataWorkUsedForTesting()
			if err := read.ReserveEncoding(t.Context(), "small"); !errors.Is(err, ErrMetadataWorkBudget) ||
				view.MetadataWorkUsedForTesting() != held {
				t.Fatal("failed owner admitted later work", err)
			}
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("partial preflight quota leaked work")
			}
		})
	}
}

func TestScaleP21JSONOutputOriginalAndPartialDestination(t *testing.T) {
	type nested struct {
		Keep    string `json:"keep"`
		Changed string `json:"changed"`
	}
	type destination struct {
		Keep     string         `json:"keep"`
		Nested   *nested        `json:"nested"`
		Settings map[string]any `json:"settings"`
		Count    int            `json:"count"`
		Tail     string         `json:"tail"`
	}
	initial := func() destination {
		return destination{
			Keep: strings.Repeat("retained ", 30000), Nested: &nested{Keep: "nested retained", Changed: "before"},
			Settings: map[string]any{"retained": []any{"existing"}}, Count: 2, Tail: "before",
		}
	}
	for _, raw := range []string{
		`{"count":7,"nested":{"changed":"after"},"settings":{"added":"after"}}`,
		`{"count":"invalid integer","nested":{"changed":"after"},"settings":{"added":"after"},"tail":"after"}`,
	} {
		t.Run(fmt.Sprint("partialError=", strings.Contains(raw, "invalid")), func(t *testing.T) {
			view := scaleP21HotLogoStore(t, nil)
			read := scaleP21JSONOwner(t, view, t.Context())
			want, got := initial(), initial()
			originalNested := got.Nested
			if err := read.ReserveDecode(t.Context(), []byte(raw), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, initial()) {
				t.Fatal("reservation changed the destination before Unmarshal")
			}
			wantErr := json.Unmarshal([]byte(raw), &want)
			gotErr := json.Unmarshal([]byte(raw), &got)
			if reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) || !reflect.DeepEqual(got, want) ||
				got.Nested != originalNested || got.Settings["retained"] == nil || got.Keep == "" {
				t.Fatal("original-destination merge or partial error differs", gotErr, wantErr)
			}
			if got.Nested.Changed != "after" || got.Settings["added"] != "after" ||
				read.Err() != nil || view.MetadataWorkUsedForTesting() < 512<<10 {
				t.Fatal("partial result lost output ownership or a legacy decode error was latched", gotErr, read.Err())
			}
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("partial-result ownership leaked")
			}
		})
	}
}

func TestScaleP21JSONOutputCancellationAndChildren(t *testing.T) {
	view := scaleP21HotLogoStore(t, nil)
	request, cancel := context.WithCancel(t.Context())
	read := scaleP21JSONOwner(t, view, request)
	ctx := read.Context(request)
	if !metadataOutputOwnerPresent(ctx) || read.Context(ctx) == nil {
		t.Fatal("JSON owner did not bind subordinate output")
	}
	if err := read.ReserveEncoding(ctx, map[string]any{"value": "output"}); err != nil {
		t.Fatal(err)
	}
	if err := read.ReserveEncoded(ctx, 100000); err != nil {
		t.Fatal(err)
	}
	var closes []int
	for index := range 3 {
		if !registerMetadataOutputClose(ctx, func() {
			if view.MetadataWorkUsedForTesting() == 0 {
				t.Error("parent released admission before its subordinate output")
			}
			_ = read.Err()
			closes = append(closes, index)
		}) {
			t.Fatal("live owner rejected subordinate output")
		}
	}
	held := view.MetadataWorkUsedForTesting()
	cancel()
	if view.MetadataWorkUsedForTesting() != held || !metadataOutputOwnerPresent(context.WithoutCancel(ctx)) {
		t.Fatal("cancellation released output before explicit close")
	}
	if err := read.ReserveEncoded(ctx, 100000); !errors.Is(err, context.Canceled) ||
		!errors.Is(read.Err(), context.Canceled) || view.MetadataWorkUsedForTesting() != held {
		t.Fatal("cancelled growth did not latch while retaining existing output", err, read.Err())
	}
	read.Close()
	read.Close()
	if !slices.Equal(closes, []int{2, 1, 0}) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("child close order, reentrancy or exact release changed", closes, view.MetadataWorkUsedForTesting())
	}
	if registerMetadataOutputClose(context.WithoutCancel(ctx), func() { t.Error("closed parent retained late child") }) {
		t.Fatal("closed parent accepted late registration")
	}
	if err := read.ReserveDecode(t.Context(), []byte(`{}`), nil); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("closed output owner accepted new decode work", err)
	}
}

func TestScaleP21JSONOutputErrorLatchAndIndependentOwners(t *testing.T) {
	view := scaleP21HotLogoStore(t, nil)
	first := scaleP21JSONOwner(t, view, t.Context())
	second := scaleP21JSONOwner(t, view, first.Context(t.Context()))
	firstCtx, secondCtx := first.Context(t.Context()), second.Context(t.Context())
	var firstCloses, secondCloses int
	if !registerMetadataOutputClose(firstCtx, func() { firstCloses++ }) ||
		!registerMetadataOutputClose(secondCtx, func() { secondCloses++ }) {
		t.Fatal("independent owner registration failed")
	}
	if err := first.ReserveEncoded(firstCtx, 1); err != nil {
		t.Fatal(err)
	}
	if err := second.ReserveEncoded(secondCtx, 65537); err != nil {
		t.Fatal(err)
	}
	ordinary := errors.New("legacy decoder error")
	if recordMetadataOutputError(firstCtx, ordinary) != ordinary || first.Err() != nil {
		t.Fatal("ordinary projection error changed the new ownership latch")
	}
	coverage := fmt.Errorf("subordinate projection: %w", ErrBoundedMetadataCoverage)
	recordMetadataOutputError(firstCtx, coverage)
	recordMetadataOutputError(firstCtx, ErrMetadataWorkBudget)
	if first.Err() != coverage || second.Err() != nil {
		t.Fatal("first subordinate failure or independent owner isolation changed", first.Err(), second.Err())
	}
	first.Close()
	if firstCloses != 1 || secondCloses != 0 || view.MetadataWorkUsedForTesting() != 128<<10+2*65536*4 {
		t.Fatal("one output owner released another owner's work", firstCloses, secondCloses, view.MetadataWorkUsedForTesting())
	}
	lateClosed := 0
	if !registerMetadataOutputClose(firstCtx, func() { lateClosed++ }) {
		lateClosed++
	}
	if lateClosed != 1 {
		t.Fatal("late subordinate was retained after parent close", lateClosed)
	}
	second.Close()
	if secondCloses != 1 || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("independent output lifetime leaked")
	}
}

func TestScaleP21JSONOutputConcurrentGrowthAndClose(t *testing.T) {
	view := scaleP21HotLogoStore(t, nil)
	read := scaleP21JSONOwner(t, view, t.Context())
	var workers sync.WaitGroup
	failures := make(chan error, 64)
	for index := range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := read.ReserveEncoded(t.Context(), (index+1)*4096); err != nil {
				failures <- err
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error("concurrent absolute growth failed", err)
	}
	if got, want := view.MetadataWorkUsedForTesting(), 128<<10+4*256<<10; got != want {
		t.Fatal("concurrent growth was double admitted", got, want)
	}

	ctx := read.Context(t.Context())
	var closed atomic.Int64
	failures = make(chan error, 100)
	start := make(chan struct{})
	for range 100 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			childClose := func() { closed.Add(1) }
			if !registerMetadataOutputClose(ctx, childClose) {
				childClose()
			}
			if err := read.ReserveEncoding(ctx, "retained"); err != nil && !errors.Is(err, ErrBoundedMetadataCoverage) {
				failures <- err
			}
			if err := read.ReserveDecode(ctx, []byte(`{"value":1}`), nil); err != nil && !errors.Is(err, ErrBoundedMetadataCoverage) {
				failures <- err
			}
			recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
			_ = read.Err()
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		read.Close()
	}()
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error("concurrent owner operation failed", err)
	}
	read.Close()
	if closed.Load() != 100 || view.MetadataWorkUsedForTesting() != 0 || !errors.Is(read.Err(), ErrBoundedMetadataCoverage) {
		t.Fatal("concurrent child closure, error latch or work release was lost", closed.Load(), view.MetadataWorkUsedForTesting(), read.Err())
	}
}

func TestScaleP21JSONOutputActualPointLifetime(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, reload := range []bool{false, true} {
		t.Run(fmt.Sprint("reload=", reload), func(t *testing.T) {
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			projectID := idgen.New("json-point-")
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "scale_p21.json_point", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects, domain.Project{
					ID: projectID, Name: "Owned point", Description: strings.Repeat("selected body ", 10000), TeamIDs: []string{data.Teams[0].ID},
				})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if reload {
				f.mu.Lock()
				f.forgetCopyRevisionLocked(workspace)
				f.mu.Unlock()
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(view.releaseMetadataLogicalSources)
			access, err := view.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			request, cancel := context.WithCancel(t.Context())
			defer cancel()
			read := scaleP21JSONOwner(t, view, request)
			ctx := read.Context(request)
			data, allowed, err := view.ResourceReadMetadata(ctx, access, "projects", projectID)
			if err != nil || !allowed || len(data.Projects) != 1 || data.Projects[0].ID != projectID ||
				view.MetadataWorkUsedForTesting() <= 128<<10 {
				t.Fatal("actual point output was not transferred to JSON lifetime", allowed, err)
			}
			value := data.Projects[0]
			if err := read.ReserveEncoding(ctx, value); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if err := read.ReserveEncoded(ctx, len(raw)); err != nil {
				t.Fatal(err)
			}
			destination := map[string]any{"retained": "previous destination"}
			if err := read.ReserveDecode(ctx, raw, &destination); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &destination); err != nil || destination["id"] != projectID ||
				destination["retained"] != "previous destination" {
				t.Fatal("actual point JSON destination merge changed", err)
			}
			held := view.MetadataWorkUsedForTesting()
			cancel()
			if view.MetadataWorkUsedForTesting() != held || read.Err() != nil {
				t.Fatal("cancel released the selected output before its final use", read.Err())
			}
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("point source or JSON output leaked/hydrated complete metadata", view.MetadataWorkUsedForTesting())
			}
			if _, _, err := view.ResourceReadMetadata(read.Context(t.Context()), access, "projects", projectID); !errors.Is(err, ErrBoundedMetadataCoverage) ||
				!errors.Is(read.Err(), ErrBoundedMetadataCoverage) || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("late point output escaped a closed JSON owner", err, read.Err())
			}
			t.Logf("reloaded=%t encoded_bytes=%d held_work=%d", reload, len(raw), held)
		})
	}
}
