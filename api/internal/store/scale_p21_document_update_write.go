package store

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// DocumentUpdateWriteRequest closes the updateDocument callback's metadata
// selection. Its existing MutationScope still supplies explicit issue IDs.
type DocumentUpdateWriteRequest struct {
	Ref               string
	ProjectIDs        []string
	ReadExistingIssue bool
}

type documentUpdateWriteKey struct{}

func WithDocumentUpdateWrite(ctx context.Context, request DocumentUpdateWriteRequest) context.Context {
	request.ProjectIDs = slices.Clone(request.ProjectIDs)
	return context.WithValue(ctx, documentUpdateWriteKey{}, request)
}

func documentUpdateWriteFromContext(ctx context.Context) (DocumentUpdateWriteRequest, bool) {
	request, ok := ctx.Value(documentUpdateWriteKey{}).(DocumentUpdateWriteRequest)
	return request, ok
}

func boundedDocumentUpdateEvent(ctx context.Context, event string) bool {
	_, selected := documentUpdateWriteFromContext(ctx)
	return selected && (event == "document.updated" || event == "favorite.added" || event == "favorite.removed")
}

// Authorization and mutable collections retain complete native values.
// Read-only user, push and subscription inputs are projected below.
var documentUpdateCallbackFields = []string{
	"workspace", "viewer", "viewerRole",
	"teams", "teamSettings", "teamParents", "members", "teamMembers",
	"workspaceSettings", "favorites", "notificationPreferences", "auditLog",
}

type documentUpdateImageProfile struct {
	request                           DocumentUpdateWriteRequest
	scope                             MutationScope
	documentOrdinals, projectOrdinals []int
	documentIndex                     int
}

func (*documentUpdateImageProfile) kind() scopedImageWriteKind { return scopedImageDocumentUpdate }
func (*documentUpdateImageProfile) extraFields() []string      { return []string{"documents", "favorites"} }

func (p *documentUpdateImageProfile) editorDocument(data domain.Bootstrap) string {
	if p.documentIndex < 0 || p.documentIndex >= len(data.Documents) {
		return p.request.Ref
	}
	document := data.Documents[p.documentIndex]
	if document.CollaborationID != "" {
		return document.CollaborationID
	}
	return document.ID
}

func (p *documentUpdateImageProfile) resolve(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource) (MutationScope, error) {
	scope := p.scope
	scope.Resolve = nil
	best, rank := domain.Document{}, 0
	err := scopedImageVisit(ctx, source, "documents", func(_ int, node metadataLogicalNode) error {
		return owner.withHeader(ctx, node, []string{"ID", "SlugID", "PreviousSlugIDs", "IssueID"}, func(value reflect.Value) error {
			document := value.Interface().(domain.Document)
			next := 0
			if document.ID == p.request.Ref || document.SlugID == p.request.Ref {
				next = 2
			} else if p.request.Ref != "" && slices.Contains(document.PreviousSlugIDs, p.request.Ref) {
				next = 1
			}
			if next > rank {
				best, rank = document, next
			}
			return nil
		})
	})
	if err != nil {
		return scope, err
	}
	resource := p.request.Ref
	if rank != 0 {
		resource = best.ID
		if p.request.ReadExistingIssue && best.IssueID != "" {
			scope.IssueIDs = append(slices.Clone(scope.IssueIDs), best.IssueID)
		}
	}
	scope.Resources = append(slices.Clone(scope.Resources), resource)
	return scope, nil
}

func (p *documentUpdateImageProfile) inputs(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource) (domain.Bootstrap, error) {
	view := &documentUpdateAttempt{owner: owner}
	if err := view.callbackInputs(ctx, source, p.request); err != nil {
		return domain.Bootstrap{}, err
	}
	p.documentOrdinals, p.projectOrdinals, p.documentIndex = view.documentOrdinals, view.projectOrdinals, view.documentIndex
	return view.data, nil
}

func (p *documentUpdateImageProfile) validate(before, after domain.Bootstrap) error {
	return (&documentUpdateAttempt{before: before, data: after, documentIndex: p.documentIndex}).validate()
}

func (p *documentUpdateImageProfile) image(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource, before, after domain.Bootstrap) (*metadataLogicalSource, error) {
	view := &documentUpdateAttempt{owner: owner, before: before, data: after,
		documentOrdinals: p.documentOrdinals, projectOrdinals: p.projectOrdinals}
	return view.image(ctx, source)
}

