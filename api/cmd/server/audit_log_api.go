package main

import (
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/publicapi"
)

// listAuditLog returns the workspace audit log (admins only, like the
// bootstrap copy) so the audit log page can refresh without a full reload.
// Integrations can narrow it with type/actorId/actorEmail/resourceType/
// resourceId/ip/since/until (or a `filter` object) and page it
// as a connection with first/after/last/before.
func (s *server) listAuditLog(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	entries, err := filterAuditEntries(data.AuditLog, r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, err.Error())
		return
	}
	if connectionRequested(r) {
		writeConnection(w, r, entries, auditEntryFields(), func(entry domain.AuditLogEntry, _ string) pageKey {
			return pageKey{At: entry.CreatedAt, ID: entry.ID}
		}, "createdAt")
		return
	}
	if r.URL.Query().Get("filter") != "" {
		if entries, err = applyResourceFilter(entries, r.URL.Query().Get("filter"), typedFields(auditEntryFields())); err != nil {
			writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, entries)
}

func auditEntryType(entry domain.AuditLogEntry) string {
	if entry.ResourceType == "" {
		return entry.Action
	}
	return entry.ResourceType + "." + entry.Action
}

func auditEntryIP(entry domain.AuditLogEntry) string {
	for _, key := range []string{"ip", "ipAddress", "clientIp"} {
		if value, ok := entry.Metadata[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func auditEntryFields() map[string]func(domain.AuditLogEntry) any {
	return map[string]func(domain.AuditLogEntry) any{
		"id":           func(e domain.AuditLogEntry) any { return e.ID },
		"type":         func(e domain.AuditLogEntry) any { return auditEntryType(e) },
		"action":       func(e domain.AuditLogEntry) any { return e.Action },
		"resourceType": func(e domain.AuditLogEntry) any { return e.ResourceType },
		"resourceId":   func(e domain.AuditLogEntry) any { return e.ResourceID },
		"actor":        func(e domain.AuditLogEntry) any { return userRelation(&e.Actor) },
		"ip":           func(e domain.AuditLogEntry) any { return auditEntryIP(e) },
		"createdAt":    func(e domain.AuditLogEntry) any { return e.CreatedAt },
	}
}

// filterAuditEntries applies the simple query parameters shared by the array
// and connection forms.
func filterAuditEntries(source []domain.AuditLogEntry, r *http.Request) ([]domain.AuditLogEntry, error) {
	query := r.URL.Query()
	types, actors, resourceTypes := splitQueryValues(query.Get("type")), splitQueryValues(query.Get("actorId")), splitQueryValues(query.Get("resourceType"))
	actorEmail, resourceID, ip := strings.ToLower(strings.TrimSpace(query.Get("actorEmail"))), strings.TrimSpace(query.Get("resourceId")), strings.TrimSpace(query.Get("ip"))
	var since, until time.Time
	for name, target := range map[string]*time.Time{"since": &since, "until": &until} {
		if raw := strings.TrimSpace(query.Get(name)); raw != "" {
			parsed, ok := parseFilterTime(raw, time.Now())
			if !ok {
				return nil, errFilterValue(name)
			}
			*target = parsed
		}
	}
	entries := make([]domain.AuditLogEntry, 0, len(source))
	for _, entry := range source {
		if len(types) > 0 && !slices.Contains(types, auditEntryType(entry)) && !slices.Contains(types, entry.Action) ||
			len(actors) > 0 && !slices.Contains(actors, entry.Actor.ID) ||
			len(resourceTypes) > 0 && !slices.Contains(resourceTypes, entry.ResourceType) ||
			actorEmail != "" && !strings.EqualFold(entry.Actor.Email, actorEmail) ||
			resourceID != "" && entry.ResourceID != resourceID ||
			ip != "" && auditEntryIP(entry) != ip ||
			!since.IsZero() && entry.CreatedAt.Before(since) ||
			!until.IsZero() && !entry.CreatedAt.Before(until) {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

type invalidQueryValue string

func (e invalidQueryValue) Error() string {
	return string(e) + " must be an RFC 3339 timestamp, a YYYY-MM-DD date or an ISO 8601 duration such as -P7D"
}

func errFilterValue(name string) error { return invalidQueryValue(name) }

type auditEntryTypeCount = publicapi.AuditEntryTypeCount

// GET /api/workspace/audit-log/types (the reference app's auditEntryTypes): the entry
// types present in the retained log with their counts.
func (s *server) listAuditEntryTypes(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	counts := map[string]int{}
	for _, entry := range data.AuditLog {
		counts[auditEntryType(entry)]++
	}
	result := make([]auditEntryTypeCount, 0, len(counts))
	for name, count := range counts {
		result = append(result, auditEntryTypeCount{Type: name, Count: count})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Type < result[j].Type })
	writeJSON(w, http.StatusOK, result)
}
