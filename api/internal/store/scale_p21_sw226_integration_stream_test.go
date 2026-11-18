package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW226Framed(t *testing.T, repo *SQLiteStore, kind reflect.Type, raw string) {
	t.Helper()
	disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
	frames, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		frames.Close()
		frames.Close()
		if repo.MetadataWorkUsedForTesting() != 0 ||
			metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors || recordSpoolBytes.Load() != spools {
			t.Error("canonical integration framing retained native, disk or descriptor ownership")
		}
	}()
	node, err := integrationCanonicalRecord(t.Context(), repo, frames, legacySessionJSONPart{strings.NewReader(raw), 0, int64(len(raw))}, kind)
	if err != nil {
		t.Fatal("canonical integration framing", err)
	}
	if repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("canonical integration frame retained its temporary decode owner")
	}
	owner := &ResourceRead{store: repo}
	defer owner.Close()
	actual, err := owner.node(t.Context(), node, false)
	if err != nil {
		t.Fatal("owned canonical verification decode", err)
	}
	expected := reflect.New(kind)
	if err := json.Unmarshal([]byte(raw), expected.Interface()); err != nil {
		t.Fatal("canonical fixture is not a supported typed value", err)
	}
	if !reflect.DeepEqual(actual.Interface(), expected.Elem().Interface()) {
		t.Fatal("canonical typed values changed bytes, folded/repeated precedence or nil/empty state")
	}
	spool, err := legacySessionNodeSpool(t.Context(), owner, node)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	output := make([]byte, int(spool.bytes))
	if _, err := spool.ReadAt(output, 0); err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(expected.Elem().Interface())
	if err != nil || !bytes.Equal(output, wire) {
		t.Fatal("canonical integration framing changed standard typed JSON", err)
	}
}

func TestScaleP21SW226IntegrationCanonicalFrames(t *testing.T) {
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {}})
	for _, group := range []struct {
		name string
		kind reflect.Type
		raw  []string
	}{
		{"delivery", reflect.TypeFor[domain.IntegrationDelivery](), []string{
			`null`,
			`{}`,
			`{"payload":null}`,
			`{"payload":{},"previousValues":[]}`,
			`{"payload":"","previousValues":null}`,
			`{"id":"delivery","ID":null,"status":"pending","STATUS":"delivering","lastError":"first","LASTERROR":null}`,
			`{"id":"delivery","payload":{"a":1,"a":2},"PAYLOAD":[null,9007199254740993,1e-7],"previousValues":{"html":"<>&\u2028","escaped":"\\u003c"}}`,
			`{"payload":null,"PAYLOAD": { "html": "<>&", "number": 9007199254740993 },"previousValues":{},"PREVIOUSVALUES":null}`,
			`{"payload":{"old":true},"payload":null,"PAYLOAD":{"last":true},"createdAt":"2026-01-02T03:04:05.000000006Z"}`,
			`{"id":"\u0064elivery","ignored":{"nested":[{"body":"ignored"}]},"payload":"` + strings.Repeat("large raw body ", 80000) + `"}`,
		}},
		{"connection", reflect.TypeFor[domain.IntegrationConnection](), []string{
			`null`,
			`{}`,
			`{"id":"connection","config":null,"scopes":null,"channels":null}`,
			`{"id":"connection","config":{},"scopes":[],"channels":[]}`,
			`{"config":{"a":"old","same":"first","same":null},"CONFIG":{"b":"new","a":null}}`,
			`{"config":{"a":"old"},"CONFIG":null,"config":{"b":"new","html":"<>&\u2028"}}`,
			`{"config":{"\u0061":"old","a":"last"},"name":"old","NAME":null,"name":"new\u0000"}`,
			`{"scopes":["old-a","old-b","old-c"],"SCOPES":["short"],"scopes":[null,null,null]}`,
			`{"channels":["old-a","old-b"],"CHANNELS":[null],"channels":[null,null,"new"]}`,
			`{"scopes":["discard"],"scopes":[],"scopes":[null]}`,
			`{"channels":["discard"],"CHANNELS":null,"channels":[null]}`,
			`{"scopes":["retain"],"scopes":[null],"channels":[],"lastDeliveryAt":"2026-01-02T03:04:05.000000006Z"}`,
			`{"config":{"large":"` + strings.Repeat("large config ", 80000) + `"},"scopes":["` + strings.Repeat("large scope ", 80000) + `"],"channels":["` + strings.Repeat("large channel ", 80000) + `"]}`,
		}},
	} {
		t.Run(group.name, func(t *testing.T) {
			for index, raw := range group.raw {
				t.Run(fmt.Sprint(index), func(t *testing.T) {
					scaleP21SW226Framed(t, repo, group.kind, raw)
				})
			}
		})
	}
}

func TestScaleP21SW226IntegrationEveryLargeString(t *testing.T) {
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {}})
	large := strings.Repeat("large string <>& \u2028 \ufffd ", 55000)
	for _, kind := range []reflect.Type{reflect.TypeFor[domain.IntegrationDelivery](), reflect.TypeFor[domain.IntegrationConnection]()} {
		for index := range kind.NumField() {
			field := kind.Field(index)
			if field.Type.Kind() != reflect.String || field.Tag.Get("json") == "-" || field.PkgPath != "" {
				continue
			}
			t.Run(kind.Name()+"/"+field.Name, func(t *testing.T) {
				value := reflect.New(kind).Elem()
				value.Field(index).SetString(large)
				raw, err := json.Marshal(value.Interface())
				if err != nil {
					t.Fatal(err)
				}
				scaleP21SW226Framed(t, repo, kind, string(raw))
			})
		}
	}
}

