package store

// These additive contracts are shared with the journal owner. Serving remains
// disabled until the store implements ReadCommittedChanges with full coverage.
type RecordDelta struct {
	Key     RecordKey
	Version string
	Deleted bool
}

type CommittedChange struct {
	TxID, Seq, AuthzEpoch string
	Records               []RecordDelta
	Invalidations         []string
}

type CommittedPage struct {
	Next, Head, AuthzEpoch  string
	Transactions            []CommittedChange
	HasMore, ResyncRequired bool
}
