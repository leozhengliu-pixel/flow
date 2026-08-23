package main

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func (s *server) createCycleResource(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Type       string `json:"type"`
		Title      string `json:"title"`
		URL        string `json:"url"`
		DocumentID string `json:"documentId"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Type, input.Title, input.URL, input.DocumentID = strings.TrimSpace(input.Type), strings.TrimSpace(input.Title), strings.TrimSpace(input.URL), strings.TrimSpace(input.DocumentID)
	if !slices.Contains([]string{"link", "document"}, input.Type) {
		writeError(w, http.StatusBadRequest, "resource type must be link or document")
		return
	}
	if input.Type == "link" {
		parsed, err := url.ParseRequestURI(input.URL)
		if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
			writeError(w, http.StatusBadRequest, "a valid link URL is required")
			return
		}
		if input.Title == "" {
			input.Title = parsed.Host
		}
	}
	id := r.PathValue("id")
	var resource domain.CycleResource
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "cycle.resource_created", id, input, func(data *domain.Bootstrap) error {
		cycle, err := cycleByID(data, id)
		if err != nil {
			return err
		}
		if input.Type == "document" {
			document, err := documentByID(data, input.DocumentID)
			if err != nil {
				return errInvalid
			}
			input.Title, input.URL = document.Title, documentPathForResource(data.Workspace.URLKey, document.SlugID)
		}
		now := time.Now().UTC()
		resource = domain.CycleResource{ID: fmt.Sprintf("cycle_resource_%d", now.UnixNano()), Type: input.Type, Title: input.Title, URL: input.URL, DocumentID: input.DocumentID, CreatedAt: now}
		cycle.Resources = append(cycle.Resources, resource)
		cycle.UpdatedAt = now
		return nil
	})
	respondMutation(w, err, http.StatusCreated, resource)
}

func (s *server) deleteCycleResource(w http.ResponseWriter, r *http.Request) {
	id, resourceID := r.PathValue("id"), r.PathValue("resourceId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "cycle.resource_deleted", id, map[string]string{"resourceId": resourceID}, func(data *domain.Bootstrap) error {
		cycle, err := cycleByID(data, id)
		if err != nil {
			return err
		}
		before := len(cycle.Resources)
		cycle.Resources = slices.DeleteFunc(cycle.Resources, func(item domain.CycleResource) bool { return item.ID == resourceID })
		if before == len(cycle.Resources) {
			return errNotFound
		}
		cycle.UpdatedAt = time.Now().UTC()
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) cycleCalendarToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var feedURL string
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "cycle.calendar_token_requested", id, nil, func(data *domain.Bootstrap) error {
		cycle, err := cycleByID(data, id)
		if err != nil {
			return err
		}
		if cycle.CalendarToken == "" {
			cycle.CalendarToken, err = randomSecret("flow_cycle_")
			if err != nil {
				return err
			}
		}
		feedURL = fmt.Sprintf("/api/calendar/cycles/%s.ics?token=%s", url.PathEscape(cycle.ID), url.QueryEscape(cycle.CalendarToken))
		return nil
	})
	respondMutation(w, err, http.StatusOK, map[string]string{"url": feedURL})
}

func (s *server) cycleCalendar(w http.ResponseWriter, r *http.Request) {
	id, token := strings.TrimSuffix(r.PathValue("id"), ".ics"), r.URL.Query().Get("token")
	cycle, found := s.store.CycleForCalendar(id, token)
	if !found {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="cycle-%d.ics"`, cycle.Number))
	fmt.Fprintf(w, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Flow//Cycles//EN\r\nBEGIN:VEVENT\r\nUID:%s@flow\r\nDTSTART;VALUE=DATE:%s\r\nDTEND;VALUE=DATE:%s\r\nSUMMARY:%s\r\nDESCRIPTION:%s\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n", escapeICS(cycle.ID), cycle.StartsAt.UTC().Format("20060102"), cycle.EndsAt.AddDate(0, 0, 1).UTC().Format("20060102"), escapeICS(cycle.Name), escapeICS(cycle.Description))
}

func escapeICS(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, ";", "\\;")
	value = strings.ReplaceAll(value, ",", "\\,")
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r", ""), "\n", "\\n")
}

func documentPathForResource(workspace, slug string) string {
	return "/" + url.PathEscape(workspace) + "/document/" + url.PathEscape(slug)
}
