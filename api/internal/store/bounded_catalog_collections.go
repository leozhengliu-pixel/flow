package store

import "context"

// A bounded store accepts a workspace only when every collection holding
// canonical records has a completed catalog checkpoint
// (validateResidentBackfill). A first record can be written after the
// catalog backfill pass, so the journal can open a checkpoint as well as
// maintain the catalog rows.
//
// On a database a bounded store serves, the journal flush first opens such a
// collection complete (phase 2) when every record of it that this
// transaction did not write is already in the catalog. The flush that follows
// catalogs this transaction's keys and applies their omission deltas to the
// new checkpoint, which starts from the catalog's current omission count.
// A collection with other uncatalogued records is left to the backfill.
// A flag-off database never runs it.
const openCatalogCollectionsKeys = `INSERT INTO metadata_catalog_collections(workspace_key,field,last_record_key,started,phase,omissions,digest,revision)
	SELECT d.workspace_key,d.field,'',0,2,(SELECT COUNT(*) FROM metadata_catalog c WHERE c.workspace_key=d.workspace_key AND c.field=d.field AND c.omitted<>0),'',?1
	FROM (SELECT DISTINCT workspace_key,field FROM keys) d
	WHERE NOT EXISTS(SELECT 1 FROM metadata_catalog_collections p WHERE p.workspace_key=d.workspace_key AND p.field=d.field)
	AND EXISTS(SELECT 1 FROM workspace_metadata_records r WHERE r.workspace_key=d.workspace_key AND r.field=d.field)
	AND NOT EXISTS(SELECT 1 FROM workspace_metadata_records r WHERE r.workspace_key=d.workspace_key AND r.field=d.field
		AND NOT EXISTS(SELECT 1 FROM metadata_catalog c WHERE c.workspace_key=r.workspace_key AND c.field=r.field AND c.record_key=r.record_key)
		AND NOT EXISTS(SELECT 1 FROM keys k WHERE k.workspace_key=r.workspace_key AND k.field=r.field AND k.record_key=r.record_key))
	ON CONFLICT(workspace_key,field) DO NOTHING`

// SQLite keeps a small transaction's keys inline in the journal context row.
const openCatalogCollectionsSQLite = `WITH keys AS MATERIALIZED (
		SELECT workspace_key,field,record_key FROM record_journal_dirty WHERE tx_id=?1 AND source='workspace_metadata_records'
		UNION SELECT json_extract(k.value,'$.workspace'),json_extract(k.value,'$.field'),json_extract(k.value,'$.id') FROM record_journal_context i,json_each(i.keys) k
			WHERE i.id=1 AND json_extract(k.value,'$.source')='workspace_metadata_records'
	) ` + openCatalogCollectionsKeys

const openCatalogCollectionsPostgres = `INSERT INTO metadata_catalog_collections(workspace_key,field,last_record_key,started,phase,omissions,digest,revision)
	SELECT d.workspace_key,d.field,'',0,2,(SELECT COUNT(*) FROM metadata_catalog c WHERE c.workspace_key=d.workspace_key AND c.field=d.field AND c.omitted<>0),'',?
	FROM (SELECT DISTINCT workspace_key,field FROM record_journal_dirty WHERE tx_id=? AND source='workspace_metadata_records') d
	WHERE NOT EXISTS(SELECT 1 FROM metadata_catalog_collections p WHERE p.workspace_key=d.workspace_key AND p.field=d.field)
	AND EXISTS(SELECT 1 FROM workspace_metadata_records r WHERE r.workspace_key=d.workspace_key AND r.field=d.field)
	AND NOT EXISTS(SELECT 1 FROM workspace_metadata_records r WHERE r.workspace_key=d.workspace_key AND r.field=d.field
		AND NOT EXISTS(SELECT 1 FROM metadata_catalog c WHERE c.workspace_key=r.workspace_key AND c.field=r.field AND c.record_key=r.record_key)
		AND NOT EXISTS(SELECT 1 FROM record_journal_dirty k WHERE k.tx_id=? AND k.workspace_key=r.workspace_key AND k.field=r.field AND k.record_key=r.record_key))
	ON CONFLICT(workspace_key,field) DO NOTHING`

