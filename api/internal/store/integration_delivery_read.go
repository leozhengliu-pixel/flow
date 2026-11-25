package store

import (
	"context"
	"sync"

	"flow/api/internal/domain"
)

// IntegrationDeliveryRead captures the entry WorkspaceMetadata generation.
// Delivery contains entry guards; Connection contains the captured network
// configuration of its first occurrence, without session authorization.
type IntegrationDeliveryRead struct {
	HIPAA           bool
	DeliveryFound   bool
	ConnectionFound bool
	Delivery        domain.IntegrationDelivery
	Connection      domain.IntegrationConnection
	owner           *ResourceRead
	once            sync.Once
}

func (r *IntegrationDeliveryRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Delivery, r.Connection = domain.IntegrationDelivery{}, domain.IntegrationConnection{}
			r.HIPAA, r.DeliveryFound, r.ConnectionFound = false, false, false
			r.owner.Close()
		})
	}
}

func integrationDeliveryFirst(ctx context.Context, owner *ResourceRead, field, id string) (metadataLogicalNode, int, error) {
	var selected metadataLogicalNode
	ordinal := -1
	err := owner.visitNative(ctx, field, func(index int, node metadataLogicalNode) error {
		key, err := issueHotField(ctx, node, "ID")
		if err != nil {
			return err
		}
		match, err := key.StringEqual(ctx, id)
		if err != nil || !match {
			return err
		}
		selected, ordinal = node, index
		return ErrMetadataIterationDone
	})
	return selected, ordinal, err
}

// Entry timing is context.Background in the original WorkspaceMetadata call.
// Keep cancellation and actor handoffs out of this capture; later endpoint,
// claim and network stages retain their original request context.
func (s *SQLiteStore) BeginIntegrationDeliveryRead(ctx context.Context, workspace, id string) (_ *IntegrationDeliveryRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	ctx = context.Background()
	installed, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	defer installed.Close()
	owner, ctx, err := installed.commentActionOwner(ctx)
	if err != nil {
		return nil, true, err
	}
	read := &IntegrationDeliveryRead{owner: owner}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	index, _, _ := metadataLogicalField("workspaceSettings")
	settings, err := owner.projectNode(ctx, owner.source.fields[index], "HIPAACompliance")
	if err != nil {
		return nil, true, err
	}
	read.HIPAA = settings.Interface().(domain.WorkspaceSettings).HIPAACompliance
	if read.HIPAA {
		return read, true, nil
	}
	node, ordinal, err := integrationDeliveryFirst(ctx, owner, "integrationDeliveries", id)
	if err != nil {
		return nil, true, err
	}
	if ordinal < 0 {
		return read, true, nil
	}
	value, err := owner.projectNode(ctx, node, "ID", "ConnectionID", "Status", "Attempts")
	if err != nil {
		return nil, true, err
	}
	read.Delivery, read.DeliveryFound = value.Interface().(domain.IntegrationDelivery), true
	// The original status/attempt checks precede connection lookup. An
	// oversized irrelevant connection cannot replace either refusal.
	if read.Delivery.Status != "pending" && read.Delivery.Status != "failed" || read.Delivery.Attempts >= 8 {
		return read, true, nil
	}
	node, ordinal, err = integrationDeliveryFirst(ctx, owner, "integrationConnections", read.Delivery.ConnectionID)
	if err != nil {
		return nil, true, err
	}
	if ordinal < 0 {
		return read, true, nil
	}
	value, err = owner.projectNode(ctx, node, "ID", "Provider")
	if err != nil {
		return nil, true, err
	}
	read.Connection, read.ConnectionFound = value.Interface().(domain.IntegrationConnection), true
	config, err := issueHotField(ctx, node, "Config")
	if err != nil {
		return nil, true, err
	}
	if err := owner.reserve(ctx, 16<<10); err != nil {
		return nil, true, err
	}
	read.Connection.Config = map[string]string{}
	err = config.MapEntries(ctx, func(key, value metadataLogicalNode) error {
		for _, name := range []string{"deliveryURL", "deliverySecret", "deliverySecretEnv", "deliveryTokenEnv"} {
			match, err := key.StringEqual(ctx, name)
			if err != nil {
				return err
			}
			if !match {
				continue
			}
			decoded, err := owner.node(ctx, value, false)
			if err != nil {
				return err
			}
			read.Connection.Config[name] = decoded.String()
			break
		}
		return nil
	})
	if err != nil {
		return nil, true, err
	}
	return read, true, nil
}
