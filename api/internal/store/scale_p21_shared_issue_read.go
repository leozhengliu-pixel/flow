package store

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

// SharedIssueRead authenticates by the first matching public token, without
// session/team authority. It owns the complete selected detail and comments.
type SharedIssueRead struct {
	Workspace domain.Workspace
	Issue     domain.Issue
	Comments  []domain.Comment
	Found     bool
	owner     *ResourceRead
	once      sync.Once
}

func (r *SharedIssueRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Workspace, r.Issue, r.Comments, r.Found = domain.Workspace{}, domain.Issue{}, nil, false
			r.owner.Close()
		})
	}
}

func (r *SharedIssueRead) ReserveOutput(ctx context.Context, value any) error {
	if r == nil || r.owner == nil || r.owner.source == nil {
		return errMetadataLogicalClosed
	}
	work, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
	if err != nil {
		return err
	}
	return r.owner.reserve(ctx, uint64(work))
}

func (s *SQLiteStore) BeginSharedIssueRead(ctx context.Context, workspace, token string) (_ *SharedIssueRead, err error) {
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	installed, owner, err := readLeafBegin(ctx, s, workspace)
	if err != nil {
		return nil, err
	}
	defer installed.Close()
	read := &SharedIssueRead{owner: owner, Comments: []domain.Comment{}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	source := owner.source
	ctx = metadataLogicalSourceScratchContext(ctx, source)
	index, _, _ := metadataLogicalField("workspace")
	value, err := owner.node(ctx, source.fields[index], false)
	if err != nil {
		return nil, err
	}
	read.Workspace = value.Interface().(domain.Workspace)
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	accept := func(node metadataLogicalNode) error {
		refreshed := metadataLogicalIssueRecord{source: source, base: metadataAggregateStoredNode{value: node}}
		value, err := readLeafAggregate(ctx, owner, refreshed)
		if err != nil {
			return err
		}
		read.Issue, read.Found = value.Interface().(domain.Issue), true
		return ErrMetadataIterationDone
	}
	index, _, _ = metadataLogicalField("issues")
	missing, err := source.fields[index].isNil(ctx)
	if err != nil {
		return nil, err
	}
	if !missing {
		err = owner.visitNative(ctx, "issues", func(_ int, node metadataLogicalNode) error {
			share, err := issueHotField(ctx, node, "ShareToken")
			if err != nil {
				return err
			}
			match, err := share.StringEqual(ctx, token)
			if err != nil || !match {
				return err
			}
			return accept(node)
		})
	} else {
		err = s.scanLegacyIssueRows(ctx, tx, read.Workspace.URLKey, func(row *legacyIssueRow) error {
			if row.Record.issue.ShareToken != token {
				return nil
			}
			frame, err := newMetadataLogicalBuilder(ctx)
			if err != nil {
				return err
			}
			defer frame.Close()
			node, err := frame.writeValue(reflect.ValueOf(row.Record.issue), 0, false)
			if err != nil {
				return err
			}
			return accept(node)
		})
	}
	if err := readLeafIteration(err); err != nil {
		return nil, err
	}
	if !read.Found {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return read, nil
	}
	if err := read.labelCounts(ctx, tx, missing); err != nil {
		return nil, err
	}
	index, _, _ = metadataLogicalField("comments")
	missing, err = source.fields[index].isNil(ctx)
	if err != nil {
		return nil, err
	}
	if !missing {
		err = source.fields[index].MapEntries(ctx, func(key, node metadataLogicalNode) error {
			equal, err := key.StringEqual(ctx, read.Issue.ID)
			if err != nil || !equal {
				return err
			}
			comments := metadataAggregateRefreshCollection{source: source, field: "comments", base: metadataAggregateStoredNode{value: node}}
			value, err := readLeafAggregate(ctx, owner, comments)
			if err == nil {
				read.Comments = value.Interface().([]domain.Comment)
				err = ErrMetadataIterationDone
			}
			return err
		})
		err = readLeafIteration(err)
	} else {
		rows := s.newIssueMutationRows(tx, read.Workspace.URLKey)
		defer rows.Close()
		if err := rows.beginDiscussion(ctx, read.Workspace.URLKey, read.Issue.ID); err != nil {
			return nil, err
		}
		selected, loadErr := issueChildSelectedContent[domain.Comment](ctx, rows, "comment", read.Issue.ID)
		if loadErr != nil {
			return nil, loadErr
		}
		for _, comment := range selected[read.Issue.ID] {
			frame, frameErr := newMetadataLogicalBuilder(ctx)
			if frameErr != nil {
				return nil, frameErr
			}
			node, frameErr := frame.writeValue(reflect.ValueOf(comment), 0, false)
			if frameErr == nil {
				refreshed := metadataAggregateRefreshRecord{source: source, field: "comments", base: metadataAggregateStoredNode{value: node}}
				var value reflect.Value
				value, frameErr = readLeafAggregate(ctx, owner, refreshed)
				if frameErr == nil {
					if frameErr = owner.reserve(ctx, uint64(reflect.TypeFor[domain.Comment]().Size())*4+512); frameErr == nil {
						read.Comments = append(read.Comments, value.Interface().(domain.Comment))
					}
				}
			}
			frame.Close()
			if frameErr != nil {
				return nil, frameErr
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if read.Comments == nil {
		read.Comments = []domain.Comment{}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return read, nil
}

// labelCounts replays refreshResourceCounts for the selected issue's labels:
// each nonarchived issue, nonarchived project and initiative counts a label
// once, empty IDs never count, and only labels present in the directory take
// the computed count. Only the selected label counters are retained.
func (r *SharedIssueRead) labelCounts(ctx context.Context, tx *sqlTx, sqlIssues bool) error {
	targets, counts := map[string]bool{}, map[string]int{}
	for _, label := range r.Issue.Labels {
		if _, exists := targets[label.ID]; exists {
			continue
		}
		if err := r.owner.reserve(ctx, uint64(len(label.ID))*8+1024); err != nil {
			return err
		}
		targets[label.ID] = false
	}
	if len(targets) == 0 {
		return nil
	}
	if err := r.owner.visitNative(ctx, "labels", func(_ int, node metadataLogicalNode) error {
		return r.owner.withHeader(ctx, node, []string{"ID"}, func(value reflect.Value) error {
			id := value.Interface().(domain.IssueLabel).ID
			if _, selected := targets[id]; selected {
				targets[id] = true
			}
			return nil
		})
	}); err != nil {
		return err
	}
	add := func(ids []string) {
		seen := map[string]bool{}
		for _, id := range ids {
			if known := targets[id]; known && id != "" && !seen[id] {
				seen[id] = true
				counts[id]++
			}
		}
	}
	if sqlIssues {
		// issue_label_records holds each issue's distinct label IDs; IDs are
		// compared exactly here rather than by the database collation.
		rows, err := tx.QueryContext(ctx, `SELECT l.label_id FROM issue_label_records l JOIN issue_records i ON i.workspace_key=l.workspace_key AND i.id=l.issue_id WHERE l.workspace_key=? AND i.archived=0`, r.Workspace.URLKey)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			if known := targets[id]; known && id != "" {
				counts[id]++
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
	} else if err := r.owner.visitNative(ctx, "issues", func(_ int, node metadataLogicalNode) error {
		return r.owner.withHeader(ctx, node, []string{"Labels", "ArchivedAt"}, func(value reflect.Value) error {
			issue := value.Interface().(domain.Issue)
			if issue.ArchivedAt != nil {
				return nil
			}
			ids := make([]string, 0, len(issue.Labels))
			for _, label := range issue.Labels {
				ids = append(ids, label.ID)
			}
			add(ids)
			return nil
		})
	}); err != nil {
		return err
	}
	for _, kind := range []string{"projects", "initiatives"} {
		if err := r.owner.visitNative(ctx, kind, func(_ int, node metadataLogicalNode) error {
			return r.owner.withHeader(ctx, node, []string{"LabelIDs", "ArchivedAt"}, func(value reflect.Value) error {
				if kind == "projects" {
					project := value.Interface().(domain.Project)
					if project.ArchivedAt == nil {
						add(project.LabelIDs)
					}
				} else {
					add(value.Interface().(domain.Initiative).LabelIDs)
				}
				return nil
			})
		}); err != nil {
			return err
		}
	}
	for index := range r.Issue.Labels {
		if label := &r.Issue.Labels[index]; targets[label.ID] {
			label.IssueCount = counts[label.ID]
		}
	}
	return ctx.Err()
}
