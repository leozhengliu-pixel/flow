package main

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/publicapi"
	"flow/api/internal/store"
)

const (
	// auditListLimit is how many entries the array form returns (the newest
	// that match).
	auditListLimit = 1000
	// auditScanLimit bounds the entries one request reads from the table when
	// it pages, exports or filters beyond what the database query narrows
	// (type, actorEmail, filter): the newest that match since/until, which
	// reach entries of any age.
	auditScanLimit = 50000
)

// listAuditLog returns the workspace audit log (admins only) so the audit log
// page can refresh without a full reload. Integrations can narrow it with
// type/actorId/actorEmail/resourceType/resourceId/ip/since/until (or a
// `filter` object), page it as a connection with first/after/last/before,
// or download it with format=csv.
func (s *server) listAuditLog(w http.ResponseWriter, r *http.Request) {
	workspaceID, err := s.store.WorkspaceIDForKey(r.Context(), workspaceKey(r))
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	query, refine, err := auditQueryFromRequest(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, err.Error())
		return
	}
	values := r.URL.Query()
	csvExport := strings.EqualFold(values.Get("format"), "csv")
	query.Limit = auditListLimit
	if connectionRequested(r) || csvExport || refine || values.Get("filter") != "" {
		query.Limit = auditScanLimit
	}
	entries, err := s.store.ListAuditEntries(r.Context(), workspaceID, query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the audit log")
		return
	}
	entries = refineAuditEntries(entries, r)
	if connectionRequested(r) {
		writeConnection(w, r, entries, auditEntryFields(), func(entry domain.AuditLogEntry, _ string) pageKey {
			return pageKey{At: entry.CreatedAt, ID: entry.ID}
		}, "createdAt")
		return
	}
	if values.Get("filter") != "" {
		if entries, err = applyResourceFilter(entries, values.Get("filter"), typedFields(auditEntryFields())); err != nil {
			writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, err.Error())
			return
		}
	}
	if csvExport {
		writeAuditCSV(w, entries)
		return
	}
	writeJSON(w, http.StatusOK, entries[:min(len(entries), auditListLimit)])
}

func auditEntryType(entry domain.AuditLogEntry) string {
	if entry.ResourceType == "" {
		return entry.Action
	}
	return entry.ResourceType + "." + entry.Action
}

func auditEntryIP(entry domain.AuditLogEntry) string {
	if entry.IP != "" {
		return entry.IP
	}
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
		"actorType":    func(e domain.AuditLogEntry) any { return e.ActorType },
		"ip":           func(e domain.AuditLogEntry) any { return auditEntryIP(e) },
		"result":       func(e domain.AuditLogEntry) any { return e.Result },
		"createdAt":    func(e domain.AuditLogEntry) any { return e.CreatedAt },
	}
}

// auditQueryFromRequest reads the query parameters the database query
// applies. refine reports parameters applied afterwards (type, actorEmail).
func auditQueryFromRequest(r *http.Request) (store.AuditQuery, bool, error) {
	values := r.URL.Query()
	query := store.AuditQuery{
		ResourceTypes: splitQueryValues(values.Get("resourceType")),
		ActorIDs:      splitQueryValues(values.Get("actorId")),
		ResourceID:    strings.TrimSpace(values.Get("resourceId")),
		IP:            strings.TrimSpace(values.Get("ip")),
	}
	for name, target := range map[string]*time.Time{"since": &query.Since, "until": &query.Until} {
		if raw := strings.TrimSpace(values.Get(name)); raw != "" {
			parsed, ok := parseFilterTime(raw, time.Now())
			if !ok {
				return store.AuditQuery{}, false, errFilterValue(name)
			}
			*target = parsed
		}
	}
	refine := strings.TrimSpace(values.Get("type")) != "" || strings.TrimSpace(values.Get("actorEmail")) != ""
	return query, refine, nil
}

// refineAuditEntries applies the type and actorEmail parameters.
func refineAuditEntries(source []domain.AuditLogEntry, r *http.Request) []domain.AuditLogEntry {
	values := r.URL.Query()
	types := splitQueryValues(values.Get("type"))
	actorEmail := strings.ToLower(strings.TrimSpace(values.Get("actorEmail")))
	if len(types) == 0 && actorEmail == "" {
		return source
	}
	entries := make([]domain.AuditLogEntry, 0, len(source))
	for _, entry := range source {
		if len(types) > 0 && !slices.Contains(types, auditEntryType(entry)) && !slices.Contains(types, entry.Action) ||
			actorEmail != "" && !strings.EqualFold(entry.Actor.Email, actorEmail) {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

// writeAuditCSV writes entries as a CSV download, newest first.
func writeAuditCSV(w http.ResponseWriter, entries []domain.AuditLogEntry) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-log.csv"`)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"id", "seq", "created_at", "actor_type", "actor_id", "actor_name", "actor_email", "action", "resource_type", "resource_id", "ip", "user_agent", "result", "metadata"})
	for _, entry := range entries {
		metadata := ""
		if len(entry.Metadata) > 0 {
			raw, _ := json.Marshal(entry.Metadata)
			metadata = string(raw)
		}
		_ = writer.Write([]string{entry.ID, strconv.FormatInt(entry.Seq, 10), entry.CreatedAt.UTC().Format(time.RFC3339Nano), entry.ActorType, csvCell(entry.ActorID), csvCell(entry.Actor.DisplayName), csvCell(entry.Actor.Email),
			csvCell(entry.Action), csvCell(entry.ResourceType), csvCell(entry.ResourceID), csvCell(auditEntryIP(entry)), csvCell(entry.UserAgent), entry.Result, csvCell(metadata)})
	}
	writer.Flush()
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
	workspaceID, err := s.store.WorkspaceIDForKey(r.Context(), workspaceKey(r))
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	stored, err := s.store.AuditEntryTypeCounts(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the audit log")
		return
	}
	counts := map[string]int{}
	for key, count := range stored {
		counts[auditEntryType(domain.AuditLogEntry{ResourceType: key[0], Action: key[1]})] += count
	}
	result := make([]auditEntryTypeCount, 0, len(counts))
	for name, count := range counts {
		result = append(result, auditEntryTypeCount{Type: name, Count: count})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Type < result[j].Type })
	writeJSON(w, http.StatusOK, result)
}

// resourceAuditHistory is a resource's audit entries, newest first, for
// workspace admins (other viewers get none, as the audit log is admin-only).
func (s *server) resourceAuditHistory(r *http.Request, data domain.Bootstrap, resourceType, resourceID string) []domain.AuditLogEntry {
	if !s.authDisabled && !workspaceAdminRole(data.ViewerRole) {
		return []domain.AuditLogEntry{}
	}
	entries, err := s.store.ListAuditEntries(r.Context(), data.Workspace.ID, store.AuditQuery{ResourceTypes: []string{resourceType}, ResourceID: resourceID, Limit: auditListLimit})
	if err != nil {
		return []domain.AuditLogEntry{}
	}
	return entries
}
