package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW344Spool(t *testing.T, repo *SQLiteStore, value any) *RecordSpool {
	t.Helper()
	frames, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer frames.Close()
	node, err := frames.writeValue(reflect.ValueOf(value), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	owner := &ResourceRead{store: repo}
	defer owner.Close()
	spool, err := legacySessionNodeSpool(t.Context(), owner, node)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = spool.Close() })
	expected, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	actual := make([]byte, int(spool.bytes))
	if _, err := spool.ReadAt(actual, 0); err != nil || !bytes.Equal(actual, expected) {
		t.Fatal("typed integration spool differs from the original JSON encoder", err)
	}
	return spool
}

func TestScaleP21SW344IntegrationRootCanonicalCopy(t *testing.T) {
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{})
	payload := json.RawMessage(` { "duplicate": 1, "duplicate": 2, "html": "` +
		strings.Repeat("<>&\u2028\u2029 ", 12000) + `", "nul":"\u0000", "escaped":"\\u0000" } `)
	for _, test := range []struct {
		name         string
		deliveries   []domain.IntegrationDelivery
		connections  []domain.IntegrationConnection
		requireLarge bool
	}{
		{name: "nil"},
		{name: "empty", deliveries: []domain.IntegrationDelivery{}, connections: []domain.IntegrationConnection{}},
		{
			name: "large-native-and-raw", requireLarge: true,
			deliveries: []domain.IntegrationDelivery{
				{ID: "duplicate", Payload: payload, PreviousValues: json.RawMessage(` { "v": 1, "v": 2 } `), LastError: "bad\xffvalue"},
				{ID: "duplicate", Payload: json.RawMessage(`{}`), PreviousValues: json.RawMessage(`null`)},
			},
			connections: []domain.IntegrationConnection{
				{ID: "duplicate", Name: strings.Repeat("name ", 60000) + "\xff", Config: map[string]string{
					"html": "<>&\u2028\u2029", "nul": "\x00", "invalid": "\xff",
				}, Channels: []string{}},
				{ID: "duplicate", Scopes: []string{}, Channels: nil},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
			delivery := scaleP21SW344Spool(t, repo, test.deliveries)
			connection := scaleP21SW344Spool(t, repo, test.connections)
			defer delivery.Close()
			defer connection.Close()
			if test.requireLarge && (delivery.bytes <= MetadataRecordPageMaxBytes || connection.bytes <= MetadataRecordPageMaxBytes) {
				t.Fatal("canonical fixtures do not cross the large-copy boundary")
			}
			old, err := newMetadataLogicalRecordSpool(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if _, err := old.Write([]byte(` { "literal" : "old space", "duplicate":1, "duplicate":2 } `)); err != nil {
				t.Fatal(err)
			}
			fields := map[string]legacySessionJSONPart{
				"old":                    legacySessionSpoolPart(old),
				"integrationDeliveries":  legacySessionSpoolPart(delivery),
				"integrationConnections": legacySessionSpoolPart(connection),
			}
			canonical := map[string]legacySessionJSONPart{
				"integrationDeliveries": fields["integrationDeliveries"], "integrationConnections": fields["integrationConnections"],
			}
			var want, got bytes.Buffer
			owner := &ResourceRead{store: repo}
			if err := legacySessionWriteJSONObject(t.Context(), owner, fields, &want); err != nil {
				owner.Close()
				t.Fatal(err)
			}
			owner.Close()
			owner = &ResourceRead{store: repo}
			if err := legacySessionWriteJSONObjectWithCanonical(t.Context(), owner, fields, canonical, &got); err != nil {
				owner.Close()
				t.Fatal(err)
			}
			owner.Close()
			if !bytes.Equal(got.Bytes(), want.Bytes()) {
				t.Fatal("canonical root copy changed complete sorted root bytes")
			}
			var root map[string]json.RawMessage
			if err := json.Unmarshal(got.Bytes(), &root); err != nil ||
				string(root["old"]) != `{"literal":"old space","duplicate":1,"duplicate":2}` {
				t.Fatal("an untrusted old-root spool skipped original compaction", err, string(root["old"]))
			}
			// Root writers borrow the caller's canonical spools; their own
			// temporary owner closing must not consume either input.
			for _, spool := range []*RecordSpool{delivery, connection} {
				var first [1]byte
				if n, err := spool.ReadAt(first[:], 0); n != 1 || err != nil {
					t.Fatal("root writer closed a borrowed input", n, err)
				}
			}
			delivery.Close()
			connection.Close()
			old.Close()
			if repo.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk ||
				metadataLogicalDescriptorsUsed.Load() != descriptors || recordSpoolBytes.Load() != spools {
				t.Fatal("canonical root copy retained temporary owners or input storage")
			}
		})
	}
}

