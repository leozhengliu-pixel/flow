package store

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"sync"
	"time"

	"flow/api/internal/domain"
)

type integrationDeliveryProfile struct {
	data, original                     domain.Bootstrap
	owner                              *ResourceRead
	comparison                         *ResourceRead
	deltas                             *legacyDiscussionInputs
	event                              string
	deliveryOrdinal, connectionOrdinal int
	deliveryNode, connectionNode       metadataLogicalNode
	once                               sync.Once
}

func (p *integrationDeliveryProfile) Input() *domain.Bootstrap { return &p.data }

func (p *integrationDeliveryProfile) Close() {
	if p != nil {
		p.once.Do(func() {
			p.data, p.original = domain.Bootstrap{}, domain.Bootstrap{}
			p.deliveryNode, p.connectionNode = metadataLogicalNode{}, metadataLogicalNode{}
			p.deltas.Close()
			if p.comparison != nil {
				p.comparison.Close()
				p.comparison = nil
			}
			p.owner.Close()
		})
	}
}

func (s *SQLiteStore) prepareIntegrationDeliveryProfile(ctx context.Context, source *metadataLogicalSource, id, event string) (_ metadataOrdinalProfile, err error) {
	if !s.BoundedMetadataEnabled() || source == nil || event != "integration_delivery.claimed" && event != "integration_delivery.retry" {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	p := &integrationDeliveryProfile{
		owner: &ResourceRead{store: s, source: source}, event: event,
		comparison:      &ResourceRead{store: s},
		deliveryOrdinal: -1, connectionOrdinal: -1,
		deltas: &legacyDiscussionInputs{input: &issueHotInputs{store: s}},
	}
	defer func() {
		if err != nil {
			p.Close()
		}
	}()
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	for _, field := range []string{"workspace", "viewer", "viewerRole"} {
		index, _, _ := metadataLogicalField(field)
		value, err := p.owner.node(ctx, source.fields[index], false)
		if err != nil {
			return nil, err
		}
		reflect.ValueOf(&p.data).Elem().Field(index).Set(value)
	}
	p.deliveryNode, p.deliveryOrdinal, err = integrationDeliveryFirst(ctx, p.owner, "integrationDeliveries", id)
	if err != nil {
		return nil, err
	}
	if p.deliveryOrdinal >= 0 {
		delivery, err := p.owner.integrationDeliveryNative(ctx, p.deliveryNode)
		if err != nil {
			return nil, err
		}
		p.data.IntegrationDeliveries = []domain.IntegrationDelivery{delivery}
		p.connectionNode, p.connectionOrdinal, err = integrationDeliveryFirst(ctx, p.owner, "integrationConnections", delivery.ConnectionID)
		if err != nil {
			return nil, err
		}
		if p.connectionOrdinal >= 0 {
			value, err := p.owner.projectNode(ctx, p.connectionNode, "ID", "DeliveryAttempts", "LastDeliveryAt", "LastError")
			if err != nil {
				return nil, err
			}
			p.data.IntegrationConnections = []domain.IntegrationConnection{value.Interface().(domain.IntegrationConnection)}
		}
	}
	comparison := p.data
	comparison.IntegrationDeliveries = integrationDeliveryComparisonHeaders(p.data.IntegrationDeliveries)
	cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(comparison), 0)
	if err != nil {
		return nil, err
	}
	if cost > (metadataWorkBytes-4096)/2 {
		return nil, ErrMetadataWorkBudget
	}
	if err := p.comparison.reserve(ctx, cost*2+4096); err != nil {
		return nil, err
	}
	p.original = cloneBootstrap(comparison)
	if p.data.Viewer.Via != nil {
		// Via is callback attribution, deliberately excluded by cloneBootstrap.
		p.original.Viewer.Via = cloneSnapshotValue(reflect.ValueOf(p.data.Viewer.Via)).Interface().(*domain.ActorBot)
	}
	return p, nil
}

