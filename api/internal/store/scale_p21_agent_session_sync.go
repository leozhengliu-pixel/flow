package store

import (
	"context"
	"database/sql"
	"io"
)

func (p *legacyMetadataWritePlan) agentSessionCanonicalSpool(ctx context.Context, owner *ResourceRead, key string, fields ...string) (*RecordSpool, error) {
	kind := "agentSessions"
	if len(fields) != 0 {
		kind = fields[0]
	}
	var size int64
	if err := p.tx.QueryRowContext(ctx, "SELECT "+owner.byteLength("data")+" FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?",
		p.workspace, kind, key).Scan(&size); err != nil {
		return nil, err
	}
	if size < 0 || size > metadataLogicalDiskBytes {
		return nil, ErrMetadataWorkBudget
	}
	spool, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return nil, err
	}
	reader := &canonicalMetadataReader{ctx: ctx, tx: p.tx, workspace: p.workspace, key: RecordKey{kind, key}, size: size}
	if count, err := io.CopyBuffer(spool, reader, make([]byte, metadataRecordChunkBytes)); err != nil || count != size {
		spool.Close()
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return spool, nil
}

func legacySessionStoredMatches(ctx context.Context, owner *ResourceRead, tx *sqlTx, workspace string, previous legacySessionJSONPart, next legacySessionJSONPart, fields ...string) (bool, error) {
	oldFields, err := legacySessionJSONObject(ctx, owner, previous)
	if err != nil {
		return false, err
	}
	newFields, err := legacySessionJSONObject(ctx, owner, next)
	if err != nil {
		return false, err
	}
	if same, err := legacySessionObjectsEqual(ctx, owner, oldFields, newFields); err != nil || same {
		return same, err
	}
	if len(fields) != 0 && fields[0] != "agentSessions" {
		return false, nil
	}
	marker, err := legacySessionHasMarker(ctx, previous)
	if err != nil || !marker || tx.database == nil || tx.database.content == nil {
		return false, err
	}
	stored, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return false, err
	}
	defer stored.Close()
	if err := legacySessionStoredForm(ctx, owner, tx, workspace, next, stored, false); err != nil {
		return false, err
	}
	form, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(stored))
	if err != nil {
		return false, err
	}
	return legacySessionObjectsEqual(ctx, owner, oldFields, form)
}

// Stream applyMetadataRecords' exact known/wanted/previous decisions. The
// complete native field provides order and IDs; one changed row supplies live
// JSON/offload ownership. Unrelated canonical body rows are not decoded.
func (p *legacyMetadataWritePlan) syncAgentSessionLifecycleCollection(ctx context.Context, owner *ResourceRead, field *legacyMetadataWriteField, known, wanted legacySessionSplitImage) error {
	query := "SELECT " + owner.byteLength("record_key") + ",CASE WHEN " + owner.byteLength("record_key") + "<=? THEN record_key ELSE NULL END,collection_order" +
		" FROM workspace_metadata_records WHERE workspace_key=? AND field=?"
	rows, err := p.tx.QueryContext(ctx, query, MetadataRecordPageMaxBytes, p.workspace, field.field.name)
	if err != nil {
		return err
	}
	previous := map[string]int{}
	for rows.Next() {
		var size int64
		var id sql.NullString
		var order int
		if err := rows.Scan(&size, &id, &order); err != nil {
			rows.Close()
			return err
		}
		if size < 0 || size > MetadataRecordPageMaxBytes || !id.Valid {
			rows.Close()
			return ErrMetadataWorkBudget
		}
		if err := owner.reserve(ctx, uint64(len(id.String))*8+512); err != nil {
			rows.Close()
			return err
		}
		previous[id.String] = order
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	orders := issueCollectionOrder(wanted.items, previous)
	for _, item := range wanted.items {
		id := item.ID
		order := orders[id]
		previousOrder, exists := previous[id]
		delete(previous, id)
		oldNode, wasKnown := known.nodes[id]
		if !exists && wasKnown {
			// The canonical row was deleted after this native snapshot.
			continue
		}
		node := wanted.nodes[id]
		if err := func() error {
			var encoded *integrationDeliveryRowSpool
			defer func() {
				if encoded != nil {
					encoded.spool.Close()
				}
			}()
			if exists {
				same, err := func() (bool, error) {
					work := &ResourceRead{store: p.store}
					defer work.Close()
					var old *RecordSpool
					var err error
					if wasKnown {
						old, err = legacySessionNodeSpool(ctx, work, oldNode)
					} else {
						old, err = p.agentSessionCanonicalSpool(ctx, work, id, field.field.name)
					}
					if err != nil {
						return false, err
					}
					defer old.Close()
					next, err := legacySessionNodeSpool(ctx, work, node)
					if err != nil {
						return false, err
					}
					if integrationDeliveryStreamEvent(ctx) != "" &&
						(field.field.name == "integrationDeliveries" || field.field.name == "integrationConnections") {
						encoded = newIntegrationDeliveryRowSpool(p, field.field.name, id, node, next)
					} else {
						defer next.Close()
					}
					return legacySessionStoredMatches(ctx, work, p.tx, p.workspace, legacySessionSpoolPart(old), legacySessionSpoolPart(next), field.field.name)
				}()
				if err != nil {
					return err
				}
				if same {
					if previousOrder != order {
						if _, err := p.tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET collection_order=? WHERE workspace_key=? AND field=? AND record_key=?`,
							order, p.workspace, field.field.name, id); err != nil {
							return err
						}
						p.tx.metadataWritten = true
					}
					return nil
				}
			}
			// The closed callback did not create sessions. A newly split row is
			// an existing native occurrence, admitted and encoded one at a time.
			row := legacyMetadataWriteRow{id: id, before: node, next: node, changed: true}
			write := p.writeAgentSessionLifecycleNativeRow
			switch field.field.name {
			case "workflowRuns":
				write = p.writeWorkflowRunStreamNativeRow
			case "integrationDeliveries", "integrationConnections":
				if encoded != nil {
					_, err := p.writeIntegrationDeliveryNativeRowWithSpool(ctx, field, &row, order, true, encoded)
					return err
				}
				write = p.writeIntegrationDeliveryNativeRow
			}
			_, err := write(ctx, field, &row, order, true)
			return err
		}(); err != nil {
			return err
		}
	}
	for id := range previous {
		if _, wasKnown := known.nodes[id]; !wasKnown {
			// The ordinary slow tail preserves rows absent from known.
			continue
		}
		if err := deleteMetadataRecordRow(ctx, p.tx, p.workspace, metadataRecordKey{field.field.name, id}); err != nil {
			return err
		}
	}
	return nil
}