func TestScaleP21SW226IntegrationLoadedDefaultsAndIgnoredFields(t *testing.T) {
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {}})
	frames, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer frames.Close()
	raw := `{"id":"connection","config":null,"scopes":null,"channels":null,"lastError":"peer"}`
	node, err := integrationConnectionCanonicalRecord(t.Context(), repo, frames, legacySessionJSONPart{strings.NewReader(raw), 0, int64(len(raw))})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := integrationLoadedCanonical(t.Context(), frames, node)
	if err != nil {
		t.Fatal(err)
	}
	owner := &ResourceRead{store: repo}
	defer owner.Close()
	actualRaw, err := owner.node(t.Context(), node, false)
	if err != nil {
		t.Fatal(err)
	}
	actualLoaded, err := owner.node(t.Context(), loaded, false)
	if err != nil {
		t.Fatal(err)
	}
	ordinary := actualRaw.Interface().(domain.IntegrationConnection)
	normalized := actualLoaded.Interface().(domain.IntegrationConnection)
	if ordinary.Scopes != nil || ordinary.Channels != nil || ordinary.Config != nil ||
		normalized.Scopes == nil || normalized.Channels == nil || normalized.Config != nil {
		t.Fatal("loaded-only normalization changed the raw typed image or Config")
	}
	field, _ := metadataFieldByName("integrationConnections")
	expected, err := normalizedMetadataRecord(field, ordinary.ID, reflect.ValueOf(ordinary))
	got, marshalErr := json.Marshal(normalized)
	if err != nil || marshalErr != nil || !bytes.Equal(expected, got) {
		t.Fatal("streamed loaded comparison differs from literal normalize", err, marshalErr)
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	nativeKeep := domain.IntegrationConnection{SecretHash: "native-secret", LastDeliveryAt: &at}
	keep, err := frames.writeValue(reflect.ValueOf(nativeKeep), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := metadataLogicalRetryFramedRecord(t.Context(), keep, keep.typ,
		metadataAggregateNativeNode{value: reflect.ValueOf(nativeKeep)})
	if err != nil {
		t.Fatal(err)
	}
	keep, err = frames.writeAggregateNode(retained, 0)
	if err != nil {
		t.Fatal(err)
	}
	keepValue, err := owner.node(t.Context(), keep, false)
	if err != nil || !reflect.DeepEqual(keepValue.Interface(), nativeKeep) {
		t.Fatal("native keep fixture lost ignored fields or its nonnil time pointer", err)
	}
	kept, err := integrationKeepIgnored(t.Context(), frames, node, keep)
	if err != nil {
		t.Fatal(err)
	}
	value, err := owner.node(t.Context(), kept, false)
	if err != nil || value.Interface().(domain.IntegrationConnection).SecretHash != "native-secret" ||
		value.Interface().(domain.IntegrationConnection).LastDeliveryAt != nil {
		t.Fatal("SQL-ignored connection fields lost the corresponding native image", err)
	}
}

type scaleP21SW226ReadAt func([]byte, int64) (int, error)

func (read scaleP21SW226ReadAt) ReadAt(dst []byte, offset int64) (int, error) {
	return read(dst, offset)
}

func TestScaleP21SW226IntegrationCanonicalCancellationAndRelease(t *testing.T) {
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {}})
	for _, kind := range []reflect.Type{reflect.TypeFor[domain.IntegrationDelivery](), reflect.TypeFor[domain.IntegrationConnection]()} {
		for _, moment := range []string{"before", "during"} {
			t.Run(kind.Name()+"/"+moment, func(t *testing.T) {
				disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				frames, err := newMetadataLogicalBuilder(ctx)
				if err != nil {
					t.Fatal(err)
				}
				raw := `{"id":"delivery","payload":{"body":"` + strings.Repeat("large body ", 100000) + `"}}`
				if kind == reflect.TypeFor[domain.IntegrationConnection]() {
					raw = `{"id":"connection","config":{"body":"` + strings.Repeat("large config ", 100000) + `"},"scopes":["retained"]}`
				}
				input := strings.NewReader(raw)
				readBytes := 0
				reader := scaleP21SW226ReadAt(func(dst []byte, offset int64) (int, error) {
					n, err := input.ReadAt(dst, offset)
					readBytes += n
					if moment == "during" && readBytes >= 64<<10 {
						cancel()
					}
					return n, err
				})
				if moment == "before" {
					cancel()
				}
				node, runErr := integrationCanonicalRecord(ctx, repo, frames, legacySessionJSONPart{reader, 0, int64(len(raw))}, kind)
				frames.Close()
				frames.Close()
				if !errors.Is(runErr, context.Canceled) || node != (metadataLogicalNode{}) {
					t.Fatal("canonical cancellation exposed a partial integration node", runErr)
				}
				if moment == "before" && readBytes != 0 || moment == "during" && readBytes < 64<<10 {
					t.Fatal("canonical cancellation did not reach the requested input boundary", moment, readBytes)
				}
				if repo.MetadataWorkUsedForTesting() != 0 ||
					metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors || recordSpoolBytes.Load() != spools {
					t.Fatal("cancelled canonical integration retained ownership")
				}
			})
		}
	}
}

