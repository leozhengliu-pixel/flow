package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// This certifies the production authorizer, not restore/purge publication.
// The separate actual-handler test remains strict until that writer is ready.
func TestScaleP21SW319TrashAuthorizationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	t.Setenv("FLOW_TRASH_RECORDS", "off")
	for driver, repository := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP21SW215AuthMatrixSeed(t, repository)
			workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw319-")
			data, ok := repository.WorkspaceMetadata(workspace)
			if !ok {
				t.Fatal("missing owned workspace")
			}
			private, child, memberID, inheritedID := "", "", "", ""
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
			}
			for teamID, settings := range data.TeamSettings {
				if settings.ParentTeamID == private {
					child = teamID
				}
			}
			ownedUsers := map[string]bool{}
			for _, principal := range f.principals {
				ownedUsers[principal.user.ID] = true
				if principal.name == "member-private-outsider" {
					memberID = principal.user.ID
				}
				if principal.name == "sub-team-inheritance" {
					inheritedID = principal.user.ID
				}
			}
			if private == "" || child == "" || memberID == "" || inheritedID == "" {
				t.Fatal("missing private/sub-team/member M witnesses")
			}
			if err := repository.SetTeamMembership(t.Context(), data.Workspace.ID, f.public, memberID, "owner", true); err != nil {
				t.Fatal(err)
			}
			if err := repository.SetTeamMembership(t.Context(), data.Workspace.ID, private, inheritedID, "owner", true); err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 11, 17, 1, 2, 3, 0, time.UTC)
			totalPairs, partialDenials, featureDenials, inheritedAllows, purgeAllows := 0, 0, 0, 0, 0
			for _, row := range []struct {
				name, kind string
				teams      []string
				disabled   bool
				invalid    bool
				missing    bool
				duplicate  bool
			}{
				{name: "partial-owner", kind: "release_pipeline", teams: []string{f.public, private}},
				{name: "all-owned", kind: "release_pipeline", teams: []string{f.public}},
				{name: "inherited-owner", kind: "release_pipeline", teams: []string{child}},
				{name: "pipeline-disabled", kind: "release_pipeline", teams: []string{f.public, private}, disabled: true},
				{name: "release-disabled", kind: "release", disabled: true},
				{name: "release-enabled", kind: "release"},
				{name: "missing-disabled", kind: "release_pipeline", disabled: true, missing: true},
				{name: "malformed-pipeline", kind: "release_pipeline", invalid: true},
				{name: "first-duplicate", kind: "release", disabled: true, duplicate: true},
			} {
				entryID, resourceID := prefix+"trash", prefix+"resource"
				var payload []byte
				var err error
				if row.kind == "release_pipeline" {
					payload, err = json.Marshal(domain.ReleasePipeline{ID: resourceID, Name: "SW319 pipeline", TeamIDs: row.teams, CreatedAt: at, UpdatedAt: at})
				} else {
					payload, err = json.Marshal(domain.Release{ID: resourceID, Name: "SW319 release", CreatedAt: at, UpdatedAt: at})
				}
				if err != nil {
					t.Fatal(err)
				}
				if row.invalid {
					payload = []byte(`{"teamIds":"not-an-array"}`)
				}
				full := store.ForceFullMutationsForTesting(true)
				err = repository.MutateWorkspace(t.Context(), workspace, "scale_p21.sw319_seed", "", nil, func(data *domain.Bootstrap) error {
					if data.WorkspaceSettings.FeatureFlags == nil {
						data.WorkspaceSettings.FeatureFlags = map[string]bool{}
					}
					data.WorkspaceSettings.FeatureFlags["releases"] = !row.disabled
					for index := range data.APIKeys {
						if ownedUsers[data.APIKeys[index].CreatorID] {
							data.APIKeys[index].Scopes = []string{"read", "write"}
						}
					}
					data.Trash = []domain.TrashEntry{{
						ID: entryID, ResourceID: resourceID, ResourceType: row.kind,
						Title: "SW319 deleted resource", Payload: payload, TeamIDs: row.teams,
						DeletedBy: data.Viewer, DeletedAt: at, ExpiresAt: at.AddDate(1, 0, 0),
					}}
					if row.missing {
						data.Trash = nil
					}
					if row.duplicate {
						second, marshalErr := json.Marshal(domain.ReleasePipeline{ID: prefix + "second", TeamIDs: []string{private}})
						if marshalErr != nil {
							return marshalErr
						}
						data.Trash = append(data.Trash, domain.TrashEntry{ID: entryID, ResourceType: "release_pipeline", Payload: second})
					}
					return nil
				})
				full()
				if err != nil {
					t.Fatal(err)
				}
				view, err := repository.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, f.foreign)
				if err != nil {
					t.Fatal(err)
				}
				for _, principal := range f.principals {
					for _, credential := range f.credentials[principal.user.ID] {
						for _, method := range []string{http.MethodPost, http.MethodDelete} {
							name := fmt.Sprintf("%s/%s/%s/%s", row.name, principal.name, credential.name, method)
							t.Run(name, func(t *testing.T) {
								var expectedCode int
								var expectedBody []byte
								for _, source := range []*store.SQLiteStore{repository, view} {
									service := &server{store: source}
									mux := http.NewServeMux()
									var held *store.ColdMetadataRead
									authorized := func(w http.ResponseWriter, r *http.Request) {
										held, _ = r.Context().Value(coldAuthorizationReadKey{}).(*store.ColdMetadataRead)
										writeJSON(w, http.StatusOK, map[string]bool{"authorized": true})
									}
									mux.HandleFunc("POST /api/trash/{id}/restore", authorized)
									mux.HandleFunc("DELETE /api/trash/{id}", authorized)
									path := "/api/trash/" + entryID
									if method == http.MethodPost {
										path += "/restore"
									}
									request := httptest.NewRequest(method, path, nil)
									request.Header.Set("X-Workspace-Key", workspace)
									scaleP21SW319Credential(request, credential)
									request, release := beginRequestResourceReads(request)
									scope := request.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
									recorder := httptest.NewRecorder()
									outputChecks := 0
									writer := &scaleP21SW215ColdOutputWriter{ResponseRecorder: recorder, check: func() {
										outputChecks++
										if source.BoundedMetadataEnabled() && (scope.closed || len(scope.closers) == 0) {
											t.Error("trash authorization released its cold graph before encoding")
										}
									}}
									service.withColdMetadataRouteMux(mux, service.authenticate(mux)).ServeHTTP(writer, request)
									release()
									if outputChecks == 0 || !scope.closed || held != nil && held.Store() != nil {
										t.Fatal("trash response did not release its complete outer owner")
									}
									if !source.BoundedMetadataEnabled() {
										expectedCode, expectedBody = recorder.Code, append([]byte(nil), recorder.Body.Bytes()...)
									} else {
										if recorder.Code != expectedCode || !bytes.Equal(recorder.Body.Bytes(), expectedBody) {
											t.Fatalf("production auth differs: legacy=%d %s bounded=%d %s", expectedCode, expectedBody, recorder.Code, recorder.Body.String())
										}
										totalPairs++
									}
								}
								if method == http.MethodDelete {
									if expectedCode != http.StatusOK {
										t.Fatal("purge gained restore-only ownership or release feature gates", expectedCode, string(expectedBody))
									}
									purgeAllows++
								}
								if method == http.MethodPost && principal.name == "member-private-outsider" && row.name == "partial-owner" {
									if expectedCode != http.StatusForbidden || !bytes.Contains(expectedBody, []byte("Only admins and team owners can restore this pipeline")) {
										t.Fatal("partial-team-owner denial witness was not exercised", expectedCode, string(expectedBody))
									}
									partialDenials++
								}
								if method == http.MethodPost && row.name == "release-disabled" {
									if expectedCode != http.StatusForbidden || !bytes.Contains(expectedBody, []byte("This workspace feature is disabled")) {
										t.Fatal("releases-disabled witness was not exercised", expectedCode, string(expectedBody))
									}
									featureDenials++
								}
								if method == http.MethodPost && principal.name == "sub-team-inheritance" && row.name == "inherited-owner" {
									if expectedCode != http.StatusOK {
										t.Fatal("inherited team owner was denied by auth", expectedCode, string(expectedBody))
									}
									inheritedAllows++
								}
							})
						}
					}
				}
				// Cross-workspace checks use real credentials and no injected read.
				for _, principal := range f.principals {
					if principal.user.ID == f.data.Viewer.ID {
						continue
					}
					credential := f.credentials[principal.user.ID][0]
					var expectedCode int
					var expectedBody []byte
					for _, source := range []*store.SQLiteStore{repository, view} {
						service := &server{store: source}
						mux := http.NewServeMux()
						mux.HandleFunc("POST /api/trash/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
							t.Error("cross-workspace trash route reached its callback")
							w.WriteHeader(http.StatusOK)
						})
						request := httptest.NewRequest(http.MethodPost, "/api/trash/"+entryID+"/restore", nil)
						request.Header.Set("X-Workspace-Key", f.foreign)
						scaleP21SW319Credential(request, credential)
						recorder := httptest.NewRecorder()
						service.withColdMetadataRouteMux(mux, service.authenticate(mux)).ServeHTTP(recorder, request)
						if source == repository {
							expectedCode, expectedBody = recorder.Code, append([]byte(nil), recorder.Body.Bytes()...)
						} else if recorder.Code != expectedCode || !bytes.Equal(recorder.Body.Bytes(), expectedBody) {
							t.Fatal("cross-workspace trash auth differs")
						}
						if recorder.Code != http.StatusForbidden {
							t.Fatal("cross-workspace boundary witness was not exercised", recorder.Code)
						}
					}
				}
			}
			if totalPairs == 0 || partialDenials == 0 || featureDenials == 0 || inheritedAllows == 0 || purgeAllows == 0 {
				t.Fatal("trash authorization table lost required witnesses")
			}
			t.Logf("auth pairs=%d partial-owner denials=%d disabled-release denials=%d inherited allows=%d purge allows=%d", totalPairs, partialDenials, featureDenials, inheritedAllows, purgeAllows)
		})
	}
}

func scaleP21SW319Credential(request *http.Request, credential scaleP21SW215RealCredential) {
	if credential.session != "" {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: credential.session})
	} else {
		request.Header.Set("Authorization", "Bearer "+credential.bearer)
	}
}
