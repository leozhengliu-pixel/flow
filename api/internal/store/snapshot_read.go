package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"

	"flow/api/internal/domain"
)

// project and initiative queries computes global hierarchy and filter aggregates. Its complete summary
// graph is request-owned, capped here and charged to the replica work pool;
// this is not a constant-space page reader or the 2 MiB list-page target.
const snapshotMaxWork = 128 << 20

var snapshotFields = []string{
	"workspace", "viewer", "viewerRole",
	"teams", "teamSettings", "cycleSettings", "workspaceSettings", "userSettings",
	"settings", "labels", "labelGroups", "projectStatuses", "projectTemplates",
	"projectRelations", "initiativeRelations", "projects", "initiatives",
	"projectUpdates", "initiativeUpdates", "savedViews", "favorites",
	"favoriteFolders", "subscriptions", "agentSkills", "customers",
	"customerStatuses", "customerTiers", "customerRequests", "cycles", "documents",
	"releases", "releasePipelines", "reviews", "users", "members", "teamMembers",
	"invitations",
}

var snapshotInitiativeFields = directoryOutputFields(reflect.TypeFor[domain.Initiative](),
	"Description", "DescriptionHistory", "Resources", "Comments", "CommentAttachments")
var snapshotDocumentFields = directoryOutputFields(reflect.TypeFor[domain.Document](),
	"Content", "ContentState", "ContentData", "Revisions")

// SnapshotRead retains native summary values, their processing allowance and
// all projection children through the final response use.
type SnapshotRead struct {
	Snapshot ScaleSnapshot
	owner    *ResourceRead
	work     uint64
}

func (r *SnapshotRead) Close() {
	if r != nil {
		r.Snapshot = ScaleSnapshot{}
		r.owner.Close()
	}
}

func (r *SnapshotRead) reserve(ctx context.Context, cost uint64) error {
	if cost > snapshotMaxWork-r.work {
		return ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, cost); err != nil {
		return err
	}
	r.work += cost
	return nil
}

// BeginSnapshotRead preserves ReadScaleSnapshot's bounded-attempt installed
// copy/stamp check and reload recovery. It does not borrow caller SQL snapshots
// or installed pins: neither influenced the literal project and initiative queries accessor.
func (s *SQLiteStore) BeginSnapshotRead(ctx context.Context, workspace, principal string, unfiltered bool) (*SnapshotRead, error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	for attempt := 1; ; attempt++ {
		tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return nil, err
		}
		stamp, err := readMetadataStamp(ctx, tx, workspace)
		var read *SnapshotRead
		if err == nil {
			accessChangedReadHook(ctx, attempt)
			read, err = s.snapshotAt(ctx, workspace, principal, unfiltered, tx, stamp)
		}
		_ = tx.Rollback()
		if errors.Is(err, ErrResourceAccessChanged) {
			if err := s.accessChangedReread(ctx, workspace, attempt); err != nil {
				return nil, err
			}
			continue
		}
		return read, err
	}
}