func TestScaleP21SW226IntegrationClosedStreamGuards(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	delivery := domain.IntegrationDelivery{ID: "delivery", ConnectionID: "connection", Status: "pending", Payload: json.RawMessage(`{"body":"unchanged"}`)}
	connection := domain.IntegrationConnection{ID: "connection", Config: map[string]string{"body": "unchanged"}}
	for _, test := range []struct {
		name, field, event string
		before, after      any
	}{
		{"missing-marker", "integrationDeliveries", "", delivery, delivery},
		{"wrong-event", "integrationDeliveries", "integration_delivery.unknown", delivery, delivery},
		{"wrong-kind", "integrationDeliveries", "integration_delivery.claimed", connection, connection},
		{"undeclared-payload", "integrationDeliveries", "integration_delivery.claimed", delivery, func() domain.IntegrationDelivery {
			next := delivery
			next.Payload = json.RawMessage(`{"body":"changed"}`)
			return next
		}()},
		{"undeclared-id", "integrationDeliveries", "integration_delivery.retry", delivery, func() domain.IntegrationDelivery {
			next := delivery
			next.ID = "changed"
			return next
		}()},
		{"undeclared-config", "integrationConnections", "integration_delivery.retry", connection, func() domain.IntegrationConnection {
			next := connection
			next.Config = map[string]string{"body": "changed"}
			return next
		}()},
		{"claim-cannot-change-connection", "integrationConnections", "integration_delivery.claimed", connection, func() domain.IntegrationConnection {
			next := connection
			next.LastDeliveryAt = &at
			return next
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames, err := newMetadataLogicalBuilder(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer frames.Close()
			before, err := frames.writeValue(reflect.ValueOf(test.before), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			after, err := frames.writeValue(reflect.ValueOf(test.after), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if test.event != "" {
				ctx = withIntegrationDeliveryStream(ctx, test.event)
			}
			field, _ := metadataFieldByName(test.field)
			selected := &legacyMetadataWriteField{field: field, rows: []legacyMetadataWriteRow{{id: "delivery", before: before, next: after, changed: true}}}
			// No store/transaction is installed. These guards must decline
			// before any loading, admission, lock or SQL-stage work.
			plan := &legacyMetadataWritePlan{}
			if handled, err := plan.rebaseIntegrationDeliveryRow(ctx, selected, 0); handled || err != nil {
				t.Fatal("invalid context/delta entered streamed integration rebase", handled, err)
			}
			if handled, err := plan.writeIntegrationDeliveryRow(ctx, selected, &selected.rows[0], 0); handled || err != nil {
				t.Fatal("invalid context/delta entered streamed integration write", handled, err)
			}
			if handled, err := plan.writeIntegrationDeliveryNativeRow(ctx, selected, &selected.rows[0], 0, true); handled || err != nil {
				t.Fatal("forced write bypassed the integration context/delta guard", handled, err)
			}
		})
	}
}

func TestScaleP21SW226IntegrationValidatedObjectEquality(t *testing.T) {
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {}})
	large := strings.Repeat("unchanged payload ", 6000)
	for _, test := range []struct {
		name, left, right string
		equal             bool
	}{
		{"identical-large-bytes", `{"payload":"` + large + `"}`, `{"payload":"` + large + `"}`, true},
		{"whitespace", `{"payload": {"number":1,"nested":[true,null]} }`, `{"payload":{"number": 1, "nested": [true, null]}}`, true},
		{"escaping", `{"payload":"<>&"}`, `{"payload":"\u003c\u003e\u0026"}`, true},
		{"changed-late-byte", `{"payload":"` + large + `a"}`, `{"payload":"` + large + `b"}`, false},
		{"missing-null", `{}`, `{"payload":null}`, false},
		{"null", `{"payload":null}`, `{"payload": null}`, true},
		{"different-property", `{"payload":null}`, `{"previousValues":null}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			owner := &ResourceRead{store: repo}
			defer owner.Close()
			var fields [2]map[string]legacySessionJSONPart
			for index, raw := range []string{test.left, test.right} {
				var err error
				fields[index], err = legacySessionJSONObject(t.Context(), owner, legacySessionJSONPart{strings.NewReader(raw), 0, int64(len(raw))})
				if err != nil {
					t.Fatal(err)
				}
			}
			got, err := integrationObjectsEqual(t.Context(), owner, fields[0], fields[1])
			if err != nil || got != test.equal {
				t.Fatal("validated integration equality", got, err, "want", test.equal)
			}
			want, err := legacySessionObjectsEqual(t.Context(), owner, fields[0], fields[1])
			if err != nil || got != want {
				t.Fatal("integration equality differs from legacy comparator", got, want, err)
			}
		})
	}
	t.Run("nil-and-missing", func(t *testing.T) {
		owner := &ResourceRead{store: repo}
		defer owner.Close()
		for _, test := range []struct {
			left, right map[string]legacySessionJSONPart
			equal       bool
		}{
			{nil, map[string]legacySessionJSONPart{}, true},
			{map[string]legacySessionJSONPart{"payload": {}}, map[string]legacySessionJSONPart{"payload": {}}, true},
			{map[string]legacySessionJSONPart{"payload": {}}, nil, false},
		} {
			got, err := integrationObjectsEqual(t.Context(), owner, test.left, test.right)
			if err != nil || got != test.equal {
				t.Fatal("nil integration equality", got, err, "want", test.equal)
			}
		}
	})
	t.Run("malformed-before-equality", func(t *testing.T) {
		for _, raw := range []string{`{"payload":"unfinished}`, `{"payload":[1,]}`, `{"payload":true} trailing`, `{"payload":01}`} {
			owner := &ResourceRead{store: repo}
			_, err := legacySessionJSONObject(t.Context(), owner, legacySessionJSONPart{strings.NewReader(raw), 0, int64(len(raw))})
			owner.Close()
			if err == nil {
				t.Fatal("malformed identical bytes reached validated-object equality", raw)
			}
		}
	})
}

func TestScaleP21SW226IntegrationWriterFastProof(t *testing.T) {
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {}})
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	delivery := domain.IntegrationDelivery{ID: "delivery", Attempts: 2, Status: "pending", UpdatedAt: at}
	connection := domain.IntegrationConnection{ID: "connection", DeliveryAttempts: 2}
	for _, test := range []struct {
		name          string
		before, after any
		force, skip   bool
	}{
		{"forced", delivery, delivery, true, true},
		{"unchanged", delivery, delivery, false, false},
		{"attempts", delivery, func() domain.IntegrationDelivery { next := delivery; next.Attempts++; return next }(), false, true},
		{"status", delivery, func() domain.IntegrationDelivery { next := delivery; next.Status = "delivering"; return next }(), false, true},
		{"delivery-attempts", connection, func() domain.IntegrationConnection { next := connection; next.DeliveryAttempts++; return next }(), false, true},
		{"ignored", connection, func() domain.IntegrationConnection { next := connection; next.SecretHash = "ignored"; return next }(), false, false},
		{"nil-time", connection, func() domain.IntegrationConnection { next := connection; next.LastDeliveryAt = &at; return next }(), false, false},
		{"canonical-time", delivery, func() domain.IntegrationDelivery {
			next := delivery
			next.UpdatedAt = at.In(time.FixedZone("sw226-native", 0))
			return next
		}(), false, false},
		{"normalized-invalid-utf8", func() domain.IntegrationDelivery { next := delivery; next.Status = "\xff"; return next }(),
			func() domain.IntegrationDelivery { next := delivery; next.Status = "\xfe"; return next }(), false, false},
		{"large-status", delivery, func() domain.IntegrationDelivery {
			next := delivery
			next.Status = strings.Repeat("s", 5000)
			return next
		}(), false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "canonical-time" {
				left, leftErr := json.Marshal(test.before)
				right, rightErr := json.Marshal(test.after)
				if leftErr != nil || rightErr != nil || reflect.DeepEqual(test.before, test.after) || !bytes.Equal(left, right) {
					t.Fatal("time fixture must have distinct native values and identical JSON", leftErr, rightErr)
				}
			}
			frames, err := newMetadataLogicalBuilder(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer frames.Close()
			before, err := frames.writeValue(reflect.ValueOf(test.before), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			after, err := frames.writeValue(reflect.ValueOf(test.after), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			skip, err := integrationCanSkipBeforeJSON(t.Context(), repo, before, after, test.force)
			if err != nil || skip != test.skip {
				t.Fatal("integration before-JSON proof", skip, err, "want", test.skip)
			}
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("integration scalar proof retained work")
			}
		})
	}
}

