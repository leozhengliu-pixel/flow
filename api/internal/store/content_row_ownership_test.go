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
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW351ContentRowOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, resource := f.data.Workspace.URLKey, idgen.New("sw351-resource-")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	f.db.SetMaxOpenConns(1)
	at := time.Date(2026, 11, 17, 1, 2, 3, 456, time.UTC)
	body := strings.Repeat("Complete retained history body. ", 32768)
	marshal := func(value any) []byte {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	activityA := marshal(domain.ActivityEvent{
		ID: "json-z", Type: "issue.updated", Actor: f.data.Viewer, CreatedAt: at.Add(time.Hour),
		Metadata: domain.ActivityMetadata{"priority": "2", "priorityBefore": "1"},
	})
	activityB := marshal(domain.ActivityEvent{
		ID: "json-a", Type: "issue.updated", Actor: f.data.Viewer, CreatedAt: at.Add(-time.Hour),
		Metadata: domain.ActivityMetadata{"description": body},
	})
	activityB = append(append([]byte(" \n"), activityB[:len(activityB)-1]...), []byte(`,"unknown":{"raw":"kept exactly"}}`+"\n\t")...)
	notificationRaw := marshal(domain.Notification{
		ID: "notification-json", IssueID: "different-json-resource", RecipientID: f.data.Viewer.ID,
		Actor: f.data.Viewer, Text: body, LatestActorIDs: []string{"first", "first", "second"},
		CreatedAt: at, UpdatedAt: at,
	})
	fixture := []struct {
		kind, physical string
		raw            []byte
	}{
		{"activity", "physical-a", activityA},
		{"activity", "physical-b", activityB},
		{"notification", "physical-notification", notificationRaw},
	}
	begin := func(t *testing.T) *sqlTx {
		t.Helper()
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })
		for _, row := range fixture {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,status,created_at,data) VALUES(?,?,?,?,'read',?,?)`,
				workspace, row.kind, resource, row.physical, at.Format(issueRecordTimestamp), row.raw); err != nil {
				t.Fatal(err)
			}
		}
		return tx
	}
	usable := func(t *testing.T, tx *sqlTx) {
		t.Helper()
		var one int
		if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatal("content owner consumed the borrowed transaction", err)
		}
	}
	setLimit := func(t *testing.T, limit int) {
		t.Helper()
		budget := view.metadataResidency.work
		budget.mu.Lock()
		original := budget.limit
		budget.limit = limit
		budget.mu.Unlock()
		t.Cleanup(func() {
			budget.mu.Lock()
			budget.limit = original
			budget.mu.Unlock()
		})
	}
	t.Run("selected-display-and-native-survive-content-close", func(t *testing.T) {
		tx := begin(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		content := view.newIssueChildContentRows(tx, workspace)
		defer content.Close()
		data, err := content.loadMutationScope(ctx, []string{resource})
		if err != nil {
			t.Fatal(err)
		}
		data.Issues = []domain.Issue{{ID: resource, Title: "Selected issue", Description: "Retained issue body", Team: f.data.Teams[0], Creator: f.data.Viewer}}
		base, err := view.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if base != nil {
				base.Release()
			}
		}()
		selected, err := boundedDiscussionEventSource(ctx, base, data)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if selected != nil {
				selected.Release()
			}
		}()
		display, err := view.refreshLegacyDisplayImage(ctx, selected)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if display != nil {
				display.Release()
			}
		}()
		scope := MetadataRecordScope{Fields: []string{"issues", "comments", "activities", "notifications", "notificationDeliveries"}}
		wantSelected, err := selected.Select(t.Context(), scope, 64<<20)
		if err != nil {
			t.Fatal(err)
		}
		wantSelected = cloneBootstrap(wantSelected)
		wantDisplay, err := display.Select(t.Context(), scope, 64<<20)
		if err != nil {
			t.Fatal(err)
		}
		wantDisplay = cloneBootstrap(wantDisplay)
		owned := &boundedDiscussionAttempt{owner: &ResourceRead{store: view}}
		defer owned.Close()
		// Select includes these core fields in addition to the requested collections.
		materialized := domain.Bootstrap{Workspace: wantDisplay.Workspace, Viewer: wantDisplay.Viewer, ViewerRole: wantDisplay.ViewerRole}
		for _, name := range scope.Fields {
			index, _, _ := metadataLogicalField(name)
			value, err := owned.collectionValue(ctx, display.fields[index])
			if err != nil {
				t.Fatal(name, err)
			}
			reflect.ValueOf(&materialized).Elem().Field(index).Set(value)
		}
		rawAliases := append([][]byte(nil), content.rows.discussion.raw...)
		if len(rawAliases) != len(fixture) || !reflect.DeepEqual(materialized, wantDisplay) {
			t.Fatal("selected display materialization lost a complete fixture collection")
		}
		beforeClose := view.MetadataWorkUsedForTesting()
		content.Close()
		content.Close()
		for _, raw := range rawAliases {
			for _, value := range raw {
				if value != 0 {
					t.Fatal("early content close retained a raw backing byte")
				}
			}
		}
		held := view.MetadataWorkUsedForTesting()
		if held <= 0 || held >= beforeClose || !reflect.DeepEqual(materialized, wantDisplay) {
			t.Fatal("early content close discarded displayed native values or retained their source allowance", beforeClose, held)
		}
		scaleP21SW72Compare(t, selected, scope, wantSelected)
		scaleP21SW72Compare(t, display, scope, wantDisplay)
		cancel()
		index, _, _ := metadataLogicalField("activities")
		if _, err := owned.collectionValue(ctx, display.fields[index]); !errors.Is(err, context.Canceled) ||
			view.MetadataWorkUsedForTesting() != held || !reflect.DeepEqual(materialized, wantDisplay) {
			t.Fatal("cancellation discarded still-owned frames/native output", err)
		}
		scaleP21SW72Compare(t, selected, scope, wantSelected)
		scaleP21SW72Compare(t, display, scope, wantDisplay)
		owned.Close()
		display.Release()
		display = nil
		selected.Release()
		selected = nil
		base.Release()
		base = nil
		if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("early-content handoff retained work or fully decoded workspace metadata")
		}
		usable(t, tx)
	})
	t.Run("compact-history-independent-reads-and-close", func(t *testing.T) {
		tx := begin(t)
		wantActivities, err := scopedContent[domain.ActivityEvent](t.Context(), tx, workspace, "activity", []string{resource})
		if err != nil {
			t.Fatal(err)
		}
		wantNotifications, err := scopedContent[domain.Notification](t.Context(), tx, workspace, "notification", []string{resource})
		if err != nil {
			t.Fatal(err)
		}
		read := view.newIssueChildContentRows(tx, workspace)
		defer read.Close()
		first, err := read.loadMutationScope(t.Context(), []string{resource})
		if err != nil || !reflect.DeepEqual(first.Activities, wantActivities) || !reflect.DeepEqual(first.Notifications, wantNotifications[resource]) {
			t.Fatal("compaction changed complete physical resource/order/native history", err)
		}
		if first.Activities[resource][0].ID != "json-z" || first.Activities[resource][1].ID != "json-a" ||
			first.Notifications[0].IssueID != "different-json-resource" {
			t.Fatal("decoded identity/timestamps replaced physical resource and row ordering")
		}
		rawAliases := append([][]byte(nil), read.rows.discussion.raw...)
		if len(rawAliases) != len(fixture) {
			t.Fatal("selected raw history was capped or discarded")
		}
		var decodeWork uint64
		rawBytes := 0
		for index, row := range fixture {
			if !bytes.Equal(rawAliases[index], row.raw) {
				t.Fatal("unknown fields, whitespace or exact raw row order changed")
			}
			work, err := metadataAggregatePayloadJSONWork(t.Context(), row.raw)
			if err != nil {
				t.Fatal(err)
			}
			decodeWork += work
			rawBytes += len(row.raw)
		}
		held := view.MetadataWorkUsedForTesting()
		t.Logf("raw_bytes=%d retained_work=%d original_decode_envelopes=%d", rawBytes, held, decodeWork)
		if held <= rawBytes || uint64(held) >= decodeWork/3 {
			t.Fatal("temporary content SQL/decode envelopes remained retained", held, decodeWork)
		}
		second, err := read.loadMutationScope(t.Context(), []string{resource})
		if err != nil || !reflect.DeepEqual(second.Activities, wantActivities) || !reflect.DeepEqual(second.Notifications, wantNotifications[resource]) {
			t.Fatal("second content read changed its complete native preimage", err)
		}
		first.Activities[resource][0].Metadata["priority"] = "caller edit"
		first.Notifications[0].LatestActorIDs[0] = "caller edit"
		if !reflect.DeepEqual(second.Activities, wantActivities) || !reflect.DeepEqual(second.Notifications, wantNotifications[resource]) {
			t.Fatal("independent content loads alias mutable decoded values")
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		held = view.MetadataWorkUsedForTesting()
		if _, err := read.loadMutationScope(cancelled, []string{resource}); !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != held {
			t.Fatal("cancelled load released successful content owners", err)
		}
		setLimit(t, held+1)
		if _, err := read.loadMutationScope(t.Context(), []string{resource}); !errors.Is(err, ErrMetadataWorkBudget) ||
			!reflect.DeepEqual(second.Activities, wantActivities) || !reflect.DeepEqual(second.Notifications, wantNotifications[resource]) {
			t.Fatal("failed admission discarded earlier content", err)
		}
		read.Close()
		read.Close()
		if len(first.Activities) != 0 || len(second.Activities) != 0 || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("idempotent content close retained maps or admission")
		}
		for _, raw := range rawAliases {
			for _, value := range raw {
				if value != 0 {
					t.Fatal("closed content owner retained an exposed raw backing byte")
				}
			}
		}
		for _, notification := range first.Notifications {
			if !reflect.ValueOf(notification).IsZero() {
				t.Fatal("content close retained a returned native slice value")
			}
		}
		usable(t, tx)
	})
	t.Run("optional-native-compaction-pressure-fallback", func(t *testing.T) {
		tx := begin(t)
		var want domain.Notification
		if err := json.Unmarshal(notificationRaw, &want); err != nil {
			t.Fatal(err)
		}
		work, err := metadataAggregatePayloadJSONWork(t.Context(), notificationRaw)
		if err != nil {
			t.Fatal(err)
		}
		fixed := uint64(reflect.TypeFor[domain.Notification]().Size())*4 + 4096
		native, err := metadataAggregateNativeCost(t.Context(), reflect.ValueOf(want), 0)
		if err != nil || native+fixed >= work+fixed {
			t.Fatal("fixture has no smaller native replacement", native, work, err)
		}
		limit := int(work + fixed)
		setLimit(t, limit)
		read := view.newIssueMutationRows(tx, workspace)
		defer read.Close()
		visits := 0
		if err := discussionDecodeOwned(t.Context(), read, notificationRaw, func(got domain.Notification) error {
			visits++
			if !reflect.DeepEqual(got, want) || view.MetadataWorkUsedForTesting() != limit {
				t.Fatal("fallback changed native value or released its original envelope")
			}
			return nil
		}); err != nil || visits != 1 || view.MetadataWorkUsedForTesting() != limit {
			t.Fatal("optional native compaction introduced a refusal", err, visits)
		}
		read.Close()
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("fallback decode envelope survived its owner")
		}
		usable(t, tx)
	})
	for _, failure := range []string{"cancel-payload", "partial-payload"} {
		t.Run(failure, func(t *testing.T) {
			tx := begin(t)
			read := view.newIssueMutationRows(tx, workspace)
			defer read.Close()
			if err := read.beginDiscussion(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			headers, err := read.discussionHeaders(t.Context(), "workspace_key=? AND kind=? AND resource_id=?", []any{workspace, "activity", resource}, " ORDER BY created_at,id", 0)
			if err != nil || len(headers) != 2 {
				t.Fatal("missing exact physical activity headers", err)
			}
			if failure == "partial-payload" {
				headers[1].size++
			}
			size := headers[0].size + headers[1].size
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			atPayload, visits := 0, 0
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				if strings.Contains(query, " AS ordinal") {
					atPayload = view.MetadataWorkUsedForTesting()
					if failure == "cancel-payload" {
						cancel()
					}
				}
			})
			err = read.discussionPayloads(ctx, "activity", headers, size, func([]byte) error { visits++; return nil })
			if failure == "cancel-payload" {
				if !errors.Is(err, context.Canceled) || atPayload == 0 || view.MetadataWorkUsedForTesting() >= atPayload || len(read.discussion.raw) != 0 {
					t.Fatal("cancelled SQL retained temporary content admission", err, atPayload, view.MetadataWorkUsedForTesting())
				}
			} else if !errors.Is(err, io.ErrUnexpectedEOF) || atPayload == 0 || visits != 0 || len(read.discussion.raw) != 1 ||
				!bytes.Equal(read.discussion.raw[0], activityA) || view.MetadataWorkUsedForTesting() != atPayload {
				t.Fatal("partial SQL lost its admitted exact raw prefix", err, atPayload, view.MetadataWorkUsedForTesting(), visits)
			}
			var prefix []byte
			if len(read.discussion.raw) != 0 {
				prefix = read.discussion.raw[0]
			}
			read.Close()
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("failed content load retained work after Close")
			}
			for _, value := range prefix {
				if value != 0 {
					t.Fatal("partial raw prefix survived Close")
				}
			}
			usable(t, tx)
		})
	}
}
