package store

import (
	"context"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// UpdatePortfolioProject owns project edits that need content, dependent
// document headers, or native references outside the record-key format.
func (s *SQLiteStore) UpdatePortfolioProject(ctx context.Context, workspace, id string, input domain.ProjectMutationInput, mutate func(*domain.Bootstrap) error) error {
	return s.writePortfolioImage(ctx, workspace, "project.updated", id, input, &portfolioProjectImage{ref: id, input: input}, func(data *domain.Bootstrap) (string, error) {
		return id, mutate(data)
	})
}

func (s *SQLiteStore) writePortfolioImage(ctx context.Context, workspace, event, id string, payload any, profile scopedImageWriteProfile, mutate func(*domain.Bootstrap) (string, error)) error {
	ctx = withMutationAggregateID(ctx, id)
	// The event transaction commits the staged audit, resetting it on retry.
	ctx, _, callback := collectMutationAudit(ctx, workspace, mutate)
	return s.mutateBoundedScopedImage(ctx, workspace, event, payload, profile, callback)
}

type portfolioProjectImage struct {
	ref                          string
	input                        domain.ProjectMutationInput
	projectIndex, projectOrdinal int
	documentOrdinals             []int
}

func (*portfolioProjectImage) kind() scopedImageWriteKind { return scopedImagePortfolioProject }
func (*portfolioProjectImage) extraFields() []string {
	return []string{"documents", "projectRelations"}
}

func (p *portfolioProjectImage) resolve(context.Context, *ResourceRead, *metadataLogicalSource) (MutationScope, error) {
	if p.input.StartDate != nil || p.input.UpdateSchedule != nil {
		return MutationScope{Resources: []string{p.ref}}, nil
	}
	return MutationScope{}, nil
}

func portfolioReadFields(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource, data *domain.Bootstrap, names ...string) error {
	out := reflect.ValueOf(data).Elem()
	for _, name := range names {
		index, _, _ := metadataLogicalField(name)
		value, err := cycleScheduleNativeValue(ctx, owner, source.fields[index])
		if err != nil {
			return err
		}
		out.Field(index).Set(value)
	}
	return nil
}

func (p *portfolioProjectImage) inputs(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource) (domain.Bootstrap, error) {
	data := domain.Bootstrap{}
	p.projectIndex, p.projectOrdinal, p.documentOrdinals = -1, -1, nil
	if err := portfolioReadFields(ctx, owner, source, &data, "workspace", "viewer", "viewerRole", "users"); err != nil {
		return data, err
	}
	fields := []string{}
	if p.input.LabelIDs != nil || p.input.TeamIDs != nil {
		fields = append(fields, "teams", "teamSettings", "teamParents")
	}
	if p.input.LabelIDs != nil {
		fields = append(fields, "labels", "labelGroups")
	}
	if p.input.StatusID != nil {
		fields = append(fields, "projectStatuses")
	}
	if len(p.input.Initiatives) != 0 || len(p.input.Customers) != 0 {
		fields = append(fields, "workspaceSettings")
	}
	if p.input.DependencyIDs != nil || p.input.DependencyRelations != nil {
		// The callback canonically moves replaced relations to the tail.
		fields = append(fields, "projectRelations")
	}
	if err := portfolioReadFields(ctx, owner, source, &data, fields...); err != nil {
		return data, err
	}
	wanted := map[string]bool{}
	add := func(id string) error {
		if wanted[id] {
			return nil
		}
		if err := owner.reserve(ctx, uint64(len(id))+128); err != nil {
			return err
		}
		wanted[id] = true
		return nil
	}
	for _, id := range p.input.DependencyIDs {
		if err := add(strings.TrimSpace(id)); err != nil {
			return data, err
		}
	}
	for _, relation := range p.input.DependencyRelations {
		if err := add(strings.TrimSpace(relation.ProjectID)); err != nil {
			return data, err
		}
	}
	if p.input.TeamIDs != nil {
		names := []string{"ID", "IssueID", "IssueTeamID", "ProjectIDs", "InheritedTeamIDs"}
		err := scopedImageVisit(ctx, source, "documents", func(ordinal int, node metadataLogicalNode) error {
			var linked bool
			err := owner.withHeader(ctx, node, []string{"ProjectIDs"}, func(value reflect.Value) error {
				linked = slices.Contains(value.Interface().(domain.Document).ProjectIDs, p.ref)
				return nil
			})
			if err != nil || !linked {
				return err
			}
			value, err := cycleScheduleProjectNode(ctx, owner, node, names...)
			if err != nil {
				return err
			}
			doc := value.Interface().(domain.Document)
			for _, id := range doc.ProjectIDs {
				if err := add(id); err != nil {
					return err
				}
			}
			data.Documents = append(data.Documents, doc)
			p.documentOrdinals = append(p.documentOrdinals, ordinal)
			return nil
		})
		if err != nil {
			return data, err
		}
	}
	err := scopedImageVisit(ctx, source, "projects", func(ordinal int, node metadataLogicalNode) error {
		return owner.withHeader(ctx, node, []string{"ID"}, func(value reflect.Value) error {
			id := value.Interface().(domain.Project).ID
			if id == p.ref && p.projectIndex < 0 {
				selected, err := cycleScheduleNativeValue(ctx, owner, node)
				if err != nil {
					return err
				}
				p.projectIndex, p.projectOrdinal = len(data.Projects), ordinal
				data.Projects = append(data.Projects, selected.Interface().(domain.Project))
			} else if wanted[id] {
				selected, err := cycleScheduleProjectNode(ctx, owner, node, "ID", "TeamIDs")
				if err != nil {
					return err
				}
				data.Projects = append(data.Projects, selected.Interface().(domain.Project))
			}
			return nil
		})
	})
	if err != nil {
		return data, err
	}
	if p.input.UpdateSchedule != nil && p.input.UpdateSchedule.Mode == "never" {
		index, _, _ := metadataLogicalField("projectUpdates")
		updates := source.fields[index]
		isNil, err := updates.isNil(ctx)
		if err != nil {
			return data, err
		}
		if !isNil {
			data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
			err := updates.MapEntries(ctx, func(key, node metadataLogicalNode) error {
				match, err := key.StringEqual(ctx, p.ref)
				if err != nil || !match {
					return err
				}
				value, err := cycleScheduleNativeValue(ctx, owner, node)
				if err == nil {
					data.ProjectUpdates[p.ref] = value.Interface().([]domain.ProjectUpdate)
				}
				return err
			})
			if err != nil {
				return data, err
			}
		}
	}
	return data, nil
}

func (p *portfolioProjectImage) validate(before, after domain.Bootstrap) error {
	if len(before.Projects) != len(after.Projects) || len(before.Documents) != len(after.Documents) {
		return ErrBoundedMetadataCoverage
	}
	for i, project := range before.Projects {
		if project.ID != after.Projects[i].ID || i != p.projectIndex && !reflect.DeepEqual(project, after.Projects[i]) {
			return ErrBoundedMetadataCoverage
		}
	}
	for i, doc := range after.Documents {
		doc.InheritedTeamIDs, doc.IssueTeamID = before.Documents[i].InheritedTeamIDs, before.Documents[i].IssueTeamID
		if !reflect.DeepEqual(doc, before.Documents[i]) {
			return ErrBoundedMetadataCoverage
		}
	}
	return nil
}

func (*portfolioProjectImage) reserveCallback(ctx context.Context, owner *ResourceRead, data domain.Bootstrap, payload any) error {
	return (&boundedDiscussionAttempt{owner: owner}).reserveCallback(ctx, data, payload)
}

func (p *portfolioProjectImage) changedProjectStartDates(before, after domain.Bootstrap) []string {
	if p.projectIndex >= 0 && optionalValue(before.Projects[p.projectIndex].StartDate) != optionalValue(after.Projects[p.projectIndex].StartDate) {
		return []string{p.ref}
	}
	return nil
}

func (p *portfolioProjectImage) image(ctx context.Context, _ *ResourceRead, source *metadataLogicalSource, before, after domain.Bootstrap) (*metadataLogicalSource, error) {
	return source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		switch name {
		case "projects":
			if p.projectIndex >= 0 {
				value := legacyMetadataWriteNativeRecord{reflect.ValueOf(after.Projects[p.projectIndex])}
				return documentUpdateCollection{original, []int{p.projectOrdinal}, []metadataAggregateNode{value}}, true, nil
			}
		case "documents":
			patches := map[int]map[string]reflect.Value{}
			for i, doc := range after.Documents {
				old := before.Documents[i]
				if old.IssueTeamID != doc.IssueTeamID || !reflect.DeepEqual(old.InheritedTeamIDs, doc.InheritedTeamIDs) {
					patches[p.documentOrdinals[i]] = map[string]reflect.Value{
						"InheritedTeamIDs": reflect.ValueOf(doc.InheritedTeamIDs),
						"IssueTeamID":      reflect.ValueOf(doc.IssueTeamID),
					}
				}
			}
			if len(patches) > 0 {
				return portfolioCollection{base: original, fields: patches}, true, nil
			}
		case "projectUpdates":
			old, existed := before.ProjectUpdates[p.ref]
			value, present := after.ProjectUpdates[p.ref]
			if present && (!existed || !reflect.DeepEqual(old, value)) {
				return metadataLogicalPatchedCollection{base: metadataAggregateStoredNode{original}, shape: original, key: p.ref, value: metadataAggregateNativeNode{reflect.ValueOf(value)}}, true, nil
			}
		case "projectRelations":
			if !reflect.DeepEqual(before.ProjectRelations, after.ProjectRelations) {
				return metadataAggregateNativeNode{reflect.ValueOf(after.ProjectRelations)}, true, nil
			}
		}
		return metadataAggregateStoredNode{original}, false, nil
	})
}

