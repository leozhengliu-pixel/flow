package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"slices"

	"flow/api/internal/domain"
)

// A legacy snapshot is still large even without the output buffer. Queue
// concurrent bootstrap requests without materializing their collections.
func serializeLegacyBootstrap(next http.Handler) http.Handler {
	gate := make(chan struct{}, 1)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-r.Context().Done():
			return
		}
		if r.Context().Err() == nil {
			next.ServeHTTP(w, r)
		}
	})
}

// The compatibility client still needs the full snapshot. Encode its large
// collections one entity at a time instead of creating another snapshot-sized
// JSON buffer alongside the decoded workspace.
func writeBootstrapJSON(w http.ResponseWriter, data domain.Bootstrap) {
	metadata := data
	metadata.Issues, metadata.Comments, metadata.Activities = nil, nil, nil
	metadata.Notifications, metadata.NotificationDeliveries = nil, nil
	raw, err := json.Marshal(metadata)
	if err != nil {
		writeError(w, 500, "Could not encode workspace")
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		writeError(w, 500, "Could not encode workspace")
		return
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	buffer := bufio.NewWriterSize(w, 64<<10)
	encoder := json.NewEncoder(buffer)
	buffer.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			buffer.WriteByte(',')
		}
		if err := encoder.Encode(key); err != nil {
			return
		}
		buffer.WriteByte(':')
		switch key {
		case "issues":
			err = encodeBootstrapArray(buffer, encoder, data.Issues)
		case "comments":
			err = encodeBootstrapMap(buffer, encoder, data.Comments)
		case "activities":
			err = encodeBootstrapMap(buffer, encoder, data.Activities)
		case "notifications":
			err = encodeBootstrapArray(buffer, encoder, data.Notifications)
		case "notificationDeliveries":
			err = encodeBootstrapArray(buffer, encoder, data.NotificationDeliveries)
		default:
			_, err = buffer.Write(fields[key])
		}
		if err != nil {
			return
		}
	}
	buffer.WriteString("}\n")
	_ = buffer.Flush()
}

func encodeBootstrapArray[T any](w io.Writer, encoder *json.Encoder, items []T) error {
	if items == nil {
		_, err := io.WriteString(w, "null")
		return err
	}
	if _, err := io.WriteString(w, "["); err != nil {
		return err
	}
	for i := range items {
		if i > 0 {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if err := encoder.Encode(&items[i]); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "]")
	return err
}

func encodeBootstrapMap[T any](w io.Writer, encoder *json.Encoder, items map[string][]T) error {
	if items == nil {
		_, err := io.WriteString(w, "null")
		return err
	}
	if _, err := io.WriteString(w, "{"); err != nil {
		return err
	}
	i := 0
	for key, values := range items {
		if i > 0 {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if err := encoder.Encode(key); err != nil {
			return err
		}
		if _, err := io.WriteString(w, ":"); err != nil {
			return err
		}
		if err := encodeBootstrapArray(w, encoder, values); err != nil {
			return err
		}
		i++
	}
	_, err := io.WriteString(w, "}")
	return err
}
