package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type timestampFatOwner struct {
	Padding [8 << 20]byte
	Zone    time.Location
}

func timestampFatLocation() time.Time {
	owner := new(timestampFatOwner)
	owner.Padding[0], owner.Padding[len(owner.Padding)-1] = 1, 2
	owner.Zone = *time.FixedZone("", 20700)
	return time.Date(2026, 11, 13, 1, 2, 3, 4, &owner.Zone)
}

func TestTimestampInputsReplacedLocalNotAudited(t *testing.T) {
	fat := timestampFatLocation()
	if _, err := metadataTimestampParsedLocation(fat, fat.Location()); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("replaced time.Local minted provenance for an interior private Location", err)
	}
	parsed, err := time.Parse(time.RFC3339, "2026-11-13T01:02:03+05:45")
	if err != nil {
		t.Fatal(err)
	}
	location, err := metadataTimestampParsedLocation(parsed, metadataLogicalProcessLocal)
	if err != nil || location != parsed.Location() {
		t.Fatal("ordinary parser-owned fixed-zone pointer was replaced", err)
	}
}

func timestampFixture(t *testing.T) (scaleFixture, *SQLiteStore, string) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := journalScaleDatabase(t)
	id := idgen.New("time_input_receipt_")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "integrationConnections"), f.data.Workspace.URLKey, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = []domain.IntegrationConnection{
			{ID: id, Provider: "github", Status: "connected"},
			{ID: id + "-retained", Provider: "github", Status: "connected"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return f, view, id
}

func TestTimestampInputsProducerAndNativeIdentity(t *testing.T) {
	f, view, id := timestampFixture(t)
	workspace := f.data.Workspace.URLKey
	ctx, timestamps := view.WithMetadataTimestampInputs(WithMetadataFields(t.Context(), "integrationConnections"))
	if _, err := timestamps.ParseRFC3339("2026-11-13T01:02:03+05:45"); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("unbound parser minted provenance", err)
	}
	var native time.Time
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "integrationConnections", ID: id}}}
	err := view.MutateWorkspaceScoped(ctx, workspace, "integration.adapter_configured", id, nil, scope, func(data *domain.Bootstrap) error {
		var err error
		native, err = timestamps.ParseRFC3339("2026-11-13T01:02:03.000000004+05:45")
		if err != nil {
			return err
		}
		data.IntegrationConnections[0].LastWebhookAt = &native
		timestamps.mu.Lock()
		activeCtx := timestamps.operation.ctx
		timestamps.mu.Unlock()
		captured, err := captureMetadataLogicalSource(activeCtx, *data, copyStamp{})
		if err != nil {
			return err
		}
		defer captured.Release()
		selected, err := captured.Select(ctx, scope, 2<<20)
		if err != nil || *selected.IntegrationConnections[0].LastWebhookAt != native {
			t.Fatalf("active receipt capture changed native identity: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	read := MetadataRecordScope{Read: []RecordKey{{Kind: "integrationConnections", ID: id}}}
	data, _, err := view.WorkspaceMetadataScoped(t.Context(), workspace, read)
	if err != nil || *data.IntegrationConnections[0].LastWebhookAt != native {
		t.Fatal("producer receipt did not survive installed source publication", err)
	}
	if _, err := timestamps.ParseRFC3339("2026-11-13T01:02:03+05:45"); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("completed parser still minted provenance", err)
	}
	if err := view.MutateWorkspaceScoped(WithMetadataFields(t.Context(), "integrationConnections"), workspace, "integration.adapter_configured", id, nil, scope, func(data *domain.Bootstrap) error {
		data.IntegrationConnections[0].Status = "after"
		return nil
	}); err != nil {
		t.Fatal("subsequent writer lost existing receipt", err)
	}
	data, _, err = view.WorkspaceMetadataScoped(t.Context(), workspace, read)
	if err != nil || *data.IntegrationConnections[0].LastWebhookAt != native {
		t.Fatal("subsequent writer changed exact time.Time ==", err)
	}
	if view.metadataResidency.work.used != 0 {
		t.Fatal("receipt operation leaked work admission")
	}
}

func TestTimestampInputsKeepColdRegistryOrder(t *testing.T) {
	f, view, id := timestampFixture(t)
	workspace := f.data.Workspace.URLKey
	ctx, timestamps := view.WithMetadataTimestampInputs(WithMetadataFields(t.Context(), "integrationConnections"))
	writeBoth := MetadataRecordScope{Write: []RecordKey{{Kind: "integrationConnections", ID: id}, {Kind: "integrationConnections", ID: id + "-retained"}}}
	if err := view.MutateWorkspaceScoped(ctx, workspace, "integration.adapter_configured", id, nil, writeBoth, func(data *domain.Bootstrap) error {
		parsed, err := timestamps.ParseRFC3339("2026-11-13T01:02:03+05:45")
		if err != nil {
			return err
		}
		for i := range data.IntegrationConnections {
			data.IntegrationConnections[i].LastWebhookAt = &parsed
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	readCtx, release, err := view.admitMetadataWork(t.Context(), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := view.db.BeginTx(readCtx, metadataReadOptions(view.dialect))
	if err != nil {
		release()
		t.Fatal(err)
	}
	cold, err := view.readRawMetadataLogicalSource(readCtx, tx, workspace)
	_ = tx.Rollback()
	release()
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource(workspace, cold)
	readBoth := MetadataRecordScope{Read: writeBoth.Write}
	before, _, err := view.WorkspaceMetadataScoped(t.Context(), workspace, readBoth)
	if err != nil {
		t.Fatal(err)
	}
	retainedIndex := slices.IndexFunc(before.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.ID == id+"-retained" })
	oldNative := *before.IntegrationConnections[retainedIndex].LastWebhookAt
	ctx, timestamps = view.WithMetadataTimestampInputs(WithMetadataFields(t.Context(), "integrationConnections"))
	var nextNative time.Time
	if err := view.MutateWorkspaceScoped(ctx, workspace, "integration.adapter_configured", id, nil,
		MetadataRecordScope{Write: []RecordKey{{Kind: "integrationConnections", ID: id}}},
		func(data *domain.Bootstrap) error {
			var err error
			nextNative, err = timestamps.ParseRFC3339("2026-11-13T01:02:03+00:45")
			data.IntegrationConnections[0].LastWebhookAt = &nextNative
			return err
		}); err != nil {
		t.Fatal(err)
	}
	after, _, err := view.WorkspaceMetadataScoped(t.Context(), workspace, readBoth)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range after.IntegrationConnections {
		expected := nextNative
		if item.ID == id+"-retained" {
			expected = oldNative
		}
		if item.LastWebhookAt == nil || *item.LastWebhookAt != expected {
			t.Fatal("copied cold receipt index was remapped or native pointer identity changed", item.ID)
		}
	}
}

func TestTimestampInputsRetryLimitAndFailureCleanup(t *testing.T) {
	f, view, id := timestampFixture(t)
	workspace := f.data.Workspace.URLKey
	ctx, timestamps := view.WithMetadataTimestampInputs(WithMetadataFields(t.Context(), "integrationConnections"))
	var native time.Time
	attempts := 0
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "integrationConnections", ID: id}}}
	if err := view.MutateWorkspaceScoped(ctx, workspace, "integration.adapter_configured", id, nil, scope, func(data *domain.Bootstrap) error {
		attempts++
		for range metadataTimestampInputLimit {
			var err error
			native, err = timestamps.ParseRFC3339("2026-11-13T01:02:03+05:45")
			if err != nil {
				return err
			}
		}
		if attempts == 1 {
			return ErrConcurrentWrite
		}
		data.IntegrationConnections[0].LastWebhookAt = &native
		return nil
	}); err != nil || attempts != 2 {
		t.Fatal("retry did not receive a fresh bounded parser ledger", attempts, err)
	}
	ctx, timestamps = view.WithMetadataTimestampInputs(WithMetadataFields(t.Context(), "integrationConnections"))
	err := view.MutateWorkspaceScoped(ctx, workspace, "integration.adapter_configured", id, nil, scope, func(data *domain.Bootstrap) error {
		for range metadataTimestampInputLimit + 1 {
			parsed, err := timestamps.ParseRFC3339("2026-11-13T01:02:03+05:45")
			if err != nil {
				return err
			}
			data.IntegrationConnections[0].LastWebhookAt = &parsed
		}
		return nil
	})
	if !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("unbounded receipt minting was admitted", err)
	}
	data, _, err := view.WorkspaceMetadataScoped(t.Context(), workspace, MetadataRecordScope{Read: []RecordKey{{Kind: "integrationConnections", ID: id}}})
	if err != nil || *data.IntegrationConnections[0].LastWebhookAt != native || view.metadataResidency.work.used != 0 {
		t.Fatal("failed parser limit changed prior source or leaked work", err)
	}
}

func TestTimestampInputsForeignConcurrentAndCancellation(t *testing.T) {
	f, view, id := timestampFixture(t)
	workspace := f.data.Workspace.URLKey
	foreign, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, timestamps := view.WithMetadataTimestampInputs(WithMetadataFields(t.Context(), "integrationConnections"))
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "integrationConnections", ID: id}}}
	called := false
	if err := foreign.MutateWorkspaceScoped(ctx, workspace, "integration.adapter_configured", id, nil, scope, func(*domain.Bootstrap) error {
		called = true
		return nil
	}); !errors.Is(err, ErrMetadataRecordQuery) || called {
		t.Fatal("foreign store used producer scope", called, err)
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- view.MutateWorkspaceScoped(ctx, workspace, "integration.adapter_configured", id, nil, scope, func(*domain.Bootstrap) error {
			close(started)
			<-release
			return ErrNoMutation
		})
	}()
	<-started
	err = view.MutateWorkspaceScoped(ctx, workspace, "integration.adapter_configured", id, nil, scope, func(*domain.Bootstrap) error {
		called = true
		return nil
	})
	close(release)
	first := <-done
	if first != nil || !errors.Is(err, ErrMetadataRecordQuery) || called {
		t.Fatal("concurrent operation reused producer scope", first, err, called)
	}
	cancelCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cancelCtx, timestamps = view.WithMetadataTimestampInputs(WithMetadataFields(cancelCtx, "integrationConnections"))
	err = view.MutateWorkspaceScoped(cancelCtx, workspace, "integration.adapter_configured", id, nil, scope, func(*domain.Bootstrap) error {
		cancel()
		_, err := timestamps.ParseRFC3339("2026-11-13T01:02:03+05:45")
		return err
	})
	if !errors.Is(err, context.Canceled) || view.metadataResidency.work.used != 0 {
		t.Fatal("canceled parser lost cancellation or leaked admission", err)
	}
}