// portfolioCollection overlays selected fields or drops selected occurrences
// without re-encoding the bodies of untouched records.
type portfolioCollection struct {
	base   metadataLogicalNode
	shape  metadataLogicalNode
	fields map[int]map[string]reflect.Value
	remove map[int]bool
	first  []metadataAggregateNode
}

func (n portfolioCollection) logicalBase() metadataLogicalNode {
	if n.shape.typ != nil {
		return n.shape
	}
	return n.base
}
func (n portfolioCollection) nodeType() reflect.Type { return n.base.typ }
func (n portfolioCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n portfolioCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	next := 0
	for _, node := range n.first {
		if err := visit(reflect.ValueOf(next), node); err != nil {
			return err
		}
		next++
	}
	return n.base.Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		i := int(ordinal.Int())
		if n.remove[i] {
			return nil
		}
		var value metadataAggregateNode = metadataAggregateStoredNode{node}
		if fields := n.fields[i]; len(fields) != 0 {
			value = legacyDiscussionNativeDelta{base: node, changed: fields}
		}
		err := visit(reflect.ValueOf(next), value)
		next++
		return err
	})
}

// PortfolioDocumentCreate declares the rows named by the document create
// callback. The resulting ID and document are created inside the transaction.
type PortfolioDocumentCreate struct {
	TemplateID    string
	IssueIDs      []string
	ProjectIDs    []string
	SubscriberIDs []string
}