func (s *SQLiteStore) snapshotAt(ctx context.Context, workspace, principal string, unfiltered bool, tx *sqlTx, stamp copyStamp) (_ *SnapshotRead, err error) {
	r := &SnapshotRead{owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	// Covers fixed projection state and the logical iterator's one bounded
	// map-key scratch value before the selected graph preflight.
	if err = r.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	s.mu.RLock()
	data, exists := s.workspaces[workspace]
	if !exists {
		s.mu.RUnlock()
		return nil, ErrResourceAccessChanged
	}
	nextIssueNumber := data.NextIssueNumber
	s.metadataResidency.mu.Lock()
	source := s.metadataResidency.logical[workspace]
	if source == nil {
		err = ErrBoundedMetadataCoverage
	} else if source.Stamp() != stamp {
		// The immutable full source carries the old copy's certificate.
		// The resident policy projection deliberately has no copy revision.
		err = ErrResourceAccessChanged
	} else {
		err = source.Retain()
	}
	s.metadataResidency.mu.Unlock()
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	r.owner.source = source
	projected, err := r.projectedWork(ctx)
	if err != nil {
		return nil, err
	}
	// The ordinary native readers admit projected before decoding. Reserve
	// three additional copies for clone/defaults, viewer projection and final
	// encoding, before any complete selected collection is transferred.
	if projected > (snapshotMaxWork-r.work)/4 {
		return nil, ErrMetadataWorkBudget
	}
	if err = r.owner.reserve(ctx, projected*3); err != nil {
		return nil, err
	}
	r.work += projected * 4
	if err = r.load(ctx); err != nil {
		return nil, err
	}
	r.Snapshot.Data.NextIssueNumber = nextIssueNumber
	// Only the already admitted projected graph is cloned. The installed
	// source's unrelated collections and excluded histories are never decoded.
	r.Snapshot.Data = cloneBootstrap(r.Snapshot.Data)
	r.Snapshot.Data.NextIssueNumber = nextIssueNumber
	normalizeStoredMetadata(&r.Snapshot.Data)
	snapshot := context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx, metadata: r.Snapshot.Data})
	snapshot = r.owner.metadataOutputContext(snapshot)
	if unfiltered {
		r.Snapshot.Data.ViewerRole = "admin"
	} else {
		r.Snapshot.Data, err = s.PagedWorkspaceMetadata(snapshot, workspace, principal)
		if err != nil {
			return nil, err
		}
		if err = metadataOutputError(snapshot); err != nil {
			return nil, err
		}
	}
	r.Snapshot.Revision = strconv.FormatInt(stamp.revision, 10)
	r.Snapshot.Versions = map[string]map[string]string{"projects": {}, "initiatives": {}}
	if s.db.journalReady.Load() {
		if err = checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
			return nil, err
		}
		if err = r.versions(ctx, tx, workspace); err != nil {
			return nil, err
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

func snapshotHeaderWork(ctx context.Context, node metadataLogicalNode, fields []string) (uint64, error) {
	cost := uint64(2048)
	err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if slices.Contains(fields, key.String()) {
			if child.cost > (snapshotMaxWork-cost)/8 {
				return ErrMetadataWorkBudget
			}
			cost += child.cost * 8
		}
		return nil
	})
	return cost, err
}

func (r *SnapshotRead) firstUpdate(ctx context.Context, updates metadataLogicalNode, visit func(metadataLogicalNode) error) error {
	err := r.owner.nativeArray(ctx, updates, func(_ int, update metadataLogicalNode) error {
		if err := visit(update); err != nil {
			return err
		}
		return ErrMetadataIterationDone
	})
	if errors.Is(err, ErrMetadataIterationDone) {
		return nil
	}
	return err
}

func (r *SnapshotRead) projectedWork(ctx context.Context) (uint64, error) {
	cost := uint64(4096)
	add := func(part uint64, err error) error {
		if err != nil {
			return err
		}
		if part > snapshotMaxWork-cost {
			return ErrMetadataWorkBudget
		}
		cost += part
		return nil
	}
	for _, name := range snapshotFields {
		index, _, _ := metadataLogicalField(name)
		node := r.owner.source.fields[index]
		var err error
		switch name {
		case "projects", "initiatives", "documents":
			err = r.owner.nativeArray(ctx, node, func(_ int, item metadataLogicalNode) error {
				fields := projectDirectoryListFields
				if name == "initiatives" {
					fields = snapshotInitiativeFields
				} else if name == "documents" {
					fields = snapshotDocumentFields
				}
				if err := add(snapshotHeaderWork(ctx, item, fields)); err != nil {
					return err
				}
				if name != "projects" {
					return nil
				}
				return item.Children(ctx, func(key reflect.Value, milestones metadataLogicalNode) error {
					if key.String() != "Milestones" {
						return nil
					}
					return r.owner.nativeArray(ctx, milestones, func(_ int, milestone metadataLogicalNode) error {
						return add(snapshotHeaderWork(ctx, milestone, directoryOutputFields(reflect.TypeFor[domain.ProjectMilestone](), "Description", "DescriptionRevisions")))
					})
				})
			})
		case "projectUpdates", "initiativeUpdates":
			err = node.Children(ctx, func(key reflect.Value, updates metadataLogicalNode) error {
				if err := add(uint64(key.Len())*16+1024, nil); err != nil {
					return err
				}
				field := "ProjectID"
				if name == "initiativeUpdates" {
					field = "InitiativeID"
				}
				return r.firstUpdate(ctx, updates, func(update metadataLogicalNode) error {
					return add(snapshotHeaderWork(ctx, update, []string{"ID", field, "CreatedAt", "Health"}))
				})
			})
		default:
			if node.cost > (snapshotMaxWork-(128<<10))/8 {
				return 0, ErrMetadataWorkBudget
			}
			err = add(node.cost*8+128<<10, nil)
		}
		if err != nil {
			return 0, err
		}
	}
	return cost, ctx.Err()
}

