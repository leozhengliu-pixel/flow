-- Flow database size report for MySQL 8.
--
--   mysql --table -u <user> -p <database> < scripts/db-size-report/mysql.sql
--
-- Read-only. The account needs PROCESS (for information_schema.INNODB_*);
-- without it the first query reports 0 for file and full-text sizes.
-- Run ANALYZE TABLE first if you want fresh data_mb/index_mb estimates.

-- 1. Tables by allocated size. file_mb is the table's own tablespace;
--    fulltext_mb adds the InnoDB full-text auxiliary tablespaces
--    (fts_<table id>_*) that an ngram FULLTEXT index keeps beside the table.
SELECT t.TABLE_NAME AS table_name,
       t.TABLE_ROWS AS approx_rows,
       ROUND(t.DATA_LENGTH / 1048576, 1) AS data_mb,
       ROUND(t.INDEX_LENGTH / 1048576, 1) AS index_mb,
       ROUND(COALESCE(ts.ALLOCATED_SIZE, 0) / 1048576, 1) AS file_mb,
       ROUND(COALESCE(fts.allocated, 0) / 1048576, 1) AS fulltext_mb,
       ROUND((COALESCE(ts.ALLOCATED_SIZE, 0) + COALESCE(fts.allocated, 0)) / 1048576, 1) AS total_mb
FROM information_schema.TABLES t
LEFT JOIN information_schema.INNODB_TABLESPACES ts
       ON ts.NAME = CONCAT(t.TABLE_SCHEMA, '/', t.TABLE_NAME)
LEFT JOIN (
    SELECT it.NAME AS name, SUM(aux.ALLOCATED_SIZE) AS allocated
    FROM information_schema.INNODB_TABLES it
    JOIN information_schema.INNODB_TABLESPACES aux
      ON aux.NAME LIKE CONCAT(SUBSTRING_INDEX(it.NAME, '/', 1), '/fts\_', LPAD(LOWER(HEX(it.TABLE_ID)), 16, '0'), '\_%')
    GROUP BY it.NAME
) fts ON fts.name = CONCAT(t.TABLE_SCHEMA, '/', t.TABLE_NAME)
WHERE t.TABLE_SCHEMA = DATABASE() AND t.TABLE_TYPE = 'BASE TABLE'
ORDER BY total_mb DESC, data_mb + index_mb DESC;

SELECT ROUND(SUM(ALLOCATED_SIZE) / 1048576, 1) AS database_total_mb
FROM information_schema.INNODB_TABLESPACES
WHERE NAME LIKE CONCAT(DATABASE(), '/%');

-- 2. Workspace metadata by field (documents, agentSessions, trash, …).
SELECT field,
       COUNT(*) AS records,
       ROUND(SUM(LENGTH(data)) / 1048576, 1) AS data_mb,
       MAX(LENGTH(data)) AS largest_bytes
FROM workspace_metadata_records
GROUP BY field
ORDER BY SUM(LENGTH(data)) DESC;

-- 3. Domain events by type, with the time range each type covers.
SELECT event_type,
       COUNT(*) AS events,
       ROUND(SUM(LENGTH(payload) + COALESCE(LENGTH(previous_values), 0)) / 1048576, 1) AS data_mb,
       MIN(created_at) AS oldest,
       MAX(created_at) AS newest
FROM domain_events
GROUP BY event_type
ORDER BY data_mb DESC;

-- 4. Content kept outside metadata rows (document revision bodies, large
--    agent tool output) when FLOW_STORAGE_DRIVER is not s3.
SELECT SUBSTRING_INDEX(blob_key, '/', 1) AS kind,
       COUNT(*) AS bodies,
       ROUND(SUM(LENGTH(data)) / 1048576, 1) AS stored_mb,
       ROUND(SUM(size) / 1048576, 1) AS uncompressed_mb
FROM metadata_blobs
GROUP BY kind;