func (s *SQLiteStore) CreatePortfolioDocument(ctx context.Context, workspace string, selection PortfolioDocumentCreate, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	profile := &portfolioDocumentImage{create: &selection}
	return s.writePortfolioImage(ctx, workspace, "document.created", "", payload, profile, mutate)
}

func (s *SQLiteStore) DeletePortfolioDocument(ctx context.Context, workspace, ref string, mutate func(*domain.Bootstrap, string) error) error {
	profile := &portfolioDocumentImage{ref: ref}
	return s.writePortfolioImage(ctx, workspace, "document.deleted", ref, nil, profile, func(data *domain.Bootstrap) (string, error) {
		return ref, mutate(data, profile.documentID)
	})
}

type portfolioDocumentImage struct {
	create          *PortfolioDocumentCreate
	ref, documentID string
	documentOrdinal int
	ordinals        map[string][]int
}

func (*portfolioDocumentImage) kind() scopedImageWriteKind { return scopedImagePortfolioDocument }

func (p *portfolioDocumentImage) extraFields() []string {
	if p.create != nil {
		return []string{"documents"}
	}
	return []string{"documents", "drafts", "favorites", "subscriptions", "trash"}
}

func (p *portfolioDocumentImage) resolve(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource) (MutationScope, error) {
	if p.create != nil {
		return MutationScope{IssueIDs: p.create.IssueIDs}, nil
	}
	p.documentOrdinal, p.documentID = -1, p.ref
	rank := 0
	err := scopedImageVisit(ctx, source, "documents", func(ordinal int, node metadataLogicalNode) error {
		if rank == 2 {
			return nil
		}
		return owner.withHeader(ctx, node, []string{"ID", "SlugID", "PreviousSlugIDs"}, func(value reflect.Value) error {
			doc := value.Interface().(domain.Document)
			next := 0
			switch {
			case doc.ID == p.ref || doc.SlugID == p.ref:
				next = 2
			case p.ref != "" && slices.Contains(doc.PreviousSlugIDs, p.ref):
				next = 1
			}
			if next > rank {
				if err := owner.reserve(ctx, uint64(len(doc.ID))+128); err != nil {
					return err
				}
				p.documentOrdinal, p.documentID, rank = ordinal, doc.ID, next
			}
			return nil
		})
	})
	if err == nil && rank == 1 {
		// A historical slug yields an ID. The legacy delete then takes the
		// first occurrence of that ID, which may be an older imported row.
		err = scopedImageVisit(ctx, source, "documents", func(ordinal int, node metadataLogicalNode) error {
			if ordinal >= p.documentOrdinal {
				return nil
			}
			id, err := issueHotField(ctx, node, "ID")
			if err != nil {
				return err
			}
			match, err := id.StringEqual(ctx, p.documentID)
			if match {
				p.documentOrdinal = ordinal
			}
			return err
		})
	}
	return MutationScope{Resources: []string{p.documentID}}, err
}