func (p *integrationDeliveryProfile) Merge(ctx context.Context) (*metadataLogicalSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, next := p.original, p.data
	before.IntegrationDeliveries, next.IntegrationDeliveries = nil, nil
	before.IntegrationConnections, next.IntegrationConnections = nil, nil
	if !reflect.DeepEqual(before, next) || len(p.original.IntegrationDeliveries) != 1 || len(p.data.IntegrationDeliveries) != 1 ||
		len(p.original.IntegrationConnections) != 1 || len(p.data.IntegrationConnections) != 1 || p.deliveryOrdinal < 0 || p.connectionOrdinal < 0 {
		return nil, ErrBoundedMetadataCoverage
	}
	left, right := p.original.IntegrationDeliveries[0], p.data.IntegrationDeliveries[0]
	for _, field := range []struct {
		name  string
		value json.RawMessage
	}{
		{"Payload", right.Payload},
		{"PreviousValues", right.PreviousValues},
	} {
		node, err := issueHotField(ctx, p.deliveryNode, field.name)
		if err != nil {
			return nil, err
		}
		equal, err := integrationDeliveryRawFrameEqual(ctx, node, field.value)
		if err != nil {
			return nil, err
		}
		if !equal {
			return nil, ErrBoundedMetadataCoverage
		}
	}
	// The immutable frames above are the independent byte preimage. Keep
	// only scalar/header comparisons; deltaFrame retains the original bytes.
	right.Payload, right.PreviousValues = nil, nil
	deliveryFields := []string{"Status", "UpdatedAt"}
	connectionFields := []string(nil)
	if p.event == "integration_delivery.claimed" {
		if left.Attempts >= 8 || left.Status == "delivering" || right.Status != "delivering" ||
			!reflect.DeepEqual(p.original.IntegrationConnections, p.data.IntegrationConnections) {
			return nil, ErrBoundedMetadataCoverage
		}
	} else {
		if left.Status != "delivering" || right.Attempts != left.Attempts+1 ||
			right.Status != "delivered" && right.Status != "failed" || len(p.data.IntegrationConnections) != 1 || p.connectionOrdinal < 0 {
			return nil, ErrBoundedMetadataCoverage
		}
		deliveryFields = append(deliveryFields, "Attempts", "LastError", "NextAttemptAt")
		connectionFields = []string{"DeliveryAttempts", "LastDeliveryAt", "LastError"}
		oldConnection, connection := p.original.IntegrationConnections[0], p.data.IntegrationConnections[0]
		if connection.DeliveryAttempts != oldConnection.DeliveryAttempts+1 || connection.LastDeliveryAt == nil ||
			!connection.LastDeliveryAt.Equal(right.UpdatedAt) {
			return nil, ErrBoundedMetadataCoverage
		}
		if right.Status == "failed" {
			at := right.UpdatedAt.Add(time.Duration(1<<min(right.Attempts, 6)) * time.Minute)
			if right.NextAttemptAt == nil || !right.NextAttemptAt.Equal(at) || connection.LastError != right.LastError {
				return nil, ErrBoundedMetadataCoverage
			}
		} else if right.NextAttemptAt != nil || right.LastError != "" || connection.LastError != "" {
			return nil, ErrBoundedMetadataCoverage
		}
		oldConnection, connection = domain.IntegrationConnection{}, domain.IntegrationConnection{}
	}
	ctx = metadataLogicalSourceScratchContext(ctx, p.owner.source)
	frames, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer frames.Close()
	for _, storage := range p.owner.source.stores {
		if err := frames.inheritLocations(storage); err != nil {
			return nil, err
		}
	}
	if err := frames.inheritTimestampInputs(ctx); err != nil {
		return nil, err
	}
	delivery, err := p.deltas.deltaFrame(ctx, frames, p.deliveryNode, legacyDiscussionRecordDelta{
		before: reflect.ValueOf(left), next: reflect.ValueOf(right), allowed: deliveryFields,
	})
	if err != nil {
		return nil, err
	}
	var connection metadataLogicalNode
	if len(connectionFields) != 0 {
		connection, err = p.deltas.deltaFrame(ctx, frames, p.connectionNode, legacyDiscussionRecordDelta{
			before: reflect.ValueOf(p.original.IntegrationConnections[0]), next: reflect.ValueOf(p.data.IntegrationConnections[0]), allowed: connectionFields,
		})
		if err != nil {
			return nil, err
		}
	}
	merged, err := p.owner.source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		ordinal, node := p.deliveryOrdinal, delivery
		if name == "integrationConnections" && len(connectionFields) != 0 {
			ordinal, node = p.connectionOrdinal, connection
		} else if name != "integrationDeliveries" {
			return metadataAggregateStoredNode{value: original}, false, nil
		}
		return legacyDiscussionSlicePatch{
			base: original, shape: original, replacements: map[int]metadataLogicalNode{ordinal: node}, dropped: map[int]bool{},
		}, true, nil
	})
	if err != nil {
		return nil, err
	}
	// deltaFrame writes native changes synchronously into immutable frames;
	// Transform likewise finishes every patch before it returns. Neither
	// retains these comparison values. Captured p.data remains separately
	// owned until the outer output lifetime ends.
	p.original = domain.Bootstrap{}
	before, next = domain.Bootstrap{}, domain.Bootstrap{}
	left, right = domain.IntegrationDelivery{}, domain.IntegrationDelivery{}
	if p.comparison != nil {
		p.comparison.Close()
		p.comparison = nil
	}
	return merged, nil
}

