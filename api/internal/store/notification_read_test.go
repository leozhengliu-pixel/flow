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

type dueSwitchContext struct {
	context.Context
	switchSource func()
}

func (c *dueSwitchContext) Err() error {
	if c.switchSource != nil {
		switchSource := c.switchSource
		c.switchSource = nil
		switchSource()
	}
	return c.Context.Err()
}

var pendingDeliverySelection = NotificationDeliverySelection{Statuses: []string{"pending"}}

func dueFixture(t *testing.T) (scaleFixture, *SQLiteStore, time.Time) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := journalScaleDatabase(t)
	now := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	if err := f.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21_sw15.delivery_fixture", "", nil, func(data *domain.Bootstrap) error {
		data.NotificationDeliveries = []domain.NotificationDelivery{
			{ID: "sw15-delivery-a", NotificationID: "sw15-notification", RecipientID: "recipient", Channel: "email", Status: "pending", NextAttemptAt: &past, CreatedAt: now, UpdatedAt: now},
			{ID: "sw15-delivery-b", NotificationID: "sw15-missing", RecipientID: "recipient", Channel: "email", Status: "pending", NextAttemptAt: &past, CreatedAt: now, UpdatedAt: now},
			{ID: "sw15-delivery-c", NotificationID: "sw15-notification", RecipientID: "recipient", Channel: "chat", Status: "pending", CreatedAt: now, UpdatedAt: now},
			{ID: "sw15-delivery-future", NotificationID: "sw15-notification", RecipientID: "recipient", Channel: "email", Status: "pending", NextAttemptAt: &future, CreatedAt: now, UpdatedAt: now},
			{ID: "sw15-delivery-failed", NotificationID: "sw15-notification", RecipientID: "recipient", Channel: "email", Status: "failed", NextAttemptAt: &past, Error: "<&>\x00" + string([]byte{0xff}), CreatedAt: now, UpdatedAt: now},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return f, view, now
}

func TestNotificationReadDueDifferential(t *testing.T) {
	kind := reflect.TypeFor[domain.NotificationDelivery]()
	for index := range kind.NumField() {
		field := kind.Field(index)
		if field.Type != reflect.TypeFor[time.Time]() && field.Type != reflect.TypeFor[*time.Time]() &&
			field.Type.Kind() != reflect.String && field.Type.Kind() != reflect.Int {
			t.Fatal("review flat typed delivery budget", field)
		}
	}
	f, view, now := dueFixture(t)
	workspace := f.data.Workspace.URLKey
	for _, statuses := range [][]string{{"pending"}, {"failed"}, {"pending", "failed"}, {"missing"}, {}} {
		selection := NotificationDeliverySelection{Statuses: statuses}
		expected, err := f.NotificationDeliveries(t.Context(), workspace, selection, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
			for _, key := range []string{workspace, ""} {
				read, err := source.BeginDueNotificationDeliveryRead(t.Context(), key, selection, now)
				if err != nil || !reflect.DeepEqual(read.Deliveries, expected.NotificationDeliveries) {
					t.Fatal("due delivery selection/order/typed fields changed", err)
				}
				read.Close()
				read.Close()
				if read.Deliveries != nil {
					t.Fatal("Close retained result")
				}
			}
		}
	}
	for _, key := range []string{"missing", workspace + "\x00", strings.ToUpper(workspace)} {
		_, expectedErr := f.NotificationDeliveries(t.Context(), key, pendingDeliverySelection, now)
		if read, err := view.BeginDueNotificationDeliveryRead(t.Context(), key, pendingDeliverySelection, now); read != nil || err == nil || err.Error() != expectedErr.Error() {
			t.Fatal("physical installed lookup changed", key, err, expectedErr)
		}
	}
	if view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("native due selection decoded metadata")
	}
	before := testing.AllocsPerRun(3, func() {
		if _, err := f.NotificationDeliveries(t.Context(), workspace, pendingDeliverySelection, now); err != nil {
			t.Fatal(err)
		}
	})
	after := testing.AllocsPerRun(3, func() {
		read, err := view.BeginDueNotificationDeliveryRead(t.Context(), workspace, pendingDeliverySelection, now)
		if err != nil {
			t.Fatal(err)
		}
		read.Close()
	})
	t.Logf("due selection allocations/op: legacy=%.0f owned=%.0f; lifetime bound, no speed claim", before, after)
}

