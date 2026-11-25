package domain

import "context"

// CollaborationWork supplies the write's live allocation owner to the editor
// validator. It exists only during a callback and never enters stored metadata.
type CollaborationWork struct {
	Context context.Context
	Admit   func(bytes uint64) (release func(), err error)
}
