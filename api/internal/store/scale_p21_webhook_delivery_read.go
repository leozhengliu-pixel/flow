package store

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// WebhookDeliveryRead owns the installed identity, routing and actor values
// used by one original accessor. Keep actor/routing reads open through enqueue.
type WebhookDeliveryRead struct {
	Data    domain.Bootstrap
	source  *metadataLogicalSource
	store   *SQLiteStore
	release []func()
	once    sync.Once
}

func (r *WebhookDeliveryRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Data = domain.Bootstrap{}
			if r.source != nil {
				r.source.Release()
				r.source = nil
			}
			for _, release := range r.release {
				release()
			}
			r.release = nil
		})
	}
}

// BeginWebhookDeliveryRead preserves the physical/default installed registry.
// Routing omits hooks the original routing/enqueue predicates always reject.
func (s *SQLiteStore) BeginWebhookDeliveryRead(ctx context.Context, workspace string, routing bool) (_ *WebhookDeliveryRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	owner, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	defer owner.Close()
	source, err := owner.retainSource(ctx, owner.workspace)
	if err != nil {
		return nil, true, err
	}
	r := &WebhookDeliveryRead{source: source, store: s}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if err := r.reserve(ctx, 64<<10); err != nil {
		return nil, true, err
	}
	if err := r.project(ctx, r.field("workspace"), reflect.ValueOf(&r.Data.Workspace).Elem(), strings.Fields("ID URLKey")); err != nil {
		return nil, true, err
	}
	if !routing {
		return r, true, nil
	}
	err = r.field("webhooks").Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		var enabled, audit bool
		var emptyURL bool
		if err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
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
			case "URL":
				var err error
				emptyURL, err = node.StringEqual(ctx, "")
				return err
			}
			return nil
		}); err != nil {
			return err
		}
		if !enabled || audit || emptyURL {
			return nil
		}
		var hook domain.Webhook
		if err := r.project(ctx, record, reflect.ValueOf(&hook).Elem(),
			strings.Fields("ID URL ResourceTypes TeamIDs TeamRestriction Enabled AuditLog PayloadVersion ManagedBy ApplicationClientID")); err != nil {
			return err
		}
		r.Data.Webhooks = append(r.Data.Webhooks, hook)
		return nil
	})
	if err != nil {
		return nil, true, err
	}
	return r, true, nil
}

func (r *WebhookDeliveryRead) reserve(ctx context.Context, cost uint64) error {
	if cost > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	if err == nil {
		r.release = append(r.release, release)
	}
	return err
}

func (r *WebhookDeliveryRead) field(name string) metadataLogicalNode {
	index, _, _ := metadataLogicalField(name)
	return r.source.fields[index]
}

func (r *WebhookDeliveryRead) project(ctx context.Context, record metadataLogicalNode, target reflect.Value, fields []string) error {
	cost := uint64(4096)
	if err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if slices.Contains(fields, key.String()) {
			if node.cost > (metadataWorkBytes-cost)/8 {
				return ErrMetadataWorkBudget
			}
			cost += node.cost * 8
		}
		return nil
	}); err != nil {
		return err
	}
	if err := r.reserve(ctx, cost); err != nil {
		return err
	}
	return record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if !slices.Contains(fields, key.String()) {
			return nil
		}
		value, err := node.Value(ctx, int(node.cost))
		if err != nil {
			return err
		}
		target.FieldByName(key.String()).Set(value)
		return nil
	})
}

// LoadUsers retains first-match actor fields. A workspace-member subject needs
// the complete user's JSON, including fields that are not actor properties.
func (r *WebhookDeliveryRead) LoadUsers(ctx context.Context, actorID, subjectID string, fullSubject bool) error {
	if r.source == nil {
		return errMetadataLogicalClosed
	}
	wanted := map[string]bool{}
	if actorID != "" {
		wanted[actorID] = false
	}
	if fullSubject {
		wanted[subjectID] = true
	}
	if len(wanted) == 0 {
		return ctx.Err()
	}
	loaded := map[string]bool{}
	return r.field("users").Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		var matchID string
		matched := false
		if err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
			if key.String() != "ID" {
				return nil
			}
			for id := range wanted {
				if loaded[id] {
					continue
				}
				equal, err := node.StringEqual(ctx, id)
				if err != nil {
					return err
				}
				if equal {
					matchID, matched = id, true
					return nil
				}
			}
			return nil
		}); err != nil || !matched {
			return err
		}
		var user domain.User
		if wanted[matchID] {
			if record.cost > (metadataWorkBytes-4096)/8 {
				return ErrMetadataWorkBudget
			}
			if err := r.reserve(ctx, record.cost*8+4096); err != nil {
				return err
			}
			value, err := record.WireValue(ctx, int(record.cost))
			if err != nil {
				return err
			}
			user = value.Interface().(domain.User)
		} else if err := r.project(ctx, record, reflect.ValueOf(&user).Elem(),
			strings.Fields("ID App Name DisplayName Email Username")); err != nil {
			return err
		}
		loaded[matchID] = true
		r.Data.Users = append(r.Data.Users, user)
		return nil
	})
}
