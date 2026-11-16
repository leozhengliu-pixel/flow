package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW205AggregatePublicationDifferential(t *testing.T) {
	for _, shape := range []string{
		"document-4MiB-body-and-history", "document-HTML", "document-invalid-UTF8",
		"delivery-4.3MiB", "delivery-HTML", "raw-number-unicode-duplicates",
		"oversized-content-keys", "oversized-HTML-content-keys",
		"dense", "deep-raw", "invalid-raw", "invalid-number", "invalid-float", "invalid-time",
	} {
		t.Run(shape, func(t *testing.T) {
			before, after, id := scaleP21SW205AggregateFixture(shape)
			for _, mode := range []string{"native-overlay", "stored-frame"} {
				t.Run(mode, func(t *testing.T) {
					probe := &SQLiteStore{metadataResidency: newMetadataResidency()}
					left, right, release := scaleP21SW205AggregateImages(t, before, after, mode)
					defer release()
					for _, event := range []string{"resource.updated", "resource.deleted"} {
						payload := json.RawMessage(`{"count":9007199254740993,"input":"<>&","duplicate":0,"duplicate":1}`)
						wantPrevious := aggregatePreviousValues(before, after, id)
						wantSnapshot := enrichRealtimePayload(payload, aggregateJSONValue(after, id), event)
						publication, err := probe.prepareMetadataAggregatePublication(t.Context(), left, right, id, true, payload, event)
						if err != nil {
							t.Fatalf("legal %s/%s publication refused: %v", shape, mode, err)
						}
						if !bytes.Equal(publication.PreviousValues, wantPrevious) {
							publication.Close()
							t.Fatalf("previous differs: got=%s want=%s", scaleP21SW205Preview(publication.PreviousValues), scaleP21SW205Preview(wantPrevious))
						}
						if !bytes.Equal(publication.RealtimePayload, wantSnapshot) {
							publication.Close()
							t.Fatalf("snapshot differs: got=%s want=%s", scaleP21SW205Preview(publication.RealtimePayload), scaleP21SW205Preview(wantSnapshot))
						}
						actor := domain.User{ID: "usr_sw205_actor", Name: "Snapshot actor"}
						ctx := ContextWithActor(t.Context(), actor)
						eventValue := domain.DomainEvent{Type: event, AggregateID: id, Payload: payload, PreviousValues: publication.PreviousValues}
						gotHook := webhookEvent(ctx, eventValue, publication.RealtimePayload)
						wantHook := webhookEvent(ctx, domain.DomainEvent{Type: event, AggregateID: id, Payload: payload, PreviousValues: wantPrevious}, wantSnapshot)
						if !reflect.DeepEqual(gotHook, wantHook) {
							t.Fatal("webhook snapshot/entity/input/previous/actor differs")
						}
						wantHeld := scaleP21SW205ByteCharge(len(publication.PreviousValues)) + scaleP21SW205ByteCharge(len(publication.RealtimePayload))
						if got := probe.metadataResidency.work.used; got != wantHeld {
							t.Fatalf("temporary graphs survived construction: held=%d final bytes=%d", got, wantHeld)
						}
						publication.Close()
						publication.Close()
						if probe.metadataResidency.work.used != 0 || publication.PreviousValues != nil || publication.RealtimePayload != nil {
							t.Fatal("publication release retained bytes or allowances")
						}
					}
				})
			}
		})
	}
}

