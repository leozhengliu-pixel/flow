package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

type metadataLogicalNormalizer struct {
	store    *SQLiteStore
	source   *metadataLogicalSource
	stage    *sw26MigrationStage
	base     domain.Bootstrap
	work     *metadataLogicalNormalizeWork
	baseWork int
	values   *metadataLogicalBuilder
}

// Cold normalization runs the original normalizer on one owned record and its
// dependencies. Cross-record lookup/count/uniqueness state lives in a bounded
// disk index, rather than another complete Bootstrap.
func (s *SQLiteStore) normalizeMetadataLogicalSource(ctx context.Context, source *metadataLogicalSource) (_ *metadataLogicalSource, err error) {
	ctx = metadataLogicalSourceScratchContext(ctx, source)
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return nil, err
	}
	defer stage.Close()
	for _, query := range []string{
		`CREATE TABLE logical_values(kind TEXT NOT NULL,key TEXT NOT NULL,ordinal INTEGER NOT NULL,value BLOB,frame_offset INTEGER,frame_size INTEGER,frame_cost INTEGER,frame_unavailable INTEGER,frame_blocked INTEGER,PRIMARY KEY(kind,key))`,
		`CREATE INDEX logical_values_order ON logical_values(kind,ordinal)`,
		`CREATE TABLE logical_counts(kind TEXT NOT NULL,key TEXT NOT NULL,total INTEGER NOT NULL,PRIMARY KEY(kind,key))`,
		`CREATE TABLE logical_states(ordinal INTEGER PRIMARY KEY,team TEXT NOT NULL,id TEXT NOT NULL,is_default INTEGER NOT NULL,is_backlog INTEGER NOT NULL)`,
		`CREATE TABLE logical_issues(ordinal INTEGER PRIMARY KEY,id TEXT NOT NULL,parent TEXT)`,
		`CREATE INDEX logical_issue_ids ON logical_issues(id,ordinal)`,
		`CREATE INDEX logical_issue_parents ON logical_issues(parent,ordinal)`,
	} {
		if _, err := stage.exec(ctx, 4096, query); err != nil {
			return nil, err
		}
	}
	n := &metadataLogicalNormalizer{store: s, source: source, stage: stage}
	baseCost := uint64(64 << 10)
	if err := source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name == "workspace" || name == "viewer" || name == "viewerRole" || name == "workspaceSettings" {
			baseCost += node.cost * 4
		}
		if baseCost > metadataWorkBytes {
			return ErrMetadataWorkBudget
		}
		return nil
	}); err != nil {
		return nil, err
	}
	n.baseWork = int(baseCost)
	ctx, releaseBase, err := s.admitMetadataWork(ctx, n.baseWork)
	if err != nil {
		return nil, err
	}
	defer releaseBase()
	n.values, err = newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer n.values.Close()
	n.base, err = source.Select(ctx, MetadataRecordScope{Fields: []string{"workspaceSettings"}}, metadataPolicyBytes)
	if err != nil {
		return nil, err
	}
	normalizeStoredMetadata(&n.base)
	if err := n.index(ctx); err != nil {
		return nil, err
	}
	builder, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			builder.Close()
		}
	}()
	err = source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		index, _, ok := metadataLogicalField(name)
		if !ok || name == "-" {
			return nil
		}
		if err := builder.inheritLocations(node.source); err != nil {
			return err
		}
		if node.typ.Kind() != reflect.Map && (node.typ.Kind() != reflect.Slice || node.typ.Elem().Kind() == reflect.Uint8) {
			value := reflect.ValueOf(n.base).Field(index)
			// The normalizer only modifies workspaceSettings among scalar
			// metadata fields. Keep all other actual stored scalar values.
			if name != "workspaceSettings" {
				owned, release, err := n.value(ctx, node)
				if err != nil {
					return err
				}
				defer release()
				value = owned
			}
			if err := builder.auditJSONLocations(value); err != nil {
				return err
			}
			return builder.Field(name, value)
		}
		nilValue, err := node.isNil(ctx)
		if err != nil {
			return err
		}
		emptyDefault := reflect.ValueOf(n.base).Field(index)
		if nilValue && !emptyDefault.IsNil() || name == "projectStatuses" {
			// Empty project statuses synthesize the standard ordered list.
			empty := true
			err := node.Children(ctx, func(reflect.Value, metadataLogicalNode) error {
				empty = false
				return errMetadataAggregateStop
			})
			if err != nil && !errors.Is(err, errMetadataAggregateStop) {
				return err
			}
			if empty && emptyDefault.Len() > 0 && name != "userSettings" && name != "notificationPreferences" && name != "teamSettings" {
				return builder.Field(name, emptyDefault)
			}
			nilValue = false
		}
		if err := builder.BeginCollection(name, nilValue); err != nil {
			return err
		}
		ordinal := 0
		if err := node.Children(ctx, func(key reflect.Value, item metadataLogicalNode) error {
			owned, release, err := n.value(ctx, item)
			if err != nil {
				return err
			}
			defer release()
			normalized, err := n.record(ctx, name, key, owned, ordinal)
			ordinal++
			if err != nil {
				return err
			}
			if node.typ.Kind() == reflect.Slice {
				key = reflect.Value{}
			}
			if err := builder.auditJSONLocations(normalized); err != nil {
				return err
			}
			if err := builder.Append(key, normalized); err != nil {
				return err
			}
			if name == "userSettings" || name == "notificationPreferences" || name == "teamSettings" {
				return n.put(ctx, "seen:"+name, key.String(), ordinal, []byte("true"))
			}
			return nil
		}); err != nil {
			return err
		}
		if err := n.missing(ctx, builder, name); err != nil {
			return err
		}
		return builder.EndCollection()
	})
	if err != nil {
		return nil, err
	}
	return builder.Finish(source.Stamp())
}

