package main

import (
	"context"
	"net/http"
	"os"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func projectRecordMutationsEnabled(kind string) bool {
	flag := "FLOW_PROJECT_RECORD_MUTATIONS"
	if kind == "initiative" {
		flag = "FLOW_INITIATIVE_RECORD_MUTATIONS"
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(flag))) {
	case "1", "true", "on":
		return store.RecordHydrationEnabled()
	}
	return false
}

// These requirements describe callback inputs, including validation and
// reciprocal links. Persistence, authorization and side effects stay in the
// existing mutation adapter. Complete fields are deliberate for cascades
// whose linked keys cannot yet be resolved by the hydration contract.
func projectRecordRequirements(kind, operation, id string, input any) store.MetadataRecordScope {
	scope := store.MetadataRecordScope{Fields: []string{"users"}}
	field := kind + "s"
	if id != "" {
		if len(id) > 191 || strings.ContainsRune(id, '\x00') {
			scope.Fields = append(scope.Fields, field)
		} else {
			scope.Write = []store.RecordKey{{Kind: field, ID: id}}
		}
	}
	fields := func(names ...string) { scope.Fields = append(scope.Fields, names...) }
	read := func(kind string, ids ...string) {
		for _, id := range ids {
			if id != "" {
				if len(id) > 191 || strings.ContainsRune(id, '\x00') {
					fields(kind)
					continue
				}
				key := store.RecordKey{Kind: kind, ID: id}
				if !slices.Contains(scope.Read, key) && !slices.Contains(scope.Write, key) {
					scope.Read = append(scope.Read, key)
				}
			}
		}
	}
	switch operation {
	case "create":
		// Creation allocates the next position from the complete collection.
		fields(field, "teams", "teamSettings", "teamParents", "labels", "labelGroups", "projectStatuses", "workspaceSettings", "settings")
		if kind == "project" {
			fields("projectTemplates", "projectRelations", "documents", "states", "cycles", "cycleSettings", "members", "teamMembers", "userSettings", "notificationPreferences", "pushSubscriptions")
		} else {
			fields("projects", "initiativeRelations", "initiativeUpdates", "favorites")
		}
	case "edit":
		fields("workspaceSettings", "projectRelations")
		if value, ok := input.(domain.ProjectMutationInput); ok {
			if value.StatusID != nil {
				fields("projectStatuses")
			}
			if value.LabelIDs != nil {
				fields("labels", "labelGroups", "teams", "teamSettings", "teamParents")
			}
			if value.TeamIDs != nil {
				fields("teams", "teamSettings", "teamParents", "projects", "documents")
			}
			if value.UpdateSchedule != nil {
				fields("projectUpdates")
			}
			for _, id := range value.DependencyIDs {
				read("projects", strings.TrimSpace(id))
			}
			for _, relation := range value.DependencyRelations {
				read("projects", strings.TrimSpace(relation.ProjectID))
			}
		} else if value, ok := input.(domain.InitiativeMutationInput); ok {
			// Even a rename reconciles links from both sides, including projects
			// linked through PATCH /projects rather than the initiative's list.
			fields("projects")
			if value.ParentInitiativeIDs != nil {
				fields("initiatives", "initiativeRelations")
			}
			if value.LeadTeamID != nil || value.ContributingTeamIDs != nil {
				fields("teams")
			}
			if value.LabelIDs != nil {
				fields("labels")
			}
			if value.Favorite != nil {
				fields("favorites")
			}
		}
	case "delete":
		fields(field, "projects", kind+"Updates", kind+"Relations", "trash", "drafts", "favorites", "subscriptions", "threadSubscriptions")
	case "resource":
		if value, ok := input.(domain.ProjectResourceMutationInput); ok {
			if kind == "initiative" && value.DocumentID != nil {
				// documentByID also accepts historical slugs.
				fields("documents")
			}
			if value.PinnedTeamIDs != nil {
				fields("teams")
			}
		}
	case "comment":
		fields("threadSubscriptions", "teams", "teamSettings", "teamParents")
	case "milestone", "reminder":
	case "update":
		// P15's split update collections require complete-field declarations.
		fields(kind+"Updates", "settings", "notificationPreferences", "pushSubscriptions", "userSettings", "members", "teamMembers")
	case "update-create":
		fields(kind+"Updates", "settings")
		if kind == "initiative" {
			fields("projects", "initiatives", "initiativeRelations")
		}
	case "relation":
		fields(kind + "Relations")
		if kind == "initiative" {
			fields("initiatives") // cycle/depth validation includes all ancestors
		} else if relation, ok := input.(domain.ProjectRelation); ok {
			read("projects", relation.RelatedProjectID)
		}
	}
	// Large validation lists retain the complete-field adapter instead of
	// rejecting a request that the legacy endpoint accepts.
	if len(scope.Read)+len(scope.Write) > store.MetadataRecordBatchMaxKeys {
		for _, key := range scope.Read {
			fields(key.Kind)
		}
		scope.Read = nil
	}
	slices.Sort(scope.Fields)
	scope.Fields = slices.Compact(scope.Fields)
	return scope
}

func projectRecordScope(ctx context.Context, kind, operation, id string, input any, results ...any) context.Context {
	if !projectRecordMutationsEnabled(kind) {
		return ctx
	}
	// Reminders keep the full issue/content path: it refreshes counts used
	// in the legacy realtime entity, even though the callback only appends.
	ctx = store.WithMetadataRecords(ctx, projectRecordRequirements(kind, operation, id, input))
	return store.WithMetadataRecordVerification(ctx, results...)
}

// Route-owned callbacks outside main.go use the same family gate. Their
// uploads and object deletion still happen once, outside the transaction.
func projectRecordHandler(kind, operation string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !projectRecordMutationsEnabled(kind) {
			next(w, r)
			return
		}
		next(w, r.WithContext(projectRecordScope(r.Context(), kind, operation, r.PathValue("id"), nil)))
	}
}
