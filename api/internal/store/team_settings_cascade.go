package store

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sort"

	"flow/api/internal/domain"
)

// A team settings write (team.settings_updated) changes more than the team's
// own settings: "apply to sub-teams" rewrites descendants' settings, a default
// status clears the flag on the team's other statuses and, the first time
// the team owns statuses, remaps its issue templates and issues; inheriting
// cycles completes, renumbers and deletes the subtree's cycles and clears
// deleted cycles from its issues; a new parent remaps the subtree's issues to
// the inherited statuses; private access removes outsiders from the team's
// issues. The team metadata writer snapshots what those cascades can touch
// (the team and its sub-teams, retired ones included), writes every record
// they changed in the write's transaction and restores the copy when the
// write fails.

type teamIssueCascadeKey struct{}

// WithTeamIssueCascade declares the teams whose issues a team.settings_updated
// callback may rewrite. resolve runs on the locked metadata before the
// callback; the writer loads those teams' issue records into the callback's
// data and writes back the ones it changed in the same transaction.
func WithTeamIssueCascade(ctx context.Context, resolve func(*domain.Bootstrap) []string) context.Context {
	return context.WithValue(ctx, teamIssueCascadeKey{}, resolve)
}

func teamIssueCascadeFromContext(ctx context.Context) func(*domain.Bootstrap) []string {
	resolve, _ := ctx.Value(teamIssueCascadeKey{}).(func(*domain.Bootstrap) []string)
	return resolve
}

// teamIssueCascade holds the issue records a team settings callback may
// rewrite, loaded (and locked) in the transaction that writes them back.
type teamIssueCascade struct {
	tx      *sqlTx
	base    map[string][]byte
	issues  []domain.Issue
	written []string
}

// loadTeamIssueCascade opens the write's transaction and loads the teams'
// issue records in collection order, their references resolved against data
// as the full path presents them. It returns nil when the teams have none.
func (s *SQLiteStore) loadTeamIssueCascade(ctx context.Context, workspace string, data *domain.Bootstrap, teams []string) (*teamIssueCascade, []domain.Issue, error) {
	teams = slices.DeleteFunc(slices.Clone(teams), func(id string) bool { return id == "" })
	if len(teams) == 0 {
		return nil, nil, nil
	}
	tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
	if err != nil {
		return nil, nil, err
	}
	ids := []string{}
	for start := 0; start < len(teams); start += 500 {
		clause, args := bindList("team_id", teams[start:min(start+500, len(teams))])
		rows, err := tx.QueryContext(ctx, `SELECT id FROM issue_records WHERE workspace_key=? AND `+clause, append([]any{workspace}, args...)...)
		if err != nil {
			_ = tx.Rollback()
			return nil, nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				_ = tx.Rollback()
				return nil, nil, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			_ = tx.Rollback()
			return nil, nil, err
		}
	}
	if len(ids) == 0 {
		return nil, nil, tx.Rollback()
	}
	records, err := s.loadIssueRecordsByID(ctx, tx, workspace, ids, true)
	if err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	order := sortedKeys(records)
	sort.SliceStable(order, func(i, j int) bool { return records[order[i]].order < records[order[j]].order })
	refs := newIssueReferences(*data)
	cascade := &teamIssueCascade{tx: tx, base: make(map[string][]byte, len(order))}
	issues := make([]domain.Issue, 0, len(order))
	for _, id := range order {
		issues = append(issues, refs.resolve(records[id].issue))
		cascade.base[id] = records[id].raw
	}
	return cascade, issues, nil
}

// teamSubtreeIncludingRetired lists root and every team below it, retired
// teams included ("apply to sub-teams" reaches them). The team directory
// leaves retired teams out of TeamChildren: with any retired team the
// hierarchy is read from the teams' settings.
func teamSubtreeIncludingRetired(data *domain.Bootstrap, root string) []string {
	domain.EnsureTeamDirectory(data)
	children := data.TeamChildren
	if slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.RetiredAt != nil }) {
		children = make(map[string][]string, len(data.Teams))
		for _, team := range data.Teams {
			parent := data.TeamSettings[team.ID].ParentTeamID
			children[parent] = append(children[parent], team.ID)
		}
	}
	result := []string{root}
	seen := map[string]bool{root: true}
	queue := append([]string(nil), children[root]...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
		queue = append(queue, children[id]...)
	}
	return result
}

// snapshotTeamSettingsCascade records what a settings write's cascades can
// change in place: the sub-teams' settings and the scope's statuses, issue
// templates and cycles (by position, with the cycle collection as loaded).
func snapshotTeamSettingsCascade(snap *teamMutationSnapshot, data *domain.Bootstrap, scopes []string) {
	inScope := make(map[string]bool, len(scopes))
	for _, id := range scopes {
		inScope[id] = true
	}
	snap.scopeIDs = scopes
	snap.scopeSettings = map[string]domain.TeamSettings{}
	for _, id := range scopes[1:] {
		if settings, ok := data.TeamSettings[id]; ok {
			snap.scopeSettings[id] = cloneMutationTeamSettings(settings)
		}
	}
	snap.oldStates = map[int]domain.WorkflowState{}
	for index, state := range data.States {
		if inScope[state.TeamID] {
			snap.oldStates[index] = state
		}
	}
	snap.oldTemplates = map[int]domain.IssueTemplate{}
	for index := range data.IssueTemplates {
		if inScope[data.IssueTemplates[index].TeamID] {
			snap.oldTemplates[index] = cloneSnapshotValue(reflect.ValueOf(data.IssueTemplates[index])).Interface().(domain.IssueTemplate)
		}
	}
	snap.oldCycles = map[int]domain.Cycle{}
	for index := range data.Cycles {
		if inScope[data.Cycles[index].TeamID] {
			snap.oldCycles[index] = cloneSnapshotValue(reflect.ValueOf(data.Cycles[index])).Interface().(domain.Cycle)
		}
	}
	snap.cycleBacking = data.Cycles
}

