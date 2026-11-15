package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21ChatPointError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T: %v", err, err)
}

func scaleP21ChatPointIdentity(ctx context.Context, userID string) context.Context {
	return withChatIdentityAuthentication(ctx, domain.ChatIdentity{
		UserID: userID, AuthProvider: "password", AuthenticatedAt: time.Now().UTC(),
	})
}

func scaleP21ChatPointCall(t *testing.T, source *store.SQLiteStore, ctx context.Context, workspace, userID, path string, result any) (err error, held int, closeOutput func()) {
	t.Helper()
	ctx, closeScope := beginMetadataOutputReads(ctx)
	closeOutput = func() {
		closeScope()
		closeScope()
	}
	t.Cleanup(closeOutput)
	service := &server{store: source}
	if source.BoundedMetadataEnabled() {
		err = service.chatAuthorizedPointRead(ctx, workspace, userID, path, result)
	} else {
		err = service.chatAuthorizedMutation(ctx, workspace, userID, http.MethodGet, path, nil, result)
	}
	held = source.MetadataWorkUsedForTesting()
	return err, held, closeOutput
}

func TestScaleP21ChatPointReadPermissionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		data, found := repo.WorkspaceMetadata(workspace)
		if !found {
			t.Fatal("owned workspace missing")
		}
		privateTeam := ""
		for _, project := range data.Projects {
			if project.ID == "m2-unrelated" {
				privateTeam = project.TeamIDs[0]
			}
		}
		privateAdmin := authClient(t)
		authRequest[domain.AuthSession](t, privateAdmin, http.MethodPost, f.host.URL+"/api/auth/login", map[string]string{"email": f.principals[1].user.Email, "password": "p03-fixture-password"}, "", http.StatusOK)
		public := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Point read public", "teamId": f.public, "description": "Complete public body"}, workspace, http.StatusCreated)
		private := authRequest[domain.Issue](t, privateAdmin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Point read private", "teamId": privateTeam, "description": "Complete private body"}, workspace, http.StatusCreated)
		guest := f.principals[3].user
		authRequest[[]domain.IssuePermission](t, privateAdmin, http.MethodPut, f.host.URL+"/api/issues/"+private.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": guest.ID, "role": "viewer"}}}, workspace, http.StatusOK)
		for _, reload := range []bool{false, true} {
			if reload {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			t.Run(fmt.Sprintf("reload=%t", reload), func(t *testing.T) {
				f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
					for _, ref := range []string{public.ID, public.Identifier, strings.ToLower(public.Identifier), private.ID, "missing-point-read"} {
						ctx := scaleP21ChatPointIdentity(f.literalRequest(user, key, nil).Context(), user.ID)
						var before, after domain.Issue
						path := "/api/issue-records/" + url.PathEscape(ref)
						oldErr, _, closeOld := scaleP21ChatPointCall(t, repo, ctx, workspace, user.ID, path, &before)
						newErr, held, closeNew := scaleP21ChatPointCall(t, f.probe, ctx, workspace, user.ID, path, &after)
						if scaleP21ChatPointError(oldErr) != scaleP21ChatPointError(newErr) || !reflect.DeepEqual(before, after) {
							t.Fatalf("ref=%q point permission/output changed: old=%v new=%v", ref, oldErr, newErr)
						}
						if held == 0 {
							t.Fatal("encoded response or refusal lost its output owner")
						}
						if ref == public.ID && user.ID == f.data.Viewer.ID && key == nil && (newErr != nil || after.Description != "Complete public body") {
							t.Fatal("public admin point route did not return its complete issue", newErr)
						}
						if ref == private.ID && user.ID == guest.ID && key == nil && (newErr != nil || after.ID != private.ID) {
							t.Fatal("guest explicit issue grant was lost", newErr)
						}
						if ref == private.ID && user.ID == f.principals[2].user.ID && key == nil && newErr == nil {
							t.Fatal("private outsider gained point issue access")
						}
						if ref == public.ID && key != nil && key.TeamRestriction == "selected" && len(key.TeamIDs) == 0 && newErr == nil {
							t.Fatal("selected-empty credential gained point access")
						}
						closeOld()
						closeNew()
						if f.probe.MetadataWorkUsedForTesting() != 0 {
							t.Fatal("permission read leaked its operation output")
						}
					}
				})
			})
		}
		// Revocation is checked again by the actual handler, even after a
		// successful owned read of this issue by the same principal.
		authRequest[[]domain.IssuePermission](t, privateAdmin, http.MethodPut, f.host.URL+"/api/issues/"+private.ID+"/permissions", map[string]any{"permissions": []map[string]string{}}, workspace, http.StatusOK)
		ctx := scaleP21ChatPointIdentity(t.Context(), guest.ID)
		var old, current domain.Issue
		oldErr, _, closeOld := scaleP21ChatPointCall(t, repo, ctx, workspace, guest.ID, "/api/issue-records/"+private.ID, &old)
		newErr, _, closeNew := scaleP21ChatPointCall(t, f.probe, ctx, workspace, guest.ID, "/api/issue-records/"+private.ID, &current)
		if oldErr == nil || scaleP21ChatPointError(oldErr) != scaleP21ChatPointError(newErr) || !reflect.DeepEqual(old, current) {
			t.Fatal("point read cached an earlier explicit grant", oldErr, newErr)
		}
		closeOld()
		closeNew()
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("point reads decoded complete metadata", f.probe.WorkspaceMetadataDecodes())
		}
	}
}