func integrationDeliveryComparisonHeaders(values []domain.IntegrationDelivery) []domain.IntegrationDelivery {
	if values == nil {
		return nil
	}
	headers := make([]domain.IntegrationDelivery, len(values))
	copy(headers, values)
	for index := range headers {
		headers[index].Payload, headers[index].PreviousValues = nil, nil
	}
	return headers
}

func integrationDeliveryRawFrameEqual(ctx context.Context, node metadataLogicalNode, value json.RawMessage) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if node.source == nil || node.typ != reflect.TypeFor[json.RawMessage]() {
		return false, ErrBoundedMetadataCoverage
	}
	nilFrame, err := node.isNil(ctx)
	if err != nil || nilFrame != (value == nil) {
		return false, err
	}
	equal := true
	err = node.StreamBytes(ctx, func(input io.Reader) error {
		var chunk [4096]byte
		offset := 0
		for {
			count, err := input.Read(chunk[:])
			if count > len(value)-offset || !bytes.Equal(chunk[:count], value[offset:offset+count]) {
				equal = false
				return nil
			}
			offset += count
			if err == io.EOF {
				equal = offset == len(value)
				return nil
			}
			if err != nil {
				return err
			}
		}
	})
	return equal && err == nil, err
}

// MutateIntegrationDelivery supplies a closed first-occurrence profile to the
// shared metadata-only transaction kernel. It is not an authorization boundary.
func (s *SQLiteStore) MutateIntegrationDelivery(ctx context.Context, workspace, id, event string, mutate func(*domain.Bootstrap) error) (err error) {
	if !s.BoundedMetadataEnabled() || mutate == nil || !metadataOutputOwnerPresent(ctx) ||
		event != "integration_delivery.claimed" && event != "integration_delivery.retry" {
		return ErrBoundedMetadataCoverage
	}
	ctx, trace := startMutationTrace(ctx)
	defer func() { trace.report(workspace, event, err) }()
	traceStart(ctx, "integration_delivery")
	ctx, closePages, err := s.integrationDeliveryPageContext(withIntegrationDeliveryStream(ctx, event))
	if err != nil {
		return err
	}
	defer closePages()
	factory := func(ctx context.Context, source *metadataLogicalSource) (metadataOrdinalProfile, error) {
		return s.prepareIntegrationDeliveryProfile(ctx, source, id, event)
	}
	return s.mutateMetadataOrdinalProfile(ctx, workspace, event, id, nil, factory, mutate)
}