func TestScaleP21SW205AggregatePublicationPayloadShapesAndWinnerPrecedence(t *testing.T) {
	const id = "issue_sw205_winner"
	before := domain.Bootstrap{
		Issues:   []domain.Issue{{ID: id, Title: "first typed"}, {ID: id, Title: "last typed"}},
		Settings: map[string]any{"id": id, "title": "nested losing winner"},
	}
	after := before
	after.Issues = append([]domain.Issue(nil), before.Issues...)
	after.Issues[0].Title = "changed first typed"
	left, right, release := scaleP21SW205AggregateImages(t, before, after, "stored-frame")
	defer release()
	probe := &SQLiteStore{metadataResidency: newMetadataResidency()}
	for _, payload := range []string{
		"", "null", "true", "42", `"scalar"`, "[1,null,{}]", "{",
		`{"entity":"old","deleted":false}`, `{"z":0,"a":1}`,
		`{"ok":1,"huge":1e400,"tail":2}`,
		`{"huge":1e400,"huge":2,"tail":3}`,
		`{"nested":{"before":1,"huge":-1e400,"after":2},"tail":3}`,
		`{"nested":[1,1e400,2],"tail":3}`,
		`{"entity":1e400,"deleted":1e400,"tail":2}`,
		`[1e400,2]`, `1e400`,
		`{"ok":1,"huge":1e400,"tail":`,
	} {
		for _, id := range []string{id, "missing_sw205", "", "  "} {
			publication, err := probe.prepareMetadataAggregatePublication(t.Context(), left, right, id, true, json.RawMessage(payload), "issue.deleted")
			if err != nil {
				t.Fatalf("payload %q, id %q: %v", payload, id, err)
			}
			want := enrichRealtimePayload(json.RawMessage(payload), aggregateJSONValue(after, id), "issue.deleted")
			if !bytes.Equal(publication.RealtimePayload, want) ||
				!bytes.Equal(publication.PreviousValues, aggregatePreviousValues(before, after, id)) {
				t.Fatalf("payload or winner changed: payload=%q id=%q", payload, id)
			}
			publication.Close()
			if probe.metadataResidency.work.used != 0 {
				t.Fatal("payload graph leaked")
			}
		}
	}
	// Legacy counts are refreshed after previous-values are computed.
	// The previous-only hook must preserve that distinct callback image.
	previous, err := probe.prepareMetadataAggregatePrevious(t.Context(), left, right, id)
	if err != nil {
		t.Fatal(err)
	}
	defer previous.Close()
	if !bytes.Equal(previous.PreviousValues, aggregatePreviousValues(before, after, id)) || previous.RealtimePayload != nil {
		t.Fatal("previous-only hook changed its image or retained a realtime envelope")
	}
}

func TestScaleP21SW205AggregatePublicationQuotaCancellationAndIO(t *testing.T) {
	before, after, id := scaleP21SW205AggregateFixture("document-HTML")
	left, right, release := scaleP21SW205AggregateImages(t, before, after, "stored-frame")
	defer release()
	for _, scenario := range []string{"small-quota", "decode-quota", "cancel-before", "cancel-during"} {
		t.Run(scenario, func(t *testing.T) {
			probe := &SQLiteStore{metadataResidency: newMetadataResidency()}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var work context.Context = ctx
			want := ErrMetadataWorkBudget
			switch scenario {
			case "small-quota":
				probe.metadataResidency.work.limit = 1 << 20
			case "decode-quota":
				probe.metadataResidency.work.limit = 32 << 20
			case "cancel-before":
				cancel()
				want = context.Canceled
			case "cancel-during":
				work = &scaleP21SW205CancelContext{Context: ctx, cancel: cancel, at: 2000}
				want = context.Canceled
			}
			disk := metadataLogicalDiskUsed.Load()
			descriptors := metadataLogicalDescriptorsUsed.Load()
			publication, err := probe.prepareMetadataAggregatePublication(work, left, right, id, true, nil, "document.updated")
			if !errors.Is(err, want) || publication != nil {
				if publication != nil {
					publication.Close()
				}
				t.Fatalf("failure changed: publication=%v err=%v want=%v", publication != nil, err, want)
			}
			if probe.metadataResidency.work.used != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatalf("refusal/cancellation leaked: work=%d disk delta=%d descriptors delta=%d", probe.metadataResidency.work.used, metadataLogicalDiskUsed.Load()-disk, metadataLogicalDescriptorsUsed.Load()-descriptors)
			}
		})
	}
	t.Run("closed-backing-source-is-not-absent", func(t *testing.T) {
		builder, err := newMetadataLogicalBuilder(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		node, err := builder.writeValue(reflect.ValueOf(json.RawMessage(`{"valid":true}`)), 0, false)
		if err != nil {
			builder.Close()
			t.Fatal(err)
		}
		builder.Close()
		probe := &SQLiteStore{metadataResidency: newMetadataResidency()}
		value, release, err := probe.metadataAggregateNormalizeOwned(t.Context(), metadataAggregateStoredNode{node})
		if release != nil {
			release()
		}
		if err == nil || value != nil || probe.metadataResidency.work.used != 0 {
			t.Fatalf("backing IO converted to missing entity: value=%v err=%v work=%d", value, err, probe.metadataResidency.work.used)
		}
	})
}

