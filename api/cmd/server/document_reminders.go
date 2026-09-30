package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// createDocumentReminder backs a document's "Remind me" menu (⇧H). Like the
// issue, project and initiative reminders it is a snoozed inbox notification
// for the viewer that surfaces once remindAt passes.
func (s *server) createDocumentReminder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RemindAt string `json:"remindAt"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	remindAt, err := time.Parse(time.RFC3339, strings.TrimSpace(input.RemindAt))
	if err != nil || !remindAt.After(time.Now().UTC()) {
		writeError(w, http.StatusBadRequest, "remindAt must be a future RFC3339 timestamp")
		return
	}
	remindAt = remindAt.UTC()
	id := r.PathValue("id")
	var reminder domain.Notification
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.reminder_created", id, input, func(data *domain.Bootstrap) error {
		document, documentErr := documentByID(data, id)
		if documentErr != nil {
			return documentErr
		}
		now := time.Now().UTC()
		reminder = domain.Notification{
			ID: fmt.Sprintf("notification_document_reminder_%d", now.UnixNano()), RecipientID: data.Viewer.ID,
			Type: "documentReminder", SourceType: "document", SourceID: document.ID,
			Actor: data.Viewer, Category: "reminders", GroupKey: "document-reminder:" + document.ID + ":" + strconv.FormatInt(remindAt.Unix(), 10),
			OccurrenceCount: 1, LatestActorIDs: []string{data.Viewer.ID}, SnoozedUntil: &remindAt,
			CreatedAt: now, UpdatedAt: now,
		}
		data.Notifications = append([]domain.Notification{reminder}, data.Notifications...)
		return nil
	})
	respondMutation(w, err, http.StatusCreated, reminder)
}