func (n *metadataLogicalNormalizer) value(ctx context.Context, node metadataLogicalNode) (reflect.Value, func(), error) {
	if node.cost > uint64(metadataWorkBytes/8) {
		return reflect.Value{}, nil, ErrMetadataWorkBudget
	}
	operation, err := startMetadataLogicalNormalizeWork(ctx, n.store, n.baseWork, int(node.cost)*8+64<<10)
	if err != nil {
		return reflect.Value{}, nil, err
	}
	previous := n.work
	n.work = operation
	release := func() {
		operation.Close()
		n.work = previous
	}
	value, err := node.Value(ctx, int(node.cost))
	if err != nil {
		release()
		return reflect.Value{}, nil, err
	}
	return value, release, nil
}

func (n *metadataLogicalNormalizer) put(ctx context.Context, kind, key string, ordinal int, raw []byte) error {
	_, err := n.stage.exec(ctx, int64(len(raw)+len(key)+len(kind)+256),
		`INSERT INTO logical_values(kind,key,ordinal,value) VALUES(?,?,?,?) ON CONFLICT(kind,key) DO UPDATE SET ordinal=excluded.ordinal,value=excluded.value,frame_offset=NULL,frame_size=NULL,frame_cost=NULL,frame_unavailable=NULL,frame_blocked=NULL`,
		kind, key, ordinal, raw)
	return err
}

func (n *metadataLogicalNormalizer) get(ctx context.Context, kind, key string, output any) (bool, error) {
	found, release, err := n.getAdmitted(ctx, kind, key, output)
	if err != nil {
		return false, err
	}
	if _, scalar := output.(*bool); scalar {
		release()
	} else if n.work != nil {
		n.work.keep(release)
	} else {
		release()
	}
	return found, nil
}

func (n *metadataLogicalNormalizer) count(ctx context.Context, kind, key string) (int, error) {
	var result int
	err := n.stage.db.QueryRowContext(ctx, `SELECT total FROM logical_counts WHERE kind=? AND key=?`, kind, key).Scan(&result)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return result, err
}