func (*documentUpdateImageProfile) reserveCallback(ctx context.Context, owner *ResourceRead, data domain.Bootstrap, payload any) error {
	budget := &boundedDiscussionAttempt{owner: owner}
	// Changes, mentions, moves and added/removed subscriptions can notify
	// the same audience in one callback, with four delivery channels each.
	for range 4 {
		if err := budget.reserveCallback(ctx, data, payload); err != nil {
			return err
		}
	}
	return nil
}

type documentUpdateAttempt struct {
	owner                             *ResourceRead
	data                              domain.Bootstrap
	before                            domain.Bootstrap
	documentOrdinals, projectOrdinals []int
	documentIndex                     int
	documentID                        string
}

func (r *documentUpdateAttempt) value(ctx context.Context, node metadataLogicalNode) (reflect.Value, error) {
	return cycleScheduleNativeValue(ctx, r.owner, node)
}

func (r *documentUpdateAttempt) callbackInputs(ctx context.Context, source *metadataLogicalSource, request DocumentUpdateWriteRequest) error {
	for _, name := range documentUpdateCallbackFields {
		index, _, ok := metadataLogicalField(name)
		if !ok {
			return ErrMetadataRecordQuery
		}
		value, err := r.value(ctx, source.fields[index])
		if err != nil {
			return err
		}
		reflect.ValueOf(&r.data).Elem().Field(index).Set(value)
	}
	// The callback's complete Markdown scan needs every Name/Email and first
	// ID occurrence. Recipient role evaluation additionally reads these app
	// flags. None of the user's unrelated display/profile strings are written.
	users, _, _ := metadataLogicalField("users")
	if err := source.fields[users].Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		value, err := r.owner.projectNode(ctx, node, "ID", "Name", "Email", "App", "Active", "AppTeamIDs", "AppScopes", "AppAllPublicTeams")
		if err != nil {
			return err
		}
		if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.User]().Size())*2+128); err != nil {
			return err
		}
		r.data.Users = append(r.data.Users, value.Interface().(domain.User))
		return nil
	}); err != nil {
		return err
	}
	// Push delivery only tests existence of an enabled recipient entry.
	push, _, _ := metadataLogicalField("pushSubscriptions")
	if err := source.fields[push].Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		value, err := r.owner.projectNode(ctx, node, "UserID", "Enabled")
		if err != nil {
			return err
		}
		if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.PushSubscription]().Size())*2+128); err != nil {
			return err
		}
		r.data.PushSubscriptions = append(r.data.PushSubscriptions, value.Interface().(domain.PushSubscription))
		return nil
	}); err != nil {
		return err
	}
	best, rank := -1, 0
	var selected domain.Document
	if err := scopedImageVisit(ctx, source, "documents", func(ordinal int, node metadataLogicalNode) error {
		return r.owner.withHeader(ctx, node, []string{"ID", "SlugID", "PreviousSlugIDs"}, func(value reflect.Value) error {
			header := value.Interface().(domain.Document)
			next := 0
			if header.ID == request.Ref || header.SlugID == request.Ref {
				next = 2
			} else if request.Ref != "" && slices.Contains(header.PreviousSlugIDs, request.Ref) {
				next = 1
			}
			if next > rank {
				best, rank, selected = ordinal, next, header
			}
			return nil
		})
	}); err != nil {
		return err
	}
	r.documentIndex = -1
	if best >= 0 {
		r.documentID = selected.ID
		if err := r.owner.reserve(ctx, uint64(len(selected.ID))*8+4096); err != nil {
			return err
		}
	}
	index, _, _ := metadataLogicalField("documents")
	absent, err := source.fields[index].isNil(ctx)
	if err != nil {
		return err
	}
	if !absent {
		r.data.Documents = []domain.Document{}
	}
	if best >= 0 {
		if err := scopedImageVisit(ctx, source, "documents", func(ordinal int, node metadataLogicalNode) error {
			return r.owner.withHeader(ctx, node, []string{"ID"}, func(value reflect.Value) error {
				if value.Interface().(domain.Document).ID != selected.ID {
					return nil
				}
				full, err := r.value(ctx, node)
				if err != nil {
					return err
				}
				if err := r.owner.reserve(ctx, 128); err != nil {
					return err
				}
				if ordinal == best {
					r.documentIndex = len(r.data.Documents)
				}
				r.data.Documents = append(r.data.Documents, full.Interface().(domain.Document))
				r.documentOrdinals = append(r.documentOrdinals, ordinal)
				return nil
			})
		}); err != nil {
			return err
		}
	}
	subscriptions, _, _ := metadataLogicalField("subscriptions")
	if err := source.fields[subscriptions].Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		return r.owner.withHeader(ctx, node, []string{"ResourceType", "ResourceID"}, func(value reflect.Value) error {
			header := value.Interface().(domain.Subscription)
			if best < 0 || header.ResourceType != "document" || header.ResourceID != selected.ID {
				return nil
			}
			value, err := r.owner.projectNode(ctx, node, "ResourceType", "ResourceID", "UserID")
			if err != nil {
				return err
			}
			if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Subscription]().Size())*2+128); err != nil {
				return err
			}
			r.data.Subscriptions = append(r.data.Subscriptions, value.Interface().(domain.Subscription))
			return nil
		})
	}); err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, id := range request.ProjectIDs {
		if err := r.owner.reserve(ctx, uint64(len(id))*8+128); err != nil {
			return err
		}
		wanted[id] = true
	}
	if r.documentIndex >= 0 {
		for _, id := range r.data.Documents[r.documentIndex].ProjectIDs {
			if err := r.owner.reserve(ctx, uint64(len(id))*8+128); err != nil {
				return err
			}
			wanted[id] = true
		}
	}
	projectField, _, _ := metadataLogicalField("projects")
	absent, err = source.fields[projectField].isNil(ctx)
	if err != nil {
		return err
	}
	if !absent {
		r.data.Projects = []domain.Project{}
	}
	return scopedImageVisit(ctx, source, "projects", func(ordinal int, node metadataLogicalNode) error {
		match := false
		if err := r.owner.withHeader(ctx, node, []string{"ID"}, func(value reflect.Value) error {
			match = wanted[value.Interface().(domain.Project).ID]
			return nil
		}); err != nil {
			return err
		}
		if !match && best >= 0 {
			resources, err := issueHotField(ctx, node, "Resources")
			if err != nil {
				return err
			}
			if err := resources.Children(ctx, func(_ reflect.Value, resource metadataLogicalNode) error {
				return r.owner.withHeader(ctx, resource, []string{"ID"}, func(value reflect.Value) error {
					match = match || value.Interface().(domain.ProjectResource).ID == selected.ID
					return nil
				})
			}); err != nil {
				return err
			}
		}
		if !match {
			return nil
		}
		// The callback reads a linked project's ID, Name and TeamIDs and
		// edits only its Resources; the stored remainder (descriptions,
		// updates, milestones) stays framed and is never materialized.
		header, err := cycleScheduleProjectNode(ctx, r.owner, node, documentUpdateProjectFields...)
		if err != nil {
			return err
		}
		if err := r.owner.reserve(ctx, 128); err != nil {
			return err
		}
		r.data.Projects = append(r.data.Projects, header.Interface().(domain.Project))
		r.projectOrdinals = append(r.projectOrdinals, ordinal)
		return nil
	})
}