func (p *portfolioDocumentImage) inputs(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource) (domain.Bootstrap, error) {
	p.ordinals = map[string][]int{}
	data := domain.Bootstrap{}
	if p.create != nil {
		if err := portfolioReadFields(ctx, owner, source, &data, "workspace", "viewer", "teams"); err != nil {
			return data, err
		}
		if id := p.create.TemplateID; id != "" {
			if err := portfolioSelectRows(ctx, owner, source, "documentTemplates", "ID", []string{id}, &data.DocumentTemplates, nil); err != nil {
				return data, err
			}
		}
		if err := portfolioSelectRows(ctx, owner, source, "users", "ID", p.create.SubscriberIDs, &data.Users, nil); err != nil {
			return data, err
		}
		err := p.selectProjects(ctx, owner, source, &data, p.create.ProjectIDs)
		return data, err
	}
	if p.documentOrdinal < 0 {
		return data, nil
	}
	if err := portfolioReadFields(ctx, owner, source, &data, "workspace", "viewer", "viewerRole", "teams", "teamSettings", "members", "teamMembers", "workspaceSettings"); err != nil {
		return data, err
	}
	if err := scopedImageVisit(ctx, source, "documents", func(ordinal int, node metadataLogicalNode) error {
		if ordinal != p.documentOrdinal {
			return nil
		}
		value, err := cycleScheduleNativeValue(ctx, owner, node)
		if err == nil {
			data.Documents = append(data.Documents, value.Interface().(domain.Document))
		}
		return err
	}); err != nil {
		return data, err
	}
	if len(data.Documents) != 1 {
		return data, ErrBoundedMetadataCoverage
	}
	recipients := data.Documents[0].SubscriberIDs
	if err := portfolioSelectRows(ctx, owner, source, "users", "ID", recipients, &data.Users, nil); err != nil {
		return data, err
	}
	if err := portfolioSelectRows(ctx, owner, source, "pushSubscriptions", "UserID", recipients, &data.PushSubscriptions, nil); err != nil {
		return data, err
	}
	if len(recipients) != 0 {
		index, _, _ := metadataLogicalField("notificationPreferences")
		data.NotificationPreferences = map[string]domain.NotificationPreferences{}
		if err := source.fields[index].MapEntries(ctx, func(key, node metadataLogicalNode) error {
			for _, id := range recipients {
				match, err := key.StringEqual(ctx, id)
				if err != nil {
					return err
				}
				if match {
					value, err := cycleScheduleNativeValue(ctx, owner, node)
					if err == nil {
						data.NotificationPreferences[id] = value.Interface().(domain.NotificationPreferences)
					}
					return err
				}
			}
			return nil
		}); err != nil {
			return data, err
		}
	}
	id := []string{p.documentID}
	if err := portfolioSelectRows(ctx, owner, source, "drafts", "ResourceID", id, &data.Drafts, p.ordinals); err != nil {
		return data, err
	}
	if err := portfolioSelectRows(ctx, owner, source, "favorites", "ResourceID", id, &data.Favorites, p.ordinals); err != nil {
		return data, err
	}
	if err := portfolioSelectRows(ctx, owner, source, "subscriptions", "ResourceID", id, &data.Subscriptions, p.ordinals); err != nil {
		return data, err
	}
	err := p.selectProjects(ctx, owner, source, &data, nil)
	return data, err
}

