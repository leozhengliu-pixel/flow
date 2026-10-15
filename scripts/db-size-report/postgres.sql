-- Flow database size report for PostgreSQL.
--
--   psql "$FLOW_DATABASE_URL" -f scripts/db-size-report/postgres.sql
--
-- Read-only. table_mb includes TOAST (large JSON values); index_mb includes
-- the pg_trgm GIN indexes used for search.

-- 1. Tables by total size.
SELECT c.relname AS table_name,
       c.reltuples::bigint AS approx_rows,
       ROUND(pg_table_size(c.oid) / 1048576.0, 1) AS table_mb,
       ROUND(pg_indexes_size(c.oid) / 1048576.0, 1) AS index_mb,
       ROUND(pg_total_relation_size(c.oid) / 1048576.0, 1) AS total_mb
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'r' AND n.nspname = current_schema()
ORDER BY pg_total_relation_size(c.oid) DESC;

SELECT ROUND(pg_database_size(current_database()) / 1048576.0, 1) AS database_total_mb;

-- 2. Workspace metadata by field (documents, agentSessions, trash, …).
SELECT field,
       COUNT(*) AS records,
       ROUND(SUM(octet_length(data)) / 1048576.0, 1) AS data_mb,
       MAX(octet_length(data)) AS largest_bytes
FROM workspace_metadata_records
GROUP BY field
ORDER BY SUM(octet_length(data)) DESC;

-- 3. Domain events by type, with the time range each type covers.
SELECT event_type,
       COUNT(*) AS events,
       ROUND(SUM(octet_length(payload) + COALESCE(octet_length(previous_values), 0)) / 1048576.0, 1) AS data_mb,
       MIN(created_at) AS oldest,
       MAX(created_at) AS newest
FROM domain_events
GROUP BY event_type
ORDER BY data_mb DESC;

-- 4. Content kept outside metadata rows (document revision bodies, large
--    agent tool output) when FLOW_STORAGE_DRIVER is not s3.
SELECT split_part(blob_key, '/', 1) AS kind,
       COUNT(*) AS bodies,
       ROUND(SUM(octet_length(data)) / 1048576.0, 1) AS stored_mb,
       ROUND(SUM(size) / 1048576.0, 1) AS uncompressed_mb
FROM metadata_blobs
GROUP BY kind;
