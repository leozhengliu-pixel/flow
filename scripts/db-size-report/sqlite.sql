-- Flow database size report for SQLite.
--
--   sqlite3 -box data/flow.db < scripts/db-size-report/sqlite.sql
--
-- Read-only. Section 1 needs the dbstat virtual table, which the sqlite3
-- command-line shell includes. Full-text tables appear under their FTS5
-- shadow names (issue_search_fts_data, metadata_search_fts_idx, …).

-- 1. Tables and indexes by allocated pages.
SELECT name,
       COUNT(*) AS pages,
       ROUND(SUM(pgsize) / 1048576.0, 1) AS allocated_mb,
       ROUND(SUM(pgsize - unused) / 1048576.0, 1) AS used_mb
FROM dbstat
GROUP BY name
ORDER BY SUM(pgsize) DESC;

SELECT ROUND(page_count * page_size / 1048576.0, 1) AS database_total_mb,
       ROUND(freelist_count * page_size / 1048576.0, 1) AS free_mb
FROM pragma_page_count(), pragma_page_size(), pragma_freelist_count();

-- 2. Workspace metadata by field (documents, agentSessions, trash, …).
SELECT field,
       COUNT(*) AS records,
       ROUND(SUM(length(CAST(data AS BLOB))) / 1048576.0, 1) AS data_mb,
       MAX(length(CAST(data AS BLOB))) AS largest_bytes
FROM workspace_metadata_records
GROUP BY field
ORDER BY SUM(length(CAST(data AS BLOB))) DESC;

-- 3. Domain events by type, with the time range each type covers.
SELECT event_type,
       COUNT(*) AS events,
       ROUND(SUM(length(CAST(payload AS BLOB)) + COALESCE(length(CAST(previous_values AS BLOB)), 0)) / 1048576.0, 1) AS data_mb,
       MIN(created_at) AS oldest,
       MAX(created_at) AS newest
FROM domain_events
GROUP BY event_type
ORDER BY data_mb DESC;

-- 4. Content kept outside metadata rows (document revision bodies, large
--    agent tool output) when FLOW_STORAGE_DRIVER is not s3.
SELECT substr(blob_key, 1, instr(blob_key, '/') - 1) AS kind,
       COUNT(*) AS bodies,
       ROUND(SUM(length(data)) / 1048576.0, 1) AS stored_mb,
       ROUND(SUM(size) / 1048576.0, 1) AS uncompressed_mb
FROM metadata_blobs
GROUP BY kind;