func (r *SnapshotRead) load(ctx context.Context) error {
	target := reflect.ValueOf(&r.Snapshot.Data).Elem()
	header := &DocumentDirectoryRead{owner: r.owner}
	for _, name := range snapshotFields {
		index, field, _ := metadataLogicalField(name)
		node := r.owner.source.fields[index]
		switch name {
		case "projects", "initiatives", "documents":
			value := target.Field(field.Index[0])
			if name == "projects" {
				value.Set(reflect.MakeSlice(value.Type(), 0, 0))
			} else {
				absent, err := node.isNil(ctx)
				if err != nil {
					return err
				}
				if !absent {
					value.Set(reflect.MakeSlice(value.Type(), 0, 0))
				}
			}
			if err := r.owner.nativeArray(ctx, node, func(_ int, item metadataLogicalNode) error {
				var result reflect.Value
				var err error
				if name == "projects" {
					var project domain.Project
					project, err = projectDirectoryNative(ctx, r.owner, item, projectDirectoryListFields, true)
					result = reflect.ValueOf(project)
				} else {
					fields := snapshotInitiativeFields
					if name == "documents" {
						fields = snapshotDocumentFields
					}
					result, err = header.header(ctx, item, fields)
				}
				if err == nil {
					value.Set(reflect.Append(value, result))
				}
				return err
			}); err != nil {
				return err
			}
		case "projectUpdates", "initiativeUpdates":
			value := target.Field(field.Index[0])
			value.Set(reflect.MakeMap(value.Type()))
			if err := node.Children(ctx, func(key reflect.Value, updates metadataLogicalNode) error {
				if err := r.owner.reserve(ctx, uint64(key.Len())*16+1024); err != nil {
					return err
				}
				selected := reflect.MakeSlice(value.Type().Elem(), 0, 0)
				field := "ProjectID"
				if name == "initiativeUpdates" {
					field = "InitiativeID"
				}
				if err := r.firstUpdate(ctx, updates, func(update metadataLogicalNode) error {
					item, err := header.header(ctx, update, []string{"ID", field, "CreatedAt", "Health"})
					if err == nil {
						selected = reflect.Append(selected, item)
					}
					return err
				}); err != nil {
					return err
				}
				value.SetMapIndex(key, selected)
				return nil
			}); err != nil {
				return err
			}
		default:
			value, err := r.owner.node(ctx, node, false)
			if err != nil {
				return err
			}
			target.Field(field.Index[0]).Set(value)
		}
	}
	return ctx.Err()
}

func (r *SnapshotRead) versions(ctx context.Context, tx *sqlTx, workspace string) error {
	var count, keyBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(LENGTH(field)+LENGTH(record_key)),0) FROM record_versions WHERE workspace_key=? AND deleted=0 AND field IN ('projects','initiatives')`, workspace).Scan(&count, &keyBytes); err != nil {
		return err
	}
	// LENGTH can count characters on external databases; four bytes per
	// character plus map/number/encoding overhead bounds the transfer.
	if count < 0 || keyBytes < 0 || count > snapshotMaxWork/512 || keyBytes > snapshotMaxWork/64 {
		return ErrMetadataWorkBudget
	}
	if err := r.reserve(ctx, uint64(count)*512+uint64(keyBytes)*64+4096); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT field,record_key,version FROM record_versions WHERE workspace_key=? AND deleted=0 AND field IN ('projects','initiatives')`, workspace)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, id string
		var version int64
		if err := rows.Scan(&kind, &id, &version); err != nil {
			return err
		}
		r.Snapshot.Versions[kind][id] = strconv.FormatInt(version, 10)
	}
	return rows.Err()
}
