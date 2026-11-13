package store

// One inline metadata record and its event need no union, ordering or grouping.
// The same commit trigger installs the bundle; every other transaction keeps
// the generic bounded plan, including spilled keys and deletions.
// Explicit join order also bounds databases without planner statistics.
// The key limit leaves room for worst-case JSON escaping in the delta budget.
const recordJournalPointCommitSQL = `INSERT INTO record_journal_commit_v3(tx_id,data) ` + recordJournalPointCommitData

const recordJournalPointCommitData = `WITH point AS MATERIALIZED (
		SELECT w.workspace_key,w.workspace_id,m.field,m.record_key,v.version,
			json_extract(i.events,'$[0].id') event_id,
			modes.generation||':'||COALESCE(a.epoch,0) epoch,
			jsonb(CAST(flow_rj_project(CAST(m.data AS BLOB),m.field,m.record_key,m.collection_order,COALESCE(c.omitted,0),CAST(c.summary AS BLOB),c.collection_order) AS TEXT)) projection
		FROM record_journal_context i
		CROSS JOIN workspace_metadata_records m
		CROSS JOIN workspace_states w
		CROSS JOIN record_versions v
		CROSS JOIN record_journal_mode modes
		LEFT JOIN metadata_catalog c ON c.workspace_key=m.workspace_key AND c.field=m.field AND c.record_key=m.record_key
		LEFT JOIN workspace_authz_epochs a ON a.workspace_id=w.workspace_id
		WHERE i.id=1
			AND m.workspace_key=json_extract(i.keys,'$[0].workspace') AND m.field=json_extract(i.keys,'$[0].field') AND m.record_key=json_extract(i.keys,'$[0].id')
			AND w.workspace_key=m.workspace_key AND w.workspace_id=json_extract(i.events,'$[0].workspaceId')
			AND v.workspace_key=m.workspace_key AND v.field=m.field AND v.record_key=m.record_key AND v.deleted=0
			AND modes.id=1 AND modes.enabled=1
			AND json_array_length(i.keys)=1 AND json_array_length(i.events)=1
			AND json_extract(i.keys,'$[0].source')='workspace_metadata_records'
			AND m.field<>'@authz'
			AND LENGTH(CAST(m.field AS BLOB))+LENGTH(CAST(m.record_key AS BLOB))<=4096
			AND LENGTH(CAST(m.data AS BLOB))<=8192
			AND LENGTH(CAST(json_extract(i.events,'$[0].id') AS BLOB))<=60
			AND NOT EXISTS(SELECT 1 FROM record_journal_dirty WHERE tx_id=i.tx_id)
			AND NOT EXISTS(SELECT 1 FROM record_journal_event_pending WHERE tx_id=i.tx_id)
	)
	SELECT ?1 AS tx_id,CASE WHEN EXISTS(SELECT 1 FROM point) THEN (
		SELECT jsonb_object('Created',?2,'CacheRevisions',1,'QueuedRevisions',json(?3),
			'Workspaces',json_array(json_object('Key',workspace_key,'ID',workspace_id,
				'Catalog',json_array(json(projection)),'Events',json_array(event_id),
				'Change',json_object('txId','rje:'||event_id,'seq','','authzEpoch',epoch,
					'records',json_array(json_object('key',json_object('Kind',field,'ID',record_key),'version',CAST(version AS TEXT),'deleted',json('false'))),
					'invalidations',json('[]')))))
		FROM point WHERE flow_rj_small(json_extract(projection,'$.Cost')>262144)=1
	) ELSE (SELECT data FROM (` + recordJournalSQLCommitData + `)) END AS data`
