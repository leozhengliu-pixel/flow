package store

import "context"

// A bounded store accepts a workspace only when every collection holding
// canonical records has a completed catalog checkpoint
// (validateResidentBackfill). Only the catalog backfill creates checkpoints;
// the journal flush maintains the rows of collections that already have one
// (record_journal.go, journal_flush.go). A collection whose first
// record is written after the backfill pass (the first workflow run, API key,
// favorite, ...) therefore had catalog rows but no checkpoint, and every
// later reload of its workspace (a COLD write's publication, a peer change,
// a restart) was refused.
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

const openCatalogCollectionsExternal = `INSERT INTO metadata_catalog_collections(workspace_key,field,last_record_key,started,phase,omissions,digest,revision)
	SELECT d.workspace_key,d.field,'',0,2,(SELECT COUNT(*) FROM metadata_catalog c WHERE c.workspace_key=d.workspace_key AND c.field=d.field AND c.omitted<>0),'',?
	FROM (SELECT DISTINCT workspace_key,field FROM record_journal_dirty WHERE tx_id=? AND source='workspace_metadata_records') d
	WHERE NOT EXISTS(SELECT 1 FROM metadata_catalog_collections p WHERE p.workspace_key=d.workspace_key AND p.field=d.field)
	AND EXISTS(SELECT 1 FROM workspace_metadata_records r WHERE r.workspace_key=d.workspace_key AND r.field=d.field)
	AND NOT EXISTS(SELECT 1 FROM workspace_metadata_records r WHERE r.workspace_key=d.workspace_key AND r.field=d.field
		AND NOT EXISTS(SELECT 1 FROM metadata_catalog c WHERE c.workspace_key=r.workspace_key AND c.field=r.field AND c.record_key=r.record_key)
		AND NOT EXISTS(SELECT 1 FROM record_journal_dirty k WHERE k.tx_id=? AND k.workspace_key=r.workspace_key AND k.field=r.field AND k.record_key=r.record_key))
	ON CONFLICT(workspace_key,field) DO NOTHING`

// openNewCatalogCollections runs just before the journal flush catalogs this
// transaction's keys, inside the flush's savepoint when it has one.
func (tx *sqlTx) openNewCatalogCollections(ctx context.Context) error {
	if tx.database == nil || !tx.database.boundedCatalog.Load() || tx.journalToken == "" {
		return nil
	}
	if tx.dialect == "sqlite" {
		observeSQLStatement(ctx, openCatalogCollectionsSQLite)
		_, err := tx.Tx.ExecContext(ctx, openCatalogCollectionsSQLite, tx.journalToken)
		return err
	}
	observeSQLStatement(ctx, openCatalogCollectionsExternal)
	_, err := tx.Tx.ExecContext(ctx, rewriteSQL(openCatalogCollectionsExternal, tx.dialect), tx.journalToken, tx.journalToken, tx.journalToken)
	return err
}
