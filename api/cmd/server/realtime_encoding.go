package main

import "flow/api/internal/jsonwire"

const (
	syncType    = `{"type":`
	syncID      = `,"documentId":`
	syncState   = `,"contentState":`
	syncUpdates = `,"updates":`
	syncMore    = `,"more":true`
	syncRead    = `,"readOnly":true`
	syncUpdate  = `{"id":`
	syncData    = `,"data":`
)

func collaborationSyncBytes(message collaborationSyncMessage) int64 {
	size := int64(len(syncType)+len(syncID)+len(syncUpdates)+1) +
		jsonwire.StringBytes(message.Type) + jsonwire.StringBytes(message.DocumentID)
	if message.ContentState != "" {
		size += int64(len(syncState)) + jsonwire.StringBytes(message.ContentState)
	}
	if message.Updates == nil {
		size += int64(len("null"))
	} else {
		size += int64(2 + max(0, len(message.Updates)-1))
		for _, update := range message.Updates {
			size += int64(len(syncUpdate)+len(syncData)+1) +
				jsonwire.StringBytes(update.ID) + jsonwire.StringBytes(update.Data)
		}
	}
	if message.More {
		size += int64(len(syncMore))
	}
	if message.ReadOnly {
		size += int64(len(syncRead))
	}
	return size
}

func encodeCollaborationSync(message collaborationSyncMessage, size int64) []byte {
	out := make([]byte, 0, size)
	out = append(out, syncType...)
	out = jsonwire.AppendString(out, message.Type)
	out = append(out, syncID...)
	out = jsonwire.AppendString(out, message.DocumentID)
	if message.ContentState != "" {
		out = append(out, syncState...)
		out = jsonwire.AppendString(out, message.ContentState)
	}
	out = append(out, syncUpdates...)
	if message.Updates == nil {
		out = append(out, "null"...)
	} else {
		out = append(out, '[')
		for i, update := range message.Updates {
			if i > 0 {
				out = append(out, ',')
			}
			out = append(out, syncUpdate...)
			out = jsonwire.AppendString(out, update.ID)
			out = append(out, syncData...)
			out = jsonwire.AppendString(out, update.Data)
			out = append(out, '}')
		}
		out = append(out, ']')
	}
	if message.More {
		out = append(out, syncMore...)
	}
	if message.ReadOnly {
		out = append(out, syncRead...)
	}
	return append(out, '}')
}