func TestScaleP21ChatPointReadErrorsMergeAndCap(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, userID := f.data.Workspace.URLKey, f.data.Viewer.ID
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Point merge target", "teamId": f.data.Teams[0].ID, "description": strings.Repeat("complete body ", 5000)}, workspace, http.StatusCreated)
		view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		path := "/api/issue-records/" + issue.ID
		ctx := scaleP21ChatPointIdentity(t.Context(), userID)
		for _, name := range []string{"map-merge", "partial-type-error", "nil-target", "nil-result", "missing-issue", "missing-workspace", "empty-workspace", "empty-user", "missing-user", "unverified", "expired", "unsupported-path", "query", "fragment", "malformed-path", "scope-canceled"} {
			t.Run(name, func(t *testing.T) {
				var results []any
				var failures []string
				var closes []func()
				defer func() {
					for _, close := range closes {
						close()
					}
					if view.MetadataWorkUsedForTesting() != 0 {
						t.Error("point errors/partial output escaped their outer owner")
					}
				}()
				for _, source := range []*store.SQLiteStore{repo, view} {
					readCtx, readWorkspace, readUser, readPath := ctx, workspace, userID, path
					var target any
					switch name {
					case "map-merge":
						target = &map[string]any{"kept": []any{"retained value"}, "description": "replaced"}
					case "partial-type-error":
						target = &struct {
							Identifier  string `json:"identifier"`
							Version     string `json:"version"`
							Description string `json:"description"`
							Kept        string `json:"kept"`
						}{Identifier: "before", Version: "before", Description: "before", Kept: "retained"}
					case "nil-target":
						var typedNil *domain.Issue
						target = typedNil
					case "nil-result":
						target = nil
					default:
						target = &domain.Issue{}
					}
					switch name {
					case "missing-issue":
						readPath += "-missing"
					case "missing-workspace":
						readWorkspace += "-missing"
					case "empty-workspace":
						readWorkspace = ""
					case "empty-user":
						readUser = ""
					case "missing-user":
						readUser += "-missing"
					case "unverified":
						readCtx = t.Context()
					case "expired":
						readCtx = withChatIdentityAuthentication(t.Context(), domain.ChatIdentity{UserID: userID, AuthProvider: "password", AuthenticatedAt: time.Now().UTC().Add(-40 * 24 * time.Hour)})
					case "unsupported-path":
						readPath = "/api/api-keys"
					case "query":
						readPath += "?unexpected=true"
					case "fragment":
						readPath += "#unexpected"
					case "malformed-path":
						readPath = "://bad-url"
					case "scope-canceled":
						var cancel context.CancelFunc
						readCtx, cancel = context.WithCancel(readCtx)
						cancel()
					}
					callErr, _, closeOutput := scaleP21ChatPointCall(t, source, readCtx, readWorkspace, readUser, readPath, target)
					closes = append(closes, closeOutput)
					failures = append(failures, scaleP21ChatPointError(callErr))
					results = append(results, target)
				}
				if failures[0] != failures[1] || !reflect.DeepEqual(results[0], results[1]) {
					t.Fatal("legacy/new point error or original-destination merge changed", failures)
				}
				switch name {
				case "map-merge":
					got := *results[1].(*map[string]any)
					if failures[1] != "" || got["description"] != issue.Description || !reflect.DeepEqual(got["kept"], []any{"retained value"}) {
						t.Fatal("original map members were replaced or not decoded", failures)
					}
				case "partial-type-error":
					raw, _ := json.Marshal(results[1])
					if !strings.Contains(failures[1], "response_invalid") || !strings.Contains(string(raw), issue.Identifier) || !strings.Contains(string(raw), "retained") || !strings.Contains(string(raw), issue.Description) {
						t.Fatal("partial Unmarshal failed to preserve/decode the original target", failures)
					}
				case "nil-target":
					if !strings.Contains(failures[1], "response_invalid") {
						t.Fatal("nil target did not retain the original decode error", failures)
					}
				}
			})
		}
		var response map[string]any
		readErr, _, closeResponse := scaleP21ChatPointCall(t, repo, ctx, workspace, userID, path, &response)
		if readErr != nil {
			t.Fatal(readErr)
		}
		raw, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		closeResponse()
		for _, limit := range []int{1, len(raw), len(raw) + 1} {
			t.Run(fmt.Sprintf("cap=%d", limit), func(t *testing.T) {
				lowerChatOperationLimit(t, limit)
				var failures []string
				var closes []func()
				defer func() {
					for _, close := range closes {
						close()
					}
					if view.MetadataWorkUsedForTesting() != 0 {
						t.Error("capped point response leaked its operation")
					}
				}()
				for _, source := range []*store.SQLiteStore{repo, view} {
					var got map[string]any
					err, held, closeOutput := scaleP21ChatPointCall(t, source, ctx, workspace, userID, path, &got)
					closes = append(closes, closeOutput)
					failures = append(failures, scaleP21ChatPointError(err))
					if source == view && held == 0 {
						t.Fatal("over-limit response released before caller handled its error")
					}
				}
				if failures[0] != failures[1] || (limit <= len(raw)) != strings.Contains(failures[1], "response_too_large") {
					t.Fatal("response cap/newline boundary changed", limit, len(raw), failures)
				}
			})
		}
		// The wrapper forwards to the literal helper before requiring a scope
		// or rejecting in-process bootstrap on an ordinary store.
		var bootstrap domain.Bootstrap
		if err := (&server{store: repo}).chatAuthorizedPointRead(ctx, workspace, userID, "/api/issue-records/bootstrap", &bootstrap); err != nil || bootstrap.Workspace.URLKey != workspace {
			t.Fatal("off wrapper changed the in-process bootstrap accessor", err)
		}
		for _, ownerState := range []string{"absent", "closed", "bootstrap"} {
			t.Run("owner="+ownerState, func(t *testing.T) {
				readCtx := ctx
				readPath := path
				var closeScope func()
				if ownerState != "absent" {
					readCtx, closeScope = beginMetadataOutputReads(readCtx)
					defer closeScope()
					if ownerState == "closed" {
						closeScope()
					} else {
						readPath = "/api/issue-records/bootstrap"
					}
				}
				var target any = &domain.Issue{}
				if ownerState == "bootstrap" {
					target = &domain.Bootstrap{}
				}
				if err := (&server{store: view}).chatAuthorizedPointRead(readCtx, workspace, userID, readPath, target); !errors.Is(err, store.ErrBoundedMetadataCoverage) || view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("point read silently accepted an absent/closed owner or bootstrap", err)
				}
			})
		}
		inactive, err := repo.ProvisionSCIMUser(t.Context(), f.data.Workspace.ID, idgen.New("point-inactive-"), idgen.New("point-inactive-"), "Inactive point user", idgen.New("point-inactive-")+"@example.test", "", "member", false)
		if err != nil || inactive.User.Active {
			t.Fatal("inactive-account fixture missing", err)
		}
		f.resources.users = append(f.resources.users, inactive.User)
		for _, name := range []string{"missing-consent-time", "mismatched-consent", "missing-provider", "mfa-required", "mfa-expired", "mfa-valid", "issuer-enforced", "inactive-account"} {
			t.Run("auth="+name, func(t *testing.T) {
				if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "workspaceSettings", "identityProviders"), workspace, "scale_p21.point_auth_policy", "", nil, func(data *domain.Bootstrap) error {
					data.WorkspaceSettings.SessionDurationDays = 7
					data.WorkspaceSettings.RequireTwoFactor = strings.HasPrefix(name, "mfa-")
					data.WorkspaceSettings.DisableAdminBypass = true
					data.IdentityProviders = nil
					if name == "issuer-enforced" {
						data.IdentityProviders = []domain.IdentityProvider{{ID: "point-provider", Enabled: true, Enforced: true, Issuer: "https://point-idp.example.test"}}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				identity := domain.ChatIdentity{UserID: userID, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
				if name == "missing-consent-time" {
					identity.AuthenticatedAt = time.Time{}
				}
				if name == "mismatched-consent" {
					identity.UserID += "-mismatch"
				}
				if name == "missing-provider" {
					identity.AuthProvider = ""
				}
				if name == "mfa-expired" || name == "mfa-valid" {
					at := time.Now().UTC().Add(-8 * 24 * time.Hour)
					if name == "mfa-valid" {
						at = time.Now().UTC()
					}
					identity.MFAVerifiedAt = &at
				}
				acting := userID
				if name == "inactive-account" {
					acting, identity.UserID = inactive.User.ID, inactive.User.ID
				}
				var before, after domain.Issue
				readCtx := withChatIdentityAuthentication(t.Context(), identity)
				oldErr, _, closeOld := scaleP21ChatPointCall(t, repo, readCtx, workspace, acting, path, &before)
				defer closeOld()
				newErr, _, closeNew := scaleP21ChatPointCall(t, view, readCtx, workspace, acting, path, &after)
				defer closeNew()
				if scaleP21ChatPointError(oldErr) != scaleP21ChatPointError(newErr) || !reflect.DeepEqual(before, after) || (name == "mfa-valid") != (newErr == nil) {
					t.Fatal("current consent/account policy changed", oldErr, newErr)
				}
			})
		}
		// Repeated references keep heap small while the previous destination
		// logically exceeds the shared quota. Admission precedes Unmarshal.
		shared := strings.Repeat("retained graph ", 1<<17)
		previous := make([]string, 100)
		for index := range previous {
			previous[index] = shared
		}
		destination := map[string]any{"kept": previous}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "workspaceSettings", "identityProviders"), workspace, "scale_p21.point_auth_reset", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.RequireTwoFactor, data.WorkspaceSettings.DisableAdminBypass = false, false
			data.IdentityProviders = nil
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		owned, closeScope := beginMetadataOutputReads(ctx)
		err = (&server{store: view}).chatAuthorizedPointRead(owned, workspace, userID, path, &destination)
		if !errors.Is(err, store.ErrMetadataWorkBudget) || len(destination) != 1 || destination["kept"].([]string)[0] != shared || view.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("previous graph admission failed after decoding or released early", err)
		}
		closeScope()
		if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("point decode quota failure leaked or decoded full metadata")
		}
	}
}