func TestScaleP21SW205AggregatePublicationConcurrentOwnership(t *testing.T) {
	before, after, id := scaleP21SW205AggregateFixture("raw-number-unicode-duplicates")
	left, right, release := scaleP21SW205AggregateImages(t, before, after, "stored-frame")
	defer release()
	probe := &SQLiteStore{metadataResidency: newMetadataResidency()}
	var group sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < cap(failures); i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			publication, err := probe.prepareMetadataAggregatePublication(t.Context(), left, right, id, true, json.RawMessage(`{"ok":true}`), "document.updated")
			if err != nil {
				failures <- err
				return
			}
			defer publication.Close()
			want := enrichRealtimePayload(json.RawMessage(`{"ok":true}`), aggregateJSONValue(after, id), "document.updated")
			if !bytes.Equal(publication.RealtimePayload, want) {
				failures <- errors.New("concurrent output differs")
			}
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if probe.metadataResidency.work.used != 0 {
		t.Fatal("concurrent publication leaked ownership")
	}
}

func scaleP21SW205AggregateImages(t *testing.T, before, after domain.Bootstrap, mode string) (metadataAggregateImage, metadataAggregateImage, func()) {
	t.Helper()
	left, err := captureMetadataLogicalSource(t.Context(), before, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	right, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{})
	if err != nil {
		left.Release()
		t.Fatal(err)
	}
	a, b := metadataAggregateImage{Source: left}, metadataAggregateImage{Source: right}
	if mode == "native-overlay" {
		fields := []string{"documents", "integrationDeliveries", "settings"}
		a.Selected, a.Scope.Fields = &before, fields
		b.Selected, b.Scope.Fields = &after, fields
	}
	return a, b, func() { left.Release(); right.Release() }
}

