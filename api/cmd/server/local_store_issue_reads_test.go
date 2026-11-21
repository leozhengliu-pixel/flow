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
	"flow/api/internal/store"
)

func TestLocalStoreIssueReadsDifferential(t *testing.T) {
	enableLocalStoreShadowTest(t)
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			fixture := newScaleP12Fixture(t, repo)
			workspace, team := fixture.data.Workspace.URLKey, fixture.data.Teams[0].ID
			t.Setenv("FLOW_LOCAL_STORE_WORKSPACES", workspace)
			t.Setenv("FLOW_LOCAL_STORE_RESUME_WORKSPACES", workspace)
			label := authRequest[domain.IssueLabel](t, fixture.admin, http.MethodPost, fixture.host.URL+"/api/labels", map[string]string{"name": "Summary label", "color": "#676f88"}, workspace, http.StatusCreated)
			created := authRequest[domain.Issue](t, fixture.admin, http.MethodPost, fixture.host.URL+"/api/issue-records", map[string]any{
				"teamId": team, "title": "Issue with label", "description": "Body is loaded separately", "labelIds": []string{label.ID},
			}, workspace, http.StatusCreated)
			bounded, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			member := fixture.principals[2]
			stamp, ok := fixture.service.localStoreAuthority(member.request(workspace), workspace)
			if !ok {
				t.Fatal("missing member authority")
			}
			request := func(principal scaleP12Principal, path string, args url.Values, body string) *http.Request {
				r := principal.request(workspace)
				r.URL.Path = path
				query := r.URL.Query()
				query.Set("surface", "local-store")
				query.Set("workspaceId", stamp.WorkspaceID)
				query.Set("authzEpoch", stamp.AuthzEpoch)
				for key, value := range args {
					query[key] = value
				}
				r.URL.RawQuery = query.Encode()
				r.Header.Set(localStoreHeader, principal.user.ID)
				if body != "" {
					r.Method = http.MethodPost
					r.Body = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).Body
				}
				return r
			}
			read := func(service *server, r *http.Request) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				if r.Method == http.MethodPost {
					service.syncBatch(w, r)
				} else {
					service.syncRecords(w, r)
				}
				return w
			}
			pageArgs := url.Values{"kind": {localStoreIssueKind}, "limit": {"1"}, "maxBytes": {"8192"}}
			batchBody := `{"projection":"summary","keys":[{"kind":"issues","id":` + string(mustJSON(t, created.ID)) + `},{"kind":"issues","id":` + string(mustJSON(t, fixture.secret.ID)) + `},{"kind":"issues","id":"unknown"}]}`
			var previous store.RecordBatch
			for _, service := range []*server{fixture.service, {store: bounded}} {
				args := url.Values{"kind": {localStoreIssueKind}, "limit": {"1"}, "maxBytes": {"8192"}}
				seen := map[string]store.MetadataRecord{}
				for pages := 0; ; pages++ {
					w := read(service, request(member, "/api/sync/records", args, ""))
					var page store.RecordPage
					if err := json.Unmarshal(w.Body.Bytes(), &page); w.Code != http.StatusOK || err != nil || w.Body.Len() > 8192 || page.AuthzEpoch != stamp.AuthzEpoch || len(page.Items) > 1 || pages > 10 {
						t.Fatalf("invalid issue page %d: %d %s", pages, w.Code, w.Body.String())
					}
					for _, record := range page.Items {
						if _, duplicate := seen[record.Key.ID]; duplicate {
							t.Fatal("issue page duplicated an ID")
						}
						seen[record.Key.ID] = record
					}
					if !page.HasMore {
						break
					}
					args.Set("cursor", page.Next)
					args.Set("revision", page.Revision)
				}
				if seen[fixture.open.ID].Key.ID == "" || seen[created.ID].Key.ID == "" || seen[fixture.secret.ID].Key.ID != "" || seen[fixture.descendant.ID].Key.ID != "" {
					t.Fatal("issue page changed the visible issue set")
				}
				w := read(service, request(member, "/api/sync/batch", nil, batchBody))
				var batch store.RecordBatch
				if err := json.Unmarshal(w.Body.Bytes(), &batch); w.Code != http.StatusOK || err != nil || batch.AuthzEpoch != stamp.AuthzEpoch || len(batch.Items) != 3 {
					t.Fatalf("invalid batch: %d %s", w.Code, w.Body.String())
				}
				if previous.Items != nil && !reflect.DeepEqual(batch, previous) {
					t.Fatal("bounded mode changes the issue batch")
				}
				previous = batch
				if !reflect.DeepEqual(seen[created.ID], batch.Items[0]) || !batch.Items[1].Missing || batch.Items[1].Version != "" || len(batch.Items[1].Data) != 0 || !batch.Items[2].Missing {
					t.Fatal("batch and page disagree or disclose an inaccessible issue")
				}
				var summary domain.Issue
				item := batch.Items[0]
				if err := json.Unmarshal(item.Data, &summary); err != nil || !summary.IsSummary || summary.Description != "" || len(summary.Labels) != 1 || summary.Labels[0].ID != label.ID || item.Version == "0" || item.Version == "" {
					t.Fatalf("issue summary lost its label or version: %+v", item)
				}
				for _, principal := range []scaleP12Principal{fixture.principals[len(fixture.principals)-1], {user: domain.User{ID: "no-member"}}} {
					if w := read(service, request(principal, "/api/sync/records", pageArgs, "")); w.Code != http.StatusForbidden {
						t.Fatalf("unentitled principal received %d", w.Code)
					}
				}
				args.Set("revision", "0:0")
				if w := read(service, request(member, "/api/sync/records", args, "")); w.Code != http.StatusConflict {
					t.Fatalf("stale issue revision received %d", w.Code)
				}
			}
			if err := repo.SetTeamMembership(t.Context(), stamp.WorkspaceID, fixture.private, fixture.principals[4].user.ID, "member", false); err != nil {
				t.Fatal(err)
			}
			if w := read(fixture.service, request(member, "/api/sync/batch", nil, batchBody)); w.Code != http.StatusForbidden {
				t.Fatalf("accepted a stale epoch: %d", w.Code)
			}
			stamp, ok = fixture.service.localStoreAuthority(member.request(workspace), workspace)
			if !ok {
				t.Fatal("public team member lost workspace access")
			}
			for _, title := range []string{"First offline edit", "Latest offline edit"} {
				authRequest[domain.Issue](t, fixture.admin, http.MethodPatch, fixture.host.URL+"/api/issues/"+created.ID, map[string]string{"title": title}, workspace, http.StatusOK)
			}
			readChanges := func(service *server) localStoreIssueDelta {
				t.Helper()
				r := request(member, "/api/sync/changes", url.Values{"after": {stamp.Head}, "mode": {"issues"}}, "")
				w := httptest.NewRecorder()
				service.syncChanges(w, r)
				var delta localStoreIssueDelta
				if err := json.Unmarshal(w.Body.Bytes(), &delta); w.Code != http.StatusOK || err != nil {
					t.Fatalf("invalid delta: %d %s", w.Code, w.Body.String())
				}
				return delta
			}
			first := readChanges(fixture.service)
			second := readChanges(&server{store: bounded})
			if !reflect.DeepEqual(first, second) || first.ResyncRequired || first.HasMore || first.Next == stamp.Head || len(first.Transactions) != 2 {
				t.Fatalf("repeated edits failed to replay: %+v vs %+v", first, second)
			}
			for _, tx := range first.Transactions {
				if len(tx.Invalidations) != 0 || len(tx.Upserts) != 1 {
					t.Fatalf("latest summary was not replayed: %+v", tx)
				}
				var issue domain.Issue
				if err := json.Unmarshal(tx.Upserts[0].Data, &issue); err != nil || issue.Title != "Latest offline edit" || issue.Description != "" || len(issue.Labels) != 1 {
					t.Fatal("replayed summary lost display data or included the body")
				}
			}
			authRequest[domain.Issue](t, fixture.admin, http.MethodPatch, fixture.host.URL+"/api/issues/"+created.ID, map[string]string{"teamId": fixture.private}, workspace, http.StatusOK)
			for _, service := range []*server{fixture.service, {store: bounded}} {
				moved := readChanges(service)
				for _, tx := range moved.Transactions {
					if len(tx.Upserts)+len(tx.Removals) != 0 {
						t.Fatalf("revealed a key after the issue moved out of scope: %+v", tx)
					}
				}
				if !moved.ResyncRequired && (len(moved.Transactions) == 0 || len(moved.Transactions[0].Invalidations) == 0) {
					t.Fatal("moving out of scope could leave an old visible issue in the store")
				}
			}
		})
	}
}