func TestScaleP21SW226IntegrationInvalidBeforeTime(t *testing.T) {
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {}})
	invalid := time.Date(10000, 1, 2, 3, 4, 5, 0, time.UTC)
	valid := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	large := strings.Repeat("large unchanged body ", 5000)
	for _, test := range []struct {
		name, field   string
		before, after any
	}{
		{"updated-at", "integrationDeliveries",
			domain.IntegrationDelivery{ID: "delivery", Attempts: 2, UpdatedAt: invalid, Payload: json.RawMessage(`"` + large + `"`)},
			domain.IntegrationDelivery{ID: "delivery", Attempts: 3, UpdatedAt: valid, Payload: json.RawMessage(`"` + large + `"`)}},
		{"next-attempt-at", "integrationDeliveries",
			domain.IntegrationDelivery{ID: "delivery", Attempts: 2, NextAttemptAt: &invalid, Payload: json.RawMessage(`"` + large + `"`)},
			domain.IntegrationDelivery{ID: "delivery", Attempts: 3, NextAttemptAt: &valid, Payload: json.RawMessage(`"` + large + `"`)}},
		{"last-delivery-at", "integrationConnections",
			domain.IntegrationConnection{ID: "connection", DeliveryAttempts: 2, LastDeliveryAt: &invalid, Config: map[string]string{"large": large}},
			domain.IntegrationConnection{ID: "connection", DeliveryAttempts: 3, LastDeliveryAt: &valid, Config: map[string]string{"large": large}}},
	} {
		for _, force := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/force%t", test.name, force), func(t *testing.T) {
				frames, err := newMetadataLogicalBuilder(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer frames.Close()
				before, err := frames.writeValue(reflect.ValueOf(test.before), 0, false)
				if err != nil {
					t.Fatal(err)
				}
				after, err := frames.writeValue(reflect.ValueOf(test.after), 0, false)
				if err != nil {
					t.Fatal(err)
				}
				ctx := withIntegrationDeliveryStream(t.Context(), "integration_delivery.retry")
				skip, err := integrationCanSkipBeforeJSON(ctx, repo, before, after, force)
				if err != nil || skip {
					t.Fatal("invalid old time bypassed before JSON validation", skip, err)
				}
				owner := &ResourceRead{store: repo}
				defer owner.Close()
				spool, original := legacySessionNodeSpool(ctx, owner, before)
				if spool != nil {
					spool.Close()
				}
				if original == nil {
					t.Fatal("invalid old time fixture unexpectedly marshaled")
				}
				field, _ := metadataFieldByName(test.field)
				row := &legacyMetadataWriteRow{id: "row", before: before, next: after, changed: true}
				plan := &legacyMetadataWritePlan{store: repo}
				handled, err := plan.writeIntegrationDeliveryNativeRow(ctx, &legacyMetadataWriteField{field: field}, row, 0, force)
				if !handled || err == nil || err.Error() != original.Error() {
					t.Fatal("writer changed the original before-time failure", handled, err, original)
				}
			})
		}
	}
}

