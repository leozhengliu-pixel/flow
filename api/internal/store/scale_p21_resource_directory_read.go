package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

// ResourceDirectoryToken identifies an occurrence rather than a resource ID.
// Child is -1 for a top-level record and the milestone ordinal otherwise.
type ResourceDirectoryToken struct {
	Ordinal int
	Child   int
}

// Some legacy callers use current installed metadata while retaining the
// caller's SQL snapshot for live directory and authority queries.
type resourceDirectoryInstalledSourceKey struct{}

// ResourceDirectoryRead owns the compact policy graph and its installed source.
// Data contains policy fields only. Name/Title carry internal occurrence tags;
// Visit and Load always restore the source's real names before returning values.
type ResourceDirectoryRead struct {
	Data      domain.Bootstrap
	owner     *ResourceRead
	nodes     map[string][]metadataLogicalNode
	indices   map[string]map[int]int
	kind      string
	workspace string
	stamp     copyStamp
	epoch     string
	borrowed  bool
	principal bool
	trusted   bool
	key       *domain.APIKey
}

func (r *ResourceDirectoryRead) Close() {
	if r != nil {
		r.Data, r.nodes, r.indices = domain.Bootstrap{}, nil, nil
		r.owner.Close()
	}
}

// Reserve retains caller-owned selection metadata until Close.
func (r *ResourceDirectoryRead) Reserve(ctx context.Context, size uint64) error {
	if r == nil || r.owner == nil || r.owner.source == nil {
		return errMetadataLogicalClosed
	}
	return r.owner.reserve(ctx, size)
}

// RetainSelection admits one retained page key independently, so a displaced
// winner can release its allowance before the next candidate is visited.
func (r *ResourceDirectoryRead) RetainSelection(ctx context.Context, size uint64) (func(), error) {
	if r == nil || r.owner == nil || r.owner.source == nil {
		return nil, errMetadataLogicalClosed
	}
	if size == 0 || size > metadataWorkBytes {
		return nil, ErrMetadataWorkBudget
	}
	_, release, err := r.owner.store.tryAdmitAdditionalMetadataWork(ctx, int(size))
	return release, err
}