func (n *metadataLogicalNormalizer) index(ctx context.Context) error {
	return n.source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if !slices.Contains([]string{"users", "teams", "states", "labels", "issues", "projectStatuses", "projects", "initiatives"}, name) {
			return nil
		}
		ordinal := 0
		err := node.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error {
			owned, release, err := n.value(ctx, item)
			if err != nil {
				return err
			}
			defer release()
			if name == "projects" || name == "initiatives" || name == "issues" {
				ids := []string(nil)
				if name == "projects" {
					project := owned.Interface().(domain.Project)
					if project.ArchivedAt == nil {
						ids = project.LabelIDs
					}
				} else if name == "initiatives" {
					ids = owned.Interface().(domain.Initiative).LabelIDs
				} else {
					issue := owned.Interface().(domain.Issue)
					if _, err := n.stage.exec(ctx, int64(len(issue.ID)+256),
						`INSERT INTO logical_issues(ordinal,id,parent) VALUES(?,?,?)`, ordinal, issue.ID, issue.ParentID); err != nil {
						return err
					}
					ordinal++
					if issue.ArchivedAt == nil {
						ids = labelIDs(issue.Labels)
						if issue.Project != nil {
							if err := n.increment(ctx, "projects", issue.Project.ID); err != nil {
								return err
							}
						}
					}
				}
				seen := map[string]bool{}
				for _, id := range ids {
					if id == "" || seen[id] {
						continue
					}
					seen[id] = true
					if err := n.increment(ctx, "labels", id); err != nil {
						return err
					}
				}
				return nil
			}
			if name == "states" {
				mini := domain.Bootstrap{States: []domain.WorkflowState{owned.Interface().(domain.WorkflowState)}}
				normalizeStoredMetadata(&mini)
				owned = reflect.ValueOf(mini.States[0])
				state := mini.States[0]
				if _, err := n.stage.exec(ctx, int64(len(state.ID)+len(state.TeamID)+256),
					`INSERT INTO logical_states(ordinal,team,id,is_default,is_backlog) VALUES(?,?,?,?,?)`,
					ordinal, state.TeamID, state.ID, state.Default, state.Type == "backlog"); err != nil {
					return err
				}
			}
			if name == "projectStatuses" {
				mini := domain.Bootstrap{ProjectStatuses: []domain.ProjectStatus{owned.Interface().(domain.ProjectStatus)}}
				normalizeStoredMetadata(&mini)
				if mini.ProjectStatuses[0].Position == 0 && ordinal > 0 {
					mini.ProjectStatuses[0].Position = float64(ordinal)
				}
				owned = reflect.ValueOf(mini.ProjectStatuses[0])
			}
			id, ok := metadataRecordValueKey(owned)
			if !ok {
				return ErrMetadataRecordQuery
			}
			if err := n.putTyped(ctx, name, id, ordinal, owned, item.source); err != nil {
				return err
			}
			ordinal++
			return nil
		})
		if err != nil {
			return err
		}
		if name == "projectStatuses" && ordinal == 0 {
			for ordinal, status := range n.base.ProjectStatuses {
				if err := n.putTyped(ctx, name, status.ID, ordinal, reflect.ValueOf(status), nil); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (n *metadataLogicalNormalizer) increment(ctx context.Context, kind, key string) error {
	_, err := n.stage.exec(ctx, int64(len(key)+len(kind)+128),
		`INSERT INTO logical_counts(kind,key,total) VALUES(?,?,1) ON CONFLICT(kind,key) DO UPDATE SET total=total+1`, kind, key)
	return err
}

func (n *metadataLogicalNormalizer) record(ctx context.Context, name string, key reflect.Value, item reflect.Value, ordinal int) (reflect.Value, error) {
	mini := domain.Bootstrap{Viewer: n.base.Viewer}
	index, field, _ := metadataLogicalField(name)
	target := reflect.ValueOf(&mini).Elem().Field(index)
	isMap := field.Type.Kind() == reflect.Map
	if isMap {
		target.Set(reflect.MakeMap(field.Type))
		target.SetMapIndex(key, item)
	} else {
		target.Set(reflect.MakeSlice(field.Type, 1, 1))
		target.Index(0).Set(item)
	}
	user := func(value domain.User) error {
		var current domain.User
		found, err := n.get(ctx, "users", value.ID, &current)
		if found {
			mini.Users = append(mini.Users, current)
		}
		return err
	}
	restoreProjectStatus := false
	switch name {
	case "members":
		if err := user(mini.Members[0].User); err != nil {
			return item, err
		}
	case "projects":
		project := &mini.Projects[0]
		var status domain.ProjectStatus
		if found, err := n.get(ctx, "projectStatuses", project.Status.ID, &status); err != nil {
			return item, err
		} else if found {
			mini.ProjectStatuses = []domain.ProjectStatus{status}
		} else {
			restoreProjectStatus = true
		}
		if project.Lead != nil {
			if err := user(*project.Lead); err != nil {
				return item, err
			}
		}
		if project.Creator != nil {
			if err := user(*project.Creator); err != nil {
				return item, err
			}
		}
	case "initiatives":
		initiative := mini.Initiatives[0]
		if initiative.Creator.ID == "" {
			if err := user(n.base.Viewer); err != nil {
				return item, err
			}
		}
		if err := user(initiative.Creator); err != nil {
			return item, err
		}
		if initiative.Owner != nil {
			if err := user(*initiative.Owner); err != nil {
				return item, err
			}
		}
	case "projectUpdates":
		for _, update := range mini.ProjectUpdates[key.String()] {
			if err := user(update.User); err != nil {
				return item, err
			}
		}
	case "initiativeUpdates":
		for _, update := range mini.InitiativeUpdates[key.String()] {
			if err := user(update.User); err != nil {
				return item, err
			}
		}
	case "comments":
		for _, comment := range mini.Comments[key.String()] {
			if err := user(comment.User); err != nil {
				return item, err
			}
		}
	case "activities":
		for _, activity := range mini.Activities[key.String()] {
			if err := user(activity.Actor); err != nil {
				return item, err
			}
		}
	case "notifications":
		if err := user(mini.Notifications[0].Actor); err != nil {
			return item, err
		}
	case "teamSettings", "cycleSettings":
		var team domain.Team
		if found, err := n.get(ctx, "teams", key.String(), &team); err != nil {
			return item, err
		} else if found {
			mini.Teams = []domain.Team{team}
			var stateID string
			err := n.stage.db.QueryRowContext(ctx,
				`SELECT id FROM logical_states WHERE (team='' OR team=?) AND (is_default=1 OR is_backlog=1) ORDER BY is_default DESC,ordinal LIMIT 1`,
				team.ID).Scan(&stateID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return item, err
			}
			if err == nil {
				mini.States = []domain.WorkflowState{{ID: stateID, TeamID: team.ID, Default: true}}
			}
		}
	}
	normalizeStoredMetadata(&mini)
	target = reflect.ValueOf(mini).Field(index)
	if isMap {
		return target.MapIndex(key), nil
	}
	result := target.Index(0)
	switch name {
	case "projects":
		if restoreProjectStatus {
			mini.Projects[0].Status = item.Interface().(domain.Project).Status
			result = reflect.ValueOf(mini.Projects[0])
		}
		count, err := n.count(ctx, "projects", mini.Projects[0].ID)
		if err != nil {
			return item, err
		}
		mini.Projects[0].IssueCount = count
		result = reflect.ValueOf(mini.Projects[0])
	case "issues":
		issue := mini.Issues[0]
		original := item.Interface().(domain.Issue)
		if original.ParentID != nil {
			var parent int
			err := n.stage.db.QueryRowContext(ctx, `SELECT ordinal FROM logical_issues WHERE id=? ORDER BY ordinal DESC LIMIT 1`, *original.ParentID).Scan(&parent)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return item, err
			}
			if err == nil && parent != ordinal {
				issue.ParentID = original.ParentID
			}
		}
		var lastOrdinal int
		err := n.stage.db.QueryRowContext(ctx, `SELECT ordinal FROM logical_issues WHERE id=? ORDER BY ordinal DESC LIMIT 1`, issue.ID).Scan(&lastOrdinal)
		if err != nil {
			return item, err
		}
		if lastOrdinal == ordinal {
			last, started := -1, false
			for {
				var child string
				var childOrdinal int
				where := ""
				args := []any{issue.ID, ordinal}
				if started {
					where = " AND ordinal>?"
					args = append(args, last)
				}
				err := n.stage.db.QueryRowContext(ctx,
					`SELECT id,ordinal FROM logical_issues WHERE parent=? AND ordinal<>?`+where+` ORDER BY ordinal LIMIT 1`,
					args...).Scan(&child, &childOrdinal)
				if errors.Is(err, sql.ErrNoRows) {
					break
				}
				if err != nil {
					return item, err
				}
				last, started = childOrdinal, true
				if !slices.Contains(issue.SubIssueIDs, child) {
					issue.SubIssueIDs = append(issue.SubIssueIDs, child)
				}
			}
		}
		for i, label := range issue.Labels {
			var current domain.IssueLabel
			found, err := n.get(ctx, "labels", label.ID, &current)
			if err != nil {
				return item, err
			}
			if found {
				normalized, err := n.record(ctx, "labels", reflect.Value{}, reflect.ValueOf(current), 0)
				if err != nil {
					return item, err
				}
				issue.Labels[i] = normalized.Interface().(domain.IssueLabel)
			}
		}
		result = reflect.ValueOf(issue)
	case "labels":
		count, err := n.count(ctx, "labels", mini.Labels[0].ID)
		if err != nil {
			return item, err
		}
		mini.Labels[0].IssueCount = count
		result = reflect.ValueOf(mini.Labels[0])
	case "projectStatuses":
		if mini.ProjectStatuses[0].Position == 0 && ordinal > 0 {
			mini.ProjectStatuses[0].Position = float64(ordinal)
		}
		result = reflect.ValueOf(mini.ProjectStatuses[0])
	case "releasePipelines":
		pipeline := mini.ReleasePipelines[0]
		original := item.Interface().(domain.ReleasePipeline)
		if original.SlugID == "" {
			base := legacyReleaseSlug(original.Name)
			if base == "" {
				base = fmt.Sprintf("pipeline-%d", ordinal+1)
			}
			candidate := base
			for suffix := 2; ; suffix++ {
				var exists bool
				if found, err := n.get(ctx, "pipelineSlugs", candidate, &exists); err != nil {
					return item, err
				} else if !found {
					break
				}
				candidate = fmt.Sprintf("%s-%d", base, suffix)
			}
			pipeline.SlugID = candidate
		}
		if err := n.put(ctx, "pipelineSlugs", pipeline.SlugID, ordinal, []byte("true")); err != nil {
			return item, err
		}
		result = reflect.ValueOf(pipeline)
	}
	return result, nil
}

func (n *metadataLogicalNormalizer) missing(ctx context.Context, builder *metadataLogicalBuilder, name string) error {
	kind := "users"
	if name == "teamSettings" {
		kind = "teams"
	} else if name != "userSettings" && name != "notificationPreferences" {
		return nil
	}
	last, started := "", false
	for {
		var key string
		var ordinal int
		where := ""
		args := []any{kind}
		if started {
			where = " AND key>?"
			args = append(args, last)
		}
		err := n.stage.db.QueryRowContext(ctx, `SELECT key,ordinal FROM logical_values WHERE kind=?`+where+` ORDER BY key LIMIT 1`, args...).Scan(&key, &ordinal)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		last, started = key, true
		var seen bool
		if found, err := n.get(ctx, "seen:"+name, key, &seen); err != nil {
			return err
		} else if found {
			continue
		}
		operation, err := startMetadataLogicalNormalizeWork(ctx, n.store, n.baseWork, 64<<10)
		if err != nil {
			return err
		}
		previous := n.work
		n.work = operation
		err = func() error {
			defer operation.Close()
			defer func() { n.work = previous }()
			_, field, _ := metadataLogicalField(name)
			value := reflect.Zero(field.Type.Elem())
			switch name {
			case "userSettings":
				value = reflect.ValueOf(defaultUserSettings(key))
			case "notificationPreferences":
				value = reflect.ValueOf(defaultNotificationPreferences(key))
			case "teamSettings":
				var err error
				value, err = n.record(ctx, name, reflect.ValueOf(key), value, ordinal)
				if err != nil {
					return err
				}
			}
			if err := builder.auditJSONLocations(value); err != nil {
				return err
			}
			return builder.Append(reflect.ValueOf(key), value)
		}()
		if err != nil {
			return err
		}
	}
}
