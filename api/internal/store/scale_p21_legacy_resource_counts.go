package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

type legacyIssueKey struct {
	ID   string
	Blob bool
}

// refreshLegacyResourceCounts applies the legacy full-image count pass without
// retaining the issue population. Overrides use SQL physical keys, including
// deletions, so decoded IDs never change replacement or duplicate semantics.
func (s *SQLiteStore) refreshLegacyResourceCounts(ctx context.Context, tx *sqlTx, workspace string, source *metadataLogicalSource, overrides map[legacyIssueKey]*domain.Issue) (_ *metadataLogicalSource, err error) {
	if s == nil || !s.BoundedMetadataEnabled() || source == nil || tx == nil || tx.database != s.db {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = metadataLogicalSourceScratchContext(ctx, source)
	owner := &ResourceRead{store: s}
	defer owner.Close()
	if err := owner.reserve(ctx, 64<<10); err != nil {
		return nil, err
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return nil, err
	}
	if stamp.workspaceID == "" || source.Stamp().workspaceID != stamp.workspaceID {
		return nil, ErrMetadataRecordQuery
	}
	// Membership and deduplication live on disk. Only one row or projected
	// directory entry is materialized at a time.
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return nil, err
	}
	defer stage.Close()
	for _, query := range []string{
		`CREATE TABLE logical_counts(kind TEXT NOT NULL,key TEXT NOT NULL,total INTEGER NOT NULL,PRIMARY KEY(kind,key))`,
		`CREATE TABLE legacy_count_seen(id TEXT NOT NULL,is_blob INTEGER NOT NULL,PRIMARY KEY(id,is_blob))`,
		`CREATE TABLE legacy_count_labels(id TEXT PRIMARY KEY)`,
	} {
		if _, err := stage.exec(ctx, 4096, query); err != nil {
			return nil, err
		}
	}
	counts := &metadataLogicalNormalizer{store: s, stage: stage}
	beginLabels := func() error {
		_, err := stage.exec(ctx, 256, `DELETE FROM legacy_count_labels`)
		return err
	}
	addLabel := func(id string) error {
		if id == "" {
			return nil
		}
		result, err := stage.exec(ctx, int64(len(id)+256),
			`INSERT INTO legacy_count_labels(id) VALUES(?) ON CONFLICT(id) DO NOTHING`, id)
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if inserted != 0 {
			return counts.increment(ctx, "labels", id)
		}
		return nil
	}
	addLabels := func(ids []string) error {
		if err := beginLabels(); err != nil {
			return err
		}
		for _, id := range ids {
			if err := addLabel(id); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	addIssue := func(issue *domain.Issue) error {
		if issue == nil || issue.ArchivedAt != nil {
			return nil
		}
		if issue.Project != nil {
			if err := counts.increment(ctx, "projects", issue.Project.ID); err != nil {
				return err
			}
		}
		if err := beginLabels(); err != nil {
			return err
		}
		for _, label := range issue.Labels {
			if err := addLabel(label.ID); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	if err := s.scanLegacyIssueRows(ctx, tx, workspace, func(row *legacyIssueRow) error {
		issue := &row.Record.issue
		if replacement, found := overrides[legacyIssueKey{ID: row.PhysicalID, Blob: row.idBlob}]; found {
			issue = replacement
			if _, err := stage.exec(ctx, int64(len(row.PhysicalID)+256),
				`INSERT INTO legacy_count_seen(id,is_blob) VALUES(?,?) ON CONFLICT(id,is_blob) DO NOTHING`, row.PhysicalID, row.idBlob); err != nil {
				return err
			}
		}
		return addIssue(issue)
	}); err != nil {
		return nil, err
	}
	for physical, issue := range overrides {
		var seen bool
		if err := stage.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_count_seen WHERE id=? AND is_blob=?)`, physical.ID, physical.Blob).Scan(&seen); err != nil {
			return nil, err
		}
		if !seen {
			if err := addIssue(issue); err != nil {
				return nil, err
			}
		}
	}
	if err := source.VisitFields(ctx, func(name string, collection metadataLogicalNode) error {
		if name != "projects" && name != "initiatives" {
			return nil
		}
		return collection.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error {
			read := &ResourceRead{store: s}
			defer read.Close()
			if name == "projects" {
				archived, err := issueHotField(ctx, item, "ArchivedAt")
				if err != nil {
					return err
				}
				active, err := archived.isNil(ctx)
				if err != nil || !active {
					return err
				}
			}
			value, err := read.projectNode(ctx, item, "LabelIDs")
			if err != nil {
				return err
			}
			if name == "projects" {
				project := value.Interface().(domain.Project)
				return addLabels(project.LabelIDs)
			}
			return addLabels(value.Interface().(domain.Initiative).LabelIDs)
		})
	}); err != nil {
		return nil, err
	}
	counted, err := source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		base := metadataAggregateStoredNode{original}
		if name == "labels" || name == "projects" {
			return legacyCountCollection{base: base, counts: counts, kind: name}, true, nil
		}
		return base, false, nil
	})
	if err != nil {
		return nil, err
	}
	defer counted.Release()
	return counted.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == "issues" {
			nilValue, err := original.isNil(ctx)
			if err != nil {
				return nil, false, err
			}
			if !nilValue {
				return legacyCountIssues{base: metadataAggregateStoredNode{original}, source: counted}, true, nil
			}
		}
		return metadataAggregateStoredNode{original}, false, nil
	})
}

type legacyCountIssues struct {
	base   metadataAggregateNode
	source *metadataLogicalSource
}

func (n legacyCountIssues) nodeType() reflect.Type { return n.base.nodeType() }
func (n legacyCountIssues) logicalBase() metadataLogicalNode {
	return n.base.(metadataAggregateStoredNode).value
}
func (n legacyCountIssues) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyCountIssues) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		return visit(key, legacyCountIssue{base: child, source: n.source})
	})
}

type legacyCountIssue struct {
	base   metadataAggregateNode
	source *metadataLogicalSource
}

func (n legacyCountIssue) nodeType() reflect.Type { return n.base.nodeType() }
func (n legacyCountIssue) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyCountIssue) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		if key.String() == "Labels" {
			child = metadataLogicalIssueCollection{n.source, child, true}
		}
		return visit(key, child)
	})
}

type legacyCountCollection struct {
	base   metadataAggregateNode
	counts *metadataLogicalNormalizer
	kind   string
}

func (n legacyCountCollection) nodeType() reflect.Type { return n.base.nodeType() }
func (n legacyCountCollection) logicalBase() metadataLogicalNode {
	return n.base.(metadataAggregateStoredNode).value
}
func (n legacyCountCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyCountCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		return visit(key, legacyCountRecord{base: child, counts: n.counts, kind: n.kind})
	})
}

type legacyCountRecord struct {
	base   metadataAggregateNode
	counts *metadataLogicalNormalizer
	kind   string
}

func (n legacyCountRecord) nodeType() reflect.Type { return n.base.nodeType() }
func (n legacyCountRecord) logicalBase() metadataLogicalNode {
	return n.base.(metadataAggregateStoredNode).value
}
func (n legacyCountRecord) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyCountRecord) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	id, err := metadataAggregateChild(ctx, n.base, "ID")
	if err != nil {
		return err
	}
	stored, ok := id.(metadataAggregateStoredNode)
	if !ok {
		return ErrMetadataRecordQuery
	}
	read := &ResourceRead{store: n.counts.store}
	defer read.Close()
	value, err := read.node(ctx, stored.value, false)
	if err != nil {
		return err
	}
	total, err := n.counts.count(ctx, n.kind, value.String())
	if err != nil {
		return err
	}
	return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		if key.String() == "IssueCount" {
			child = metadataAggregateNativeNode{reflect.ValueOf(total)}
		}
		return visit(key, child)
	})
}
