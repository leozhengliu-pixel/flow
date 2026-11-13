package main

import (
	"encoding/json"
	"net/http"
	"strconv"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) scaleP19Manifest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("surface") != "projects-page" && r.URL.Query().Get("surface") != "initiatives-page" {
		writeSyncError(w, store.ErrMetadataRecordQuery)
		return
	}
	_, ready := s.syncV2Journal()
	if !ready || !syncFlagEnabled("FLOW_INDEXED_READS") {
		writeError(w, http.StatusNotImplemented, "Query windows are not ready")
		return
	}
	head, err := s.store.WorkspaceChangeHead(r.Context(), workspaceKey(r))
	if err != nil {
		writeSyncError(w, err)
		return
	}
	access, ok := s.syncAccess(w, r)
	if !ok {
		return
	}
	journalHead, err := s.store.RecordJournalHead(r.Context(), access.WorkspaceID)
	if err != nil || journalHead == "" {
		writeError(w, http.StatusNotImplemented, "Query windows are not ready")
		return
	}
	if _, err := s.store.OpenScaleP19Cursor(r.Context(), access, "projects", "manifest", ""); err != nil {
		writeError(w, http.StatusNotImplemented, "Query windows are not ready")
		return
	}
	snapshot, ok := s.scaleP19Metadata(w, r)
	if !ok {
		return
	}
	data := snapshot.Data
	if s.authDisabled {
		data.TeamMembers, err = s.store.ListTeamMembers(r.Context(), data.Workspace.ID)
		if err != nil {
			writeSyncError(w, err)
			return
		}
	}
	// Keep navigation and chooser summaries in core, with explicit incomplete
	// collection markers. Project memberships and initiative aggregates stay on
	// the query endpoint, never in the manifest.
	initiatives := make([]map[string]any, 0, len(data.Initiatives))
	for _, item := range data.Initiatives {
		initiatives = append(initiatives, map[string]any{"id": item.ID, "slugId": item.SlugID, "name": item.Name, "icon": item.Icon, "color": item.Color, "status": item.Status, "parentInitiativeIds": item.ParentInitiativeIDs})
	}
	labels, groups, templates, customers, statuses, tiers := data.Labels, data.LabelGroups, data.ProjectTemplates, data.Customers, data.CustomerStatuses, data.CustomerTiers
	teamSettings, savedViews := data.TeamSettings, data.SavedViews
	store.ProjectListBootstrapProjection(&data)
	data.Labels, data.LabelGroups, data.ProjectTemplates = labels, groups, templates
	data.Customers, data.CustomerStatuses, data.CustomerTiers = customers, statuses, tiers
	data.TeamSettings, data.SavedViews = teamSettings, savedViews
	data.Initiatives = []domain.Initiative{}
	core := map[string]any{}
	// Reuse the bootstrap's exact JSON field names, including nil versus empty.
	raw, err := json.Marshal(data)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	if err = json.Unmarshal(raw, &core); err != nil {
		writeSyncError(w, err)
		return
	}
	core["initiatives"] = initiatives
	for _, field := range []string{"users", "teams", "labels", "labelGroups", "projectStatuses", "projectTemplates", "projectRelations", "savedViews", "favorites", "favoriteFolders", "subscriptions", "agentSkills", "invitations", "customers", "customerStatuses", "customerTiers", "customerRequests"} {
		if core[field] == nil {
			core[field] = []any{}
		}
	}
	core["resourceDetailsOmitted"] = true
	collections := map[string]map[string]any{}
	for _, kind := range []string{"users", "teams", "labels", "savedViews", "favorites"} {
		collections[kind] = map[string]any{"revision": strconv.FormatInt(head, 10), "complete": true}
	}
	for _, kind := range []string{"projects", "initiatives"} {
		collections[kind] = map[string]any{"revision": strconv.FormatInt(head, 10), "complete": false}
	}
	if err := s.store.ValidateAccess(r.Context(), access); err != nil {
		writeSyncError(w, err)
		return
	}
	result := map[string]any{"schema": 2, "workspaceId": access.WorkspaceID, "viewerId": access.PrincipalID, "authzEpoch": access.AuthzEpoch, "head": journalHead, "capabilities": []string{"sync-v2", scaleP19Capability}, "core": core, "collections": collections}
	encoded, _ := json.Marshal(result)
	if len(encoded)+1 > store.MetadataRecordPageMaxBytes {
		writeError(w, http.StatusNotImplemented, "Query window core exceeds its budget")
		return
	}
	writeSyncJSON(w, result, store.MetadataRecordPageMaxBytes)
}