// BeginResourceDirectoryRead preserves the public connections' installed,
// caller-snapshot and already-projected handoff choices. It deliberately does
// not replace an uncertified installed generation with canonical records.
func (s *SQLiteStore) BeginResourceDirectoryRead(ctx context.Context, workspace, userID string, key *domain.APIKey, kind string, handed *domain.Bootstrap) (_ *ResourceDirectoryRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	if !slices.Contains([]string{"projects", "projectMilestones", "initiatives", "documents", "labels"}, kind) {
		return nil, ErrMetadataRecordQuery
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	installed, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		if err == nil {
			err = ErrAuthForbidden
		}
		return nil, err
	}
	defer installed.Close()
	source, err := installed.retainSource(ctx, installed.workspace)
	if err != nil {
		return nil, err
	}
	r := &ResourceDirectoryRead{owner: &ResourceRead{store: s, source: source}, nodes: map[string][]metadataLogicalNode{}, kind: kind, workspace: installed.workspace, principal: userID != "", key: key}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if err = r.owner.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	if userID == "" && handed == nil {
		r.trusted = true
		if err = r.nativePolicy(ctx); err != nil {
			return nil, err
		}
		r.Data.ViewerRole = "admin"
		return r, ctx.Err()
	}
	tx, borrowed := (*sqlTx)(nil), false
	var caller *domain.Bootstrap
	if held, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && held.database == s.db && held.workspace == r.workspace {
		tx, borrowed = held.reader, true
		installedSource, _ := ctx.Value(resourceDirectoryInstalledSourceKey{}).(bool)
		if userID != "" && !installedSource {
			caller = &held.metadata
		}
		if tx == nil {
			return nil, ErrMetadataRecordQuery
		}
	} else {
		tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
	}
	r.borrowed = borrowed
	snapshot := context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: r.workspace, reader: tx})
	snapshot = r.owner.metadataOutputContext(snapshot)
	r.stamp, r.epoch, err = s.accessStampAndEpoch(snapshot, r.workspace)
	if err != nil {
		return nil, err
	}
	if handed != nil {
		caller = handed
	}
	if caller != nil {
		if caller.Workspace.ID == "" {
			return nil, ErrAuthForbidden
		}
		candidate := domain.Bootstrap{
			Workspace: caller.Workspace, Viewer: caller.Viewer, ViewerRole: caller.ViewerRole,
			Teams: caller.Teams, TeamSettings: caller.TeamSettings, TeamParents: caller.TeamParents,
			WorkspaceSettings: caller.WorkspaceSettings, Favorites: caller.Favorites,
			TeamMembers: caller.TeamMembers,
			Projects:    caller.Projects, Initiatives: caller.Initiatives, InitiativeRelations: caller.InitiativeRelations,
			Labels: caller.Labels, LabelGroups: caller.LabelGroups,
		}
		if kind == "documents" {
			candidate.Documents = caller.Documents
		}
		captured, captureErr := captureMetadataLogicalSource(snapshot, candidate, r.stamp)
		if captureErr != nil {
			return nil, captureErr
		}
		r.owner.source.Release()
		r.owner.source = captured
	}
	if err = r.nativePolicy(snapshot); err != nil {
		return nil, err
	}
	document := &DocumentRead{Data: r.Data, owner: r.owner}
	if userID != "" && handed == nil {
		document.Data = r.Data
		if err = document.documentProjectionWork(snapshot, r.stamp.workspaceID, userID); err != nil {
			return nil, err
		}
		memoContext, closeMemo := withResourcePolicyMemo(snapshot, r.owner)
		func() {
			defer closeMemo()
			r.Data, err = s.projectDocumentRecordMetadata(memoContext, r.workspace, userID, key, r.Data)
		}()
		if err != nil {
			return nil, err
		}
		r.Data.IssueCollectionPaged = true
	} else if handed != nil {
		r.Data.ViewerRole = handed.ViewerRole
		document.Data = r.Data
		if err = document.documentNativeField(snapshot, "teamMembers", true); err != nil {
			return nil, err
		}
		r.Data.TeamMembers = document.Data.TeamMembers
		r.Data.IssueCollectionPaged = handed.IssueCollectionPaged
		if userID != "" {
			// The handoff already owns viewer projection. Recheck only the
			// live principal; canonical team policy cannot replace its source.
			if err = document.documentProjectionRows(snapshot,
				[]string{"u.id", "u.email", "u.name", "u.display_name", "u.avatar_url", "u.email_verified_at", "u.active", "u.deleted_at", "m.role", "m.status", "t.team_id", "t.role"},
				`FROM workspace_memberships m JOIN auth_users u ON u.id=m.user_id LEFT JOIN team_memberships t ON t.workspace_id=m.workspace_id AND t.user_id=m.user_id WHERE m.workspace_id=? AND m.user_id=?`,
				r.stamp.workspaceID, userID); err != nil {
				return nil, err
			}
			if _, _, _, err = s.resourceAccessPrincipal(snapshot, r.stamp.workspaceID, userID); err != nil {
				return nil, err
			}
		}
	} else {
		r.Data.ViewerRole = "admin"
	}
	if !borrowed {
		if err = tx.Rollback(); err != nil {
			return nil, err
		}
	}
	if err = r.Validate(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *ResourceDirectoryRead) nativePolicy(ctx context.Context) error {
	document := &DocumentRead{owner: r.owner}
	for _, field := range append([]string{"workspace", "viewer", "favorites", "initiativeRelations"}, documentReadPolicyFields...) {
		if err := document.documentNativeField(ctx, field, true); err != nil {
			return err
		}
	}
	r.Data = document.Data
	return r.policy(ctx)
}