type scaleP21SW344CancelWriter struct {
	bytes.Buffer
	cancel      context.CancelFunc
	chunks      int
	closeOnPart []byte
}

func (w *scaleP21SW344CancelWriter) Write(part []byte) (int, error) {
	n, err := w.Buffer.Write(part)
	if len(part) == metadataRecordChunkBytes {
		w.chunks++
		if w.chunks == 2 && w.closeOnPart == nil {
			w.cancel()
		}
	}
	if w.closeOnPart != nil && bytes.Equal(part, w.closeOnPart) {
		w.cancel()
	}
	return n, err
}

func TestScaleP21SW344CanonicalReceiptGuardsAndCancellation(t *testing.T) {
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{})
	spool := scaleP21SW344Spool(t, repo, []domain.IntegrationDelivery{{Payload: json.RawMessage(`"` + strings.Repeat("x", 2*MetadataRecordPageMaxBytes) + `"`)}})
	other := scaleP21SW344Spool(t, repo, []domain.IntegrationDelivery{})
	defer spool.Close()
	defer other.Close()
	part := legacySessionSpoolPart(spool)
	for _, name := range []string{"extra", "nonspool", "nil-spool", "offset", "size", "input", "actual-size"} {
		t.Run(name, func(t *testing.T) {
			fields := map[string]legacySessionJSONPart{"integrationDeliveries": part}
			canonical := map[string]legacySessionJSONPart{"integrationDeliveries": part}
			bad := part
			switch name {
			case "extra":
				canonical["absent"] = part
			case "nonspool":
				bad = legacySessionJSONPart{strings.NewReader("[]"), 0, 2}
				fields["integrationDeliveries"], canonical["integrationDeliveries"] = bad, bad
			case "nil-spool":
				bad.input = (*RecordSpool)(nil)
				fields["integrationDeliveries"], canonical["integrationDeliveries"] = bad, bad
			case "offset":
				bad.start++
				bad.size--
				fields["integrationDeliveries"], canonical["integrationDeliveries"] = bad, bad
			case "size":
				bad.size--
				fields["integrationDeliveries"], canonical["integrationDeliveries"] = bad, bad
			case "input":
				canonical["integrationDeliveries"] = legacySessionSpoolPart(other)
			case "actual-size":
				bad.size--
				fields["integrationDeliveries"] = bad
			}
			owner := &ResourceRead{store: repo}
			defer owner.Close()
			var output bytes.Buffer
			if err := legacySessionWriteJSONObjectWithCanonical(t.Context(), owner, fields, canonical, &output); !errors.Is(err, ErrBoundedMetadataCoverage) || output.Len() != 0 {
				t.Fatal("invalid canonical receipt reached output", err, output.Len())
			}
		})
	}
	for _, phase := range []string{"before", "during-copy", "final-chunk"} {
		t.Run("owner-close/"+phase, func(t *testing.T) {
			owner := &ResourceRead{store: repo}
			beforeWork := repo.MetadataWorkUsedForTesting()
			if err := owner.reserve(t.Context(), logicalJSONStreamWork); err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			input := part
			output := &scaleP21SW344CancelWriter{cancel: owner.Close}
			if phase == "before" {
				owner.Close()
			} else if phase == "final-chunk" {
				input = legacySessionSpoolPart(other)
				output.closeOnPart = []byte("[]")
			}
			err := legacySessionWriteJSONObjectWithCanonical(t.Context(), owner,
				map[string]legacySessionJSONPart{"integrationDeliveries": input},
				map[string]legacySessionJSONPart{"integrationDeliveries": input}, output)
			if !errors.Is(err, errMetadataLogicalClosed) || repo.MetadataWorkUsedForTesting() != beforeWork {
				t.Fatal("canonical copy escaped its output owner's lifetime", err, phase)
			}
			if phase == "before" && output.Len() != 0 || phase == "during-copy" && output.chunks != 2 ||
				phase == "final-chunk" && !bytes.HasSuffix(output.Bytes(), []byte("[]")) {
				t.Fatal("owner close was not checked at its exact boundary", phase, output.Len(), output.chunks)
			}
		})
	}
	t.Run("missing-or-unbounded-owner", func(t *testing.T) {
		for _, owner := range []*ResourceRead{nil, {store: &SQLiteStore{}}} {
			var output bytes.Buffer
			err := legacySessionWriteJSONObjectWithCanonical(t.Context(), owner,
				map[string]legacySessionJSONPart{"integrationDeliveries": part},
				map[string]legacySessionJSONPart{"integrationDeliveries": part}, &output)
			if !errors.Is(err, ErrBoundedMetadataCoverage) || output.Len() != 0 {
				t.Fatal("canonical copy accepted no bounded output owner", err, output.Len())
			}
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	owner := &ResourceRead{store: repo}
	beforeWork, beforeDisk, beforeSpools := repo.MetadataWorkUsedForTesting(), metadataLogicalDiskUsed.Load(), recordSpoolBytes.Load()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		t.Fatal(err)
	}
	output := &scaleP21SW344CancelWriter{cancel: cancel}
	err := legacySessionWriteJSONObjectWithCanonical(ctx, owner,
		map[string]legacySessionJSONPart{"integrationDeliveries": part},
		map[string]legacySessionJSONPart{"integrationDeliveries": part}, output)
	owner.Close()
	prefix := []byte(`{"integrationDeliveries":`)
	expected := make([]byte, 2*metadataRecordChunkBytes)
	if _, readErr := spool.ReadAt(expected, 0); readErr != nil {
		t.Fatal(readErr)
	}
	if !errors.Is(err, context.Canceled) || output.chunks != 2 ||
		!bytes.Equal(output.Bytes(), append(prefix, expected...)) ||
		repo.MetadataWorkUsedForTesting() != beforeWork || metadataLogicalDiskUsed.Load() != beforeDisk || recordSpoolBytes.Load() != beforeSpools {
		t.Fatal("mid-copy cancellation changed the exact prefix or released/retained an input owner", err, output.chunks)
	}
}

func TestScaleP21SW344IntegrationCanonicalReceiptConstructor(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	for _, event := range []string{"", "integration_delivery.claimed", "integration_delivery.retry", "integration_delivery.other"} {
		for _, invalid := range []bool{false, true} {
			t.Run(event+map[bool]string{false: "/valid", true: "/invalid-raw"}[invalid], func(t *testing.T) {
				frames, err := newMetadataLogicalBuilder(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer frames.Close()
				payload := json.RawMessage(` { "html":"<>&", "n":1, "n":2 } `)
				if invalid {
					payload = json.RawMessage("{")
				}
				node, err := frames.writeValue(reflect.ValueOf([]domain.IntegrationDelivery{{ID: "duplicate", Payload: payload}, {ID: "duplicate", Payload: payload}}), 0, false)
				if err != nil {
					t.Fatal(err)
				}
				tx, err := f.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				field, _ := metadataFieldByName("integrationDeliveries")
				plan := &legacyMetadataWritePlan{store: view, tx: tx, workspace: f.data.Workspace.URLKey}
				beforeDisk, beforeWork := metadataLogicalDiskUsed.Load(), view.MetadataWorkUsedForTesting()
				patch, err := plan.prepareAgentSessionLifecycleRoot(withIntegrationDeliveryStream(t.Context(), event),
					&legacyMetadataWriteField{field: field, before: node, next: node, full: true})
				if invalid {
					if err == nil || patch != nil {
						if patch != nil {
							patch.Close()
						}
						t.Fatal("failed typed encoder produced a canonical receipt", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if patch.split || patch.spool == nil || patch.part != legacySessionSpoolPart(patch.spool) ||
						patch.canonical != (event == "integration_delivery.claimed" || event == "integration_delivery.retry") {
						patch.Close()
						t.Fatal("canonical receipt escaped successful integration spool ownership")
					}
					if _, err := io.ReadAll(patch.part.reader()); err != nil {
						patch.Close()
						t.Fatal("receipt lost its producer spool", err)
					}
					patch.Close()
				}
				if metadataLogicalDiskUsed.Load() != beforeDisk || view.MetadataWorkUsedForTesting() != beforeWork {
					t.Fatal("receipt construction retained failed or closed publication work")
				}
			})
		}
	}
}
