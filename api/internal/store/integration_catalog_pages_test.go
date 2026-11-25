package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW340IntegrationCatalogPages(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	foreign := scaleP09Database(t)
	view := scaleP21ResidentProbe(f.SQLiteStore)
	workspace := f.data.Workspace.URLKey
	key := RecordKey{Kind: "integrationDeliveries", ID: "sw340-" + f.data.Workspace.ID}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	foreignTx, err := foreign.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer foreignTx.Rollback()
	quote := func(value string) string {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	// Put the next field beside a physical/logical page boundary, after a
	// large skipped value. Only the existing logical capture may decide it.
	afterPayload := func(tail string, shift int) []byte {
		before, after := `{"id":`+quote(key.ID)+`,"payload":{"body":"`, `"},`
		padding := 2*MetadataRecordPageMaxBytes + shift - len(before) - len(after)
		return []byte(before + strings.Repeat("x", padding) + after + tail + "}")
	}
	type catalogCase struct {
		name          string
		raw           []byte
		requireStatus bool
		requireOmit   bool
	}
	at := time.Date(2026, 11, 17, 1, 2, 3, 456, time.UTC)
	payload := json.RawMessage(`{"body":` + quote(strings.Repeat("x<&\\\"\u4e2d\U0001f600", 60000)) + `}`)
	ordinary, err := json.Marshal(domain.IntegrationDelivery{
		ID: key.ID, ConnectionID: "sw340-connection", ResourceID: "sw340-resource",
		EventType: "issue.updated", Payload: payload, Status: "queued", CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []catalogCase{{name: "ordinary-producer", raw: ordinary, requireStatus: true}}
	for _, shift := range []int{-1, 0, 1} {
		cases = append(cases,
			catalogCase{
				name: fmt.Sprintf("skipped-payload-then-known/shift=%d", shift),
				raw:  afterPayload(`"status":"queued","title":"after skipped payload"`, shift), requireStatus: true,
			},
			catalogCase{
				name: fmt.Sprintf("known-value-capture-boundary/shift=%d", shift),
				raw:  afterPayload(`"title":`+quote(strings.Repeat("v", metadataSummaryMaxBytes-1))+`,"status":"queued"`, shift),
			},
			catalogCase{
				name: fmt.Sprintf("unknown-name-capture-boundary/shift=%d", shift),
				raw:  afterPayload(quote(strings.Repeat("u", metadataSummaryMaxBytes+1))+`:0,"status":"queued"`, shift),
			},
		)
	}
	cases = append(cases,
		catalogCase{name: "oversized-unknown-name", raw: afterPayload(quote(strings.Repeat("u", 2*metadataSummaryMaxBytes))+`:0,"status":"queued"`, 0), requireOmit: true},
		catalogCase{name: "oversized-known-value", raw: afterPayload(`"title":`+quote(strings.Repeat("v", 2*metadataSummaryMaxBytes))+`,"status":"queued"`, 0), requireOmit: true},
	)
	observe := func(ctx context.Context, queries *int) context.Context {
		return WithSQLStatementObserver(ctx, func(query string) {
			if strings.HasPrefix(query, "SELECT SUBSTR(") {
				*queries++
			}
		})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if len(test.raw) <= 2*MetadataRecordPageMaxBytes || !json.Valid(test.raw) {
				t.Fatal("catalog fixture must be valid and larger than 512 KiB")
			}
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`,
				workspace, key.Kind, key.ID, test.raw); err != nil {
				t.Fatal(err)
			}
			legacyQueries := 0
			want, err := f.projectCanonicalMetadata(observe(t.Context(), &legacyQueries), tx, workspace, key, int64(len(test.raw)))
			if err != nil {
				t.Fatal("literal catalog oracle", err)
			}
			if want.version != metadataFingerprint(test.raw) || want.bodyBytes != int64(len(test.raw)) || want.key != key {
				t.Fatal("literal catalog did not hash the complete physical row")
			}
			if test.requireOmit && !want.omitted {
				t.Fatal("oversized field did not exercise literal capture omission")
			}
			if test.requireStatus {
				var summary map[string]json.RawMessage
				if err := json.Unmarshal(want.summary, &summary); err != nil || want.omitted || string(summary["status"]) != `"queued"` {
					t.Fatal("known field after the skipped payload was not projected", err)
				}
			}
			for _, event := range []string{"integration_delivery.claimed", "integration_delivery.retry"} {
				queries := 0
				ctx, closePages, err := view.integrationDeliveryPageContext(withIntegrationDeliveryStream(observe(t.Context(), &queries), event))
				if err != nil {
					t.Fatal(err)
				}
				if view.MetadataWorkUsedForTesting() != integrationDeliveryPageWorkBytes {
					closePages()
					t.Fatal("private physical page owner has no complete independent allowance")
				}
				got, readErr := view.projectCanonicalMetadata(ctx, tx, workspace, key, int64(len(test.raw)))
				if !reflect.DeepEqual(got, want) || readErr != nil {
					closePages()
					t.Fatalf("event=%s: complete catalog row/summary/hash/omission differs: err=%v legacy=%+v bounded=%+v", event, readErr, want, got)
				}
				expectedQueries := (len(test.raw) + MetadataRecordPageMaxBytes - 1) / MetadataRecordPageMaxBytes
				if queries != expectedQueries || queries >= legacyQueries || view.MetadataWorkUsedForTesting() != integrationDeliveryPageWorkBytes {
					closePages()
					t.Fatalf("event=%s: physical page count or containing ownership changed: legacy=%d bounded=%d want=%d", event, legacyQueries, queries, expectedQueries)
				}
				closePages()
				closePages()
				if view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("private catalog pages retained memory admission after method owner close")
				}
				t.Logf("event=%s row_bytes=%d legacy_queries=%d owned_queries=%d omitted=%t", event, len(test.raw), legacyQueries, queries, got.omitted)
			}
		})
	}
	t.Run("binding-cancellation-and-admission-before-sql", func(t *testing.T) {
		queries := 0
		base := observe(t.Context(), &queries)
		event := withIntegrationDeliveryStream(base, "integration_delivery.claimed")
		for _, ctx := range []context.Context{base, event} {
			bytes, err := integrationDeliveryCanonicalPageBytes(ctx, tx)
			if err != nil || bytes != 0 {
				t.Fatal("default or event-only context changed literal physical pages", bytes, err)
			}
		}
		if ctx, close, err := f.integrationDeliveryPageContext(event); !errors.Is(err, ErrBoundedMetadataCoverage) || ctx != nil || close != nil {
			t.Fatal("off-mode store admitted a private integration page owner", err)
		}
		if ctx, close, err := view.integrationDeliveryPageContext(base); !errors.Is(err, ErrBoundedMetadataCoverage) || ctx != nil || close != nil {
			t.Fatal("missing event admitted a private integration page owner", err)
		}
		cancelled, cancel := context.WithCancel(event)
		cancel()
		if ctx, close, err := view.integrationDeliveryPageContext(cancelled); !errors.Is(err, context.Canceled) || ctx != nil || close != nil {
			t.Fatal("pre-cancelled owner admission changed error or retained a result", err)
		}
		ctx, closePages, err := view.integrationDeliveryPageContext(event)
		if err != nil {
			t.Fatal(err)
		}
		defer closePages()
		cancelled, cancel = context.WithCancel(ctx)
		cancel()
		for _, test := range []struct {
			name string
			ctx  context.Context
			tx   *sqlTx
			err  error
		}{
			{"cancelled-live-owner", cancelled, tx, context.Canceled},
			{"foreign-database", ctx, foreignTx, ErrBoundedMetadataCoverage},
			{"nil-transaction", ctx, nil, ErrBoundedMetadataCoverage},
			{"missing-event", context.WithValue(ctx, integrationDeliveryStreamKey{}, ""), tx, ErrBoundedMetadataCoverage},
		} {
			_, err := view.projectCanonicalMetadata(test.ctx, test.tx, workspace, key, 1)
			if !errors.Is(err, test.err) || queries != 0 || view.MetadataWorkUsedForTesting() != integrationDeliveryPageWorkBytes {
				t.Fatal(test.name, "binding guard changed error, reached SQL or released a live owner", err, queries)
			}
		}
		closePages()
		if _, err := view.projectCanonicalMetadata(ctx, tx, workspace, key, 1); !errors.Is(err, ErrBoundedMetadataCoverage) || queries != 0 {
			t.Fatal("closed private owner reached SQL", err, queries)
		}
		pressure := metadataWorkBytes - integrationDeliveryPageWorkBytes + 1
		_, releasePressure, err := view.tryAdmitAdditionalMetadataWork(t.Context(), pressure)
		if err != nil {
			t.Fatal(err)
		}
		defer releasePressure()
		if ctx, close, err := view.integrationDeliveryPageContext(event); !errors.Is(err, ErrMetadataWorkBudget) || ctx != nil || close != nil ||
			view.MetadataWorkUsedForTesting() != pressure || queries != 0 {
			t.Fatal("failed page admission leaked work or reached SQL", err, queries)
		}
		releasePressure()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("page guard checks leaked work")
		}
		var one int
		if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatal("catalog pages consumed the borrowed transaction", err)
		}
	})
}
