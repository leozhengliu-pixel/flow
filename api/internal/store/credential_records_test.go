package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func sw17LegacyCredential(t *testing.T, kind string, raw []byte) (reflect.Value, error) {
	t.Helper()
	recordType, err := sw17CredentialType(kind)
	if err != nil {
		t.Fatal(err)
	}
	value := reflect.New(recordType)
	err = json.Unmarshal(raw, value.Interface())
	return value.Elem(), err
}

func TestScaleP21SW17CredentialTypedDifferential(t *testing.T) {
	for kind, fixtures := range map[string][]string{
		"oauthApplications": {
			`null`,
			`{}`,
			`{"ID":"first","id":"final","Name":"retained","name":null,"Scopes":["old","retained"],"scopes":[null],"redirectUris":null,"supportsClientCredentials":true,"createdAt":"2026-01-02T03:04:05.123456789+09:00","unknown":{"ignored":true}}`,
			`{"name":17}`,
			`{"createdAt":"invalid"}`,
		},
		"apiKeys": {
			`{"id":"key","scopes":null,"teamIds":[],"teamRestriction":""}`,
			`{"scopes":["old","retained"],"SCOPES":[null,"new"],"expiresAt":"2026-01-02T03:04:05Z","expiresAt":null}`,
			`{"scopes":[false]}`,
		},
		"integrationConnections": {
			`{"provider":"github","config":{"a":"one","kept":"old"},"Config":{"a":"two"},"scopes":null,"channels":[],"deliveryAttempts":17}`,
			`{"config":{"a":"old"},"CONFIG":null,"config":{"b":"new"},"OAuthState":"secret"}`,
			`{"config":{"a":12}}`,
		},
	} {
		for index, fixture := range fixtures {
			t.Run(fmt.Sprintf("%s/%d", kind, index), func(t *testing.T) {
				raw := []byte(fixture)
				want, oldErr := sw17LegacyCredential(t, kind, raw)
				recordType := want.Type()
				cost, err := sw17CredentialPreflight(t.Context(), bytes.NewReader(raw), int64(len(raw)), recordType)
				if err != nil {
					t.Fatal("preflight failed on small typed fixture", err)
				}
				got, newErr := sw17DecodeCredential(t.Context(), bytes.NewReader(raw), int64(len(raw)), recordType)
				if (oldErr == nil) != (newErr == nil) {
					t.Fatal("typed error behavior changed", oldErr, newErr)
				}
				if newErr == nil {
					left, _ := json.Marshal(want.Interface())
					right, _ := json.Marshal(got.Interface())
					if !bytes.Equal(left, right) {
						t.Fatalf("typed fields changed: old=%s new=%s", left, right)
					}
				}
				if cost < sw17CredentialFixedWork {
					t.Fatal("preflight lost fixed parser/encoder admission")
				}
			})
		}
	}
	raw := []byte(`{"id":"short","future":{"ignored":"` + strings.Repeat("x", 3<<20) + `"}}`)
	recordType := reflect.TypeFor[domain.OAuthApplication]()
	cost, err := sw17CredentialPreflight(t.Context(), bytes.NewReader(raw), int64(len(raw)), recordType)
	if err != nil || cost >= 1<<20 {
		t.Fatal("unknown future body entered typed admission", cost, err)
	}
	value, err := sw17DecodeCredential(t.Context(), bytes.NewReader(raw), int64(len(raw)), recordType)
	if err != nil || value.Interface().(domain.OAuthApplication).ID != "short" {
		t.Fatal("unknown future body changed typed output", err)
	}
	source := &SQLiteStore{}
	for _, fixture := range []struct {
		raw, ref string
		want     bool
	}{
		{`{"id":"first","ID":"final","id":null}`, "final", true},
		{`{"ID":"first","id":null}`, "final", false},
		{`{"ID":"\u0066inal"}`, "final", true},
		{`null`, "", true},
		{`{"id":"long-unrelated"}`, "", false},
		{`{"ID":"final"}`, "final\x00", false},
	} {
		got, err := source.sw17CredentialIDMatches(t.Context(), bytes.NewReader([]byte(fixture.raw)), int64(len(fixture.raw)), fixture.ref)
		if err != nil || got != fixture.want {
			t.Fatal("typed ID precedence/null/exact matching changed", fixture.raw, fixture.ref, got, err)
		}
	}
}