func TestScaleP21SW226IntegrationStoredJSONIdentity(t *testing.T) {
	for _, test := range []struct {
		name, raw string
		identity  bool
	}{
		{"empty", "", true},
		{"ordinary", `{"body":"ordinary <>&"}`, true},
		{"nul-token", `{"body":"a\u0000b"}`, false},
		{"escaped-backslash", `{"body":"a\\u0000b"}`, false},
		{"long-no-token", `{"body":"` + strings.Repeat("n", 3*metadataRecordChunkBytes) + `"}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			spool, err := newMetadataLogicalRecordSpool(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer spool.Close()
			if _, err := io.WriteString(spool, test.raw); err != nil {
				t.Fatal(err)
			}
			identity, err := integrationStoredJSONIdentity(t.Context(), spool)
			if err != nil || identity != test.identity {
				t.Fatal("stored JSON identity", identity, err, "want", test.identity)
			}
			if identity && !bytes.Equal(StripJSONNUL([]byte(test.raw)), []byte(test.raw)) {
				t.Fatal("identity probe skipped a required NUL transformation")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := integrationStoredJSONIdentity(ctx, spool); !errors.Is(err, context.Canceled) {
				t.Fatal("identity probe ignored cancellation", err)
			}
		})
	}
	for split := 1; split < len(`\u0000`); split++ {
		t.Run(fmt.Sprintf("chunk-boundary-%d", split), func(t *testing.T) {
			raw := `"` + strings.Repeat("n", metadataRecordChunkBytes-split-1) + `\u0000"`
			spool, err := newMetadataLogicalRecordSpool(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer spool.Close()
			if _, err := io.WriteString(spool, raw); err != nil {
				t.Fatal(err)
			}
			identity, err := integrationStoredJSONIdentity(t.Context(), spool)
			if err != nil || identity {
				t.Fatal("identity probe missed a split NUL token", split, identity, err)
			}
		})
	}
}

type scaleP21SW226Result struct {
	delivery, connection, root json.RawMessage
	payloads, previous         []json.RawMessage
	rows                       [][]any
	secret                     string
	conflict                   bool
	conflicts                  []string
	peak, sql, rootWrites      int
}

func scaleP21SW226ConflictKeys(err error) []string {
	keys, _ := conflictedMetadataKeys(err)
	names := make([]string, len(keys))
	for index, key := range keys {
		names[index] = key.field + "\x00" + key.key
	}
	sort.Strings(names)
	return names
}

type scaleP21SW226PeerMutation struct {
	mutate func(*domain.Bootstrap) error
	shapes map[string]string
}

func scaleP21SW226Clone(data domain.Bootstrap) domain.Bootstrap {
	clone := cloneBootstrap(data)
	for index := range clone.IntegrationConnections {
		// The direct row-kernel oracle deliberately exercises the existing
		// decodeMetadataRecord keep contract for SQL-ignored native fields.
		clone.IntegrationConnections[index].SecretHash = data.IntegrationConnections[index].SecretHash
	}
	return clone
}

func scaleP21SW226Capture(ctx context.Context, data domain.Bootstrap, stamp copyStamp) (*metadataLogicalSource, error) {
	source, err := captureMetadataLogicalSource(ctx, data, stamp)
	if err != nil {
		return nil, err
	}
	defer source.Release()
	frames, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer frames.Close()
	// This direct row-kernel fixture keeps the native json:"-" values that
	// its literal clone supplies to decodeMetadataRecord's keep argument.
	index, _, _ := metadataLogicalField("integrationConnections")
	var connections []metadataLogicalNode
	err = source.fields[index].Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		retained, err := metadataLogicalRetryFramedRecord(ctx, node, node.typ,
			metadataAggregateNativeNode{value: reflect.ValueOf(data.IntegrationConnections[key.Int()])})
		if err != nil {
			return err
		}
		connection, err := frames.writeAggregateNode(retained, 0)
		if err == nil {
			connections = append(connections, connection)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == "integrationConnections" {
			return legacySessionMessagesNode{shape: node, items: connections}, true, nil
		}
		return metadataAggregateStoredNode{node}, false, nil
	})
}

func scaleP21SW226LogPhysicalRows(t *testing.T, literal, bounded [][]any) {
	t.Helper()
	for _, table := range []struct {
		name string
		rows [][]any
	}{{"literal", literal}, {"bounded", bounded}} {
		for _, row := range table.rows {
			t.Logf("%s physical row field=%q key=%q order=%v bytes=%d", table.name, row[0], row[1], row[2], len(row[3].(json.RawMessage)))
		}
	}
	for index := range min(len(literal), len(bounded)) {
		left, right := literal[index], bounded[index]
		if left[0] != right[0] || left[1] != right[1] || bytes.Equal(left[3].(json.RawMessage), right[3].(json.RawMessage)) {
			continue
		}
		var a, b map[string]json.RawMessage
		if json.Unmarshal(left[3].(json.RawMessage), &a) != nil || json.Unmarshal(right[3].(json.RawMessage), &b) != nil {
			t.Fatal("physical diagnostic encountered invalid stored JSON")
		}
		names := map[string]bool{}
		for name := range a {
			names[name] = true
		}
		for name := range b {
			names[name] = true
		}
		var different []string
		for name := range names {
			if !bytes.Equal(a[name], b[name]) {
				different = append(different, name)
			}
		}
		sort.Strings(different)
		t.Logf("physical data field=%q key=%q differing_JSON_fields=%v", left[0], left[1], different)
	}
}

func scaleP21SW226Execute(t *testing.T, fixture scaleP09Fixture, view *SQLiteStore, before, requested domain.Bootstrap, peers map[string][]byte, deleted string, bounded bool, cancelPhase string, peerMutations ...scaleP21SW226PeerMutation) scaleP21SW226Result {
	t.Helper()
	workspace := before.Workspace.URLKey
	if err := fixture.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := fixture.MutateWorkspace(t.Context(), workspace, "scale_p21.sw226_seed", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationDeliveries = scaleP21SW226Clone(before).IntegrationDeliveries
		data.IntegrationConnections = scaleP21SW226Clone(before).IntegrationConnections
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"integrationDeliveries", "integrationConnections"} {
		id := before.IntegrationDeliveries[0].ID
		if name == "integrationConnections" {
			id = before.IntegrationConnections[0].ID
		}
		if strings.IndexByte(id, 0) < 0 {
			continue
		}
		want := id
		if fixture.dialect == "postgres" {
			want = strings.ReplaceAll(want, "\x00", "")
		}
		var physical string
		if err := fixture.db.QueryRowContext(t.Context(), `SELECT record_key FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, name, id).Scan(&physical); err != nil || physical != want {
			t.Fatal("native-NUL fixture did not exercise the dialect's physical key identity", name, fixture.dialect, physical, want, err)
		}
	}
	for _, peer := range peerMutations {
		if err := fixture.MutateWorkspace(t.Context(), workspace, "scale_p21.sw226_peer_shape", "", nil, peer.mutate); err != nil {
			t.Fatal(err)
		}
		for name, want := range peer.shapes {
			var shape sql.NullString
			query := "SELECT " + fixture.jsonText("data", metadataCollectionsKey+"."+name) + " FROM workspace_states WHERE workspace_key=?"
			if err := fixture.db.QueryRowContext(t.Context(), query, workspace).Scan(&shape); err != nil || shape.String != want {
				t.Fatal("peer shape transition was not installed", name, shape, want, err)
			}
			var records int
			if err := fixture.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=?`, workspace, name).Scan(&records); err != nil ||
				want == "" && records != 0 || want == "array" && records == 0 {
				t.Fatal("peer shape transition kept the wrong canonical rows", name, records, want, err)
			}
		}
	}
	if len(peers) != 0 || deleted != "" {
		tx, err := fixture.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"integrationConnections", "integrationDeliveries"} {
			id := before.IntegrationDeliveries[0].ID
			if name == "integrationConnections" {
				id = before.IntegrationConnections[0].ID
			}
			if name == deleted {
				err = deleteMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{name, id})
			} else if raw, found := peers[name]; found {
				_, err = tx.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`, StripJSONNUL(raw), workspace, name, id)
			}
			if err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if alias, found := peers["physical:"+name]; found {
				if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET record_key=? WHERE workspace_key=? AND field=? AND record_key=?`,
					string(alias), workspace, name, id); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				var physical string
				if err := tx.QueryRowContext(t.Context(), `SELECT record_key FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`,
					workspace, name, string(alias)).Scan(&physical); err != nil || physical != string(alias) {
					tx.Rollback()
					t.Fatal("physical-key alias was not installed", name, physical, string(alias), err)
				}
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: fixture.db, workspace: workspace, reader: tx})
	baseline := scaleP21SW77Image(t, tx, workspace)
	result := scaleP21SW226Result{}
	if !bounded {
		target := scaleP21SW226Clone(requested)
		_, err = fixture.writeWorkspaceMetadataDelta(ctx, tx, workspace, scaleP21SW226Clone(before), &target)
		if errors.Is(err, ErrConcurrentWrite) {
			result.conflict = true
			result.conflicts = scaleP21SW226ConflictKeys(err)
			return result
		}
		if err != nil {
			t.Fatal("literal integration row delta", err)
		}
		result.delivery, err = json.Marshal(target.IntegrationDeliveries)
		if err == nil {
			result.connection, err = json.Marshal(target.IntegrationConnections)
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(target.IntegrationConnections) != 0 {
			result.secret = target.IntegrationConnections[0].SecretHash
		}
		for _, delivery := range target.IntegrationDeliveries {
			result.payloads = append(result.payloads, bytes.Clone(delivery.Payload))
			result.previous = append(result.previous, bytes.Clone(delivery.PreviousValues))
		}
	} else {
		left, err := scaleP21SW226Capture(ctx, before, copyStamp{workspaceID: before.Workspace.ID})
		if err != nil {
			t.Fatal(err)
		}
		defer left.Release()
		right, err := scaleP21SW226Capture(ctx, requested, left.Stamp())
		if err != nil {
			t.Fatal(err)
		}
		defer right.Release()
		ctx = withIntegrationDeliveryStream(ctx, "integration_delivery.retry")
		ctx = context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, []string{"integrationDeliveries", "integrationConnections"})
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		reads, cancelled := 0, false
		ctx = WithSQLStatementObserver(ctx, func(query string) {
			result.peak = max(result.peak, view.MetadataWorkUsedForTesting())
			result.sql++
			lower := strings.ToLower(strings.TrimSpace(query))
			if strings.HasPrefix(lower, "update workspace_states set ") {
				result.rootWrites++
			}
			if strings.Contains(lower, "select substr(") && strings.Contains(lower, "workspace_metadata_records") {
				reads++
			}
			if !cancelled && (cancelPhase == "read" && reads == 4 ||
				cancelPhase == "write" && strings.Contains(lower, "update bounded_metadata_write_stage set data")) {
				cancelled = true
				cancel()
			}
		})
		merged, runErr := view.persistLegacyMetadataImage(ctx, tx, workspace, left, right)
		if cancelPhase != "" {
			if merged != nil {
				merged.Release()
			}
			if !cancelled || !errors.Is(runErr, context.Canceled) {
				t.Fatal("integration cancellation did not reach its real boundary", cancelPhase, cancelled, runErr)
			}
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				t.Fatal(err)
			}
			verify, err := fixture.db.BeginTx(t.Context(), metadataReadOptions(fixture.dialect))
			if err != nil {
				t.Fatal(err)
			}
			unchanged := reflect.DeepEqual(baseline, scaleP21SW77Image(t, verify, workspace))
			verify.Rollback()
			if !unchanged || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("cancelled integration delta changed durable rows or retained work")
			}
			if tx.metadataSQLStages != 0 || len(tx.metadataSQLStageOwners) != 0 {
				t.Fatal("cancelled integration delta retained an owned SQL stage")
			}
			return result
		}
		if errors.Is(runErr, ErrConcurrentWrite) {
			if merged != nil {
				merged.Release()
			}
			result.conflict = true
			result.conflicts = scaleP21SW226ConflictKeys(runErr)
			return result
		}
		if runErr != nil {
			t.Fatal("streamed integration row delta", runErr)
		}
		defer merged.Release()
		for _, name := range []string{"integrationDeliveries", "integrationConnections"} {
			index, _, _ := metadataLogicalField(name)
			owner := &ResourceRead{store: view}
			spool, err := legacySessionNodeSpool(t.Context(), owner, merged.fields[index])
			if err != nil {
				owner.Close()
				t.Fatal(err)
			}
			raw := make([]byte, int(spool.bytes))
			_, err = spool.ReadAt(raw, 0)
			spool.Close()
			if err != nil {
				owner.Close()
				t.Fatal(err)
			}
			if name == "integrationDeliveries" {
				result.delivery = raw
				err = merged.fields[index].Children(t.Context(), func(_ reflect.Value, node metadataLogicalNode) error {
					for _, field := range []string{"Payload", "PreviousValues"} {
						value, err := issueHotField(t.Context(), node, field)
						if err != nil {
							return err
						}
						absent, err := value.isNil(t.Context())
						if err != nil {
							return err
						}
						var native json.RawMessage
						if !absent {
							if err := value.StreamBytes(t.Context(), func(input io.Reader) error {
								var readErr error
								native, readErr = io.ReadAll(input)
								return readErr
							}); err != nil {
								return err
							}
							if native == nil {
								native = json.RawMessage{}
							}
						}
						if field == "Payload" {
							result.payloads = append(result.payloads, native)
						} else {
							result.previous = append(result.previous, native)
						}
					}
					return nil
				})
			} else {
				result.connection = raw
				err = merged.fields[index].Children(t.Context(), func(key reflect.Value, node metadataLogicalNode) error {
					if key.Int() != 0 {
						return nil
					}
					secret, err := issueHotField(t.Context(), node, "SecretHash")
					if err != nil {
						return err
					}
					value, err := owner.node(t.Context(), secret, false)
					if err == nil {
						result.secret = value.String()
					}
					return err
				})
			}
			owner.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("integration delta retained native work")
		}
	}
	image := scaleP21SW77Image(t, tx, workspace)
	result.root = json.RawMessage(image.Root)
	for key, row := range image.Rows {
		if key.field == "integrationDeliveries" || key.field == "integrationConnections" {
			result.rows = append(result.rows, []any{key.field, key.key, row.order, json.RawMessage(row.data)})
		}
	}
	sort.Slice(result.rows, func(i, j int) bool {
		left, right := result.rows[i], result.rows[j]
		if left[0].(string) != right[0].(string) {
			return left[0].(string) < right[0].(string)
		}
		return left[1].(string) < right[1].(string)
	})
	if tx.metadataSQLStages != 0 || len(tx.metadataSQLStageOwners) != 0 {
		t.Fatal("integration delta retained an owned SQL stage")
	}
	return result
}

func TestScaleP21SW226IntegrationRowStreamDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	for _, test := range []string{
		"raw-equality", "native-nul", "loaded-only-defaults", "peer-payload", "peer-config",
		"peer-last-error-conflict", "attempts-cas-same-number", "connection-attempts-cas-same-number",
		"both-attempts-cas-same-number", "folded-canonical-peer", "physical-case-alias", "canonical-id-change",
		"peer-later-updated-at", "peer-delivery-deleted", "peer-connection-deleted",
		"inline-duplicate", "inline-empty", "native-key-nul",
	} {
		t.Run(test, func(t *testing.T) {
			// Peer key/shape writes commit. Each scenario needs its own
			// workspace so a prior physical alias cannot change its seed.
			fixture := scaleP09Database(t)
			view := &SQLiteStore{db: fixture.db, dialect: fixture.dialect, maxStateBytes: fixture.maxStateBytes, metadataResidency: newMetadataResidency()}
			before := cloneBootstrap(fixture.data)
			before.IntegrationDeliveries = []domain.IntegrationDelivery{{
				ID: "sw226-delivery", ConnectionID: "sw226-connection", Status: "delivering", Attempts: 1,
				Payload:        json.RawMessage(`{ "body": "` + strings.Repeat("retained payload ", 65000) + `" }`),
				PreviousValues: json.RawMessage(` { "html" : "<>&" } `), LastError: "old failure", CreatedAt: at, UpdatedAt: at,
			}}
			before.IntegrationConnections = []domain.IntegrationConnection{{
				ID: "sw226-connection", Name: "First connection", Status: "connected", SecretHash: "native-secret",
				Config: map[string]string{"body": strings.Repeat("retained config ", 20000)}, Scopes: []string{}, Channels: []string{},
				DeliveryAttempts: 3, LastError: "old failure", CreatedAt: at, UpdatedAt: at,
			}}
			switch test {
			case "native-nul":
				before.IntegrationDeliveries[0].LastError = "old\x00failure" + string([]byte{0xff})
			case "inline-duplicate":
				before.IntegrationDeliveries = append(before.IntegrationDeliveries, before.IntegrationDeliveries[0])
				before.IntegrationConnections = append(before.IntegrationConnections, before.IntegrationConnections[0])
			case "inline-empty":
				before.IntegrationDeliveries[0].ID, before.IntegrationConnections[0].ID = "", ""
				before.IntegrationDeliveries[0].ConnectionID = ""
			case "native-key-nul":
				before.IntegrationDeliveries[0].ID = "sw226-\x00delivery"
				before.IntegrationConnections[0].ID = "sw226-\x00connection"
				before.IntegrationDeliveries[0].ConnectionID = before.IntegrationConnections[0].ID
			}
			requested := scaleP21SW226Clone(before)
			delivery, connection := &requested.IntegrationDeliveries[0], &requested.IntegrationConnections[0]
			delivery.Attempts++
			delivery.Status, delivery.LastError, delivery.UpdatedAt = "failed", "HTTP 502", at.Add(time.Hour)
			delivery.NextAttemptAt = new(delivery.UpdatedAt.Add(4 * time.Minute))
			connection.DeliveryAttempts++
			connection.LastDeliveryAt, connection.LastError = &delivery.UpdatedAt, delivery.LastError
			peers := map[string][]byte{}
			peer := scaleP21SW226Clone(before)
			deleted := ""
			switch test {
			case "loaded-only-defaults":
				peer.IntegrationConnections[0].Scopes, peer.IntegrationConnections[0].Channels = nil, nil
				peers["integrationConnections"], _ = json.Marshal(peer.IntegrationConnections[0])
			case "peer-payload":
				peer.IntegrationDeliveries[0].Payload = json.RawMessage(` { "peer" : "` + strings.Repeat("<>& peer ", 50000) + `" } `)
				peer.IntegrationDeliveries[0].PreviousValues = json.RawMessage(` {"peer":"previous"} `)
				peers["integrationDeliveries"], _ = json.Marshal(peer.IntegrationDeliveries[0])
			case "peer-config":
				peer.IntegrationConnections[0].Config = map[string]string{"peer": strings.Repeat("peer config ", 55000)}
				peer.IntegrationConnections[0].Scopes = []string{"peer-scope"}
				peer.IntegrationConnections[0].Channels = nil
				peers["integrationConnections"], _ = json.Marshal(peer.IntegrationConnections[0])
			case "peer-last-error-conflict":
				peer.IntegrationDeliveries[0].LastError = "independent peer failure"
				peers["integrationDeliveries"], _ = json.Marshal(peer.IntegrationDeliveries[0])
			case "attempts-cas-same-number":
				peer.IntegrationDeliveries[0].Attempts++
				peers["integrationDeliveries"], _ = json.Marshal(peer.IntegrationDeliveries[0])
			case "connection-attempts-cas-same-number":
				peer.IntegrationConnections[0].DeliveryAttempts++
				peers["integrationConnections"], _ = json.Marshal(peer.IntegrationConnections[0])
			case "both-attempts-cas-same-number":
				peer.IntegrationConnections[0].DeliveryAttempts++
				peer.IntegrationDeliveries[0].Attempts++
				peers["integrationConnections"], _ = json.Marshal(peer.IntegrationConnections[0])
				peers["integrationDeliveries"], _ = json.Marshal(peer.IntegrationDeliveries[0])
			case "folded-canonical-peer":
				raw, err := json.Marshal(peer.IntegrationDeliveries[0])
				if err != nil {
					t.Fatal(err)
				}
				peers["integrationDeliveries"] = append(append([]byte(nil), raw[:len(raw)-1]...),
					[]byte(`,"PAYLOAD":{"html":"<>&","body":"`+strings.Repeat("peer payload ", 20000)+`"},"previousValues":null,"PREVIOUSVALUES":{"last":true},"STATUS":null}`)...)
				raw, err = json.Marshal(peer.IntegrationConnections[0])
				if err != nil {
					t.Fatal(err)
				}
				peers["integrationConnections"] = append(append([]byte(nil), raw[:len(raw)-1]...),
					[]byte(`,"CONFIG":{"a":"old"},"config":null,"CoNfIg":{"last":"peer"},"SCOPES":["a","b","c"],"scopes":["short"],"sCoPeS":[null,null,null],"CHANNELS":[],"channels":null}`)...)
			case "physical-case-alias":
				peers["physical:integrationDeliveries"] = []byte(strings.ToUpper(before.IntegrationDeliveries[0].ID))
				peers["physical:integrationConnections"] = []byte(strings.ToUpper(before.IntegrationConnections[0].ID))
			case "canonical-id-change":
				peer.IntegrationDeliveries[0].ID += "-peer"
				peer.IntegrationConnections[0].ID += "-peer"
				peers["integrationDeliveries"], _ = json.Marshal(peer.IntegrationDeliveries[0])
				peers["integrationConnections"], _ = json.Marshal(peer.IntegrationConnections[0])
			case "peer-later-updated-at":
				peer.IntegrationDeliveries[0].UpdatedAt = at.Add(2 * time.Hour)
				peers["integrationDeliveries"], _ = json.Marshal(peer.IntegrationDeliveries[0])
			case "peer-delivery-deleted":
				deleted = "integrationDeliveries"
			case "peer-connection-deleted":
				deleted = "integrationConnections"
			}
			literal := scaleP21SW226Execute(t, fixture, view, before, requested, peers, deleted, false, "")
			bounded := scaleP21SW226Execute(t, fixture, view, before, requested, peers, deleted, true, "")
			if literal.conflict != bounded.conflict || !reflect.DeepEqual(literal.conflicts, bounded.conflicts) {
				t.Fatal("streamed integration scalar CAS/key receipts differ", literal.conflict, bounded.conflict, literal.conflicts, bounded.conflicts)
			}
			if !literal.conflict && (!bytes.Equal(literal.delivery, bounded.delivery) ||
				!bytes.Equal(literal.connection, bounded.connection) || literal.secret != bounded.secret ||
				!reflect.DeepEqual(literal.payloads, bounded.payloads) || !reflect.DeepEqual(literal.previous, bounded.previous) ||
				!bytes.Equal(literal.root, bounded.root) || !reflect.DeepEqual(literal.rows, bounded.rows)) {
				if !reflect.DeepEqual(literal.rows, bounded.rows) {
					scaleP21SW226LogPhysicalRows(t, literal.rows, bounded.rows)
				}
				t.Fatalf("streamed integration delta changed native image, SQL rows, root or ignored fields: delivery=%t connection=%t secret=%t payloads=%t previous=%t root=%t rows=%t",
					bytes.Equal(literal.delivery, bounded.delivery), bytes.Equal(literal.connection, bounded.connection),
					literal.secret == bounded.secret, reflect.DeepEqual(literal.payloads, bounded.payloads),
					reflect.DeepEqual(literal.previous, bounded.previous), bytes.Equal(literal.root, bounded.root),
					reflect.DeepEqual(literal.rows, bounded.rows))
			}
			if strings.Contains(test, "attempts-cas-same-number") && !literal.conflict {
				t.Fatal("fixture failed to exercise scalar numeric CAS")
			}
			if test == "both-attempts-cas-same-number" && len(literal.conflicts) != 2 {
				t.Fatal("fixture did not preserve both conflict-refresh keys", literal.conflicts)
			}
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("integration stream retained work after result")
			}
			t.Logf("case=%s bounded_SQL=%d peak_work=%d conflict=%t", test, bounded.sql, bounded.peak, bounded.conflict)
		})
	}
	for _, phase := range []string{"read", "write"} {
		t.Run("cancel-"+phase, func(t *testing.T) {
			fixture := scaleP09Database(t)
			view := &SQLiteStore{db: fixture.db, dialect: fixture.dialect, maxStateBytes: fixture.maxStateBytes, metadataResidency: newMetadataResidency()}
			before := cloneBootstrap(fixture.data)
			before.IntegrationDeliveries = []domain.IntegrationDelivery{{
				ID: "sw226-cancel", ConnectionID: "sw226-connection", Status: "pending",
				Payload: json.RawMessage(`{"body":"` + strings.Repeat("large body ", 100000) + `"}`), CreatedAt: at, UpdatedAt: at,
			}}
			before.IntegrationConnections = []domain.IntegrationConnection{{ID: "sw226-connection"}}
			requested := scaleP21SW226Clone(before)
			requested.IntegrationDeliveries[0].Status = "delivering"
			requested.IntegrationDeliveries[0].UpdatedAt = at.Add(time.Hour)
			scaleP21SW226Execute(t, fixture, view, before, requested, nil, "", true, phase)
		})
	}
	for _, name := range []string{"integrationDeliveries", "integrationConnections"} {
		if metadataSearchFieldSet[name] || offloadFields[name] {
			t.Fatal("integration field gained an unsupported search/offload hook", name)
		}
		if err := writeCustomerFilterRecord(t.Context(), nil, "", name, "", nil); err != nil {
			t.Fatal(err)
		}
		if err := writeReleaseIssueRecords(t.Context(), nil, "", name, "", nil); err != nil {
			t.Fatal(err)
		}
	}
}

var _ io.ReaderAt = scaleP21SW226ReadAt(nil)
