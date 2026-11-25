package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func enableLocalStoreShadowTest(t *testing.T) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_SYNC_V2", "on")
	t.Setenv("FLOW_SYNC_MANIFEST", "on")
	t.Setenv("FLOW_INDEXED_READS", "on")
	t.Setenv("FLOW_LOCAL_STORE", "")
}

func TestLocalStoreShadowAuthority(t *testing.T) {
	enableLocalStoreShadowTest(t)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		bounded, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		var first localStoreStamp
		for _, service := range []*server{f.service, {store: bounded}} {
			member := f.principals[2]
			r := member.request(workspace)
			r.URL.Path = "/api/bootstrap"
			r.Header.Set(localStoreHeader, localStoreShadowCapability)
			w := httptest.NewRecorder()
			service.stampLocalStoreResponse(w, r, workspace)
			var stamp localStoreStamp
			if err := json.Unmarshal([]byte(w.Header().Get(localStoreHeader)), &stamp); err != nil || stamp.ViewerID != member.user.ID || stamp.WorkspaceID != f.data.Workspace.ID || stamp.AuthzEpoch == "" || stamp.Head == "" {
				t.Fatalf("invalid shadow stamp: %+v %v", stamp, err)
			}
			if first != (localStoreStamp{}) && first != stamp {
				t.Fatalf("bounded authority differs: %+v vs %+v", first, stamp)
			}
			first = stamp

			query := r.URL.Query()
			query.Set("surface", "local-store")
			r.URL.RawQuery = query.Encode()
			w = httptest.NewRecorder()
			service.syncManifest(w, r)
			var manifest struct {
				localStoreStamp
				Capabilities []string `json:"capabilities"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &manifest); w.Code != http.StatusOK || err != nil || manifest.localStoreStamp != stamp || len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != localStoreShadowCapability {
				t.Fatalf("shadow manifest disagrees: %d %+v %v", w.Code, manifest, err)
			}
			for _, denied := range []scaleP12Principal{{user: domain.User{ID: "former-viewer"}}, {user: member.user, key: &domain.APIKey{TeamRestriction: "selected", TeamIDs: []string{f.data.Teams[0].ID}}}} {
				r := denied.request(workspace)
				r.URL.Path = "/api/bootstrap"
				r.Header.Set(localStoreHeader, localStoreShadowCapability)
				w := httptest.NewRecorder()
				service.stampLocalStoreResponse(w, r, workspace)
				if w.Header().Get(localStoreHeader) != "" {
					t.Fatal("issued a stamp to a revoked viewer or team-scoped key")
				}
				r.Header.Set(localStoreHeader, stamp.ViewerID)
				r.URL.Path = "/api/sync/changes"
				r.URL.RawQuery = "surface=local-store"
				w = httptest.NewRecorder()
				if !service.localStoreChanges(w, r) || w.Code != http.StatusForbidden {
					t.Fatalf("accepted a mismatched credential: %d", w.Code)
				}
			}
		}

		r := f.principals[2].request(workspace)
		r.URL.Path = "/api/bootstrap"
		r.Header.Set(localStoreHeader, localStoreShadowCapability)
		t.Run("disabled", func(t *testing.T) {
			t.Setenv("FLOW_LOCAL_STORE", "off")
			w := httptest.NewRecorder()
			f.service.setChangeCursor(w, r, workspace)
			f.service.stampLocalStoreResponse(w, r, workspace)
			if w.Header().Get(localStoreHeader) != localStoreDisabledValue || w.Header().Get(changeCursorHeader) == "" {
				t.Fatal("disabled store changed the legacy cursor or did not advertise the opt-out")
			}
			r.URL.Path = "/api/sync/manifest"
			r.URL.RawQuery = "surface=local-store&mode=issues"
			r.Header.Set(localStoreHeader, f.principals[2].user.ID)
			w = httptest.NewRecorder()
			f.service.syncManifest(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatalf("disabled issue manifest returned %d", w.Code)
			}
		})
	}
}

func TestLocalStoreShadowChangesDifferential(t *testing.T) {
	enableLocalStoreShadowTest(t)
	for name, repo := range scaleP03ServerTestStores(t) {
		t.Run(name, func(t *testing.T) {
			f := newScaleP12Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issues", map[string]string{"teamId": f.data.Teams[0].ID, "title": "Plain issue"}, workspace, http.StatusCreated)
			member, guest := f.principals[2], f.principals[3]
			stamp, ok := f.service.localStoreAuthority(member.request(workspace), workspace)
			if !ok {
				t.Fatal("member did not get an authorized baseline")
			}
			guestStamp, ok := f.service.localStoreAuthority(guest.request(workspace), workspace)
			if !ok {
				t.Fatal("guest did not get an authorized baseline")
			}
			read := func(service *server, principal scaleP12Principal, baseline localStoreStamp, shadow bool) syncV2ChangesResponse {
				t.Helper()
				r := principal.request(workspace)
				r.URL.Path = "/api/sync/changes"
				query := url.Values{"workspace": {workspace}, "after": {baseline.Head}, "authzEpoch": {baseline.AuthzEpoch}}
				if shadow {
					query.Set("surface", "local-store")
					query.Set("workspaceId", baseline.WorkspaceID)
					r.Header.Set(localStoreHeader, principal.user.ID)
				}
				r.URL.RawQuery = query.Encode()
				w := httptest.NewRecorder()
				service.syncChanges(w, r)
				var page syncV2ChangesResponse
				if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != http.StatusOK {
					t.Fatalf("changes request failed: %d %s", w.Code, w.Body.String())
				}
				return page
			}

			const title = "Local store title edit"
			authRequest[domain.Issue](t, f.admin, http.MethodPatch, f.host.URL+"/api/issues/"+issue.ID, map[string]string{"title": title}, workspace, http.StatusOK)
			legacy := read(f.service, member, stamp, false)
			if len(legacy.Transactions) != 1 || !strings.Contains(strings.Join(legacy.Transactions[0].Invalidations, ","), "issue_field_history") {
				t.Fatalf("fixture edit did not exercise a history invalidation: %+v", legacy)
			}
			bounded, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			other := &server{store: bounded}
			page := read(f.service, member, stamp, true)
			if got := read(other, member, stamp, true); !reflect.DeepEqual(page, got) {
				t.Fatalf("bounded metadata produced a different delta: %+v vs %+v", page, got)
			}
			if page.Next != legacy.Next || page.Head != legacy.Head || page.AuthzEpoch != stamp.AuthzEpoch || page.ResyncRequired || len(page.Transactions) != 1 {
				t.Fatalf("shadow changed cursor or authority: %+v", page)
			}
			tx := page.Transactions[0]
			if len(tx.Invalidations) != 0 || len(tx.Upserts) != 1 || tx.Upserts[0].Key.ID != issue.ID || !strings.Contains(string(tx.Upserts[0].Data), title) {
				t.Fatalf("shadow lost the authorized issue edit: %+v", tx)
			}
			formerWorkspace := stamp
			formerWorkspace.WorkspaceID = "former-workspace-id"
			for _, service := range []*server{f.service, other} {
				if wrong := read(service, member, formerWorkspace, true); !wrong.ResyncRequired || len(wrong.Transactions) != 0 {
					t.Fatalf("shadow reused a cursor from another workspace: %+v", wrong)
				}
			}

			authRequest[[]domain.IssuePermission](t, f.admin, http.MethodPut, f.host.URL+"/api/issues/"+f.secret.ID+"/permissions", map[string]any{"permissions": []any{}}, workspace, http.StatusOK)
			for _, service := range []*server{f.service, other} {
				page := read(service, guest, guestStamp, true)
				if !page.ResyncRequired || page.AuthzEpoch == guestStamp.AuthzEpoch || len(page.Transactions) != 0 {
					t.Fatalf("revoked issue grant was replayed: %+v", page)
				}
			}
		})
	}
}
