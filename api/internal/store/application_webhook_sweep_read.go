package store

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

// BeginApplicationWebhookSweepRead owns the exact hook eligibility of the app
// notification or SLA sweep. App notifications also accept audit/empty URLs.
func (s *SQLiteStore) BeginApplicationWebhookSweepRead(ctx context.Context, workspace, resource string) (_ *WebhookDeliveryRead, found bool, err error) {
	if resource != "app_user_notifications" && resource != "issue_sla" {
		return nil, false, ErrMetadataRecordQuery
	}
	if !s.BoundedMetadataEnabled() {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if workspace == "" {
			workspace = s.lastWorkspaceKey
		}
		data, found := s.workspaces[workspace]
		if !found {
			return nil, false, nil
		}
		read := &WebhookDeliveryRead{Data: domain.Bootstrap{Workspace: domain.Workspace{ID: data.Workspace.ID, URLKey: data.Workspace.URLKey}}}
		for _, hook := range data.Webhooks {
			if !hook.Enabled || !slices.Contains(hook.ResourceTypes, resource) ||
				resource == "app_user_notifications" && hook.ManagedBy != "application" || resource == "issue_sla" && hook.AuditLog {
				continue
			}
			read.Data.Webhooks = append(read.Data.Webhooks, domain.Webhook{
				ID: hook.ID, URL: hook.URL, TeamIDs: slices.Clone(hook.TeamIDs), TeamRestriction: hook.TeamRestriction,
				Enabled: hook.Enabled, AuditLog: hook.AuditLog, ManagedBy: hook.ManagedBy, ApplicationClientID: hook.ApplicationClientID,
				ResourceTypes: []string{resource},
			})
		}
		return read, true, nil
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	read, found, err := s.BeginWebhookDeliveryRead(ctx, workspace, false)
	if err != nil || !found {
		return nil, found, err
	}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	err = read.field("webhooks").Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		var enabled, audit, application, subscribed bool
		err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
			switch key.String() {
			case "Enabled", "AuditLog":
				value, err := node.Value(ctx, 4096)
				if err != nil {
					return err
				}
				if key.String() == "Enabled" {
					enabled = value.Bool()
				} else {
					audit = value.Bool()
				}
			case "ManagedBy":
				var err error
				application, err = node.StringEqual(ctx, "application")
				return err
			case "ResourceTypes":
				return node.Children(ctx, func(_ reflect.Value, value metadataLogicalNode) error {
					match, err := value.StringEqual(ctx, resource)
					subscribed = subscribed || match
					return err
				})
			}
			return nil
		})
		if err != nil || !enabled || !subscribed || resource == "app_user_notifications" && !application || resource == "issue_sla" && audit {
			return err
		}
		var hook domain.Webhook
		if err := read.project(ctx, record, reflect.ValueOf(&hook).Elem(),
			strings.Fields("ID URL TeamIDs TeamRestriction Enabled AuditLog ManagedBy ApplicationClientID")); err != nil {
			return err
		}
		hook.ResourceTypes = []string{resource}
		read.Data.Webhooks = append(read.Data.Webhooks, hook)
		return nil
	})
	if err != nil {
		return nil, true, err
	}
	return read, true, nil
}

type ApplicationSLAWebhookEvent struct {
	Key, Action, IssueID string
	SLA                  domain.IssueSLA
	At                   time.Time
}

// ApplicationSLAEventRead owns the event headers and last-wins SLA index from
// the second original metadata lookup, independently of the hook lookup.
type ApplicationSLAEventRead struct {
	Events []ApplicationSLAWebhookEvent
	owner  *WebhookDeliveryRead
	once   sync.Once
}

func (r *ApplicationSLAEventRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Events = nil
			r.owner.Close()
		})
	}
}

func applicationSLAWebhookProject(ctx context.Context, owner *WebhookDeliveryRead, record metadataLogicalNode, target reflect.Value, fields []string) error {
	var native []string
	for _, name := range fields {
		field, _ := target.Type().FieldByName(name)
		if field.Type != reflect.TypeFor[time.Time]() && field.Type != reflect.TypeFor[*time.Time]() {
			native = append(native, name)
		}
	}
	if err := owner.project(ctx, record, target, native); err != nil {
		return err
	}
	return record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		name := key.String()
		if !slices.Contains(fields, name) || slices.Contains(native, name) {
			return nil
		}
		// Read-only SLA comparisons and JSON use the supported wire time
		// receipt; a fixed floor keeps native/reloaded time admission stable.
		cost := max(node.cost, 4096)
		if cost > (uint64(metadataWorkBytes())-4096)/8 {
			return ErrMetadataWorkBudget
		}
		if err := owner.reserve(ctx, cost*8+4096); err != nil {
			return err
		}
		value, err := node.WireValue(ctx, int(node.cost))
		if err != nil {
			return err
		}
		target.FieldByName(name).Set(value)
		return nil
	})
}

