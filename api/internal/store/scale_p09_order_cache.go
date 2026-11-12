package store

type recordJournalOrder struct {
	workspace, field, id string
	order                int64
	valid                bool
}

func (tx *sqlTx) recordJournalOrder(workspace, field, id string) (int64, bool) {
	order := tx.journalOrder
	return order.order, order.valid && order.workspace == workspace && order.field == field && order.id == id
}
