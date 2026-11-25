package store

import (
	"context"
	"fmt"
)

// Keep off-mode sequencing identical to the existing PostgreSQL statement.
func (tx *sqlTx) sequenceLegacyPostgresBatch(ctx context.Context, workspaceID string) (int, bool, error) {
	var consumed, assigned, expected int
	err := tx.QueryRowContext(ctx, `WITH queued AS MATERIALIZED (
		SELECT order_id,event_id FROM domain_event_sequencing WHERE workspace_id=? ORDER BY order_id LIMIT ?
	), pending AS MATERIALIZED (
		SELECT e.id,row_number() OVER (ORDER BY q.order_id) AS position
		FROM queued q JOIN domain_events e ON e.id=q.event_id WHERE e.seq IS NULL
	), advanced AS (
		INSERT INTO workspace_event_seq(workspace_id,head,pruned)
		SELECT ?,COUNT(*),0 FROM pending HAVING COUNT(*)>0
		ON CONFLICT(workspace_id) DO UPDATE SET head=workspace_event_seq.head+excluded.head
		RETURNING head
	), numbered AS (
		UPDATE domain_events e SET seq=h.head-(SELECT COUNT(*) FROM pending)+p.position
		FROM pending p CROSS JOIN advanced h WHERE e.id=p.id AND e.seq IS NULL RETURNING e.id
	), retired AS (
		DELETE FROM domain_event_sequencing WHERE order_id IN (SELECT order_id FROM queued) RETURNING order_id
	)
	SELECT (SELECT COUNT(*) FROM retired),(SELECT COUNT(*) FROM numbered),(SELECT COUNT(*) FROM pending)`, workspaceID, sequencingBatch, workspaceID).Scan(&consumed, &assigned, &expected)
	if err != nil {
		return 0, false, err
	}
	if assigned != expected {
		return 0, false, fmt.Errorf("numbered %d of %d change log events", assigned, expected)
	}
	return consumed, false, tx.Commit()
}
