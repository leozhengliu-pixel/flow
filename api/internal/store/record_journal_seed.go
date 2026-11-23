package store

import (
	"context"
	"strconv"
	"strings"
)

const journalActivationSeed = 1
const journalSeedPage = 2048

// On first activation there are no versions from an earlier epoch. Live
// writes after trigger installation already set the current version and
// tombstone. Fill only missing versions and let the catalog pass build
// summaries, avoiding per-record journal work during the initial walk.
func (tx *sqlTx) seedRecordJournalVersions(ctx context.Context, workspace, field, key string) (bool, error) {
	keys := `field,record_key`
	switch tx.dialect {
	case "mysql":
		keys = `CAST(field AS BINARY) AS field,CAST(record_key AS BINARY) AS record_key`
	case "postgres":
		keys = `field COLLATE "C" AS field,record_key COLLATE "C" AS record_key`
	}
	query := `WITH canonical AS (SELECT ` + keys + ` FROM (` + tx.recordJournalCanonicalKeys() + `) c)
		SELECT field,record_key FROM canonical WHERE field>? OR (field=? AND record_key>?)
		ORDER BY field,record_key LIMIT ` + strconv.Itoa(journalSeedPage)
	rows, err := tx.QueryContext(ctx, query, workspace, workspace, workspace, workspace, field, field, key)
	if err != nil {
		return true, err
	}
	args := make([]any, 0, journalSeedPage*3)
	cost, budget := int64(0), tx.recordJournalRepairBudget()
	more := false
	for rows.Next() {
		var nextField, nextKey string
		if err := rows.Scan(&nextField, &nextKey); err != nil {
			rows.Close()
			return true, err
		}
		cost += int64(len(workspace) + len(nextField) + len(nextKey) + 128)
		if len(args) != 0 && cost > budget {
			more = true
			break
		}
		field, key = nextField, nextKey
		args = append(args, workspace, field, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return true, err
	}
	if count := len(args) / 3; count > 0 {
		values := strings.TrimSuffix(strings.Repeat("(?,?,?,1,0),", count), ",")
		if _, err := tx.ExecContext(ctx, `INSERT INTO record_versions(workspace_key,field,record_key,version,deleted) VALUES `+values+`
			ON CONFLICT(workspace_key,field,record_key) DO NOTHING`, args...); err != nil {
			return true, err
		}
		more = more || count == journalSeedPage
	}
	if more {
		_, err = tx.ExecContext(ctx, `UPDATE record_journal_coverage SET last_field=?,last_key=? WHERE workspace_key=?`, field, key, workspace)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM record_journal_coverage WHERE workspace_key=?`, workspace)
	}
	return more, err
}
