package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"flow/api/internal/domain"
)

type legacyIssueFrameWriteBatch struct {
	issues   []domain.Issue
	values   [][]any
	orders   map[string]int
	releases []func()
}

func (b *legacyIssueFrameWriteBatch) close() {
	clear(b.issues)
	clear(b.values)
	b.issues, b.values, b.orders = nil, nil, nil
	for _, release := range b.releases {
		release()
	}
	b.releases = nil
}

// SQL arguments already have SW238 owners. Keep the literal statement shape,
// four-MiB detail split and one index pass over this physical flush's records.
func (b *legacyIssueFrameWriteBatch) write(ctx context.Context, tx *sqlTx, workspace string) error {
	insert := strings.TrimSuffix(issueRecordInsert, ") VALUES ") + ",collection_order) VALUES "
	tuple := strings.TrimSuffix(issueRecordValuesSQL, ")") + ",?)"
	update := issueRecordUpdate + ",collection_order=excluded.collection_order"
	for start := 0; start < len(b.values); {
		end, size := start, 0
		var args []any
		var tuples []string
		for end < len(b.values) && end-start < 100 {
			raw := b.values[end][len(b.values[end])-1].([]byte)
			if end > start && size+len(raw) > 4<<20 {
				break
			}
			args = append(args, b.values[end]...)
			args = append(args, b.orders[b.issues[end].ID])
			tuples = append(tuples, tuple)
			size += len(raw)
			end++
		}
		if _, err := tx.ExecContext(ctx, insert+strings.Join(tuples, ",")+update, args...); err != nil {
			return err
		}
		start = end
	}
	return syncIssueIndexes(ctx, tx, workspace, b.issues)
}

