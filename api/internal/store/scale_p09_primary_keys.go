package store

func recordJournalPrimaryKey(table string) string {
	switch table {
	case "record_journal_dirty":
		return "tx_id,workspace_key,field,record_key"
	case "record_versions":
		return "workspace_key,field,record_key"
	case "record_journal_coverage":
		return "workspace_key"
	case "workspace_authz_epochs", "workspace_record_journal_heads":
		return "workspace_id"
	case "record_journal":
		return "id"
	case "record_journal_events", "record_journal_event_pending":
		return "event_id"
	default:
		return ""
	}
}
