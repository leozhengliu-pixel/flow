package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW316ColdGenericFixture(t *testing.T, nested bool) (*SQLiteStore, *metadataLogicalSource, domain.Bootstrap) {
	t.Helper()
	repo := scaleP21SW248Repo(t)
	raw := `{"replace":{"old":true},"null":null,"emptyMap":{},"emptyArray":[],"number":9007199254740993,"negativeZero":-0,"text":"a\u0000<>&\u2028\ud800","deep":` +
		strings.Repeat(`{"next":[`, 256) + `{"leaf":[null,{},[],true,"end"]}` + strings.Repeat(`]}`, 256) + `}`
	builder := scaleP21SW248Builder(t)
	node := scaleP21SW248Frame(t, repo, builder, reflect.TypeFor[map[string]any](), raw)
	source := scaleP21SW248Source(t, builder, node, 1)
	oracle, _ := scaleP21SW248Oracle(t, reflect.TypeFor[map[string]any](), raw)
	want := domain.Bootstrap{Settings: oracle.Interface().(map[string]any)}
	if nested {
		replacement := map[string]any{
			"integer": int64(9007199254740993), "bytes": []byte{0, 255},
			"nil": []string(nil), "empty": []string{},
		}
		before := domain.Bootstrap{Settings: map[string]any{"replace": want.Settings["replace"]}}
		selected := domain.Bootstrap{Settings: map[string]any{"replace": replacement}}
		fork, err := source.Fork(t.Context(), before, selected,
			MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "replace"}}},
			copyStamp{workspaceID: "sw248", revision: 2})
		if err != nil {
			t.Fatal("create native settings container retaining generic children", err)
		}
		t.Cleanup(fork.Release)
		source = fork
		want.Settings["replace"] = replacement
	}
	index, _, ok := metadataLogicalField("settings")
	if !ok || source.fields[index].generic == nested {
		t.Fatal("fixture lost its generic/native root distinction")
	}
	if nested {
		retained := false
		if err := source.fields[index].MapEntries(t.Context(), func(_ metadataLogicalNode, child metadataLogicalNode) error {
			retained = retained || child.generic
			return nil
		}); err != nil || !retained {
			t.Fatal("native fixture has no retained generic child", err)
		}
	}
	scaleP21SW248Install(t, repo, source)
	return repo, source, want
}

func scaleP21SW316SourceRefs(source *metadataLogicalSource) int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.refs
}

func scaleP21SW316AssertColdGeneric(t *testing.T, read *ColdMetadataRead, want domain.Bootstrap) {
	t.Helper()
	facade := read.Store()
	if facade == nil {
		t.Fatal("cold generic owner has no facade")
	}
	got, found := facade.WorkspaceMetadata("sw248")
	if !found || !reflect.DeepEqual(got.Settings, want.Settings) {
		t.Fatal("cold generic read changed exact normalized/native types or values")
	}
	expected, err := json.Marshal(want.Settings)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(got.Settings)
	if err != nil || !bytes.Equal(wire, expected) {
		t.Fatal("cold generic read changed complete literal JSON, including negative zero", err)
	}
}

func scaleP21SW316AssertHotDenied(t *testing.T, repo *SQLiteStore, node metadataLogicalNode, ctx context.Context) {
	t.Helper()
	held := repo.MetadataWorkUsedForTesting()
	for _, read := range []func(context.Context, int) (reflect.Value, error){node.Value, node.WireValue} {
		_, err := read(ctx, int(^uint(0)>>1))
		if !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("ordinary generic materialization bypassed saturated HOT admission", err)
		}
		if repo.MetadataWorkUsedForTesting() != held {
			t.Fatal("denied generic materialization changed another owner's HOT lease")
		}
	}
}