func portfolioSelectRows[T any](ctx context.Context, owner *ResourceRead, source *metadataLogicalSource, collection, field string, ids []string, selected *[]T, ordinals map[string][]int) error {
	if len(ids) == 0 {
		return nil
	}
	return scopedImageVisit(ctx, source, collection, func(ordinal int, node metadataLogicalNode) error {
		key, err := issueHotField(ctx, node, field)
		if err != nil {
			return err
		}
		for _, id := range ids {
			match, err := key.StringEqual(ctx, id)
			if err != nil {
				return err
			}
			if match {
				value, err := cycleScheduleNativeValue(ctx, owner, node)
				if err != nil {
					return err
				}
				if err := owner.reserve(ctx, uint64(node.typ.Size())*2+128); err != nil {
					return err
				}
				*selected = append(*selected, value.Interface().(T))
				if ordinals != nil {
					ordinals[collection] = append(ordinals[collection], ordinal)
				}
				return nil
			}
		}
		return nil
	})
}

func (p *portfolioDocumentImage) selectProjects(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource, data *domain.Bootstrap, projectIDs []string) error {
	if p.create != nil && len(projectIDs) == 0 {
		return nil
	}
	return scopedImageVisit(ctx, source, "projects", func(ordinal int, node metadataLogicalNode) error {
		var matched bool
		if p.create != nil {
			if err := owner.withHeader(ctx, node, []string{"ID"}, func(value reflect.Value) error {
				matched = slices.Contains(projectIDs, value.Interface().(domain.Project).ID)
				return nil
			}); err != nil {
				return err
			}
		} else {
			// Include reverse resources even if the document no longer names
			// their project. Trash records keep the original link and pins.
			resources, err := issueHotField(ctx, node, "Resources")
			if err != nil {
				return err
			}
			if err := resources.Children(ctx, func(_ reflect.Value, resource metadataLogicalNode) error {
				if matched {
					return nil
				}
				id, err := issueHotField(ctx, resource, "ID")
				if err != nil {
					return err
				}
				matched, err = id.StringEqual(ctx, p.documentID)
				return err
			}); err != nil {
				return err
			}
		}
		if !matched {
			return nil
		}
		selected, err := cycleScheduleProjectNode(ctx, owner, node, documentUpdateProjectFields...)
		if err != nil {
			return err
		}
		data.Projects = append(data.Projects, selected.Interface().(domain.Project))
		p.ordinals["projects"] = append(p.ordinals["projects"], ordinal)
		return nil
	})
}

