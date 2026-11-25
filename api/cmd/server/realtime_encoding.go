package main

import (
	"encoding/json"
	"unicode/utf8"
)

func collaborationSyncBytes(message collaborationSyncMessage) int64 {
	state := message.ContentState
	message.ContentState = ""
	var updates []collaborationSyncUpdate
	if message.Updates != nil {
		updates = make([]collaborationSyncUpdate, len(message.Updates))
	}
	var size int64
	for i, update := range message.Updates {
		updates[i].ID = update.ID
		size += int64(len(update.Data))
	}
	message.Updates = updates
	raw, _ := json.Marshal(message)
	size += int64(len(raw))
	if state != "" {
		size += int64(len(`,"contentState":`)) + jsonStringBytes(state)
	}
	return size
}

// Matches encoding/json's escaping without allocating a second large state.
func jsonStringBytes(value string) int64 {
	size := int64(2)
	for len(value) > 0 {
		b := value[0]
		if b < utf8.RuneSelf {
			switch b {
			case '"', '\\', '\b', '\f', '\n', '\r', '\t':
				size += 2
			case '<', '>', '&':
				size += 6
			default:
				if b < 0x20 {
					size += 6
				} else {
					size++
				}
			}
			value = value[1:]
			continue
		}
		r, n := utf8.DecodeRuneInString(value)
		if r == utf8.RuneError && n == 1 || r == '\u2028' || r == '\u2029' {
			size += 6
		} else {
			size += int64(n)
		}
		value = value[n:]
	}
	return size
}
