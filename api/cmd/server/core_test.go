package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21M1CoreReadsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		public, private, child := idgen.New("m1-public-"), idgen.New("m1-private-"), idgen.New("m1-child-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Workspace.LogoURL = "/uploads/m1-logo.png"
			data.WorkspaceSettings.FeatureFlags["coding-sessions"] = true
			data.Teams = append(data.Teams, domain.Team{ID: public, Key: public}, domain.Team{ID: private, Key: private, Private: true}, domain.Team{ID: child, Key: child})
			data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
			data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private}
			data.Projects = append(data.Projects, domain.Project{ID: "m1-unrelated", TeamIDs: []string{private}, Description: strings.Repeat("unrelated ", 40000)})
			for range 120 {
				data.Projects = append(data.Projects, domain.Project{ID: idgen.New("m1-extra-"), Name: "Unrelated", TeamIDs: []string{private}, Description: strings.Repeat("body ", 128)})
			}
			data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: "m1-unrelated", Provider: "gitlab", Config: map[string]string{"body": strings.Repeat("unrelated ", 40000)}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		principals := []domain.User{f.data.Viewer}
		for _, spec := range []struct {
			name, role string
			teams      []string
		}{
			{"admin", "admin", []string{private}},
			{"member-private-outsider", "member", []string{public}},
			{"guest-explicit", "guest", []string{public}},
			{"inherited", "member", []string{private}},
		} {
			_, user := scaleP03Join(t, f, spec.name, idgen.New("m1-core-")+"@example.test", spec.role, spec.teams...)
			principals = append(principals, user)
		}
		foreign := scaleP03Fixture(t, repo)
		principals = append(principals, foreign.data.Viewer)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.keys", "", nil, func(data *domain.Bootstrap) error {
			for _, principal := range principals {
				for _, restriction := range []string{"all", "selected-empty", "selected-set"} {
					data.APIKeys = append(data.APIKeys, *scaleP21M1CoreKey(principal.ID, restriction, public))
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var queued [2]store.EventAccess
		for _, principal := range principals {
			for _, restriction := range []string{"session", "all", "selected-empty", "selected-set"} {
				t.Run(principal.ID+"/"+restriction, func(t *testing.T) {
					ctx := context.WithValue(t.Context(), authUserContextKey{}, principal)
					key := scaleP21M1CoreKey(principal.ID, restriction, public)
					if key != nil {
						ctx = context.WithValue(ctx, apiKeyContextKey{}, *key)
					}
					var fingerprints []string
					var accessErrors []error
					for index, source := range []*store.SQLiteStore{repo, probe} {
						version, err := source.RealtimeSourceVersion(ctx, workspace)
						if err != nil {
							t.Fatal(err)
						}
						policy, err := source.ReadEventPolicySource(ctx, workspace, version)
						if err != nil || policy.Bytes() > 3*(256<<10)+1024 || !policy.HasTeam(private) || !policy.HasTeam(child) {
							t.Fatalf("event policy source changed or exceeded its bound: %v", err)
						}
						access, err := source.ResolveEventAccessFromSource(ctx, policy, principal.ID, key)
						accessErrors = append(accessErrors, err)
						fingerprints = append(fingerprints, access.PermissionFingerprint())
						if err == nil {
							query, queryErr := access.RealtimeQuery()
							if queryErr != nil || query.Access == nil {
								t.Fatalf("event policy query is unavailable: %v", queryErr)
							}
							if restriction == "selected-empty" && (query.AllowedTeamIDs == nil || len(query.AllowedTeamIDs) != 0) {
								t.Fatal("selected-empty key gained team content")
							}
							if principal.ID == f.data.Viewer.ID && restriction == "session" && slices.Contains(query.Access.VisibleTeamIDs, private) {
								t.Fatal("admin outside the private team gained private content")
							}
							if principal.ID == principals[4].ID && restriction == "session" && !slices.Contains(query.Access.VisibleTeamIDs, child) {
								t.Fatal("sub-team inheritance disappeared")
							}
							if principal.ID == foreign.data.Viewer.ID {
								t.Fatal("event source authorized a cross-workspace principal")
							}
							forged := access
							forged.WorkspaceID = "m1-forged-workspace"
							if err := forged.Validate(ctx); !errors.Is(err, store.ErrAuthForbidden) {
								t.Fatal("event policy accepted a forged workspace binding")
							}
						}
						if principal.ID == principals[2].ID && restriction == "session" {
							queued[index] = access
						}
					}
					if fingerprints[0] != fingerprints[1] || (accessErrors[0] == nil) != (accessErrors[1] == nil) || errors.Is(accessErrors[0], store.ErrAuthForbidden) != errors.Is(accessErrors[1], store.ErrAuthForbidden) {
						t.Fatalf("event policy permission differential: %q errors=%v", fingerprints, accessErrors)
					}
					for _, key := range []string{workspace, "", "m1-missing"} {
						r := httptest.NewRequest("GET", "/api/coding/environments", nil).WithContext(ctx)
						r.Header.Set("X-Workspace-Key", key)
						if key == "" {
							r = r.WithContext(context.WithValue(ctx, workspaceKeyContextKey{}, workspace))
						}
						var responses []codingEnvironmentRequest
						var allowed []bool
						var codes []int
						var bodies []string
						for _, source := range []*store.SQLiteStore{repo, probe} {
							s := &server{store: source}
							w := httptest.NewRecorder()
							response, ok := s.codingEnvironmentAccess(w, r)
							responses, allowed = append(responses, response), append(allowed, ok)
							codes, bodies = append(codes, w.Code), append(bodies, w.Body.String())
						}
						if !reflect.DeepEqual(responses[0], responses[1]) || allowed[0] != allowed[1] || codes[0] != codes[1] || bodies[0] != bodies[1] {
							t.Fatalf("coding access differs for workspace %q: %#v %v %v %v", key, responses, allowed, codes, bodies)
						}
					}
					for _, candidate := range []string{workspace, "", "m1-missing"} {
						for _, admin := range []bool{false, true} {
							var results []string
							for _, source := range []*store.SQLiteStore{repo, probe} {
								err := (&server{store: source}).checkGitHubAppMember(ctx, candidate, principal.ID, admin)
								message := ""
								if err != nil {
									message = err.Error()
								}
								results = append(results, message)
							}
							if results[0] != results[1] {
								t.Fatalf("GitHub membership differs for %q: %v", candidate, results)
							}
						}
					}
					var refusals []bool
					var refusalBodies []string
					for _, source := range []*store.SQLiteStore{repo, probe} {
						w := httptest.NewRecorder()
						refusals = append(refusals, (&server{store: source}).refuseSoleAdmin(w, ctx, principal.ID, ""))
						refusalBodies = append(refusalBodies, w.Body.String())
					}
					if refusals[0] != refusals[1] || refusalBodies[0] != refusalBodies[1] {
						t.Fatalf("sole-admin identity response differs: %v %v", refusals, refusalBodies)
					}
				})
			}
		}
		for _, key := range []string{workspace, "", "m1-missing"} {
			legacy, bounded := &server{store: repo}, &server{store: probe}
			if key == "" {
				for _, s := range []*server{legacy, bounded} {
					core, _ := s.store.WorkspaceSettingsMetadata("")
					if got := s.codeHostWorkspaceURLKey(""); got != core.Workspace.URLKey {
						t.Fatal("canonical key did not follow its store's default workspace")
					}
				}
			} else if a, b := legacy.codeHostWorkspaceURLKey(key), bounded.codeHostWorkspaceURLKey(key); a != b {
				t.Fatalf("canonical key differs: %q/%q", a, b)
			}
			r := httptest.NewRequest("GET", "/api/coding/environments", nil)
			r.Header.Set("X-Workspace-Key", key)
			if a, b := legacy.codingWorkspaceKey(r, f.data), bounded.codingWorkspaceKey(r, f.data); a != b {
				t.Fatalf("coding workspace key differs: %q/%q", a, b)
			}
			var responses []codingEnvironmentRequest
			var results []bool
			check := r
			if key == "" {
				check = r.WithContext(context.WithValue(r.Context(), workspaceKeyContextKey{}, workspace))
			}
			for _, source := range []*store.SQLiteStore{repo, probe} {
				response, ok := (&server{store: source, authDisabled: true}).codingEnvironmentAccess(httptest.NewRecorder(), check)
				responses, results = append(responses, response), append(results, ok)
			}
			if !reflect.DeepEqual(responses[0], responses[1]) || results[0] != results[1] {
				t.Fatal("auth-disabled coding access changed its viewer/admin identity")
			}
		}
		for _, name := range []string{"m1-logo.png", "old-logo.png"} {
			owner := &store.FileUpload{Purpose: store.FileUploadPurposeWorkspaceLogo, WorkspaceKey: workspace, ContentType: "image/png"}
			a, left := (&server{store: repo}).resolveWorkspaceLogo(name, owner)
			b, right := (&server{store: probe}).resolveWorkspaceLogo(name, owner)
			if a != b || left != right {
				t.Fatal("logo ownership/current-reference check changed")
			}
		}
		// Capacity consumes caller-owned cycles and references. Its metadata
		// fallback needs only the canonical default workspace key.
		left := domain.Bootstrap{Cycles: []domain.Cycle{{ID: "m1-cycle", TeamID: public}}}
		right := left
		if err := (&server{store: repo}).decorateCycleCapacity(t.Context(), &left); err != nil {
			t.Fatal(err)
		}
		if err := (&server{store: probe}).decorateCycleCapacity(t.Context(), &right); err != nil || !reflect.DeepEqual(left, right) {
			t.Fatalf("capacity workspace fallback differs: %v", err)
		}
		member := principals[2]
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replay"} {
			for _, access := range queued {
				if err := access.Validate(t.Context()); !errors.Is(err, store.ErrAuthForbidden) {
					t.Fatalf("%s event access survived member revocation: %v", phase, err)
				}
			}
			for _, source := range []*store.SQLiteStore{repo, probe} {
				if err := (&server{store: source}).checkGitHubAppMember(t.Context(), workspace, member.ID, false); err == nil {
					t.Fatalf("%s membership check accepted a suspended user", phase)
				}
			}
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("core reads decoded complete metadata")
		}
		for name, source := range map[string]*store.SQLiteStore{"legacy": repo, "bounded": probe} {
			s := &server{store: source}
			r := httptest.NewRequest("GET", "/api/coding/environments", nil)
			r.Header.Set("X-Workspace-Key", workspace)
			r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, f.data.Viewer))
			read := func() { s.codingEnvironmentAccess(httptest.NewRecorder(), r) }
			allocations := testing.AllocsPerRun(5, read)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for range 5 {
				read()
			}
			runtime.ReadMemStats(&after)
			t.Logf("%s coding access: bytes/op=%d allocs/op=%.0f", name, (after.TotalAlloc-before.TotalAlloc)/5, allocations)
		}
	}
}

func scaleP21M1CoreKey(userID, restriction, public string) *domain.APIKey {
	if restriction == "session" {
		return nil
	}
	key := &domain.APIKey{ID: "m1-" + userID + "-" + restriction, CreatorID: userID, SubjectUserID: userID, Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}
	if strings.HasPrefix(restriction, "selected") {
		key.TeamRestriction, key.TeamIDs = "selected", []string{}
		if restriction == "selected-set" {
			key.TeamIDs = []string{public}
		}
	}
	return key
}