// The existing batch payload SELECT and history SELECTs remain before all row
// processing. An admission flush never captures a new preimage generation.
func (b *legacyRecordBase) persistIssueFrameTargets(ctx context.Context, tx *sqlTx, workspace string, target *metadataLogicalSource) (maxNumber, total int, err error) {
	if err := b.prepareIssueOrder(ctx, target); err != nil {
		return 0, 0, err
	}
	batch := &legacyIssueFrameWriteBatch{orders: map[string]int{}}
	defer batch.close()
	var history *legacyRecordIssueHistory
	defer func() { history.Close() }()
	flush := func() error {
		if history != nil {
			if err := history.flush(ctx, tx); err != nil {
				return err
			}
		}
		if err := batch.write(ctx, tx, workspace); err != nil {
			return err
		}
		batch.close()
		batch.orders = map[string]int{}
		return nil
	}
	retryAdmission := errors.New("retry legacy issue frame admission")
	admissionError := func(err error) error {
		if errors.Is(err, ErrMetadataWorkBudget) {
			return retryAdmission
		}
		return err
	}
	process := func(node metadataLogicalNode) error {
		work := &ResourceRead{store: b.store}
		defer work.Close()
		frames, err := newMetadataLogicalBuilder(ctx)
		if err != nil {
			return err
		}
		defer frames.Close()
		idHeader, err := work.projectNode(ctx, node, "ID", "Number")
		if err != nil {
			return admissionError(err)
		}
		identity := idHeader.Interface().(domain.Issue)
		var position int
		if err := b.stage.db.QueryRowContext(ctx, `SELECT position FROM legacy_issue_positions WHERE id=?`, []byte(identity.ID)).Scan(&position); err != nil {
			return err
		}
		previous, stored, err := b.issuePayload(ctx, identity.ID, work)
		if err != nil {
			return admissionError(err)
		}
		values, columnsRelease, err := legacyIssueRecordFrameValues(ctx, b.store, frames, node, workspace)
		if err != nil {
			return admissionError(err)
		}
		transferred := false
		defer func() {
			if !transferred {
				columnsRelease()
			}
		}()
		raw := values[len(values)-1].([]byte)
		skip := false
		if b.loaded["issue"] {
			fingerprint, loaded, err := b.fingerprint(ctx, "issue", "", identity.ID)
			if err != nil {
				return err
			}
			if loaded && (!stored || sha256.Sum256(previous) != fingerprint.stored) || !loaded && stored {
				if loaded && fingerprint.loaded == sha256.Sum256(raw) {
					skip = true
				} else {
					return fmt.Errorf("issue %s: %w", identity.ID, ErrConcurrentWrite)
				}
			}
		}
		if !skip {
			previousPart := legacySessionJSONPart{input: bytes.NewReader(previous), size: int64(len(previous))}
			same, err := legacyIssueRecordFrameEqual(ctx, b.store, frames, previousPart, node, target)
			if err != nil {
				return admissionError(err)
			}
			if !same {
				var old metadataLogicalNode
				if len(previous) > 0 {
					old, err = legacyIssueCanonicalRecord(ctx, b.store, frames, previousPart)
					if err != nil {
						return admissionError(err)
					}
				}
				// Native index inputs own only the fields used by the unchanged
				// index helpers, preserving Description before SQL NUL stripping.
				native := &ResourceRead{store: b.store}
				defer func() {
					if !transferred {
						native.Close()
					}
				}()
				issue, err := legacyIssueFrameIndexInputs(ctx, native, frames, node)
				if err != nil {
					return admissionError(err)
				}
				var oldStats domain.Issue
				if len(previous) > 0 {
					oldStats, err = legacyIssueFrameStats(ctx, work, frames, old)
					if err != nil {
						return admissionError(err)
					}
				}
				nextStats, err := legacyIssueFrameStats(ctx, work, frames, node)
				if err != nil {
					return admissionError(err)
				}
				if history == nil {
					history, err = b.newIssueHistory(ctx)
					if err != nil {
						return admissionError(err)
					}
				}
				if err := history.appendFrame(ctx, workspace, frames, node, old); err != nil {
					return admissionError(err)
				}
				if len(previous) > 0 {
					if err := b.addStats(ctx, issueStatsOf(oldStats), -1); err != nil {
						return err
					}
				}
				if err := b.addStats(ctx, issueStatsOf(nextStats), 1); err != nil {
					return err
				}
				batch.issues = append(batch.issues, issue)
				batch.values = append(batch.values, values)
				batch.orders[identity.ID] = position
				batch.releases = append(batch.releases, native.Close, columnsRelease)
				transferred = true
			} else {
				_, oldOrder, known, err := b.previous(ctx, "issue", "", identity.ID)
				if err != nil {
					return err
				}
				if !known || oldOrder != position {
					if _, err := tx.ExecContext(ctx, `UPDATE issue_records SET collection_order=? WHERE workspace_key=? AND id=?`, position, workspace, identity.ID); err != nil {
						return err
					}
				}
			}
		}
		if err := b.seen(ctx, "issue", "", identity.ID); err != nil {
			return err
		}
		total++
		maxNumber = max(maxNumber, identity.Number)
		return nil
	}
	for start := 0; ; start += 64 {
		headerWork := &ResourceRead{store: b.store}
		headers, err := b.issueBatch(ctx, target, start, headerWork)
		if err != nil {
			headerWork.Close()
			return 0, 0, err
		}
		if len(headers) == 0 {
			headerWork.Close()
			break
		}
		if err := b.issuePayloadBatch(ctx, tx, workspace, headers); err != nil {
			headerWork.Close()
			return 0, 0, err
		}
		if err := b.issueHistoryPayloadBatch(ctx, tx, workspace, headers); err != nil {
			headerWork.Close()
			return 0, 0, err
		}
		for _, header := range headers {
			for {
				err := process(header.node)
				if err == retryAdmission && len(batch.issues) > 0 {
					if err := flush(); err != nil {
						headerWork.Close()
						return 0, 0, err
					}
					continue
				}
				if err != nil {
					headerWork.Close()
					if err == retryAdmission {
						err = ErrMetadataWorkBudget
					}
					return 0, 0, err
				}
				break
			}
		}
		if err := flush(); err != nil {
			headerWork.Close()
			return 0, 0, err
		}
		history.Close()
		history = nil
		headerWork.Close()
	}
	return maxNumber, total, nil
}

func legacyIssueFrameStats(ctx context.Context, owner *ResourceRead, frames *metadataLogicalBuilder, node metadataLogicalNode) (domain.Issue, error) {
	projected, err := frames.writeAggregateNode(legacyIssueColumnHeaderNode{
		base: metadataAggregateStoredNode{node},
		fields: map[string][]string{
			"Team": {"ID"}, "State": {"ID", "Type"},
			"Project": {"ID"}, "Assignee": {"ID"},
			"Priority": nil, "ArchivedAt": nil,
		},
	}, 0)
	if err != nil {
		return domain.Issue{}, err
	}
	value, err := owner.node(ctx, projected, false)
	if err != nil {
		return domain.Issue{}, err
	}
	return value.Interface().(domain.Issue), nil
}
