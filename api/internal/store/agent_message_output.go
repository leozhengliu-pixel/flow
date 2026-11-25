package store

import "context"

// TransferOutput keeps the selected native message alive through the enclosing
// operation's use of the returned value.
func (r *AgentMessageRead) TransferOutput(ctx context.Context) error {
	if r == nil || !registerMetadataOutputClose(ctx, r.Close) {
		if r != nil {
			r.Close()
		}
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	return nil
}
