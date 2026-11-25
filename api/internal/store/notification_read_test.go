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

type scaleP21SW15DueSwitchContext struct {
	context.Context
	switchSource func()
}

func (c *scaleP21SW15DueSwitchContext) Err() error {
	if c.switchSource != nil {
		switchSource := c.switchSource
		c.switchSource = nil
		switchSource()
	}
	return c.Context.Err()
}

func scaleP21SW15DueFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, time.Time) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
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

func TestScaleP21SW15NotificationReadDueDifferential(t *testing.T) {
	kind := reflect.TypeFor[domain.NotificationDelivery]()
	for index := range kind.NumField() {
		field := kind.Field(index)
		if field.Type != reflect.TypeFor[time.Time]() && field.Type != reflect.TypeFor[*time.Time]() &&
			field.Type.Kind() != reflect.String && field.Type.Kind() != reflect.Int {
			t.Fatal("review flat typed delivery budget", field)
		}
	}
	f, view, now := scaleP21SW15DueFixture(t)
	workspace := f.data.Workspace.URLKey
	for _, statuses := range [][]string{{"pending"}, {"failed"}, {"pending", "failed"}, {"missing"}, {}} {
		expected, err := f.NotificationDeliveries(t.Context(), workspace, statuses, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
			for _, key := range []string{workspace, ""} {
				read, err := source.BeginDueNotificationDeliveryRead(t.Context(), key, statuses, now)
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
		_, expectedErr := f.NotificationDeliveries(t.Context(), key, []string{"pending"}, now)
		if read, err := view.BeginDueNotificationDeliveryRead(t.Context(), key, []string{"pending"}, now); read != nil || err == nil || err.Error() != expectedErr.Error() {
			t.Fatal("physical installed lookup changed", key, err, expectedErr)
		}
	}
	if view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("native due selection decoded metadata")
	}
	before := testing.AllocsPerRun(3, func() {
		if _, err := f.NotificationDeliveries(t.Context(), workspace, []string{"pending"}, now); err != nil {
			t.Fatal(err)
		}
	})
	after := testing.AllocsPerRun(3, func() {
		read, err := view.BeginDueNotificationDeliveryRead(t.Context(), workspace, []string{"pending"}, now)
		if err != nil {
			t.Fatal(err)
		}
		read.Close()
	})
	t.Logf("due selection allocations/op: legacy=%.0f owned=%.0f; lifetime bound, no speed claim", before, after)
}

func TestScaleP21SW15NotificationReadDueOwnershipErrorsAndBounds(t *testing.T) {
	f, view, now := scaleP21SW15DueFixture(t)
	workspace := f.data.Workspace.URLKey
	parent, releaseParent, err := view.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginDueNotificationDeliveryRead(parent, workspace, []string{"pending"}, now)
	if err != nil {
		releaseParent()
		t.Fatal(err)
	}
	releaseParent()
	if scaleP21SW15DueUsed(view) != scaleP21SW15DueDeliveryWork {
		t.Fatal("parent release revoked owned result")
	}
	retained, _ := json.Marshal(read.Deliveries)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind='delivery' AND id=?`, []byte(`{"id":"replacement"}`), workspace, read.Deliveries[0].ID); err != nil {
		t.Fatal(err)
	}
	current, _ := json.Marshal(read.Deliveries)
	if string(current) != string(retained) || scaleP21SW15DueUsed(view) != scaleP21SW15DueDeliveryWork {
		t.Fatal("SQL replacement changed or uncharged owned generation")
	}
	read.Close()
	if scaleP21SW15DueUsed(view) != 0 {
		t.Fatal("due read leaked allowance")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := view.BeginDueNotificationDeliveryRead(cancelled, "missing", []string{"pending"}, now); read != nil || err == nil || err.Error() != "workspace not found" {
		t.Fatal("missing workspace lost source-first error precedence", err)
	}
	if read, err := view.BeginDueNotificationDeliveryRead(cancelled, workspace, []string{"pending"}, now); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled reader acquired output", err)
	}
	if _, err := view.NotificationDeliveries(t.Context(), workspace, []string{"pending"}, now); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("unowned compatibility API remained available bounded", err)
	}
	if _, err := view.IntegrationConnections(t.Context(), workspace, "gitlab"); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("unused unowned connection API remained available bounded", err)
	}
	view.metadataResidency.work.mu.Lock()
	originalLimit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = scaleP21SW15DueDeliveryWork - 1
	view.metadataResidency.work.mu.Unlock()
	if read, err := view.BeginDueNotificationDeliveryRead(t.Context(), "missing", []string{"pending"}, now); read != nil || err == nil || err.Error() != "workspace not found" {
		t.Fatal("missing workspace lost precedence over work saturation", err)
	}
	if read, err := view.BeginDueNotificationDeliveryRead(t.Context(), workspace, []string{"pending"}, now); read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("saturated work acquired due output", err)
	}
	view.metadataResidency.work.mu.Lock()
	view.metadataResidency.work.limit = originalLimit
	view.metadataResidency.work.mu.Unlock()
	if read, err := view.BeginDueNotificationDeliveryRead(t.Context(), workspace, []string{strings.Repeat("x", scaleP21SW15DueDeliveryWork/33+1)}, now); read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("status arguments escaped pre-bind budget", err)
	}
	for _, raw := range []string{"", `{"id":7}`, `{"id":"oversized","error":"` + strings.Repeat("x", MetadataRecordPageMaxBytes) + `"}`} {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind='delivery' AND id=?`, []byte(raw), workspace, "sw15-delivery-a"); err != nil {
			t.Fatal(err)
		}
		_, legacyErr := f.NotificationDeliveries(t.Context(), workspace, []string{"pending"}, now)
		read, err := view.BeginDueNotificationDeliveryRead(t.Context(), workspace, []string{"pending"}, now)
		if len(raw) > MetadataRecordPageMaxBytes {
			if read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("large due row cloned instead of bounded refusal", err)
			}
		} else if read != nil || err == nil || legacyErr == nil || err.Error() != legacyErr.Error() {
			t.Fatal("typed delivery decode error changed", err, legacyErr)
		}
		if scaleP21SW15DueUsed(view) != 0 {
			t.Fatal("failed reader leaked allowance")
		}
	}
}

