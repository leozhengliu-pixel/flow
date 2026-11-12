package store

// Existing MySQL tables can retain an older default collation than additive
// sidecars. IDs and transaction tokens compare by bytes across that boundary.
func recordJournalIdentitySQL(dialect, expression string) string {
	if dialect == "mysql" {
		return "CAST(" + expression + " AS BINARY)"
	}
	return expression
}

func recordJournalIdentity(reader metadataRowReader, expression string) string {
	switch db := reader.(type) {
	case *sqlDatabase:
		return recordJournalIdentitySQL(db.dialect, expression)
	case *sqlTx:
		return recordJournalIdentitySQL(db.dialect, expression)
	}
	return expression
}

func recordJournalMissingSequenceSQL(dialect string) string {
	return `NOT EXISTS (SELECT 1 FROM domain_event_sequencing q WHERE q.event_id=` + recordJournalIdentitySQL(dialect, "j.id") + ` AND q.workspace_id=` + recordJournalIdentitySQL(dialect, "j.workspace_id") + `)`
}
