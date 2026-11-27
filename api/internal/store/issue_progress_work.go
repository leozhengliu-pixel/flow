package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// progressProjects follows issueProgressChanges using only a project ID and
// the presence of a history point. The first occurrence wins for inline
// collections with duplicate IDs.
func (r *issueHotInputs) progressProjects(ctx context.Context, before, after []domain.Issue, settings map[string]domain.TeamSettings) ([]string, error) {
	// Candidate slices copy issue headers. Include their capacity growth,
	// the contribution slices used for comparison, and each project's entry.
	perIssue := uint64(reflect.TypeFor[domain.Issue]().Size())*3 + 512
	var count uint64
	for _, issues := range [][]domain.Issue{before, after} {
		for index := range issues {
			if project := issues[index].Project; project != nil && project.ID != "" {
				count++
			}
		}
	}
	if count == 0 {
		return nil, ctx.Err()
	}
	if count > (uint64(metadataWorkBytes())-4096)/perIssue {
		return nil, ErrMetadataWorkBudget
	}
	if err := r.reserve(ctx, count*perIssue+4096); err != nil {
		return nil, err
	}
	changes := issueProgressCandidates(before, after)
	index, _, _ := metadataLogicalField("projects")
	var projects []string
	err := r.source.fields[index].Children(ctx, func(_ reflect.Value, project metadataLogicalNode) error {
		id, err := issueHotField(ctx, project, "ID")
		if err != nil {
			return err
		}
		for key, change := range changes {
			match, err := id.StringEqual(ctx, key)
			if err != nil {
				return err
			}
			if !match {
				continue
			}
			if change.contributionsEqual(settings) {
				history, err := issueHotField(ctx, project, "ProgressHistory")
				if err != nil {
					return err
				}
				err = history.Children(ctx, func(reflect.Value, metadataLogicalNode) error { return errMetadataAggregateStop })
				if errors.Is(err, errMetadataAggregateStop) {
					delete(changes, key)
					break
				}
				if err != nil {
					return err
				}
			}
			projects = append(projects, key)
			delete(changes, key)
			break
		}
		if len(changes) == 0 {
			return errMetadataAggregateStop
		}
		return nil
	})
	if err != nil && !errors.Is(err, errMetadataAggregateStop) {
		return nil, err
	}
	slices.Sort(projects)
	return projects, ctx.Err()
}

type issueProgressWorkKey struct{}

type issueProgressWork struct {
	inputs       *issueHotInputs
	states       map[string]domain.WorkflowState
	seen         map[string]bool
	prepareTeams func(context.Context, []progressIssue) error
	weeklyPrefix map[string]bool
}

func issueProgressWorkFrom(ctx context.Context) *issueProgressWork {
	work, _ := ctx.Value(issueProgressWorkKey{}).(*issueProgressWork)
	return work
}

func (w *issueProgressWork) bytes(column string) string {
	if w.inputs.store.dialect == "sqlite" {
		return "LENGTH(CAST(" + column + " AS BLOB))"
	}
	return "OCTET_LENGTH(" + column + ")"
}

// The aggregate runs inside the mutation transaction before any result strings
// or bodies are copied by the driver. Admission includes retained headers,
// decoded transition maps, their indexes and transient JSON buffers.
func (w *issueProgressWork) admitRows(ctx context.Context, reader progressReader, from string, args []any, columns ...string) error {
	var count, bytes int64
	var lengths []string
	for _, column := range columns {
		lengths = append(lengths, "COALESCE("+w.bytes(column)+",0)")
	}
	err := reader.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(`+strings.Join(lengths, "+")+`),0) `+from, args...).Scan(&count, &bytes)
	if err != nil {
		return err
	}
	if count < 0 || bytes < 0 || uint64(count) > uint64(metadataWorkBytes())/2048 ||
		uint64(bytes) > (uint64(metadataWorkBytes())-uint64(count)*2048)/8 {
		return ErrMetadataWorkBudget
	}
	return w.inputs.reserve(ctx, uint64(count)*2048+uint64(bytes)*8+4096)
}

func (w *issueProgressWork) admitIssues(ctx context.Context, reader progressReader, workspace, project string) error {
	return w.admitRows(ctx, reader,
		`FROM issue_records i LEFT JOIN issue_attribute_records a ON a.workspace_key=i.workspace_key AND a.issue_id=i.id AND a.field='estimate' WHERE i.workspace_key=? AND i.project_id=?`,
		[]any{workspace, project}, "i.id", "i.created_at", "i.state_id", "i.state_type", "i.team_id", "a.value")
}

func (w *issueProgressWork) admitActivities(ctx context.Context, reader progressReader, workspace, clause string, args []any) error {
	return w.admitRows(ctx, reader,
		`FROM workspace_content_records WHERE workspace_key=? AND kind='activity' AND `+clause,
		append([]any{workspace}, args...), "resource_id", "data")
}

// Progress resolves state IDs globally, with the last duplicate winning. The
// callback's team workflow selection has a different first-match contract.
func (w *issueProgressWork) loadStates(ctx context.Context, ids []string) error {
	needed := map[string]bool{}
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	for _, id := range ids {
		if w.seen[id] {
			continue
		}
		if err := w.inputs.addKey(ctx, needed, id); err != nil {
			return err
		}
		if err := w.inputs.addKey(ctx, w.seen, id); err != nil {
			return err
		}
	}
	if len(needed) == 0 {
		return nil
	}
	index, _, _ := metadataLogicalField("states")
	return w.inputs.source.fields[index].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		wanted, err := issueHotMatches(ctx, record, "ID", needed)
		if err != nil || !wanted {
			return err
		}
		names := []string{"ID", "Type"}
		cost, err := issueHotProjectionCost(ctx, record, names)
		if err != nil {
			return err
		}
		if err := w.inputs.reserve(ctx, cost); err != nil {
			return err
		}
		value, err := issueHotProjection(ctx, record, names, cost)
		if err != nil {
			return err
		}
		state := value.Interface().(domain.WorkflowState)
		w.states[state.ID] = state
		return nil
	})
}

func (w *issueProgressWork) currentStates(ctx context.Context, issues []progressIssue) error {
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		ids = append(ids, issue.state.ID)
	}
	if err := w.loadStates(ctx, ids); err != nil {
		return err
	}
	for index := range issues {
		if state, ok := w.states[issues[index].state.ID]; ok {
			issues[index].state = state
		}
	}
	if w.prepareTeams != nil {
		return w.prepareTeams(ctx, issues)
	}
	return nil
}

func (w *issueProgressWork) historicalStates(ctx context.Context, activities map[string][]domain.ActivityEvent) error {
	var ids []string
	for _, events := range activities {
		for _, event := range events {
			if id := strings.TrimSpace(event.Metadata["stateBeforeId"]); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return w.loadStates(ctx, ids)
}

func (w *issueProgressWork) admitGrid(ctx context.Context, start, now time.Time) error {
	end := utcDay(now)
	if end.Before(start) {
		end = start
	}
	days := end.Unix()/86400 - utcDay(start).Unix()/86400
	if days < 0 {
		return ErrMetadataWorkBudget
	}
	points := uint64(days)/7 + 2
	pointBytes := uint64(reflect.TypeFor[domain.ProjectProgressHistoryPoint]().Size())
	// Five output series, previous/current copies and growth while rebuilding.
	perPoint := pointBytes*5*4 + uint64(reflect.TypeFor[time.Time]().Size())*4
	if points > (uint64(metadataWorkBytes())-4096)/perPoint {
		return ErrMetadataWorkBudget
	}
	return w.inputs.reserve(ctx, points*perPoint+4096)
}
