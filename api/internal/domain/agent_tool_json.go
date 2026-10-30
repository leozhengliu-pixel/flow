package domain

import "encoding/json"

// MarshalJSON keeps partial provider arguments out of RawMessage.MarshalJSON.
// The executor retains the original bytes for validation; streaming and saved
// transcripts must remain readable even when a provider stops mid-argument.
func (call AgentToolCall) MarshalJSON() ([]byte, error) {
	type plain AgentToolCall
	if !json.Valid(call.Arguments) {
		call.Arguments = json.RawMessage(`{}`)
	}
	if len(call.Result) > 0 && !json.Valid(call.Result) {
		call.Result, _ = json.Marshal(string(call.Result))
	}
	return json.Marshal(plain(call))
}