// SQLite and PostgreSQL can open all new checkpoints in one statement.
func (tx *sqlTx) openNewCatalogCollections(ctx context.Context) error {
	if tx.database == nil || !tx.database.boundedCatalog.Load() || tx.journalToken == "" {
		return nil
	}
	switch tx.dialect {
	case "sqlite":
		observeSQLStatement(ctx, openCatalogCollectionsSQLite)
		_, err := tx.Tx.ExecContext(ctx, openCatalogCollectionsSQLite, tx.journalToken)
		return err
	case "postgres":
		observeSQLStatement(ctx, openCatalogCollectionsPostgres)
		_, err := tx.Tx.ExecContext(ctx, rewriteSQL(openCatalogCollectionsPostgres, tx.dialect), tx.journalToken, tx.journalToken, tx.journalToken)
		return err
	}
	return nil
}

type catalogCollectionKey struct{ workspace, field string }

func (tx *sqlTx) openMySQLCatalogCollection(ctx context.Context, key catalogCollectionKey) (bool, error) {
	// Under REPEATABLE READ, INSERT ... SELECT takes shared next-key locks
	// on source rows, including canonical and dirty keys other writers hold
	// or need. A plain SELECT reads our snapshot and our own writes. Later
	// writers maintain their catalog rows or leave a journal coverage gap.
	var eligible bool
	err := tx.QueryRowContext(ctx, `SELECT
		NOT EXISTS(SELECT 1 FROM metadata_catalog_collections WHERE workspace_key=? AND field=?)
		AND EXISTS(SELECT 1 FROM workspace_metadata_records WHERE workspace_key=? AND field=?)
		AND NOT EXISTS(SELECT 1 FROM workspace_metadata_records r WHERE r.workspace_key=? AND r.field=?
			AND NOT EXISTS(SELECT 1 FROM metadata_catalog c WHERE c.workspace_key=r.workspace_key AND c.field=r.field AND c.record_key=r.record_key)
			AND NOT EXISTS(SELECT 1 FROM record_journal_dirty k WHERE k.tx_id=? AND k.workspace_key=r.workspace_key
				AND k.field=r.field AND k.record_key=r.record_key AND k.source='workspace_metadata_records'))`,
		key.workspace, key.field, key.workspace, key.field, key.workspace, key.field, tx.journalToken).Scan(&eligible)
	if err != nil || !eligible {
		return false, err
	}
	if err := tx.claimMySQLCatalogCollection(ctx, key, 2, tx.journalToken); err != nil {
		return false, err
	}
	var created bool
	err = tx.QueryRowContext(ctx, `SELECT revision=? FROM metadata_catalog_collections WHERE workspace_key=? AND field=? FOR SHARE`,
		tx.journalToken, key.workspace, key.field).Scan(&created)
	return created, err
}

func (tx *sqlTx) claimMySQLCatalogCollection(ctx context.Context, key catalogCollectionKey, phase int, revision string) error {
	// Both callers will write the checkpoint. Take an exclusive primary
	// key lock on a duplicate instead of upgrading INSERT IGNORE's S lock.
	_, err := tx.ExecContext(ctx, `INSERT INTO metadata_catalog_collections(workspace_key,field,last_record_key,started,phase,omissions,digest,revision)
		VALUES(?,?,'',0,?,0,'',?) ON CONFLICT(workspace_key,field) DO UPDATE SET phase=metadata_catalog_collections.phase`,
		key.workspace, key.field, phase, revision)
	return err
}

func (tx *sqlTx) initializeMySQLCatalogOmissions(ctx context.Context, key catalogCollectionKey) error {
	// The checkpoint is ours. Count the current catalog after all summary
	// writes, including rows a legacy replica may have cataloged since
	// our snapshot while the checkpoint was absent. The primary key range
	// can gap-lock the next collection's first insertion, so take it after
	// acquiring every checkpoint and finishing all catalog inserts.
	var omissions int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM metadata_catalog FORCE INDEX (PRIMARY)
		WHERE workspace_key=? AND field=? AND omitted<>0 FOR SHARE`, key.workspace, key.field).Scan(&omissions); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE metadata_catalog_collections SET omissions=? WHERE workspace_key=? AND field=?`,
		omissions, key.workspace, key.field)
	return err
}
