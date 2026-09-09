package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A legacy snapshot is still large even without the output buffer. Queue
// concurrent bootstrap requests without materializing their collections.
func serializeLegacyBootstrap(next http.Handler, shared ...chan struct{}) http.Handler {
	gate := make(chan struct{}, 1)
	if len(shared) > 0 {
		gate = shared[0]
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		r = r.WithContext(ctx)
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-r.Context().Done():
			writeError(w, http.StatusServiceUnavailable, "Workspace snapshot is busy; retry later")
			return
		}
		controller := http.NewResponseController(w)
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Minute))
		defer controller.SetWriteDeadline(time.Time{})
		if r.Context().Err() == nil {
			next.ServeHTTP(w, r)
		}
	})
}

// The compatibility client still needs the full snapshot. Encode its large
// collections one entity at a time instead of creating another snapshot-sized
// JSON buffer alongside the decoded workspace.
func writeBootstrapJSON(w http.ResponseWriter, data domain.Bootstrap) {
	writeBootstrapWithFields(w, data, nil)
}

func writeBootstrapWithFields(w http.ResponseWriter, data domain.Bootstrap, stream func(string, *bufio.Writer, *json.Encoder) (bool, error), overrides ...map[string]json.RawMessage) {
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
	if len(overrides) > 0 {
		fields = overrides[0]
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
		if stream != nil {
			handled, err := stream(key, buffer, encoder)
			if err != nil {
				return
			}
			if handled {
				continue
			}
		}
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

func (s *server) writeStoredBootstrap(w http.ResponseWriter, r *http.Request, data domain.Bootstrap, overrides ...map[string]json.RawMessage) {
	visible := map[string]bool{}
	for _, issue := range data.Issues {
		visible[issue.ID] = true
	}
	for _, document := range data.Documents {
		visible[document.ID] = true
	}
	for _, project := range data.Projects {
		visible[project.ID] = true
		for _, milestone := range project.Milestones {
			visible[milestone.ID] = true
		}
	}
	users := map[string]domain.User{}
	for _, user := range data.Users {
		users[user.ID] = user
	}
	writeBootstrapWithFields(w, data, func(field string, buffer *bufio.Writer, encoder *json.Encoder) (bool, error) {
		if field == "issues" {
			buffer.WriteByte('[')
			count := 0
			outlines := map[string]int{}
			for index, issue := range data.Issues {
				outlines[issue.ID] = index
			}
			err := s.store.WalkIssueRecords(r.Context(), store.IssueRecordQuery{Workspace: data.Workspace.URLKey, Archived: "all"}, func(issue domain.Issue) error {
				index, ok := outlines[issue.ID]
				if !ok {
					return nil
				}
				outline := data.Issues[index]
				issue.Team = outline.Team
				issue.State = outline.State
				issue.Creator = outline.Creator
				issue.Assignee = outline.Assignee
				issue.Delegate = outline.Delegate
				issue.Project = outline.Project
				issue.ParentID = outline.ParentID
				issue.SubIssueIDs = outline.SubIssueIDs
				issue.Relations = outline.Relations
				issue.Labels = outline.Labels
				issue.IsSummary = false
				if count > 0 {
					buffer.WriteByte(',')
				}
				if err := encoder.Encode(issue); err != nil {
					return err
				}
				count++
				return nil
			})
			if err != nil {
				return true, err
			}
			buffer.WriteByte(']')
			return true, nil
		}
		kind := ""
		object := false
		switch field {
		case "comments":
			kind = "comment"
			object = true
		case "activities":
			kind = "activity"
			object = true
		case "notifications":
			kind = "notification"
		case "notificationDeliveries":
			kind = "delivery"
		default:
			return false, nil
		}
		if object {
			buffer.WriteByte('{')
		} else {
			buffer.WriteByte('[')
		}
		resourceKey := ""
		haveResource := false
		count := 0
		err := s.store.WalkContentRecords(r.Context(), data.Workspace.URLKey, kind, func(resource string, raw json.RawMessage) error {
			var value any
			switch kind {
			case "comment":
				if !visible[resource] {
					return nil
				}
				var item domain.Comment
				if err := json.Unmarshal(raw, &item); err != nil {
					return err
				}
				if user, ok := users[item.User.ID]; ok {
					item.User = user
				}
				value = item
			case "activity":
				if !visible[resource] {
					return nil
				}
				var item domain.ActivityEvent
				if err := json.Unmarshal(raw, &item); err != nil {
					return err
				}
				if user, ok := users[item.Actor.ID]; ok {
					item.Actor = user
				}
				value = item
			case "notification":
				var item domain.Notification
				if err := json.Unmarshal(raw, &item); err != nil {
					return err
				}
				if !s.authDisabled && item.RecipientID != data.Viewer.ID {
					return nil
				}
				if user, ok := users[item.Actor.ID]; ok {
					item.Actor = user
				}
				value = item
			case "delivery":
				var item domain.NotificationDelivery
				if err := json.Unmarshal(raw, &item); err != nil {
					return err
				}
				if !s.authDisabled && item.RecipientID != data.Viewer.ID {
					return nil
				}
				value = item
			}
			if object && (!haveResource || resource != resourceKey) {
				if haveResource {
					buffer.WriteString("],")
				}
				if err := encoder.Encode(resource); err != nil {
					return err
				}
				buffer.WriteString(":[")
				resourceKey = resource
				haveResource = true
				count = 0
			}
			if count > 0 {
				buffer.WriteByte(',')
			}
			count++
			return encoder.Encode(value)
		})
		if err != nil {
			return true, err
		}
		if object {
			if haveResource {
				buffer.WriteByte(']')
			}
			buffer.WriteByte('}')
		} else {
			buffer.WriteByte(']')
		}
		return true, nil
	}, overrides...)
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
