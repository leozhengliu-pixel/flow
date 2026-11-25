package store

// The sync comparison already encoded this immutable node. Its enclosing
// iteration owns the sealed spool through the write and closes it afterwards.
type integrationDeliveryRowSpool struct {
	plan      *legacyMetadataWritePlan
	tx        *sqlTx
	workspace string
	field     string
	id        string
	node      metadataLogicalNode
	spool     *RecordSpool
	size      int64
}

func newIntegrationDeliveryRowSpool(p *legacyMetadataWritePlan, field, id string, node metadataLogicalNode, spool *RecordSpool) *integrationDeliveryRowSpool {
	return &integrationDeliveryRowSpool{
		plan: p, tx: p.tx, workspace: p.workspace, field: field, id: id,
		node: node, spool: spool, size: spool.bytes,
	}
}

func (r *integrationDeliveryRowSpool) borrow(p *legacyMetadataWritePlan, field *legacyMetadataWriteField, row *legacyMetadataWriteRow) *RecordSpool {
	if r == nil || p == nil || field == nil || row == nil || r.plan != p ||
		r.tx != p.tx || r.workspace != p.workspace || r.field != field.field.name ||
		r.id != row.id || r.node != row.next || r.spool == nil || r.spool.file == nil ||
		r.spool.bytes != r.size {
		return nil
	}
	info, err := r.spool.Stat()
	if err != nil || info.Size() != r.size {
		return nil
	}
	return r.spool
}