// These fields form the visibility/reference/count closure. They exclude all
// descriptions, histories, attachments, resource URLs and unrelated settings.
var resourceDirectoryPolicy = map[string][]string{
	"projects":    {"ID", "TeamIDs", "DependencyIDs", "Initiatives", "Customers", "LabelIDs", "Progress", "IssueCount", "ArchivedAt"},
	"initiatives": {"ID", "LeadTeamID", "ContributingTeamIDs", "ProjectIDs", "ParentInitiativeIDs", "LabelIDs", "Favorite"},
	"labels":      {"ID", "Scope", "GroupID", "IssueCount"},
	"labelGroups": {"ID", "Scope"},
	"documents":   {"ID", "SlugID", "TeamIDs", "ProjectIDs", "InheritedTeamIDs", "IssueID", "IssueTeamID", "Permissions", "Favorite"},
}

func (r *ResourceDirectoryRead) policy(ctx context.Context) error {
	count := 0
	for _, kind := range []string{"projects", "initiatives", "labels", "labelGroups", "documents"} {
		if kind == "documents" && r.kind != "documents" {
			continue
		}
		err := r.owner.visitNative(ctx, kind, func(index int, node metadataLogicalNode) error {
			value, err := (&DocumentDirectoryRead{owner: r.owner}).header(ctx, node, resourceDirectoryPolicy[kind])
			if err != nil {
				return err
			}
			if err := r.owner.reserve(ctx, 512); err != nil {
				return err
			}
			r.nodes[kind] = append(r.nodes[kind], node)
			count++
			tag := strconv.Itoa(index)
			switch kind {
			case "projects":
				item := value.Interface().(domain.Project)
				item.Name = tag
				r.Data.Projects = append(r.Data.Projects, item)
			case "initiatives":
				item := value.Interface().(domain.Initiative)
				item.Name = tag
				r.Data.Initiatives = append(r.Data.Initiatives, item)
			case "labels":
				item := value.Interface().(domain.IssueLabel)
				item.Name = tag
				r.Data.Labels = append(r.Data.Labels, item)
			case "labelGroups":
				r.Data.LabelGroups = append(r.Data.LabelGroups, value.Interface().(domain.LabelGroup))
			case "documents":
				item := value.Interface().(domain.Document)
				// Only the creator's ID participates in document policy.
				creator, err := r.creatorID(ctx, node)
				if err != nil {
					return err
				}
				item.Creator.ID, item.Title = creator, tag
				r.Data.Documents = append(r.Data.Documents, item)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return r.owner.reserve(ctx, uint64(count)*256+4096)
}

func (r *ResourceDirectoryRead) creatorID(ctx context.Context, node metadataLogicalNode) (string, error) {
	id := ""
	err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if key.String() != "Creator" {
			return nil
		}
		value, err := (&DocumentDirectoryRead{owner: r.owner}).header(ctx, child, []string{"ID"})
		if err == nil {
			id = value.Interface().(domain.User).ID
		}
		return err
	})
	return id, err
}

func (r *ResourceDirectoryRead) Validate(ctx context.Context) error {
	if r == nil || r.owner == nil || r.owner.source == nil {
		return errMetadataLogicalClosed
	}
	if r.trusted {
		return ctx.Err()
	}
	return (&DocumentDirectoryRead{owner: r.owner, workspace: r.workspace, stamp: r.stamp, epoch: r.epoch, borrowed: r.borrowed, principal: r.principal}).Validate(ctx)
}

// ResourceDirectoryValue owns a filter header or a complete connection node.
// Milestone visits also include one parent-only value (Child == -1), including
// parents with no milestones, for the legacy last-wins project-name relation.
type ResourceDirectoryValue struct {
	Token      ResourceDirectoryToken
	Project    domain.Project
	Initiative domain.Initiative
	Label      domain.IssueLabel
	Document   domain.Document
	Milestone  domain.ProjectMilestone
	owner      *ResourceRead
}

type resourceDirectoryIDs struct {
	target *[]string
	source []string
}

func (v *ResourceDirectoryValue) Close() {
	if v != nil {
		v.Project, v.Initiative, v.Label, v.Document, v.Milestone = domain.Project{}, domain.Initiative{}, domain.IssueLabel{}, domain.Document{}, domain.ProjectMilestone{}
		v.owner.Close()
	}
}

var resourceDirectoryHeaders = map[string][]string{
	"projects":    {"ID", "Name", "SlugID", "Health", "Priority", "Status", "Lead", "TeamIDs", "LabelIDs", "MemberIDs", "Initiatives", "StartDate", "TargetDate", "CreatedAt", "UpdatedAt", "ArchivedAt"},
	"initiatives": {"ID", "Name", "SlugID", "Status", "Health", "Owner", "Creator", "TargetDate", "ProjectIDs", "ParentInitiativeIDs", "CreatedAt", "UpdatedAt"},
	"labels":      {"ID", "Name", "Color", "Scope", "GroupID", "ResourceType", "CreatedAt", "ArchivedAt"},
	"documents":   {"ID", "Title", "SlugID", "SearchSnippet", "MatchedTerms", "Creator", "TeamIDs", "ProjectIDs", "IssueID", "IssueTeamID", "InheritedTeamIDs", "Permissions", "CreatedAt", "UpdatedAt", "ArchivedAt"},
	"milestones":  {"ID", "ProjectID", "Name", "TargetDate", "CreatedAt", "UpdatedAt"},
}

func directoryOutputFields(typ reflect.Type, excluded ...string) []string {
	fields := make([]string, 0, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		name := typ.Field(index).Name
		if !slices.Contains(excluded, name) {
			fields = append(fields, name)
		}
	}
	return fields
}

func (r *ResourceDirectoryRead) ordinal(kind, tag, id string) (int, error) {
	index, err := strconv.Atoi(tag)
	if err != nil || index < 0 || index >= len(r.nodes[kind]) {
		return 0, ErrMetadataRecordQuery
	}
	return index, nil
}

// Complete all credential filtering before the first Visit/Load. Indexing the
// surviving ordinal tags then avoids rescanning the policy graph per candidate.
func (r *ResourceDirectoryRead) prepare() error {
	if r.indices != nil {
		return nil
	}
	r.indices = map[string]map[int]int{}
	for _, kind := range []string{"projects", "initiatives", "labels", "documents"} {
		r.indices[kind] = map[int]int{}
	}
	put := func(kind, tag, id string, index int) error {
		ordinal, err := r.ordinal(kind, tag, id)
		if err == nil {
			r.indices[kind][ordinal] = index
		}
		return err
	}
	for index, item := range r.Data.Projects {
		if err := put("projects", item.Name, item.ID, index); err != nil {
			return err
		}
	}
	for index, item := range r.Data.Initiatives {
		if err := put("initiatives", item.Name, item.ID, index); err != nil {
			return err
		}
	}
	for index, item := range r.Data.Labels {
		if err := put("labels", item.Name, item.ID, index); err != nil {
			return err
		}
	}
	for index, item := range r.Data.Documents {
		if err := put("documents", item.Title, item.ID, index); err != nil {
			return err
		}
	}
	return nil
}

// Visit releases each header immediately after the typed callback returns.
func (r *ResourceDirectoryRead) Visit(ctx context.Context, visit func(*ResourceDirectoryValue) error) error {
	accept := func(tag, id string) error {
		kind := r.kind
		if kind == "projectMilestones" {
			kind = "projects"
		}
		ordinal, err := r.ordinal(kind, tag, id)
		if err != nil {
			return err
		}
		token := ResourceDirectoryToken{Ordinal: ordinal, Child: -1}
		value, err := r.load(ctx, token, false)
		if err != nil {
			return err
		}
		defer value.Close()
		err = visit(value)
		if err != nil || r.kind != "projectMilestones" {
			return err
		}
		return r.nodes[kind][ordinal].Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
			if key.String() != "Milestones" {
				return nil
			}
			return r.owner.nativeArray(ctx, child, func(index int, node metadataLogicalNode) error {
				milestone := &ResourceDirectoryValue{Token: ResourceDirectoryToken{Ordinal: ordinal, Child: index}, Project: value.Project, owner: &ResourceRead{store: r.owner.store}}
				defer milestone.Close()
				header, err := (&DocumentDirectoryRead{owner: milestone.owner}).header(ctx, node, resourceDirectoryHeaders["milestones"])
				if err != nil {
					return err
				}
				milestone.Milestone = header.Interface().(domain.ProjectMilestone)
				return visit(milestone)
			})
		})
	}
	switch r.kind {
	case "projects", "projectMilestones":
		for _, item := range r.Data.Projects {
			if err := accept(item.Name, item.ID); err != nil {
				return err
			}
		}
	case "initiatives":
		for _, item := range r.Data.Initiatives {
			if err := accept(item.Name, item.ID); err != nil {
				return err
			}
		}
	case "labels":
		for _, item := range r.Data.Labels {
			if err := accept(item.Name, item.ID); err != nil {
				return err
			}
		}
	case "documents":
		for _, item := range r.Data.Documents {
			if err := accept(item.Title, item.ID); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// Load admits only fields present in the public connection output. In
// particular project and milestone histories never enter an output allocation.
func (r *ResourceDirectoryRead) Load(ctx context.Context, token ResourceDirectoryToken) (*ResourceDirectoryValue, error) {
	if err := r.Validate(ctx); err != nil {
		return nil, err
	}
	return r.load(ctx, token, true)
}

func (r *ResourceDirectoryRead) LoadHeader(ctx context.Context, token ResourceDirectoryToken) (*ResourceDirectoryValue, error) {
	return r.load(ctx, token, false)
}

// SearchDocument loads one candidate body under an independent owner, leaving
// its stripped histories and attachments unread during ranking.
func (r *ResourceDirectoryRead) SearchDocument(ctx context.Context, token ResourceDirectoryToken) (*ResourceDirectoryValue, error) {
	if r.kind != "documents" {
		return nil, ErrMetadataRecordQuery
	}
	value, err := r.load(ctx, token, false)
	if err != nil {
		return nil, err
	}
	err = r.nodes["documents"][token.Ordinal].Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if key.String() != "Content" {
			return nil
		}
		if child.cost > (metadataWorkBytes-(128<<10))/32 {
			return ErrMetadataWorkBudget
		}
		if err := value.owner.reserve(ctx, child.cost*32+128<<10); err != nil {
			return err
		}
		body, err := value.owner.node(ctx, child, true)
		if err == nil {
			value.Document.Content = body.String()
		}
		return err
	})
	if err != nil {
		value.Close()
		return nil, err
	}
	return value, nil
}

func (r *ResourceDirectoryRead) load(ctx context.Context, token ResourceDirectoryToken, output bool) (_ *ResourceDirectoryValue, err error) {
	if r == nil || r.owner == nil || r.owner.source == nil {
		return nil, errMetadataLogicalClosed
	}
	kind := r.kind
	if kind == "projectMilestones" {
		kind = "projects"
	}
	if token.Ordinal < 0 || token.Ordinal >= len(r.nodes[kind]) {
		return nil, ErrMetadataRecordQuery
	}
	if err := r.prepare(); err != nil {
		return nil, err
	}
	coreIndex, allowed := r.indices[kind][token.Ordinal]
	if !allowed {
		return nil, errors.Join(ErrAuthForbidden, ErrMetadataRecordQuery)
	}
	v := &ResourceDirectoryValue{Token: token, owner: &ResourceRead{store: r.owner.store}}
	defer func() {
		if err != nil {
			v.Close()
		}
	}()
	header := &DocumentDirectoryRead{owner: v.owner}
	node := r.nodes[kind][token.Ordinal]
	fields := resourceDirectoryHeaders[kind]
	if output {
		switch kind {
		case "projects":
			fields = directoryOutputFields(reflect.TypeFor[domain.Project](), "Comments", "CommentAttachments", "DescriptionRevisions", "IssueCountHistory", "ScopeHistory", "CompletedScopeHistory", "InProgressScopeHistory", "ProgressHistory", "Milestones")
		case "initiatives":
			fields = directoryOutputFields(reflect.TypeFor[domain.Initiative](), "Comments", "DescriptionHistory")
		case "labels":
			fields = directoryOutputFields(reflect.TypeFor[domain.IssueLabel]())
		case "documents":
			fields = directoryOutputFields(reflect.TypeFor[domain.Document](), "ContentState", "ContentData", "Revisions", "Permissions")
		}
	}
	if r.kind == "projectMilestones" {
		fields = []string{"ID", "Name", "SlugID", "ArchivedAt"}
	}
	if !output {
		fields = slices.DeleteFunc(slices.Clone(fields), func(name string) bool {
			return name == "Creator" || name == "Lead" || name == "Owner"
		})
	}
	value, err := header.header(ctx, node, fields)
	if err != nil {
		return nil, err
	}
	overlays := []resourceDirectoryIDs{}
	switch kind {
	case "projects":
		v.Project = value.Interface().(domain.Project)
		core := r.Data.Projects[coreIndex]
		if r.kind != "projectMilestones" {
			overlays = append(overlays, resourceDirectoryIDs{&v.Project.TeamIDs, core.TeamIDs}, resourceDirectoryIDs{&v.Project.Initiatives, core.Initiatives}, resourceDirectoryIDs{&v.Project.LabelIDs, core.LabelIDs})
			if output {
				overlays = append(overlays, resourceDirectoryIDs{&v.Project.DependencyIDs, core.DependencyIDs}, resourceDirectoryIDs{&v.Project.Customers, core.Customers})
			}
			v.Project.Progress, v.Project.IssueCount = core.Progress, core.IssueCount
		}
		if output && r.kind == "projects" {
			err = node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
				if key.String() != "Milestones" {
					return nil
				}
				// Preserve nil versus a native empty milestone slice.
				cursor := metadataLogicalCursor{child, child.offset + 16, ctx}
				var present [1]byte
				if err := cursor.read(present[:]); err != nil {
					return err
				}
				if present[0] != 0 {
					v.Project.Milestones = []domain.ProjectMilestone{}
				}
				return v.owner.nativeArray(ctx, child, func(_ int, milestone metadataLogicalNode) error {
					item, err := header.header(ctx, milestone, directoryOutputFields(reflect.TypeFor[domain.ProjectMilestone](), "DescriptionRevisions"))
					if err == nil {
						v.Project.Milestones = append(v.Project.Milestones, item.Interface().(domain.ProjectMilestone))
					}
					return err
				})
			})
		}
		if r.kind == "projectMilestones" && token.Child >= 0 {
			found := false
			err = node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
				if key.String() != "Milestones" {
					return nil
				}
				return v.owner.nativeArray(ctx, child, func(index int, milestone metadataLogicalNode) error {
					if index != token.Child {
						return nil
					}
					found = true
					fields := resourceDirectoryHeaders["milestones"]
					if output {
						fields = directoryOutputFields(reflect.TypeFor[domain.ProjectMilestone](), "DescriptionRevisions")
					}
					item, err := header.header(ctx, milestone, fields)
					if err == nil {
						v.Milestone = item.Interface().(domain.ProjectMilestone)
					}
					return err
				})
			})
			if err == nil && !found {
				err = ErrMetadataRecordQuery
			}
		}
	case "initiatives":
		v.Initiative = value.Interface().(domain.Initiative)
		core := r.Data.Initiatives[coreIndex]
		v.Initiative.LeadTeamID, v.Initiative.Favorite = core.LeadTeamID, core.Favorite
		overlays = append(overlays, resourceDirectoryIDs{&v.Initiative.ProjectIDs, core.ProjectIDs}, resourceDirectoryIDs{&v.Initiative.ParentInitiativeIDs, core.ParentInitiativeIDs})
		if output {
			overlays = append(overlays, resourceDirectoryIDs{&v.Initiative.ContributingTeamIDs, core.ContributingTeamIDs}, resourceDirectoryIDs{&v.Initiative.LabelIDs, core.LabelIDs})
		}
	case "labels":
		v.Label = value.Interface().(domain.IssueLabel)
		core := r.Data.Labels[coreIndex]
		v.Label.GroupID, v.Label.IssueCount = core.GroupID, core.IssueCount
	case "documents":
		v.Document = value.Interface().(domain.Document)
		core := r.Data.Documents[coreIndex]
		overlays = append(overlays, resourceDirectoryIDs{&v.Document.TeamIDs, core.TeamIDs}, resourceDirectoryIDs{&v.Document.InheritedTeamIDs, core.InheritedTeamIDs}, resourceDirectoryIDs{&v.Document.ProjectIDs, core.ProjectIDs})
		v.Document.IssueID, v.Document.IssueTeamID, v.Document.Favorite = core.IssueID, core.IssueTeamID, core.Favorite
		if !output {
			v.Document.Permissions = slices.Clone(core.Permissions)
		}
	}
	if err != nil {
		return nil, err
	}
	for _, overlay := range overlays {
		if err := v.copyIDs(ctx, overlay.target, overlay.source); err != nil {
			return nil, err
		}
	}
	if !output {
		if err := v.filterActors(ctx, node, kind); err != nil {
			return nil, err
		}
	}
	if r.key != nil && (r.key.TeamRestriction == "selected" || r.key.TeamRestriction == "" && len(r.key.TeamIDs) > 0) {
		allowedTeam := func(id string) bool { return slices.Contains(r.key.TeamIDs, id) }
		for _, user := range []*domain.User{v.Project.Creator, v.Project.Lead, v.Initiative.Owner, &v.Initiative.Creator, &v.Document.Creator} {
			if user != nil {
				RedactUserTeams(user, allowedTeam)
			}
		}
		for index := range v.Project.Resources {
			v.Project.Resources[index].PinnedTeamIDs = slices.DeleteFunc(v.Project.Resources[index].PinnedTeamIDs, func(id string) bool { return !allowedTeam(id) })
		}
	}
	return v, ctx.Err()
}