// documentUpdateProjectFields are every project field updateDocument reads:
// validateResourceIDs (ID), InheritedDocumentTeams (ID, TeamIDs),
// documentDestinationName (Name) and syncDocumentProjectResources (ID,
// Resources). Only Resources may change.
var documentUpdateProjectFields = []string{"ID", "Name", "TeamIDs", "Resources"}

func (r *documentUpdateAttempt) validate() error {
	refuse := func() error {
		return fmt.Errorf("%w: document update exceeded its closed callback contract", ErrBoundedMetadataCoverage)
	}
	before, after := reflect.ValueOf(r.before), reflect.ValueOf(r.data)
	for _, field := range persistedMetadataFields() {
		switch field.name {
		case "documents", "projects", "favorites", "notifications", "notificationDeliveries":
			continue
		}
		if !metadataValueEqual(before.Field(field.index), after.Field(field.index)) {
			return refuse()
		}
	}
	if len(r.before.Documents) != len(r.data.Documents) || len(r.before.Projects) != len(r.data.Projects) {
		return refuse()
	}
	for index, document := range r.data.Documents {
		if document.ID != r.before.Documents[index].ID ||
			index != r.documentIndex && !reflect.DeepEqual(document, r.before.Documents[index]) {
			return refuse()
		}
	}
	for index, project := range r.data.Projects {
		project.Resources = r.before.Projects[index].Resources
		if !reflect.DeepEqual(project, r.before.Projects[index]) {
			return refuse()
		}
	}
	return nil
}