func scaleP21SW205AggregateFixture(shape string) (before, after domain.Bootstrap, id string) {
	id = "document_sw205"
	content := "before"
	switch shape {
	case "document-4MiB-body-and-history":
		content = strings.Repeat("x", 4<<20)
	case "document-HTML":
		content = strings.Repeat("<>&", (4<<20)/3)
	case "document-invalid-UTF8":
		content = strings.Repeat("\xff", 4<<20)
	case "delivery-4.3MiB", "delivery-HTML":
		id = "delivery_sw205"
		body := strings.Repeat("x", 43*(1<<20)/10)
		if shape == "delivery-HTML" {
			body = strings.Repeat("<", 43*(1<<20)/10)
		}
		prior := json.RawMessage(`{"body":"` + body + `","changed":false}`)
		next := json.RawMessage(`{"body":"` + body + `","changed":true}`)
		before.IntegrationDeliveries = []domain.IntegrationDelivery{{ID: id, Status: "pending", Payload: prior}}
		after.IntegrationDeliveries = []domain.IntegrationDelivery{{ID: id, Status: "delivered", Payload: next}}
		return before, after, id
	}
	before.Documents = []domain.Document{{
		ID: id, Title: "before", Content: content,
		CreatedAt: time.Date(2026, 11, 14, 10, 30, 12, 123456789, time.FixedZone("sw205", 9*60*60)),
	}}
	if shape == "document-4MiB-body-and-history" || shape == "document-HTML" {
		before.Documents[0].Revisions = []domain.DocumentRevision{{ID: "revision_sw205", Content: strings.Repeat("history ", (4<<20)/8)}}
	}
	after = before
	after.Documents = append([]domain.Document(nil), before.Documents...)
	after.Documents[0].Title = "after"
	after.Documents[0].Content += " changed"
	switch shape {
	case "raw-number-unicode-duplicates":
		before.Documents[0].ContentData = map[string]any{"raw": json.RawMessage(`{"n":9007199254740993,"n":1.25,"unicode":"\ud800","body":"<>&"}`), "number": json.Number("1e30")}
		after.Documents[0].ContentData = map[string]any{"raw": json.RawMessage(`{"n":2.5,"unicode":"\ud800","body":"<>&"}`), "number": json.Number("1e30")}
	case "oversized-content-keys", "oversized-HTML-content-keys":
		prefix := strings.Repeat("k", (1<<20)+8192)
		if shape == "oversized-HTML-content-keys" {
			prefix = strings.Repeat("<>&", ((1<<20)+8192)/3+1)
		}
		// Shared prefixes exercise SQLite's exact BLOB ordering, continuation
		// and simultaneous ownership of previous and current cursor keys.
		before.Documents[0].ContentData = map[string]any{prefix + "b": "old b", prefix + "a": "old a"}
		after.Documents[0].ContentData = map[string]any{prefix + "b": "new b", prefix + "a": "new a"}
	case "dense":
		prior, next := make([]any, 2048), make([]any, 2048)
		for i := range prior {
			prior[i] = map[string]any{"k": fmt.Sprint(i), "array": []any{nil, true, float64(i)}, "value": "before"}
			next[i] = map[string]any{"k": fmt.Sprint(i), "array": []any{nil, true, float64(i)}, "value": "after"}
		}
		before.Documents[0].ContentData = map[string]any{"items": prior}
		after.Documents[0].ContentData = map[string]any{"items": next}
	case "deep-raw":
		before.Documents[0].ContentData = map[string]any{"raw": json.RawMessage(strings.Repeat("[", 180) + "1" + strings.Repeat("]", 180))}
		after.Documents[0].ContentData = map[string]any{"raw": json.RawMessage(strings.Repeat("[", 180) + "2" + strings.Repeat("]", 180))}
	case "invalid-raw":
		before.Documents[0].ContentData = map[string]any{"raw": json.RawMessage(`{"truncated":`)}
		after.Documents[0].ContentData = map[string]any{"raw": json.RawMessage(`{"truncated":`)}
	case "invalid-number":
		before.Documents[0].ContentData = map[string]any{"number": json.Number("NaN")}
		after.Documents[0].ContentData = map[string]any{"number": json.Number("NaN")}
	case "invalid-float":
		before.Documents[0].ContentData = map[string]any{"float": math.Inf(1)}
		after.Documents[0].ContentData = map[string]any{"float": math.Inf(1)}
	case "invalid-time":
		before.Documents[0].CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		after.Documents[0].CreatedAt = before.Documents[0].CreatedAt
	}
	return before, after, id
}

func scaleP21SW205ByteCharge(size int) int {
	if size == 0 {
		return 0
	}
	return ((size + 8191) &^ 8191) + 64
}

func scaleP21SW205Preview(raw []byte) string {
	if len(raw) > 256 {
		return string(raw[:256]) + fmt.Sprintf("... (%d bytes)", len(raw))
	}
	return string(raw)
}

type scaleP21SW205CancelContext struct {
	context.Context
	cancel context.CancelFunc
	at     int64
	calls  atomic.Int64
}

func (c *scaleP21SW205CancelContext) Err() error {
	if c.calls.Add(1) == c.at {
		c.cancel()
	}
	return c.Context.Err()
}