func TestNotificationReadDueOwnershipErrorsAndBounds(t *testing.T) {
	f, view, now := dueFixture(t)
	workspace := f.data.Workspace.URLKey
	parent, releaseParent, err := view.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginDueNotificationDeliveryRead(parent, workspace, pendingDeliverySelection, now)
	if err != nil {
		releaseParent()
		t.Fatal(err)
	}
	releaseParent()
	if dueUsed(view) != dueDeliveryWork {
		t.Fatal("parent release revoked owned result")
	}
	retained, _ := json.Marshal(read.Deliveries)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind='delivery' AND id=?`, []byte(`{"id":"replacement"}`), workspace, read.Deliveries[0].ID); err != nil {
		t.Fatal(err)
	}
	current, _ := json.Marshal(read.Deliveries)
	if string(current) != string(retained) || dueUsed(view) != dueDeliveryWork {
		t.Fatal("SQL replacement changed or uncharged owned generation")
	}
	read.Close()
	if dueUsed(view) != 0 {
		t.Fatal("due read leaked allowance")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := view.BeginDueNotificationDeliveryRead(cancelled, "missing", pendingDeliverySelection, now); read != nil || err == nil || err.Error() != "workspace not found" {
		t.Fatal("missing workspace lost source-first error precedence", err)
	}
	if read, err := view.BeginDueNotificationDeliveryRead(cancelled, workspace, pendingDeliverySelection, now); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled reader acquired output", err)
	}
	if _, err := view.NotificationDeliveries(t.Context(), workspace, pendingDeliverySelection, now); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("unowned compatibility API remained available bounded", err)
	}
	if _, err := view.IntegrationConnections(t.Context(), workspace, "gitlab"); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("unused unowned connection API remained available bounded", err)
	}
	view.metadataResidency.work.mu.Lock()
	originalLimit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = dueDeliveryWork - 1
	view.metadataResidency.work.mu.Unlock()
	if read, err := view.BeginDueNotificationDeliveryRead(t.Context(), "missing", pendingDeliverySelection, now); read != nil || err == nil || err.Error() != "workspace not found" {
		t.Fatal("missing workspace lost precedence over work saturation", err)
	}
	if read, err := view.BeginDueNotificationDeliveryRead(t.Context(), workspace, pendingDeliverySelection, now); read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("saturated work acquired due output", err)
	}
	view.metadataResidency.work.mu.Lock()
	view.metadataResidency.work.limit = originalLimit
	view.metadataResidency.work.mu.Unlock()
	oversized := strings.Repeat("x", dueDeliveryWork/33+1)
	if read, err := view.BeginDueNotificationDeliveryRead(t.Context(), workspace, NotificationDeliverySelection{Statuses: []string{oversized}}, now); read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("status arguments escaped pre-bind budget", err)
	}
	if read, err := view.BeginDueNotificationDeliveryRead(t.Context(), workspace, NotificationDeliverySelection{Channel: oversized}, now); read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("channel argument escaped pre-bind budget", err)
	}
	for _, raw := range []string{"", `{"id":7}`, `{"id":"oversized","error":"` + strings.Repeat("x", MetadataRecordPageMaxBytes) + `"}`} {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind='delivery' AND id=?`, []byte(raw), workspace, "sw15-delivery-a"); err != nil {
			t.Fatal(err)
		}
		_, legacyErr := f.NotificationDeliveries(t.Context(), workspace, pendingDeliverySelection, now)
		read, err := view.BeginDueNotificationDeliveryRead(t.Context(), workspace, pendingDeliverySelection, now)
		if len(raw) > MetadataRecordPageMaxBytes {
			if read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("large due row cloned instead of bounded refusal", err)
			}
		} else if read != nil || err == nil || legacyErr == nil || err.Error() != legacyErr.Error() {
			t.Fatal("typed delivery decode error changed", err, legacyErr)
		}
		if dueUsed(view) != 0 {
			t.Fatal("failed reader leaked allowance")
		}
	}
}

