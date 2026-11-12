package store

import (
	"context"
	"fmt"
	"time"
)

const metadataCatalogSchemaVersion = 25

// The sidecar has its own versioned, idempotent startup stage because the
// canonical record table is installed after the base schema migrations.
func (s *SQLiteStore) ensureMetadataCatalog(ctx context.Context) error {
	applied, err := s.migrationApplied(ctx, metadataCatalogSchemaVersion)
	if err != nil || applied {
		return err
	}
	key, blob := "VARCHAR(191)", "BLOB"
	if s.dialect == "mysql" {
		key, blob = "VARBINARY(191)", "LONGBLOB"
	} else if s.dialect == "postgres" {
		key, blob = `VARCHAR(191) COLLATE "C"`, "BYTEA"
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS metadata_catalog(workspace_key ` + key + ` NOT NULL,field ` + key + ` NOT NULL,record_key ` + key + ` NOT NULL,collection_order BIGINT NOT NULL,version VARCHAR(64) NOT NULL,body_bytes BIGINT NOT NULL,summary ` + blob + ` NOT NULL,omitted INTEGER NOT NULL,created_at VARCHAR(40) NOT NULL,updated_at VARCHAR(40) NOT NULL,archived INTEGER NOT NULL,PRIMARY KEY(workspace_key,field,record_key))`,
		`CREATE TABLE IF NOT EXISTS metadata_catalog_relations(workspace_key ` + key + ` NOT NULL,field ` + key + ` NOT NULL,record_key ` + key + ` NOT NULL,relation VARCHAR(64) NOT NULL,value_hash VARCHAR(64) NOT NULL,value TEXT NOT NULL,PRIMARY KEY(workspace_key,field,record_key,relation,value_hash))`,
		`CREATE TABLE IF NOT EXISTS metadata_catalog_collections(workspace_key ` + key + ` NOT NULL,field ` + key + ` NOT NULL,last_record_key ` + key + ` NOT NULL,started INTEGER NOT NULL,phase INTEGER NOT NULL,omissions BIGINT NOT NULL,digest VARCHAR(64) NOT NULL,revision VARCHAR(64) NOT NULL,PRIMARY KEY(workspace_key,field))`,
	}
	for _, index := range []string{
		`metadata_catalog_order_idx ON metadata_catalog(workspace_key,field,collection_order,record_key)`,
		`metadata_catalog_created_idx ON metadata_catalog(workspace_key,field,created_at,record_key)`,
		`metadata_catalog_updated_idx ON metadata_catalog(workspace_key,field,updated_at,record_key)`,
		`metadata_catalog_archived_idx ON metadata_catalog(workspace_key,field,archived,collection_order,record_key)`,
		`metadata_catalog_relation_idx ON metadata_catalog_relations(workspace_key,field,relation,value_hash,record_key)`,
	} {
		prefix := "CREATE INDEX IF NOT EXISTS "
		if s.dialect == "mysql" {
			prefix = "CREATE INDEX "
		}
		statements = append(statements, prefix+index)
	}
	if err := s.applyMigrationStatements(ctx, statements); err != nil {
		return fmt.Errorf("metadata catalog schema: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?)`, metadataCatalogSchemaVersion, "shadow metadata catalog", time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
