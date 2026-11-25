package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// persistLegacyActorImage persists only mutateFull's actor step. The caller
// owns the transaction, publication and policy installation.
func (s *SQLiteStore) persistLegacyActorImage(ctx context.Context, tx *sqlTx, workspace string, source *metadataLogicalSource, actor *domain.User) (_ *legacyActorImage, err error) {
	if s == nil || !s.BoundedMetadataEnabled() || source == nil || tx == nil || tx.database != s.db || tx.dialect != s.dialect {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	defer source.Release()
	ctx = metadataLogicalSourceScratchContext(metadataLogicalUnpinnedContext(ctx), source)
	image, err := s.prepareLegacyActorImage(ctx, source, actor)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			image.Close()
		}
	}()
	work := &ResourceRead{store: s}
	defer work.Close()
	if uint64(len(workspace)) > (metadataWorkBytes-(256<<10))/16 {
		return nil, ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(len(workspace))*16+256<<10); err != nil {
		return nil, err
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return nil, err
	}
	if stamp.workspaceID == "" || source.Stamp().workspaceID != stamp.workspaceID {
		return nil, ErrMetadataRecordQuery
	}
	workspaceIndex, _, _ := metadataLogicalField("workspace")
	workspaceID, err := issueHotField(ctx, source.fields[workspaceIndex], "ID")
	if err != nil {
		return nil, err
	}
	matched, err := workspaceID.StringEqual(ctx, stamp.workspaceID)
	if err != nil {
		return nil, err
	}
	if !matched {
		return nil, ErrMetadataRecordQuery
	}
	if actor == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return image, nil
	}
	actorWork, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(*actor), 0)
	if err != nil {
		return nil, err
	}
	if uint64(actorWork) > (metadataWorkBytes-(64<<10))/8 {
		return nil, ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(actorWork)*8+64<<10); err != nil {
		return nil, err
	}
	usersIndex, _, _ := metadataLogicalField("users")
	viewerIndex, _, _ := metadataLogicalField("viewer")
	users, viewer := source.fields[usersIndex], source.fields[viewerIndex]
	beforeUsers, actorOrdinal, err := legacyActorWriteHeaders(ctx, work, users, actor.ID)
	if err != nil {
		return nil, err
	}
	if err := legacyActorWriteNodeWork(ctx, work, viewer, 64); err != nil {
		return nil, err
	}
	oldViewer, err := viewer.Value(ctx, metadataWorkBytes)
	if err != nil {
		return nil, err
	}
	before := domain.Bootstrap{
		Workspace: domain.Workspace{ID: stamp.workspaceID},
		Viewer:    oldViewer.Interface().(domain.User),
		Users:     beforeUsers,
	}
	next := before
	next.Viewer, next.Users = *actor, slices.Clone(before.Users)
	if actorOrdinal < len(next.Users) {
		next.Users[actorOrdinal] = *actor
	} else {
		next.Users = append(next.Users, *actor)
	}
	rootShape, err := s.legacyActorWriteRootWork(ctx, tx, workspace, work)
	if err != nil {
		return nil, err
	}
	inline := false
	if !metadataValueEqual(reflect.ValueOf(before.Users), reflect.ValueOf(next.Users)) {
		beforeShape, err := legacyActorWriteShape(before.Users)
		if err != nil {
			return nil, err
		}
		afterShape, err := legacyActorWriteShape(next.Users)
		if err != nil {
			return nil, err
		}
		inline = rootShape != "array" || beforeShape != "array" && len(before.Users) != 0 || afterShape != "array"
	}
	if inline {
		// Invalid/duplicate IDs cannot split. The literal writer needs every
		// native User in that explicitly admitted field, including duplicates.
		if err := legacyActorWriteNodeWork(ctx, work, users, 64); err != nil {
			return nil, err
		}
		full, err := users.Value(ctx, metadataWorkBytes)
		if err != nil {
			return nil, err
		}
		before.Users = full.Interface().([]domain.User)
		next.Users = slices.Clone(before.Users)
		if actorOrdinal < len(next.Users) {
			next.Users[actorOrdinal] = *actor
		} else {
			next.Users = append(next.Users, *actor)
		}
	}
	// Both images own their slice containers. The generic writer can replace
	// or remove the first actor without changing the native source snapshot.
	expected := slices.Clone(next.Users)
	writeCtx := withLegacyMetadataDeltaReadBudget(ctx, work)
	if _, err := s.writeWorkspaceMetadataDelta(writeCtx, tx, workspace, before, &next); err != nil {
		return nil, err
	}
	dropped := len(next.Users) == len(expected)-1
	if len(next.Users) != len(expected) && !dropped {
		return nil, ErrBoundedMetadataCoverage
	}
	for i := range expected {
		if i == actorOrdinal {
			continue
		}
		j := i
		if dropped && i > actorOrdinal {
			j--
		}
		if j < 0 || j >= len(next.Users) || !metadataValueEqual(reflect.ValueOf(expected[i]), reflect.ValueOf(next.Users[j])) {
			return nil, ErrBoundedMetadataCoverage
		}
	}
	if dropped || !reflect.DeepEqual(expected[actorOrdinal], next.Users[actorOrdinal]) {
		var actual *domain.User
		if !dropped {
			actual = &next.Users[actorOrdinal]
		}
		candidate, err := legacyActorWritePostimage(ctx, image.Source(), actorOrdinal, actual)
		if err != nil {
			return nil, err
		}
		image.owner.source.Release()
		image.owner.source = candidate
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return image, nil
}