func TestNotificationReadDueLimitAndOffConnections(t *testing.T) {
	f, view, now := dueFixture(t)
	workspace := f.data.Workspace.URLKey
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for index := range 1005 {
		if err := writeContentRecord(t.Context(), tx, workspace, "delivery", "", domain.NotificationDelivery{
			ID: fmt.Sprintf("sw15-limit-%04d", index), Status: "pending", Channel: "desktop", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeContentRecord(t.Context(), tx, workspace, "delivery", "", domain.NotificationDelivery{
		ID: "sw15-other-channel", Status: "pending", Channel: "email ", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeContentRecord(t.Context(), tx, workspace, "delivery", "", domain.NotificationDelivery{
		ID: "sw15-terminal-failure", Status: "failed", Channel: "email", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	expected, err := f.NotificationDeliveries(t.Context(), workspace, pendingDeliverySelection, now)
	if err != nil || len(expected.NotificationDeliveries) != 1000 {
		t.Fatal("legacy due-limit fixture failed", err)
	}
	read, err := view.BeginDueNotificationDeliveryRead(t.Context(), workspace, pendingDeliverySelection, now)
	if err != nil || !reflect.DeepEqual(read.Deliveries, expected.NotificationDeliveries) {
		t.Fatal("due 1000-row limit or ordering changed", err)
	}
	read.Close()
	pendingEmail := NotificationDeliverySelection{Statuses: []string{"pending"}, Channel: "email"}
	for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
		for _, check := range []struct {
			selection NotificationDeliverySelection
			want      []string
		}{
			{pendingEmail, []string{"sw15-delivery-a", "sw15-delivery-b"}},
			{NotificationDeliverySelection{Statuses: []string{"failed", "pending-disabled"}, Channel: "email", ExcludeTerminalFailures: true}, []string{"sw15-delivery-failed"}},
			{NotificationDeliverySelection{Statuses: []string{"pending"}, Channel: "chat"}, []string{"sw15-delivery-c"}},
		} {
			selected, err := source.BeginDueNotificationDeliveryRead(t.Context(), workspace, check.selection, now)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, row := range selected.Deliveries {
				ids = append(ids, row.ID)
			}
			selected.Close()
			if !reflect.DeepEqual(ids, check.want) {
				t.Fatalf("selection %+v got %v, want %v", check.selection, ids, check.want)
			}
		}
	}
	if f.dialect == "sqlite" {
		query, args := f.dueNotificationDeliveryQuery(workspace, pendingEmail, now)
		var id, parent, unused int
		var plan string
		if err := f.db.QueryRowContext(t.Context(), "EXPLAIN QUERY PLAN SELECT data"+query, args...).Scan(&id, &parent, &unused, &plan); err != nil {
			t.Fatal(err)
		}
		t.Logf("email selection plan: %s", plan)
		if !strings.Contains(plan, "content_status_idx") || !strings.Contains(plan, "next_attempt_at<") {
			t.Fatalf("email selection lost the indexed due range: %s", plan)
		}
	}
	legacy := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"physical": {IntegrationConnections: []domain.IntegrationConnection{
		{ID: "first", Provider: "GitLab", SecretHash: "not-public", Config: map[string]string{"key": "value"}},
		{ID: "other", Provider: "slack"}, {ID: "duplicate", Provider: "gitlab"},
	}}}, lastWorkspaceKey: "physical"}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	connections, err := legacy.IntegrationConnections(cancelled, "physical", "GITLAB")
	if err != nil || len(connections) != 2 || connections[0].ID != "first" || connections[1].ID != "duplicate" || connections[0].SecretHash != "" {
		t.Fatal("literal off provider filter/source/redaction/cancellation changed", err)
	}
	connections[0].Config["key"] = "changed"
	again, _ := legacy.IntegrationConnections(t.Context(), "physical", "gitlab")
	empty, emptyErr := legacy.IntegrationConnections(t.Context(), "", "gitlab")
	if again[0].Config["key"] != "value" || emptyErr != nil || empty == nil || len(empty) != 0 {
		t.Fatal("off ownership or empty-key no-default behavior changed")
	}
}

func TestNotificationReadDefaultSwitch(t *testing.T) {
	f, view, now := dueFixture(t)
	workspace := f.data.Workspace.URLKey
	view.mu.Lock()
	source := view.workspaces[workspace]
	view.workspaces["old-physical"] = domain.Bootstrap{Workspace: domain.Workspace{URLKey: "old-logical"}}
	view.workspaces["new-physical"] = source
	view.lastWorkspaceKey = "old-physical"
	view.mu.Unlock()
	ctx := &dueSwitchContext{Context: t.Context(), switchSource: func() {
		view.mu.Lock()
		prior := view.workspaces["old-physical"]
		prior.Workspace.URLKey = "changed-old-logical"
		view.workspaces["old-physical"] = prior
		view.lastWorkspaceKey = "new-physical"
		view.mu.Unlock()
	}}
	read, err := view.BeginDueNotificationDeliveryRead(ctx, "", pendingDeliverySelection, now)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	expected, err := f.NotificationDeliveries(t.Context(), workspace, pendingDeliverySelection, now)
	if err != nil || !reflect.DeepEqual(read.Deliveries, expected.NotificationDeliveries) {
		t.Fatal("default/key source was not re-resolved atomically after admission", err)
	}
}

func dueUsed(s *SQLiteStore) int {
	s.metadataResidency.work.mu.Lock()
	defer s.metadataResidency.work.mu.Unlock()
	return s.metadataResidency.work.used
}