// An unreceipted native callback time in a named zone stays opaque and is
// rejected before commit. One with an unnamed RFC 3339 offset, even a
// caller-owned copy inside a larger allocation, commits as the legacy writer
// commits it: the installed source holds it by value (an offset time) and
// keeps no pointer to the caller's location.
func TestTimestampInputsOpaqueNativeRejectedBeforeCommit(t *testing.T) {
	f, view, id := timestampFixture(t)
	workspace := f.data.Workspace.URLKey
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "integrationConnections", ID: id}}}
	write := func(at time.Time) error {
		ctx, timestamps := view.WithMetadataTimestampInputs(WithMetadataFields(t.Context(), "integrationConnections"))
		return view.MutateWorkspaceScoped(ctx, workspace, "integration.adapter_configured", id, nil, scope, func(data *domain.Bootstrap) error {
			if _, err := timestamps.ParseRFC3339("2026-11-13T01:02:03+05:45"); err != nil {
				return err
			}
			data.IntegrationConnections[0].LastWebhookAt = &at
			return nil
		})
	}
	installed := func() *time.Time {
		t.Helper()
		data, _, err := view.WorkspaceMetadataScoped(t.Context(), workspace, MetadataRecordScope{Read: []RecordKey{{Kind: "integrationConnections", ID: id}}})
		if err != nil || len(data.IntegrationConnections) != 1 {
			t.Fatal("installed connection", err)
		}
		return data.IntegrationConnections[0].LastWebhookAt
	}
	stored := func() []byte {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='integrationConnections' AND record_key=?`, workspace, id).Scan(&raw); err != nil || !json.Valid(raw) {
			t.Fatal("opaque hold modified canonical JSON behavior", err)
		}
		return raw
	}
	named := time.Date(2026, 11, 13, 1, 2, 3, 4, time.FixedZone("caller-owned", 20700))
	if err := write(named); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("unreceipted named-zone callback value reached commit", err)
	}
	if installed() != nil {
		t.Fatal("rejected private pointer changed the prior installed source")
	}
	stored()
	fat := timestampFatLocation()
	if err := write(fat); err != nil {
		t.Fatal("an unnamed RFC 3339 offset callback value was refused", err)
	}
	got := installed()
	if got == nil || !got.Equal(fat) || got.Format(time.RFC3339Nano) != fat.Format(time.RFC3339Nano) || got.Location() == fat.Location() {
		t.Fatal("offset time was not installed by value", got)
	}
	var row domain.IntegrationConnection
	if err := json.Unmarshal(stored(), &row); err != nil || row.LastWebhookAt == nil || row.LastWebhookAt.Format(time.RFC3339Nano) != fat.Format(time.RFC3339Nano) {
		t.Fatal("canonical row differs from the legacy encoding", err)
	}
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	for _, storage := range source.stores {
		if slices.Contains(storage.locations, fat.Location()) {
			t.Fatal("the installed source retained the caller's location")
		}
	}
}