func (p *portfolioDocumentImage) reserveCallback(ctx context.Context, owner *ResourceRead, data domain.Bootstrap, payload any) error {
	budget := &boundedDiscussionAttempt{owner: owner}
	if err := budget.reserveCallback(ctx, data, payload); err != nil {
		return err
	}
	// Delete stages an encoded copy of the removed document and its links.
	if p.create == nil {
		for _, value := range []any{data.Documents, data.Projects, data.Drafts, data.Favorites, data.Subscriptions} {
			if err := budget.reserveValue(ctx, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *portfolioDocumentImage) validateAggregate(_ domain.Bootstrap, after domain.Bootstrap, aggregate string) error {
	if p.create != nil && (aggregate == "" || len(after.Documents) != 1 || aggregate != after.Documents[0].ID) ||
		p.create == nil && aggregate != p.ref {
		return ErrBoundedMetadataCoverage
	}
	return nil
}

func (p *portfolioDocumentImage) validate(before, after domain.Bootstrap) error {
	if len(before.Projects) != len(after.Projects) {
		return ErrBoundedMetadataCoverage
	}
	for i, project := range after.Projects {
		project.Resources = before.Projects[i].Resources
		if !reflect.DeepEqual(project, before.Projects[i]) {
			return ErrBoundedMetadataCoverage
		}
	}
	if p.create != nil {
		return portfolioValidateFields(before, after, "documents", "projects")
	}
	if len(before.Documents) != 1 || len(after.Documents) != 0 || len(after.Trash) != 1 || after.Trash[0].ResourceID != p.documentID {
		return ErrBoundedMetadataCoverage
	}
	return portfolioValidateFields(before, after, "documents", "projects", "trash", "drafts", "favorites", "subscriptions", "notifications", "notificationDeliveries")
}

func portfolioValidateFields(before, after domain.Bootstrap, writable ...string) error {
	b, a := reflect.ValueOf(before), reflect.ValueOf(after)
	for _, field := range persistedMetadataFields() {
		if !slices.Contains(writable, field.name) && !metadataValueEqual(b.Field(field.index), a.Field(field.index)) {
			return ErrBoundedMetadataCoverage
		}
	}
	return nil
}

func (p *portfolioDocumentImage) image(ctx context.Context, _ *ResourceRead, source *metadataLogicalSource, before, after domain.Bootstrap) (*metadataLogicalSource, error) {
	frames, err := newMetadataLogicalBuilderIn(ctx, source.spoolGroup())
	if err != nil {
		return nil, err
	}
	defer frames.Close()
	return source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		overlay := portfolioCollection{base: original}
		switch name {
		case "documents":
			if p.create == nil {
				overlay.remove = map[int]bool{p.documentOrdinal: true}
				return overlay, true, nil
			}
			overlay.first = []metadataAggregateNode{legacyMetadataWriteNativeRecord{reflect.ValueOf(after.Documents[0])}}
		case "trash":
			for _, entry := range after.Trash {
				overlay.first = append(overlay.first, legacyMetadataWriteNativeRecord{reflect.ValueOf(entry)})
			}
		case "projects":
			overlay.fields = map[int]map[string]reflect.Value{}
			for i, project := range after.Projects {
				if !reflect.DeepEqual(before.Projects[i].Resources, project.Resources) {
					overlay.fields[p.ordinals[name][i]] = map[string]reflect.Value{"Resources": reflect.ValueOf(project.Resources)}
				}
			}
			return overlay, len(overlay.fields) != 0, nil
		case "drafts", "favorites", "subscriptions":
			if p.create == nil {
				index, _, _ := metadataLogicalField(name)
				removed, err := portfolioRemovedOrdinals(p.ordinals[name], reflect.ValueOf(before).Field(index), reflect.ValueOf(after).Field(index))
				overlay.remove = removed
				return overlay, len(removed) != 0, err
			}
		}
		if len(overlay.first) != 0 {
			shape, err := legacyDiscussionShape(frames, original.typ, false)
			overlay.shape = shape
			return overlay, true, err
		}
		return metadataAggregateStoredNode{original}, false, nil
	})
}

func portfolioRemovedOrdinals(ordinals []int, before, after reflect.Value) (map[int]bool, error) {
	removed := map[int]bool{}
	next := 0
	for i, ordinal := range ordinals {
		if next < after.Len() && metadataValueEqual(before.Index(i), after.Index(next)) {
			next++
		} else {
			removed[ordinal] = true
		}
	}
	if next != after.Len() {
		return nil, ErrBoundedMetadataCoverage
	}
	return removed, nil
}