func TestScaleP21SW17CredentialInline(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"nil", "empty", "empty-id", "duplicate-id", "long-id"} {
		t.Run(mode, func(t *testing.T) {
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "oauthApplications"), workspace, "oauth_application.updated", "", nil, func(data *domain.Bootstrap) error {
				data.OAuthApplications = nil
				if mode == "empty" {
					data.OAuthApplications = []domain.OAuthApplication{}
				} else if mode != "nil" {
					id := ""
					if mode == "duplicate-id" {
						id = "duplicate"
					} else if mode == "long-id" {
						id = strings.Repeat("x", 192)
					}
					data.OAuthApplications = []domain.OAuthApplication{{ID: id, Name: "first"}, {ID: id, Name: "second"}}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			legacy, _ := f.WorkspaceMetadata(workspace)
			var actual []domain.OAuthApplication
			shape, err := probe.VisitCanonicalCredentialSettings(t.Context(), workspace, "oauthApplications", nil, func(_ context.Context, data domain.Bootstrap) error {
				actual = append(actual, data.OAuthApplications...)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !shape.Nil && actual == nil {
				actual = []domain.OAuthApplication{}
			}
			if !reflect.DeepEqual(legacy.OAuthApplications, actual) {
				t.Fatal("inline collection shape/order changed")
			}
		})
	}
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("inline canonical reader decoded full metadata or leaked work")
	}
}

func TestScaleP21SW17CredentialEscapingAdmission(t *testing.T) {
	// Raw stored JSON need not have come from json.Marshal: HTML bytes may
	// expand sixfold when a callback encodes the typed value.
	raw := []byte(`{"id":"escaped","description":"` + strings.Repeat("<>&", 1<<20) + "\u2028\u2029" + `","scopes":null}`)
	recordType := reflect.TypeFor[domain.OAuthApplication]()
	cost, err := sw17CredentialPreflight(t.Context(), bytes.NewReader(raw), int64(len(raw)), recordType)
	if err != nil {
		t.Fatal(err)
	}
	want, err := sw17LegacyCredential(t, "oauthApplications", raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(want.Interface())
	if err != nil {
		t.Fatal(err)
	}
	if cost < len(encoded)*3 || cost >= metadataWorkBytes {
		t.Fatal("preflight lost encoded growth/copy allowance", cost, len(encoded))
	}
	got, err := sw17DecodeCredential(t.Context(), bytes.NewReader(raw), int64(len(raw)), recordType)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := json.Marshal(got.Interface())
	if err != nil || !bytes.Equal(encoded, actual) {
		t.Fatal("raw HTML or Unicode encoding changed", err)
	}
	t.Logf("literal HTML encoded-input=%d encoded-output=%d admitted=%d", len(raw), len(encoded), cost)
}

func TestScaleP21SW17CredentialCanonicalVisitor(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fixtures := sw17CredentialFixtures(t)
	var app domain.OAuthApplication
	var key domain.APIKey
	var connection domain.IntegrationConnection
	if err := json.Unmarshal(fixtures["oauthApplications"], &app); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixtures["apiKeys"], &key); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixtures["integrationConnections"], &connection); err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "apiKeys", "oauthApplications", "integrationConnections"), workspace, "scale_p21_sw17.credential_fixture", "", nil, func(data *domain.Bootstrap) error {
		data.APIKeys, data.IntegrationConnections = []domain.APIKey{key}, []domain.IntegrationConnection{connection}
		data.OAuthApplications = []domain.OAuthApplication{app}
		for index := range 61 {
			data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{ID: fmt.Sprintf("small-%03d", index), Name: fmt.Sprintf("position-%03d", index)})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	legacy, _ := f.WorkspaceMetadata(workspace)
	spoolBefore := recordSpoolBytes.Load()
	events := 0
	probe.SetWebhookSink(func(string, domain.DomainEvent) { events++ })
	probe.SetRealtimeSink(func(string, domain.RealtimeEvent) { events++ })
	f.db.SetMaxOpenConns(1)
	defer f.db.SetMaxOpenConns(4)
	for _, kind := range []string{"apiKeys", "oauthApplications", "integrationConnections"} {
		var got []json.RawMessage
		shape, err := probe.VisitCanonicalCredentialSettings(t.Context(), workspace, kind, nil, func(ctx context.Context, data domain.Bootstrap) error {
			lease, ok := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease)
			if !ok || !lease.live || lease.bytes < sw17CredentialFixedWork {
				t.Fatal("typed callback lost its work lease")
			}
			var value any
			switch kind {
			case "apiKeys":
				value = data.APIKeys[0]
			case "oauthApplications":
				value = data.OAuthApplications[0]
			default:
				value = data.IntegrationConnections[0]
			}
			raw, err := json.Marshal(value)
			if err == nil {
				got = append(got, raw)
			}
			// Begin detached SQL, so this cannot deadlock a pool of one.
			var exists int
			if err == nil {
				err = probe.db.QueryRowContext(ctx, `SELECT 1`).Scan(&exists)
			}
			return err
		})
		if err != nil || !shape.WorkspaceFound || shape.Nil {
			t.Fatal("canonical visitor failed", kind, shape, err)
		}
		var want any
		switch kind {
		case "apiKeys":
			want = legacy.APIKeys
		case "oauthApplications":
			want = legacy.OAuthApplications
		default:
			want = legacy.IntegrationConnections
		}
		left, _ := json.Marshal(want)
		right, _ := json.Marshal(got)
		if !bytes.Equal(left, right) {
			t.Fatal("canonical typed family/order changed", kind)
		}
	}
	// An unmatched or later small point must not reserve/decode a preceding
	// 3 MiB Description when the process allowance cannot hydrate it.
	probe.metadataResidency.work.limit = 4 << 20
	for _, ref := range []string{"small-060", "missing", "small-060\x00", strings.Repeat("x", 192)} {
		visits := 0
		_, err := probe.VisitCanonicalCredentialSettings(t.Context(), workspace, "oauthApplications", &ref, func(_ context.Context, data domain.Bootstrap) error {
			visits++
			if data.OAuthApplications[0].ID != ref {
				t.Fatal("point selection changed exact ID semantics")
			}
			return nil
		})
		expected := 0
		if ref == "small-060" {
			expected = 1
		}
		if err != nil || visits != expected {
			t.Fatal("large unmatched app affected point selection", ref, visits, err)
		}
	}
	ref := app.ID
	_, err = probe.VisitCanonicalCredentialSettings(t.Context(), workspace, "oauthApplications", &ref, func(context.Context, domain.Bootstrap) error {
		t.Fatal("under-admitted selected app reached callback")
		return nil
	})
	if !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("selected record bypassed process admission", err)
	}
	probe.metadataResidency.work.limit = metadataWorkBytes
	callbackErr := errors.New("sw17 callback fixture")
	_, err = probe.VisitCanonicalCredentialSettings(t.Context(), workspace, "oauthApplications", &ref, func(context.Context, domain.Bootstrap) error { return callbackErr })
	if !errors.Is(err, callbackErr) {
		t.Fatal("callback failure was hidden", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := probe.VisitCanonicalCredentialSettings(cancelled, workspace, "oauthApplications", nil, func(context.Context, domain.Bootstrap) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled reader hid its error", err)
	}
	if events != 0 || probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 || recordSpoolBytes.Load() != spoolBefore {
		t.Fatal("canonical reader emitted events, decoded full metadata or leaked resources")
	}
}

func TestScaleP21SW17CredentialNativeMeasurement(t *testing.T) {
	for kind, raw := range sw17CredentialFixtures(t) {
		t.Run(kind, func(t *testing.T) {
			recordType, err := sw17CredentialType(kind)
			if err != nil {
				t.Fatal(err)
			}
			charged, err := sw17CredentialPreflight(t.Context(), bytes.NewReader(raw), int64(len(raw)), recordType)
			if err != nil {
				t.Fatal(err)
			}
			run := func() {
				value, err := sw17DecodeCredential(t.Context(), bytes.NewReader(raw), int64(len(raw)), recordType)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := json.Marshal(value.Interface()); err != nil {
					t.Fatal(err)
				}
			}
			run()
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for range 3 {
				run()
			}
			runtime.ReadMemStats(&after)
			t.Logf("native family=%s encoded=%d charged=%d bytes/op=%d allocs/op=%d", kind, len(raw), charged, (after.TotalAlloc-before.TotalAlloc)/3, (after.Mallocs-before.Mallocs)/3)
		})
	}
}

func sw17CredentialFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	app := domain.OAuthApplication{
		ID: "sw17-application", Name: "Large application", Description: strings.Repeat("x", 3<<20),
		Scopes: []string{"read"}, RedirectURIs: []string{}, CreatedAt: stamp,
	}
	key := domain.APIKey{ID: "sw17-key", Name: "Dense scopes", Scopes: []string{}, TeamIDs: nil}
	for range 32768 {
		key.Scopes = append(key.Scopes, "")
	}
	connection := domain.IntegrationConnection{ID: "sw17-connection", Provider: "github", Config: map[string]string{}}
	for index := range 4096 {
		connection.Config[fmt.Sprintf("key-%05d", index)] = "value"
	}
	result := map[string][]byte{}
	for kind, value := range map[string]any{"oauthApplications": app, "apiKeys": key, "integrationConnections": connection} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		result[kind] = raw
	}
	return result
}

// Keep these family-specific fixtures unchanged for the native comparison.
// This measures decode/encode allocations, not retained heap or capacity.
func TestScaleP21SW17CredentialRecordBaseline(t *testing.T) {
	for kind, raw := range sw17CredentialFixtures(t) {
		t.Run(kind, func(t *testing.T) {
			run := func() {
				var value any
				switch kind {
				case "apiKeys":
					value = &domain.APIKey{}
				case "oauthApplications":
					value = &domain.OAuthApplication{}
				default:
					value = &domain.IntegrationConnection{}
				}
				if err := json.Unmarshal(raw, value); err != nil {
					t.Fatal(err)
				}
				if _, err := json.Marshal(value); err != nil {
					t.Fatal(err)
				}
			}
			run()
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for range 3 {
				run()
			}
			runtime.ReadMemStats(&after)
			t.Logf("legacy family=%s encoded=%d bytes/op=%d allocs/op=%d", kind, len(raw), (after.TotalAlloc-before.TotalAlloc)/3, (after.Mallocs-before.Mallocs)/3)
		})
	}
}