func TestScaleP21SW316ColdGenericIndependentOfHotQuota(t *testing.T) {
	for _, test := range []struct {
		name   string
		nested bool
	}{{"generic-root", false}, {"native-root-generic-child", true}} {
		t.Run(test.name, func(t *testing.T) {
			repo, source, want := scaleP21SW316ColdGenericFixture(t, test.nested)
			index, _, _ := metadataLogicalField("settings")
			node := source.fields[index]
			refs := scaleP21SW316SourceRefs(source)
			limit := repo.metadataResidency.work.limit
			_, release, err := repo.tryAdmitAdditionalMetadataWork(t.Context(), limit)
			if err != nil {
				t.Fatal("independently hold the entire HOT work quota", err)
			}
			defer release()
			scaleP21SW316AssertHotDenied(t, repo, node, t.Context())
			read, err := repo.beginColdMetadataRead(t.Context())
			if err != nil {
				t.Fatal("COLD generic decode must succeed while the entire HOT quota is independently held", err)
			}
			defer read.Close()
			scaleP21SW316AssertColdGeneric(t, read, want)
			if repo.MetadataWorkUsedForTesting() != limit || len(repo.metadataResidency.coldSlot) != 1 ||
				scaleP21SW316SourceRefs(source) != refs+1 {
				t.Fatal("cold generic decode borrowed HOT work or lost its slot/source pin")
			}
			for _, contextTest := range []struct {
				name string
				ctx  context.Context
			}{
				{"unbound", t.Context()},
				{"public-cold-context", read.Context()},
				{"ownerless", context.WithValue(t.Context(), coldMetadataReadKey{}, &coldMetadataRead{})},
				{"foreign-owner", context.WithValue(t.Context(), coldMetadataReadKey{}, &coldMetadataRead{
					owner: newMetadataResidency(), facade: read.Store(),
				})},
				{"same-owner-forged-facade", context.WithValue(t.Context(), coldMetadataReadKey{}, &coldMetadataRead{
					owner: repo.metadataResidency, facade: repo,
				})},
				{"private-token-empty", context.WithValue(t.Context(), coldMetadataGenericDecodeKey{}, &coldMetadataGenericDecode{})},
				{"private-token-foreign-storage", context.WithValue(t.Context(), coldMetadataGenericDecodeKey{}, &coldMetadataGenericDecode{
					stores: map[*metadataLogicalStorage]bool{new(metadataLogicalStorage): true},
				})},
			} {
				t.Run(contextTest.name, func(t *testing.T) {
					scaleP21SW316AssertHotDenied(t, repo, node, contextTest.ctx)
				})
			}
			facade, expired := read.Store(), read.Context()
			read.Close()
			read.Close()
			if read.Store() != nil || len(facade.workspaces) != 0 || facade.coldParent != nil ||
				len(repo.metadataResidency.coldSlot) != 0 || scaleP21SW316SourceRefs(source) != refs ||
				repo.MetadataWorkUsedForTesting() != limit {
				t.Fatal("double cold close retained graph/pins/slot or released the independent HOT owner")
			}
			scaleP21SW316AssertHotDenied(t, repo, node, expired)
			next, err := repo.beginColdMetadataRead(t.Context())
			if err != nil {
				t.Fatal("cold generic slot did not recover with HOT quota still held", err)
			}
			defer next.Close()
			scaleP21SW316AssertColdGeneric(t, next, want)
			next.Close()
			release()
			if repo.MetadataWorkUsedForTesting() != 0 || scaleP21SW316SourceRefs(source) != refs {
				t.Fatal("independent HOT release or subsequent cold close leaked work/source ownership")
			}
			wire, err := json.Marshal(want.Settings)
			if err != nil {
				t.Fatal(err)
			}
			scaleP21SW248AssertValue(t, repo, node, reflect.ValueOf(want.Settings), wire)
		})
	}
}

func TestScaleP21SW316ColdGenericCancellationReleasesOwnership(t *testing.T) {
	repo, source, want := scaleP21SW316ColdGenericFixture(t, true)
	refs := scaleP21SW316SourceRefs(source)
	limit := repo.metadataResidency.work.limit
	_, release, err := repo.tryAdmitAdditionalMetadataWork(t.Context(), limit)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := repo.beginColdMetadataRead(cancelled); read != nil || !errors.Is(err, context.Canceled) ||
		scaleP21SW316SourceRefs(source) != refs || len(repo.metadataResidency.coldSlot) != 0 {
		if read != nil {
			read.Close()
		}
		t.Fatal("pre-cancelled cold generic read retained ownership", err)
	}
	read, err := repo.beginColdMetadataRead(t.Context())
	if err != nil {
		t.Fatal("cold generic owner could not enter with HOT quota held", err)
	}
	defer read.Close()
	scaleP21SW316AssertColdGeneric(t, read, want)
	waiting, stop := context.WithCancel(t.Context())
	defer stop()
	started, result := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		queued, err := repo.beginColdMetadataRead(waiting)
		if queued != nil {
			queued.Close()
			result <- errors.New("queued cold generic read entered another owner's replica slot")
			return
		}
		result <- err
	}()
	<-started
	select {
	case err := <-result:
		t.Fatal("queued generic read did not wait for its cold slot", err)
	case <-time.After(20 * time.Millisecond):
	}
	stop()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("queued cold generic cancellation changed error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued cold generic cancellation did not return")
	}
	if len(repo.metadataResidency.coldSlot) != 1 || scaleP21SW316SourceRefs(source) != refs+1 ||
		repo.MetadataWorkUsedForTesting() != limit {
		t.Fatal("queued cancellation released another cold/HOT owner's resources")
	}
	read.Close()
	read.Close()
	if len(repo.metadataResidency.coldSlot) != 0 || scaleP21SW316SourceRefs(source) != refs ||
		repo.MetadataWorkUsedForTesting() != limit {
		t.Fatal("cancelled waiter or double close leaked cold ownership")
	}
	next, err := repo.beginColdMetadataRead(t.Context())
	if err != nil {
		t.Fatal("generic cold read did not recover after queued cancellation", err)
	}
	defer next.Close()
	scaleP21SW316AssertColdGeneric(t, next, want)
	next.Close()
	if len(repo.metadataResidency.coldSlot) != 0 || scaleP21SW316SourceRefs(source) != refs ||
		repo.MetadataWorkUsedForTesting() != limit {
		t.Fatal("recovered generic cold read leaked ownership or released unrelated HOT work")
	}
}