func TestScaleP21ChatPointReadLifetimeAndRecorder(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, userID := f.data.Workspace.URLKey, f.data.Viewer.ID
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Original point lifetime", "teamId": f.data.Teams[0].ID}, workspace, http.StatusCreated)
		view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		ctx := scaleP21ChatPointIdentity(t.Context(), userID)
		ctx, cancel := context.WithCancel(ctx)
		owned, closeScope := beginMetadataOutputReads(ctx)
		var first domain.Issue
		service := &server{store: view}
		if err := service.chatAuthorizedPointRead(owned, workspace, userID, "/api/issue-records/"+issue.ID, &first); err != nil {
			t.Fatal(err)
		}
		firstWork := view.MetadataWorkUsedForTesting()
		if firstWork == 0 {
			t.Fatal("point value lost ownership at helper return")
		}
		var counts [2]int
		for index := range counts {
			measureCtx := scaleP21ChatPointIdentity(t.Context(), userID)
			measureCtx, closeMeasure := beginIndependentMetadataOutputReads(measureCtx)
			measureCtx = store.WithSQLStatementObserver(measureCtx, func(string) { counts[index]++ })
			var measured domain.Issue
			var readErr error
			if index == 0 {
				readErr = service.chatAuthorizedMutation(measureCtx, workspace, userID, http.MethodGet, "/api/issue-records/"+issue.ID, nil, &measured)
			} else {
				readErr = service.chatAuthorizedPointRead(measureCtx, workspace, userID, "/api/issue-records/"+issue.ID, &measured)
			}
			if readErr != nil || measured.ID != issue.ID {
				t.Fatal("same-view point SQL measurement failed", readErr)
			}
			measured = domain.Issue{}
			closeMeasure()
			if view.MetadataWorkUsedForTesting() != firstWork {
				t.Fatal("measurement closed another operation's point allowance")
			}
		}
		if counts[0] == 0 || counts[0] != counts[1] {
			t.Fatal("owned recorder added SQL", counts)
		}
		t.Logf("same_bounded_view SQL legacy_helper=%d owned_point_helper=%d", counts[0], counts[1])
		authRequest[domain.Issue](t, f.admin, http.MethodPatch, f.host.URL+"/api/issue-records/"+issue.ID, map[string]any{"title": "Replacement point lifetime"}, workspace, http.StatusOK)
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		var next domain.Issue
		if err := service.chatAuthorizedPointRead(owned, workspace, userID, "/api/issue-records/"+issue.ID, &next); err != nil || next.Title != "Replacement point lifetime" || first.Title != issue.Title {
			t.Fatal("point output was aliased or an old read forced stale current-state lookup", err)
		}
		if view.MetadataWorkUsedForTesting() <= firstWork {
			t.Fatal("later point response replaced an earlier live allowance")
		}
		cancel()
		if view.MetadataWorkUsedForTesting() <= firstWork || first.Title != issue.Title {
			t.Fatal("context cancellation prematurely released still-used output")
		}
		closeScope()
		closeScope()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("containing owner failed to release multiple point frames")
		}
		for _, failure := range []string{"cancel", "encoding", "buffer", "child"} {
			t.Run(failure, func(t *testing.T) {
				parent, closeParent := beginMetadataOutputReads(t.Context())
				defer closeParent()
				response := &chatMutationResponse{header: make(http.Header)}
				output, err := service.beginChatPointReadOutput(parent, response)
				if err != nil {
					t.Fatal(err)
				}
				response.owned = output
				writeJSON(response, http.StatusOK, map[string]any{"kept": "first encoded body"})
				if output.work.Err() != nil || response.body.Len() == 0 || view.MetadataWorkUsedForTesting() <= 128<<10 {
					t.Fatal("owned recorder did not admit encoding and buffering", output.work.Err())
				}
				previousBody := response.body.String()
				previousWork := view.MetadataWorkUsedForTesting()
				childClosed := false
				var display *store.ChatDisplayRead
				if !registerRequestReadClose(output.ctx, func() {
					childClosed = true
					if view.MetadataWorkUsedForTesting() == 0 || response.body.Len() == 0 {
						t.Error("frame released bytes/work before its nested projection")
					}
				}) {
					t.Fatal("owned recorder lost child scope")
				}
				switch failure {
				case "cancel":
					canceled, cancel := context.WithCancel(output.ctx)
					cancel()
					output.ctx = canceled
					_, err = response.Write([]byte("next"))
				case "encoding":
					work := make([]string, 200)
					text := strings.Repeat("x", 1<<16)
					for index := range work {
						work[index] = text
					}
					writeJSON(response, http.StatusOK, work)
					err = output.work.Err()
				case "buffer":
					err = output.work.ReserveEncoded(output.ctx, int(^uint(0)>>1))
					_, writeErr := response.Write([]byte("next"))
					if !errors.Is(writeErr, store.ErrMetadataWorkBudget) {
						t.Fatal("recorder hid earlier buffer admission failure", writeErr)
					}
				case "child":
					read, found, readErr := view.BeginChatDisplayRead(output.ctx, workspace, store.ChatDisplayRequest{Directory: true})
					if readErr != nil || !found {
						t.Fatal(found, readErr)
					}
					display = read
					if err := read.TransferOutput(output.ctx); err != nil {
						t.Fatal("nested projection could not transfer into frame", err)
					}
				}
				if failure != "child" && (err == nil || output.work.Err() == nil || response.body.String() != previousBody || view.MetadataWorkUsedForTesting() < previousWork) {
					t.Fatal("recorder failure was hidden, wrote bytes or released existing output", err, output.work.Err())
				}
				if childClosed {
					t.Fatal("failure closed child while response was still in use")
				}
				closeParent()
				if !childClosed || response.body.Len() != 0 || view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("outer close leaked or retained recorder/child ownership")
				}
				if display != nil && !reflect.DeepEqual(display.Data, domain.Bootstrap{}) {
					t.Fatal("JSON frame did not close its nested store projection")
				}
				output.Close()
			})
		}
		var jobs sync.WaitGroup
		for range 4 {
			jobs.Go(func() {
				local := scaleP21ChatPointIdentity(t.Context(), userID)
				local, closeLocal := beginMetadataOutputReads(local)
				defer closeLocal()
				var read domain.Issue
				if err := service.chatAuthorizedPointRead(local, workspace, userID, "/api/issue-records/"+issue.ID, &read); err != nil || read.Title != "Replacement point lifetime" {
					t.Error("concurrent point output changed", err)
				}
			})
		}
		jobs.Wait()
		if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("concurrent point responses leaked or decoded complete metadata")
		}
	}
}