func (s *SQLiteStore) BeginApplicationSLAEventRead(ctx context.Context, workspace string, now time.Time) (_ *ApplicationSLAEventRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return s.legacyApplicationSLAEvents(workspace, now)
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	owner, found, err := s.BeginWebhookDeliveryRead(ctx, workspace, false)
	if err != nil || !found {
		return nil, found, err
	}
	read := &ApplicationSLAEventRead{owner: owner}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	slas := map[string]domain.IssueSLA{}
	highRisk := []ApplicationSLAWebhookEvent{}
	err = owner.field("issueSlas").Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		var sla domain.IssueSLA
		if err := applicationSLAWebhookProject(ctx, owner, record, reflect.ValueOf(&sla).Elem(),
			strings.Fields("ID IssueID RuleID StartedAt DueAt BreachedAt Status")); err != nil {
			return err
		}
		if err := owner.reserve(ctx, 1024); err != nil {
			return err
		}
		slas[sla.ID] = sla
		if applicationSLAHighRisk(sla, now) {
			if err := owner.reserve(ctx, 1024+uint64(len(sla.ID))*8); err != nil {
				return err
			}
			highRisk = append(highRisk, applicationSLAHighRiskEvent(sla, now))
		}
		return nil
	})
	if err != nil {
		return nil, true, err
	}
	err = owner.field("slaEvents").Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		var event domain.SLAEvent
		if err := applicationSLAWebhookProject(ctx, owner, record, reflect.ValueOf(&event).Elem(), []string{"Type", "CreatedAt"}); err != nil {
			return err
		}
		action := recentApplicationSLAAction(event, now)
		if action == "" {
			return nil
		}
		if err := owner.project(ctx, record, reflect.ValueOf(&event).Elem(), []string{"ID", "IssueID", "SLAID"}); err != nil {
			return err
		}
		if err := owner.reserve(ctx, 1024); err != nil {
			return err
		}
		read.Events = append(read.Events, ApplicationSLAWebhookEvent{Key: event.ID, Action: action, IssueID: event.IssueID, SLA: slas[event.SLAID], At: event.CreatedAt})
		return nil
	})
	if err != nil {
		return nil, true, err
	}
	// Capacity for both phases is admitted before the final ordered append.
	if len(highRisk) > 0 {
		if err := owner.reserve(ctx, uint64(len(highRisk))*512); err != nil {
			return nil, true, err
		}
	}
	read.Events = slices.Concat(read.Events, highRisk)
	return read, true, ctx.Err()
}

func recentApplicationSLAAction(event domain.SLAEvent, now time.Time) string {
	if now.Sub(event.CreatedAt) > 15*time.Minute {
		return ""
	}
	switch event.Type {
	case "started":
		return "set"
	case "breached":
		return "breached"
	default:
		return ""
	}
}

func applicationSLAHighRisk(sla domain.IssueSLA, now time.Time) bool {
	if sla.Status != "active" || sla.BreachedAt != nil || !now.Before(sla.DueAt) {
		return false
	}
	window := min(sla.DueAt.Sub(sla.StartedAt)/4, 24*time.Hour)
	return sla.DueAt.Sub(now) <= window
}

func applicationSLAHighRiskEvent(sla domain.IssueSLA, now time.Time) ApplicationSLAWebhookEvent {
	return ApplicationSLAWebhookEvent{
		Key:    sla.ID + ":highRisk:" + sla.DueAt.UTC().Format(time.RFC3339),
		Action: "highRisk", IssueID: sla.IssueID, SLA: sla, At: now,
	}
}

func (s *SQLiteStore) legacyApplicationSLAEvents(workspace string, now time.Time) (*ApplicationSLAEventRead, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, found := s.workspaces[workspace]
	if !found {
		return nil, false, nil
	}
	read := &ApplicationSLAEventRead{}
	needed := map[string]domain.IssueSLA{}
	var eventSLAIDs []string
	for _, event := range data.SLAEvents {
		if action := recentApplicationSLAAction(event, now); action != "" {
			read.Events = append(read.Events, ApplicationSLAWebhookEvent{Key: event.ID, Action: action, IssueID: event.IssueID, At: event.CreatedAt})
			eventSLAIDs = append(eventSLAIDs, event.SLAID)
			needed[event.SLAID] = domain.IssueSLA{}
		}
	}
	for _, sla := range data.IssueSLAs {
		_, wanted := needed[sla.ID]
		risky := applicationSLAHighRisk(sla, now)
		if !wanted && !risky {
			continue
		}
		selected := domain.IssueSLA{ID: sla.ID, IssueID: sla.IssueID, RuleID: sla.RuleID, Status: sla.Status, StartedAt: sla.StartedAt, DueAt: sla.DueAt}
		if sla.BreachedAt != nil {
			at := *sla.BreachedAt
			selected.BreachedAt = &at
		}
		if wanted {
			needed[sla.ID] = selected
		}
		if risky {
			read.Events = append(read.Events, applicationSLAHighRiskEvent(selected, now))
		}
	}
	for i, id := range eventSLAIDs {
		read.Events[i].SLA = needed[id]
	}
	return read, true, nil
}