// documentUpdateCollection replaces the selected source ordinals of one
// collection and keeps every other stored record framed.
type documentUpdateCollection struct {
	base     metadataLogicalNode
	ordinals []int
	nodes    []metadataAggregateNode
}

func (n documentUpdateCollection) nodeType() reflect.Type           { return n.base.typ }
func (n documentUpdateCollection) logicalBase() metadataLogicalNode { return n.base }
func (n documentUpdateCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n documentUpdateCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	selected := 0
	return n.base.Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		var value metadataAggregateNode = metadataAggregateStoredNode{value: node}
		if selected < len(n.ordinals) && int(ordinal.Int()) == n.ordinals[selected] {
			value = n.nodes[selected]
			selected++
		}
		return visit(ordinal, value)
	})
}

func (r *documentUpdateAttempt) image(ctx context.Context, source *metadataLogicalSource) (*metadataLogicalSource, error) {
	frames, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer frames.Close()
	favorites, _, _ := metadataLogicalField("favorites")
	favoriteShape, err := legacyDiscussionShape(frames, source.fields[favorites].typ, r.data.Favorites == nil)
	if err != nil {
		return nil, err
	}
	documents := make([]metadataAggregateNode, len(r.documentOrdinals))
	for index := range documents {
		documents[index] = legacyMetadataWriteNativeRecord{value: reflect.ValueOf(r.data.Documents[index])}
	}
	// A linked project carries only its callback header: an unchanged one
	// keeps its stored record and a changed one replaces only Resources on
	// that record, so its description and every other member stay framed.
	projects := make([]metadataAggregateNode, len(r.projectOrdinals))
	projectField, _, _ := metadataLogicalField("projects")
	position := 0
	if err := source.fields[projectField].Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		if position >= len(r.projectOrdinals) || int(ordinal.Int()) != r.projectOrdinals[position] {
			return nil
		}
		projects[position] = metadataAggregateStoredNode{value: node}
		if before, after := r.before.Projects[position].Resources, r.data.Projects[position].Resources; !reflect.DeepEqual(before, after) {
			projects[position] = legacyDiscussionNativeDelta{base: node, changed: map[string]reflect.Value{"Resources": reflect.ValueOf(after)}}
		}
		position++
		return nil
	}); err != nil {
		return nil, err
	}
	if position != len(r.projectOrdinals) {
		return nil, ErrBoundedMetadataCoverage
	}
	return source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		switch name {
		case "documents":
			return documentUpdateCollection{original, r.documentOrdinals, documents}, len(r.documentOrdinals) != 0, nil
		case "projects":
			return documentUpdateCollection{original, r.projectOrdinals, projects}, len(r.projectOrdinals) != 0, nil
		case "favorites":
			return legacyMetadataWriteNative{base: favoriteShape, value: reflect.ValueOf(r.data.Favorites)}, true, nil
		}
		return metadataAggregateStoredNode{value: original}, false, nil
	})
}

// mutateBoundedDocumentUpdate implements only updateDocument's ordinary scoped
// callback. It does not admit document creation, deletion, restore or arbitrary
// MutationScope resolvers.
func (s *SQLiteStore) mutateBoundedDocumentUpdate(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	request, declared := documentUpdateWriteFromContext(ctx)
	scope, scoped := mutationScopeFromContext(ctx)
	checked := scope
	checked.IssueIDs, checked.Resources, checked.Resolve = nil, nil, nil
	if s == nil || !s.BoundedMetadataEnabled() || !declared || !scoped ||
		!boundedDocumentUpdateEvent(ctx, eventType) || mutate == nil ||
		!metadataOutputOwnerPresent(ctx) || UsesIssueRecordMutations(ctx) || issueRenumbering(ctx) ||
		!reflect.DeepEqual(checked, MutationScope{}) || len(scope.IssueIDs) > 2 ||
		request.Ref != mutationAggregateID(ctx) {
		return ErrBoundedMetadataCoverage
	}
	return s.mutateBoundedScopedImage(ctx, workspace, eventType, payload,
		&documentUpdateImageProfile{request: request, scope: scope}, mutate)
}