func TestScaleP21SW15NotificationReadDueLimitAndOffConnections(t *testing.T) {
	f, view, now := scaleP21SW15DueFixture(t)
	workspace := f.data.Workspace.URLKey
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for index := range 1005 {
		if err := writeContentRecord(t.Context(), tx, workspace, "delivery", "", domain.NotificationDelivery{
			ID: fmt.Sprintf("sw15-limit-%04d", index), Status: "pending", Channel: "email", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	expected, err := f.NotificationDeliveries(t.Context(), workspace, []string{"pending"}, now)
	if err != nil || len(expected.NotificationDeliveries) != 1000 {
		t.Fatal("legacy due-limit fixture failed", err)
	}
	read, err := view.BeginDueNotificationDeliveryRead(t.Context(), workspace, []string{"pending"}, now)
	if err != nil || !reflect.DeepEqual(read.Deliveries, expected.NotificationDeliveries) {
		t.Fatal("due 1000-row limit or ordering changed", err)
	}
	read.Close()
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

func TestScaleP21SW15NotificationReadDefaultSwitch(t *testing.T) {
	f, view, now := scaleP21SW15DueFixture(t)
	workspace := f.data.Workspace.URLKey
	view.mu.Lock()
	source := view.workspaces[workspace]
	view.workspaces["old-physical"] = domain.Bootstrap{Workspace: domain.Workspace{URLKey: "old-logical"}}
	view.workspaces["new-physical"] = source
	view.lastWorkspaceKey = "old-physical"
	view.mu.Unlock()
	ctx := &scaleP21SW15DueSwitchContext{Context: t.Context(), switchSource: func() {
		view.mu.Lock()
		prior := view.workspaces["old-physical"]
		prior.Workspace.URLKey = "changed-old-logical"
		view.workspaces["old-physical"] = prior
		view.lastWorkspaceKey = "new-physical"
		view.mu.Unlock()
	}}
	read, err := view.BeginDueNotificationDeliveryRead(ctx, "", []string{"pending"}, now)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	expected, err := f.NotificationDeliveries(t.Context(), workspace, []string{"pending"}, now)
	if err != nil || !reflect.DeepEqual(read.Deliveries, expected.NotificationDeliveries) {
		t.Fatal("default/key source was not re-resolved atomically after admission", err)
	}
}

func scaleP21SW15DueUsed(s *SQLiteStore) int {
	s.metadataResidency.work.mu.Lock()
	defer s.metadataResidency.work.mu.Unlock()
	return s.metadataResidency.work.used
}
