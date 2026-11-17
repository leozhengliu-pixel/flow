package store

import (
	"context"
	"reflect"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// BeginApplicationWebhookReconcileRead owns the installed inputs of the
// application webhook reconcile: the workspace identity, every
// application-managed webhook whole and in order, the live grants (not
// revoked, with a client) as client/user headers in order, and the IDs of the
// teams a grant reaches (not private, archived or retired) in order. The
// reconcile reads nothing else of them, so the selection keeps its decisions.
func (s *SQLiteStore) BeginApplicationWebhookReconcileRead(ctx context.Context, workspace string) (_ *WebhookDeliveryRead, found bool, err error) {
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
		managed, err := issueHotField(ctx, record, "ManagedBy")
		if err != nil {
			return err
		}
		if match, err := managed.StringEqual(ctx, "application"); err != nil || !match {
			return err
		}
		cost, err := issueHotProjectionCost(ctx, record, nil)
		if err != nil {
			return err
		}
		if err := read.reserve(ctx, cost); err != nil {
			return err
		}
		value, err := record.Value(ctx, int(record.cost))
		if err != nil {
			return err
		}
		read.Data.Webhooks = append(read.Data.Webhooks, value.Interface().(domain.Webhook))
		return nil
	})
	if err != nil {
		return nil, true, err
	}
	// Headers of revoked or client-less grants and of unreachable teams are
	// visited, never retained.
	texts := func(record metadataLogicalNode, names ...string) ([]string, error) {
		values := make([]string, len(names))
		cost := uint64(512)
		nodes := make([]metadataLogicalNode, len(names))
		for i, name := range names {
			node, err := issueHotField(ctx, record, name)
			if err != nil {
				return nil, err
			}
			if node.typ == nil || node.typ.Kind() != reflect.String || node.cost > metadataWorkBytes/4 {
				return nil, ErrMetadataRecordQuery
			}
			nodes[i] = node
			cost += node.cost * 2
		}
		if err := read.reserve(ctx, cost); err != nil {
			return nil, err
		}
		for i, node := range nodes {
			value, err := node.Value(ctx, int(node.cost))
			if err != nil {
				return nil, err
			}
			values[i] = value.String()
		}
		return values, nil
	}
	absent := func(record metadataLogicalNode, name string) (bool, error) {
		node, err := issueHotField(ctx, record, name)
		if err != nil {
			return false, err
		}
		if node.typ == nil || node.typ.Kind() != reflect.Pointer {
			return false, ErrMetadataRecordQuery
		}
		return node.isNil(ctx)
	}
	err = read.field("oauthAuthorizations").Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		live, err := absent(record, "RevokedAt")
		if err != nil || !live {
			return err
		}
		client, err := issueHotField(ctx, record, "ClientID")
		if err != nil {
			return err
		}
		if empty, err := client.StringEqual(ctx, ""); err != nil || empty {
			return err
		}
		values, err := texts(record, "ClientID", "UserID")
		if err != nil {
			return err
		}
		read.Data.OAuthAuthorizations = append(read.Data.OAuthAuthorizations, domain.OAuthAuthorization{ClientID: values[0], UserID: values[1]})
		return nil
	})
	if err != nil {
		return nil, true, err
	}
	err = read.field("teams").Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		private, err := issueHotField(ctx, record, "Private")
		if err != nil {
			return err
		}
		if private.typ == nil || private.typ.Kind() != reflect.Bool {
			return ErrMetadataRecordQuery
		}
		flag, err := private.Value(ctx, 4096)
		if err != nil || flag.Bool() {
			return err
		}
		for _, name := range []string{"ArchivedAt", "RetiredAt"} {
			if live, err := absent(record, name); err != nil || !live {
				return err
			}
		}
		values, err := texts(record, "ID")
		if err != nil {
			return err
		}
		read.Data.Teams = append(read.Data.Teams, domain.Team{ID: values[0]})
		return nil
	})
	if err != nil {
		return nil, true, err
	}
	return read, true, nil
}

// ApplicationWebhookClientsRead owns, by client ID, the webhook settings of
// every workspace's OAuth applications: ID, ClientID, Name, WebhookEnabled,
// WebhookURL and WebhookResourceTypes, the fields the reconcile reads.
type ApplicationWebhookClientsRead struct {
	Apps    map[string]domain.OAuthApplication
	release []func()
	once    sync.Once
}

func (r *ApplicationWebhookClientsRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Apps = nil
			for _, release := range r.release {
				release()
			}
			r.release = nil
		})
	}
}

// BeginApplicationWebhookClientsRead indexes the installed OAuth applications
// of the workspaces in WorkspaceKeys order, each in collection order, so a
// client ID found in several workspaces maps to the last, as the original
// index does. A workspace no longer installed is skipped.
func (s *SQLiteStore) BeginApplicationWebhookClientsRead(ctx context.Context) (_ *ApplicationWebhookClientsRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	result := &ApplicationWebhookClientsRead{Apps: map[string]domain.OAuthApplication{}}
	defer func() {
		if err != nil {
			result.Close()
		}
	}()
	reserve := func(cost int) error {
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, cost)
		if err == nil {
			result.release = append(result.release, release)
		}
		return err
	}
	if err := reserve(4096); err != nil {
		return nil, err
	}
	fields := strings.Fields("ID ClientID Name WebhookEnabled WebhookURL WebhookResourceTypes")
	for _, workspace := range s.WorkspaceKeys() {
		read, found, err := s.BeginWebhookDeliveryRead(ctx, workspace, false)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		err = read.field("oauthApplications").Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			var app domain.OAuthApplication
			if err := read.project(ctx, record, reflect.ValueOf(&app).Elem(), fields); err != nil {
				return err
			}
			cost := 1024 + 2*(len(app.ID)+len(app.ClientID)+len(app.Name)+len(app.WebhookURL))
			for _, value := range app.WebhookResourceTypes {
				cost += 64 + 2*len(value)
			}
			if err := reserve(cost); err != nil {
				return err
			}
			result.Apps[app.ClientID] = app
			return nil
		})
		read.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
