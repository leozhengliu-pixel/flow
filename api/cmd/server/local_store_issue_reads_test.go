package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestLocalStoreIssueReadSeparatesSourceRaceFromRevocation(t *testing.T) {
	enableLocalStoreShadowTest(t)
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for driver, repo := range scaleServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := setupPolicyDifferential(t, repo)
			workspace := f.data.Workspace.URLKey
			member := f.principals[2].user
			r := f.literalRequest(member, nil, nil)
			access, err := f.service.resolveSyncAccess(r)
			if err != nil {
				t.Fatal(err)
			}
			query := r.URL.Query()
			query.Set("authzEpoch", access.AuthzEpoch)
			query.Set("after", "0")
			r.URL.RawQuery = query.Encode()
			page := store.CommittedPage{
				Next: "1", Head: "1", AuthzEpoch: access.AuthzEpoch,
				Transactions: []store.CommittedChange{{TxID: "projected", Seq: "1", Invalidations: []string{"issues"}}},
			}
			reads := 0
			journal := scaleJournalFunc(func(context.Context, string, string, int, int) (store.CommittedPage, error) {
				reads++
				if reads == 1 {
					authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
						map[string]string{"title": "Created while projecting the journal", "teamId": f.public}, workspace, http.StatusCreated)
					version, epoch, err := repo.RealtimeSourceVersionAndEpoch(r.Context(), workspace)
					if err != nil || version == access.SourceVersion() || epoch != access.AuthzEpoch {
						t.Fatalf("expected a new source version in the same epoch, got %v %q %v from %v %q", version, epoch, err, access.SourceVersion(), access.AuthzEpoch)
					}
				}
				return page, nil
			})
			w := httptest.NewRecorder()
			f.service.localStoreIssueChanges(w, r, journal, access.WorkspaceID)
			var delta localStoreIssueDelta
			if err := json.Unmarshal(w.Body.Bytes(), &delta); w.Code != http.StatusOK || err != nil ||
				reads != 2 || delta.AuthzEpoch != access.AuthzEpoch || delta.ResyncRequired || delta.Next != page.Next || len(delta.Transactions) != 1 {
				t.Fatalf("a concurrent issue write must retry with the same authority: %d (%d reads) %s", w.Code, reads, w.Body.String())
			}
			journal = scaleJournalFunc(func(context.Context, string, string, int, int) (store.CommittedPage, error) {
				if err := repo.SetTeamMembership(t.Context(), access.WorkspaceID, f.public, member.ID, "member", false); err != nil {
					t.Fatal(err)
				}
				return page, nil
			})
			w = httptest.NewRecorder()
			f.service.localStoreIssueChanges(w, r, journal, access.WorkspaceID)
			if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "transactions") {
				t.Fatalf("an in-flight revocation must not disclose the projected page: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestLocalStoreIssueReadsDifferential(t *testing.T) {
	enableLocalStoreShadowTest(t)
	for driver, repo := range scaleServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			fixture := newScaleFixture(t, repo)
			workspace, team := fixture.data.Workspace.URLKey, fixture.data.Teams[0].ID
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
			request := func(principal scalePrincipal, path string, args url.Values, body string) *http.Request {
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
				for _, principal := range []scalePrincipal{fixture.principals[len(fixture.principals)-1], {user: domain.User{ID: "no-member"}}} {
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

func TestLocalStoreIssueChangesIgnorePulseCursor(t *testing.T) {
	enableLocalStoreShadowTest(t)
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for driver, repo := range scaleServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			fixture := resourceAccessScaleFixture(t, repo)
			workspace := fixture.data.Workspace.URLKey
			principal := scalePrincipal{user: fixture.data.Viewer}
			issue := authRequest[domain.Issue](t, fixture.admin, http.MethodPost, fixture.host.URL+"/api/issue-records", map[string]string{
				"title": "Open issue", "teamId": fixture.data.Teams[0].ID,
			}, workspace, http.StatusCreated)
			authRequest[domain.UserSettings](t, fixture.admin, http.MethodPatch, fixture.host.URL+"/api/account/settings",
				map[string]string{"pulseSchedule": "daily"}, workspace, http.StatusOK)
			bounded, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			for i, service := range []*server{fixture.service, {store: bounded}} {
				r := principal.request(workspace)
				access, err := service.resolveSyncAccess(r)
				if err != nil {
					t.Fatal(err)
				}
				head, err := repo.RecordJournalHead(t.Context(), access.WorkspaceID)
				if err != nil {
					t.Fatal(err)
				}
				end := time.Date(2026, time.November, 23+i, 6, 0, 0, 0, time.UTC)
				if service.store.BoundedMetadataEnabled() {
					err = service.store.CommitPulseSummaries(t.Context(), workspace, []store.PulseSummaryCandidate{{
						UserID: principal.user.ID, Schedule: "daily", End: end,
					}}, scalePulseDispatch)
				} else {
					err = scalePulseLegacyWrite(t.Context(), repo, workspace, []pulseDue{{
						user: principal.user, schedule: "daily", end: end,
					}}, end.Add(time.Hour))
				}
				if err != nil {
					t.Fatal(err)
				}
				authRequest[domain.Issue](t, fixture.admin, http.MethodPatch, fixture.host.URL+"/api/issue-records/"+issue.ID, map[string]string{"title": "Edited after Pulse " + strconv.Itoa(i)}, workspace, http.StatusOK)
				query := r.URL.Query()
				query.Set("surface", "local-store")
				query.Set("mode", "issues")
				query.Set("workspaceId", access.WorkspaceID)
				query.Set("authzEpoch", access.AuthzEpoch)
				query.Set("after", head)
				r.URL.Path, r.URL.RawQuery = "/api/sync/changes", query.Encode()
				r.Header.Set(localStoreHeader, principal.user.ID)
				w := httptest.NewRecorder()
				service.syncChanges(w, r)
				var delta localStoreIssueDelta
				if err := json.Unmarshal(w.Body.Bytes(), &delta); w.Code != http.StatusOK || err != nil ||
					delta.AuthzEpoch != access.AuthzEpoch || delta.ResyncRequired || len(delta.Transactions) != 2 {
					t.Fatalf("expected the pulse cursor and issue edit (bounded=%t, epoch=%s): %d %s", service.store.BoundedMetadataEnabled(), access.AuthzEpoch, w.Code, w.Body.String())
				}
				if !reflect.DeepEqual(delta.Transactions[0].Invalidations, []string{localStorePulseCursor}) ||
					len(delta.Transactions[0].Upserts) != 0 || len(delta.Transactions[1].Upserts) != 1 ||
					delta.Transactions[1].Upserts[0].Key.ID != issue.ID {
					t.Fatalf("unrelated cursor changed the issue replay: %+v", delta.Transactions)
				}
				current, err := service.resolveSyncAccess(r)
				if err != nil || delta.Revision != localStoreIssueRevision(current) || current.SourceVersion().Metadata != access.SourceVersion().Metadata+1 {
					t.Fatalf("expected one metadata revision: from %v, got %q (%v)", access.SourceVersion(), delta.Revision, err)
				}
			}
		})
	}
}

func TestLocalStoreIssueChangesIgnoreNotificationSideEffects(t *testing.T) {
	enableLocalStoreShadowTest(t)
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for driver, repo := range scaleServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			fixture := resourceAccessScaleFixture(t, repo)
			workspace, team := fixture.data.Workspace.URLKey, fixture.data.Teams[0].ID
			editor, user := resourceAccessScaleJoin(t, fixture, "Editor", "local-store-editor@example.test", "member", team)
			issue := authRequest[domain.Issue](t, fixture.admin, http.MethodPost, fixture.host.URL+"/api/issue-records",
				map[string]string{"title": "Owner's issue", "teamId": team}, workspace, http.StatusCreated)
			principal := scalePrincipal{user: user}
			r := principal.request(workspace)
			access, err := fixture.service.resolveSyncAccess(r)
			if err != nil {
				t.Fatal(err)
			}
			head, err := repo.RecordJournalHead(t.Context(), access.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			updated := authRequest[domain.Issue](t, editor, http.MethodPatch, fixture.host.URL+"/api/issue-records/"+issue.ID,
				map[string]string{"title": "Member's edit"}, workspace, http.StatusOK)
			raw, err := repo.ReadCommittedChanges(t.Context(), access.WorkspaceID, head, 100, syncV2DeltaBytes)
			if err != nil {
				t.Fatal(err)
			}
			if len(raw.Transactions) != 1 {
				t.Fatalf("expected one journal transaction for the edit, got %d", len(raw.Transactions))
			}
			kinds := map[string]bool{}
			for _, record := range raw.Transactions[0].Records {
				kinds[record.Key.Kind] = true
			}
			if !kinds["issues"] || !kinds["notifications"] || !kinds["notificationDeliveries"] {
				t.Fatalf("edit fixture must write an issue and send the owner a notification: %v", kinds)
			}

			current, err := fixture.service.resolveSyncAccess(principal.request(workspace))
			if err != nil {
				t.Fatal(err)
			}
			if current.AuthzEpoch != access.AuthzEpoch || current.SourceVersion().Metadata != access.SourceVersion().Metadata {
				t.Fatal("notification side effects must leave directory authority and revision unchanged")
			}
			query := r.URL.Query()
			query.Set("surface", "local-store")
			query.Set("mode", "issues")
			query.Set("workspaceId", access.WorkspaceID)
			query.Set("authzEpoch", access.AuthzEpoch)
			query.Set("after", head)
			r.URL.Path, r.URL.RawQuery = "/api/sync/changes", query.Encode()
			r.Header.Set(localStoreHeader, user.ID)
			bounded, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			for _, service := range []*server{fixture.service, {store: bounded}} {
				w := httptest.NewRecorder()
				service.syncChanges(w, r)
				var delta localStoreIssueDelta
				if err := json.Unmarshal(w.Body.Bytes(), &delta); w.Code != http.StatusOK || err != nil ||
					delta.ResyncRequired || delta.Next != raw.Next || delta.Revision != localStoreIssueRevision(current) || len(delta.Transactions) != 1 {
					t.Fatalf("expected the edit delta at the new checkpoint (bounded=%t): %d %s", service.store.BoundedMetadataEnabled(), w.Code, w.Body.String())
				}
				tx := delta.Transactions[0]
				if len(tx.Invalidations) != 0 || len(tx.Removals) != 0 || len(tx.Upserts) != 1 || tx.Upserts[0].Key.ID != issue.ID {
					t.Fatalf("expected just the visible issue upsert: %+v", tx)
				}
				var summary domain.Issue
				if err := json.Unmarshal(tx.Upserts[0].Data, &summary); err != nil || summary.Title != updated.Title || summary.Version != updated.Version {
					t.Fatalf("projected title or version differed from the edit: %v", err)
				}
			}
		})
	}
}