// rollbackTeamSettingsCascade restores what snapshotTeamSettingsCascade
// recorded. The cycle collection is rebuilt in its loaded backing array: a
// deletion moved the cycles after it, so the copy that shares the array sees
// its cycles again.
func rollbackTeamSettingsCascade(next *domain.Bootstrap, snap *teamMutationSnapshot) {
	if snap.scopeIDs == nil {
		return
	}
	if next.TeamSettings != nil {
		for _, id := range snap.scopeIDs[1:] {
			if old, ok := snap.scopeSettings[id]; ok {
				next.TeamSettings[id] = old
			} else {
				delete(next.TeamSettings, id)
			}
		}
	}
	for index, old := range snap.oldStates {
		if index < len(next.States) {
			next.States[index] = old
		}
	}
	for index, old := range snap.oldTemplates {
		if index < len(next.IssueTemplates) {
			next.IssueTemplates[index] = old
		}
	}
	if len(snap.cycleBacking) > 0 {
		inScope := make(map[string]bool, len(snap.scopeIDs))
		for _, id := range snap.scopeIDs {
			inScope[id] = true
		}
		restored := make([]domain.Cycle, len(snap.cycleBacking))
		other := 0
		for index := range restored {
			if old, ok := snap.oldCycles[index]; ok {
				restored[index] = old
				continue
			}
			for other < len(next.Cycles) && inScope[next.Cycles[other].TeamID] {
				other++
			}
			if other < len(next.Cycles) {
				restored[index] = next.Cycles[other]
				other++
			}
		}
		copy(snap.cycleBacking, restored)
	}
	next.Cycles = snap.cycleBacking
	next.Issues = nil
}

// collectTeamSettingsCascade adds the records a settings write's cascades
// changed: sub-teams' settings, changed statuses and issue templates, and
// the scope's cycles by id (changed, added in collection order, deleted).
func collectTeamSettingsCascade(aggregateID string, snap *teamMutationSnapshot, next domain.Bootstrap, appendRecord func(field, key string, base, value any) error, drop func(field, key string)) error {
	for _, id := range snap.scopeIDs[1:] {
		if id == aggregateID {
			continue
		}
		settings, ok := next.TeamSettings[id]
		if !ok {
			continue
		}
		old, existed := snap.scopeSettings[id]
		if existed && metadataTeamSettingsEqual(old, settings) {
			continue
		}
		var base any
		if existed {
			base = old
		}
		if err := appendRecord("teamSettings", id, base, settings); err != nil {
			return err
		}
	}
	for _, index := range sortedIndexes(snap.oldStates) {
		old := snap.oldStates[index]
		if index < len(next.States) && next.States[index].ID == old.ID && next.States[index] != old {
			if err := appendRecord("states", old.ID, old, next.States[index]); err != nil {
				return err
			}
		}
	}
	for _, index := range sortedIndexes(snap.oldTemplates) {
		old := snap.oldTemplates[index]
		if index >= len(next.IssueTemplates) || next.IssueTemplates[index].ID != old.ID {
			continue
		}
		if changed, err := recordChanged(old, next.IssueTemplates[index]); err != nil {
			return err
		} else if changed {
			if err := appendRecord("issueTemplates", old.ID, old, next.IssueTemplates[index]); err != nil {
				return err
			}
		}
	}
	inScope := make(map[string]bool, len(snap.scopeIDs))
	for _, id := range snap.scopeIDs {
		inScope[id] = true
	}
	previous := make(map[string]domain.Cycle, len(snap.oldCycles))
	for _, old := range snap.oldCycles {
		previous[old.ID] = old
	}
	present := make(map[string]bool, len(snap.oldCycles))
	unmoved := len(next.Cycles) == snap.cyclesLen
	for index, old := range snap.oldCycles {
		if index >= len(next.Cycles) || next.Cycles[index].ID != old.ID {
			unmoved = false
			break
		}
	}
	if unmoved {
		// Nothing was added or deleted: only the scope's cycles can differ.
		for _, index := range sortedIndexes(snap.oldCycles) {
			old := snap.oldCycles[index]
			if changed, err := recordChanged(old, next.Cycles[index]); err != nil {
				return err
			} else if changed {
				if err := appendRecord("cycles", old.ID, old, next.Cycles[index]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, cycle := range next.Cycles {
		if old, ok := previous[cycle.ID]; ok {
			present[cycle.ID] = true
			if changed, err := recordChanged(old, cycle); err != nil {
				return err
			} else if changed {
				if err := appendRecord("cycles", cycle.ID, old, cycle); err != nil {
					return err
				}
			}
			continue
		}
		if inScope[cycle.TeamID] {
			if err := appendRecord("cycles", cycle.ID, nil, cycle); err != nil {
				return err
			}
		}
	}
	for _, index := range sortedIndexes(snap.oldCycles) {
		if id := snap.oldCycles[index].ID; !present[id] {
			drop("cycles", id)
		}
	}
	return nil
}

func recordChanged(old, current any) (bool, error) {
	before, err := json.Marshal(old)
	if err != nil {
		return false, err
	}
	after, err := json.Marshal(current)
	if err != nil {
		return false, err
	}
	return !bytes.Equal(before, after), nil
}

func sortedIndexes[T any](values map[int]T) []int {
	result := make([]int, 0, len(values))
	for index := range values {
		result = append(result, index)
	}
	slices.Sort(result)
	return result
}
