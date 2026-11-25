package store

import (
	"context"
	"reflect"
	"slices"
	"sync"

	"flow/api/internal/domain"
)

// ResourceRelationRead owns the visibility graph and selected relation rows.
// Apply the caller's existing credential filtering to Data before Select.
type ResourceRelationRead struct {
	Data                domain.Bootstrap
	ProjectRelations    []domain.ProjectRelation
	InitiativeRelations []domain.InitiativeRelation
	directory           *ResourceDirectoryRead
	owner               *ResourceRead
	kind                string
	trusted             bool
	selected            bool
	once                sync.Once
}

func (r *ResourceRelationRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Data = domain.Bootstrap{}
			r.ProjectRelations, r.InitiativeRelations = nil, nil
			r.directory.Close()
			r.owner.Close()
		})
	}
}

// BeginResourceRelationRead preserves the fresh installed Bootstrap source
// while retaining a caller's SQL reader for hydration and authority queries.
func (s *SQLiteStore) BeginResourceRelationRead(ctx context.Context, workspace, userID string, key *domain.APIKey, kind string) (_ *ResourceRelationRead, err error) {
	if kind != "projects" && kind != "initiatives" {
		return nil, ErrMetadataRecordQuery
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), resourceDirectoryInstalledSourceKey{}, true)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	directory, err := s.BeginResourceDirectoryRead(ctx, workspace, userID, key, kind, nil)
	if err != nil {
		return nil, err
	}
	read := &ResourceRelationRead{
		Data: directory.Data, directory: directory, owner: &ResourceRead{store: s},
		kind: kind, trusted: userID == "",
	}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err := read.owner.reserve(ctx, 64<<10); err != nil {
		return nil, err
	}
	return read, nil
}

// Select keeps every matching occurrence in source order. In particular, guest
// projection can retain initiative relations after removing initiative rows.
func (r *ResourceRelationRead) Select(ctx context.Context, id string) error {
	if r == nil || r.directory == nil || r.directory.owner.source == nil || r.selected {
		return ErrMetadataRecordQuery
	}
	r.selected = true
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	if err := r.directory.Validate(ctx); err != nil {
		return err
	}
	if r.kind == "initiatives" {
		cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(r.Data.InitiativeRelations), 0)
		if err != nil {
			return err
		}
		if err := r.owner.reserve(ctx, uint64(cost)+uint64(len(r.Data.InitiativeRelations))*512+4096); err != nil {
			return err
		}
		r.InitiativeRelations = slices.DeleteFunc(slices.Clone(r.Data.InitiativeRelations), func(item domain.InitiativeRelation) bool {
			return item.InitiativeID != id && item.RelatedInitiativeID != id
		})
		return r.directory.Validate(ctx)
	}
	if err := r.owner.reserve(ctx, uint64(len(r.Data.Projects))*256+4096); err != nil {
		return err
	}
	visible := make(map[string]bool, len(r.Data.Projects))
	for _, project := range r.Data.Projects {
		visible[project.ID] = true
	}
	index, _, _ := metadataLogicalField("projectRelations")
	collection := r.directory.owner.source.fields[index]
	nilValue, err := collection.isNil(ctx)
	if err != nil {
		return err
	}
	if !nilValue {
		r.ProjectRelations = []domain.ProjectRelation{}
	}
	err = collection.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		headerOwner := &ResourceRead{store: r.owner.store}
		defer headerOwner.Close()
		header, err := headerOwner.projectNode(ctx, node, "ProjectID", "RelatedProjectID")
		if err != nil {
			return err
		}
		item := header.Interface().(domain.ProjectRelation)
		if item.ProjectID != id && item.RelatedProjectID != id ||
			!r.trusted && (!visible[item.ProjectID] || !visible[item.RelatedProjectID]) {
			return nil
		}
		value, err := r.owner.node(ctx, node, true)
		if err != nil {
			return err
		}
		r.ProjectRelations = append(r.ProjectRelations, value.Interface().(domain.ProjectRelation))
		return nil
	})
	if err != nil {
		return err
	}
	cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(r.ProjectRelations), 0)
	if err != nil {
		return err
	}
	if err := r.owner.reserve(ctx, uint64(cost)+4096); err != nil {
		return err
	}
	return r.directory.Validate(ctx)
}