// Only IDs and the complete first actor enter a split-field adapter. Reserve
// all header, map, sorting and JSON copies before allocating those containers.
func legacyActorWriteHeaders(ctx context.Context, work *ResourceRead, users metadataLogicalNode, actorID string) ([]domain.User, int, error) {
	count, actorOrdinal, cost := 0, -1, uint64(64<<10)
	var actorNode metadataLogicalNode
	err := users.Children(ctx, func(_ reflect.Value, user metadataLogicalNode) error {
		id, err := issueHotField(ctx, user, "ID")
		if err != nil {
			return err
		}
		if cost > metadataWorkBytes-8192 || id.cost > (metadataWorkBytes-cost-8192)/64 {
			return ErrMetadataWorkBudget
		}
		cost += id.cost*64 + 8192
		match, err := id.StringEqual(ctx, actorID)
		if err != nil {
			return err
		}
		if match && actorOrdinal < 0 {
			actorOrdinal, actorNode = count, user
		}
		count++
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if err := work.reserve(ctx, cost); err != nil {
		return nil, 0, err
	}
	if actorOrdinal >= 0 {
		if err := legacyActorWriteNodeWork(ctx, work, actorNode, 64); err != nil {
			return nil, 0, err
		}
	}
	nilUsers, err := users.isNil(ctx)
	if err != nil {
		return nil, 0, err
	}
	var result []domain.User
	if !nilUsers {
		result = make([]domain.User, 0, count)
	}
	err = users.Children(ctx, func(_ reflect.Value, user metadataLogicalNode) error {
		if len(result) == actorOrdinal {
			value, err := user.Value(ctx, metadataWorkBytes)
			if err != nil {
				return err
			}
			result = append(result, value.Interface().(domain.User))
			return nil
		}
		id, err := issueHotField(ctx, user, "ID")
		if err != nil {
			return err
		}
		value, err := id.Value(ctx, metadataWorkBytes)
		if err != nil {
			return err
		}
		result = append(result, domain.User{ID: value.String()})
		return nil
	})
	if actorOrdinal < 0 {
		actorOrdinal = count
	}
	return result, actorOrdinal, err
}

func legacyActorWriteNodeWork(ctx context.Context, work *ResourceRead, node metadataLogicalNode, copies uint64) error {
	if node.cost > (metadataWorkBytes-(64<<10))/copies {
		return ErrMetadataWorkBudget
	}
	return work.reserve(ctx, node.cost*copies+64<<10)
}

// Use the actual JSON splitter: escaping/invalid UTF-8 can change key identity
// and duplicate detection as well as the nil/empty/inline shape decision.
func legacyActorWriteShape(users []domain.User) (string, error) {
	raw, err := json.Marshal(struct {
		Users []domain.User `json:"users"`
	}{Users: users})
	if err != nil {
		return "", err
	}
	_, shapes, _, err := splitWorkspaceMetadata(raw)
	if err != nil {
		return "", err
	}
	return shapes["users"], nil
}

func (s *SQLiteStore) legacyActorWriteRootWork(ctx context.Context, tx *sqlTx, workspace string, work *ResourceRead) (string, error) {
	length := work.byteLength("data")
	var size int64
	lock := ""
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	if err := tx.QueryRowContext(ctx, "SELECT "+length+" FROM workspace_states WHERE workspace_key=?"+lock, workspace).Scan(&size); err != nil {
		return "", err
	}
	if size < 0 || size > MetadataRecordPageMaxBytes || uint64(size) > (metadataWorkBytes-(64<<10))/128 {
		return "", ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(size)*128+64<<10); err != nil {
		return "", err
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT CASE WHEN "+length+"<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?"+lock, size, workspace).Scan(&raw); err != nil {
		return "", err
	}
	if raw == nil || int64(len(raw)) != size {
		return "", ErrConcurrentWrite
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", err
	}
	var shapes map[string]string
	if manifest := root[metadataCollectionsKey]; manifest != nil {
		if err := json.Unmarshal(manifest, &shapes); err != nil {
			return "", err
		}
	}
	if shape := shapes["users"]; shape != "" && shape != "array" {
		return "", fmt.Errorf("%w: unsupported Users shape %s", ErrBoundedMetadataCoverage, shape)
	}
	return shapes["users"], nil
}

func legacyActorWritePostimage(ctx context.Context, source *metadataLogicalSource, ordinal int, actor *domain.User) (*metadataLogicalSource, error) {
	frame, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer frame.Close()
	for _, storage := range source.stores {
		if err := frame.inheritLocations(storage); err != nil {
			return nil, err
		}
	}
	var replacement metadataAggregateNode
	if actor != nil {
		// A changed actor is the delta writer's fresh standard JSON decode.
		// Its Via was explicitly retained by decodeMetadataRecord.
		if err := frame.auditJSONLocations(reflect.ValueOf(*actor)); err != nil {
			return nil, err
		}
		node, err := frame.writeAggregateNode(legacyActorUserNode{value: reflect.ValueOf(*actor)}, 0)
		if err != nil {
			return nil, err
		}
		if node.unavailable || node.blocked {
			return nil, ErrBoundedMetadataCoverage
		}
		replacement = metadataAggregateStoredNode{value: node}
	}
	return source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == "users" {
			return legacyActorWriteUsersNode{base: original, ordinal: ordinal, replacement: replacement}, true, nil
		}
		return metadataAggregateStoredNode{value: original}, false, nil
	})
}

type legacyActorWriteUsersNode struct {
	base        metadataLogicalNode
	ordinal     int
	replacement metadataAggregateNode
}

func (n legacyActorWriteUsersNode) nodeType() reflect.Type           { return n.base.typ }
func (n legacyActorWriteUsersNode) logicalBase() metadataLogicalNode { return n.base }
func (n legacyActorWriteUsersNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyActorWriteUsersNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	ordinal, output := 0, 0
	return n.base.Children(ctx, func(_ reflect.Value, user metadataLogicalNode) error {
		var child metadataAggregateNode = metadataAggregateStoredNode{value: user}
		if ordinal == n.ordinal {
			child = n.replacement
		}
		ordinal++
		if child == nil {
			return nil
		}
		if err := visit(reflect.ValueOf(output), child); err != nil {
			return err
		}
		output++
		return nil
	})
}