func (v *ResourceDirectoryValue) copyIDs(ctx context.Context, target *[]string, source []string) error {
	size := uint64(len(source))*32 + 256
	if size > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	for _, id := range source {
		if uint64(len(id)) > (metadataWorkBytes-size)/8 {
			return ErrMetadataWorkBudget
		}
		size += uint64(len(id)) * 8
	}
	if err := v.owner.reserve(ctx, size); err != nil {
		return err
	}
	if source == nil {
		*target = nil
		return nil
	}
	result := make([]string, len(source))
	for index, id := range source {
		result[index] = strings.Clone(id)
	}
	*target = result
	return nil
}

func (v *ResourceDirectoryValue) filterActors(ctx context.Context, node metadataLogicalNode, kind string) error {
	return node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		name := key.String()
		if !(kind == "projects" && name == "Lead" || kind == "initiatives" && (name == "Creator" || name == "Owner") || kind == "documents" && name == "Creator") {
			return nil
		}
		var actor *domain.User
		load := func(record metadataLogicalNode) error {
			value, err := (&DocumentDirectoryRead{owner: v.owner}).header(ctx, record, []string{"ID", "Name", "DisplayName", "Email"})
			if err == nil {
				user := value.Interface().(domain.User)
				actor = &user
			}
			return err
		}
		var err error
		if child.typ.Kind() == reflect.Pointer {
			err = child.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error { return load(record) })
		} else {
			err = load(child)
		}
		if err != nil {
			return err
		}
		switch {
		case kind == "projects":
			v.Project.Lead = actor
		case kind == "initiatives" && name == "Owner":
			v.Initiative.Owner = actor
		case kind == "initiatives" && actor != nil:
			v.Initiative.Creator = *actor
		case kind == "documents" && actor != nil:
			v.Document.Creator = *actor
		}
		return nil
	})
}
