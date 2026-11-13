package main

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Reuse the existing middleware's permission predicates with owned, selected
// metadata. Slug resolution happens against the canonical collection first.
func (s *server) projectRecordAuthorizationMetadata(r *http.Request, workspace, userID string) (domain.Bootstrap, error, bool) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "api" || parts[1] != "projects" && parts[1] != "initiatives" || r.Method == http.MethodGet || r.Method == http.MethodHead || !projectRecordMutationsEnabled(strings.TrimSuffix(parts[1], "s")) {
		return domain.Bootstrap{}, nil, false
	}
	var data domain.Bootstrap
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		data, err = s.store.SelectedDocumentMetadata(r.Context(), workspace, userID, documentRecordCredential(r), func(source domain.Bootstrap) (store.MetadataRecordScope, bool) {
			scope := store.MetadataRecordScope{Fields: []string{"teams", "teamSettings", "teamParents", "workspaceSettings"}}
			project := func(ref string) {
				for index := range source.Projects {
					item := &source.Projects[index]
					if domain.MatchSlugRef(ref, item.ID, item.SlugID, item.PreviousSlugIDs) == domain.SlugNoMatch {
						continue
					}
					key := store.RecordKey{Kind: "projects", ID: item.ID}
					if !slices.Contains(scope.Read, key) {
						scope.Read = append(scope.Read, key)
					}
				}
			}
			initiative := func(ref string) {
				for index := range source.Initiatives {
					item := &source.Initiatives[index]
					if domain.MatchSlugRef(ref, item.ID, item.SlugID, item.PreviousSlugIDs) == domain.SlugNoMatch {
						continue
					}
					key := store.RecordKey{Kind: "initiatives", ID: item.ID}
					if !slices.Contains(scope.Read, key) {
						scope.Read = append(scope.Read, key)
					}
					for _, id := range item.ProjectIDs {
						project(id)
					}
				}
			}
			if len(parts) > 2 {
				if parts[1] == "projects" {
					project(parts[2])
				} else {
					initiative(parts[2])
				}
			}
			if r.Method == http.MethodPost && len(parts) == 2 || r.Method == http.MethodPatch {
				if parts[1] == "projects" {
					var input domain.ProjectMutationInput
					if peekRequestJSON(r, &input) {
						for _, id := range input.Initiatives {
							initiative(id)
						}
						for _, id := range input.Customers {
							scope.Read = append(scope.Read, store.RecordKey{Kind: "customers", ID: id})
						}
						for _, id := range input.DependencyIDs {
							project(id)
						}
						for _, relation := range input.DependencyRelations {
							project(relation.ProjectID)
						}
					}
				} else {
					var input domain.InitiativeMutationInput
					if peekRequestJSON(r, &input) {
						if input.ProjectIDs != nil {
							for _, id := range *input.ProjectIDs {
								project(id)
							}
						}
						if input.ParentInitiativeIDs != nil {
							for _, id := range *input.ParentInitiativeIDs {
								initiative(id)
							}
						}
					}
				}
			}
			if len(parts) >= 4 && parts[3] == "relations" {
				if parts[1] == "projects" {
					var input domain.ProjectRelation
					if r.Method == http.MethodPost && peekRequestJSON(r, &input) {
						project(input.RelatedProjectID)
					}
					for _, relation := range source.ProjectRelations {
						if len(parts) >= 5 && relation.ID == parts[4] {
							scope.Read = append(scope.Read, store.RecordKey{Kind: "projectRelations", ID: relation.ID})
							project(relation.RelatedProjectID)
						}
					}
				} else {
					var input domain.InitiativeRelation
					if r.Method == http.MethodPost && peekRequestJSON(r, &input) {
						initiative(input.RelatedInitiativeID)
					}
					for _, relation := range source.InitiativeRelations {
						if len(parts) >= 5 && relation.ID == parts[4] {
							scope.Read = append(scope.Read, store.RecordKey{Kind: "initiativeRelations", ID: relation.ID})
							initiative(relation.RelatedInitiativeID)
						}
					}
				}
			}
			return scope, len(scope.Read) <= store.MetadataRecordBatchMaxKeys
		})
		if !errors.Is(err, store.ErrResourceAccessChanged) || !errors.Is(err, store.ErrAuthForbidden) {
			break
		}
	}
	if errors.Is(err, store.ErrResourceAccessChanged) && !errors.Is(err, store.ErrAuthForbidden) {
		return domain.Bootstrap{}, nil, false
	}
	return data, err, true
}
