package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21HotPreferenceReviewVisible(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		metadata, found := repo.WorkspaceMetadata(workspace)
		if !found {
			t.Fatal("owned workspace missing")
		}
		private := ""
		for _, project := range metadata.Projects {
			if project.ID == "m2-unrelated" {
				private = project.TeamIDs[0]
			}
		}
		if private == "" {
			t.Fatal("private-team fixture missing")
		}
		privateAdmin := authClient(t)
		authRequest[domain.AuthSession](t, privateAdmin, http.MethodPost, f.host.URL+"/api/auth/login", map[string]string{"email": f.principals[1].user.Email, "password": "p03-fixture-password"}, "", http.StatusOK)
		publicIssue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Review public", "teamId": f.public}, workspace, http.StatusCreated)
		privateIssue := authRequest[domain.Issue](t, privateAdmin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Review private", "teamId": private}, workspace, http.StatusCreated)
		guest := f.principals[3].user
		authRequest[[]domain.IssuePermission](t, privateAdmin, http.MethodPut, f.host.URL+"/api/issues/"+privateIssue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": guest.ID, "role": "viewer"}}}, workspace, http.StatusOK)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_preference_review.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Reviews = []domain.CodeReview{
				{ID: "first", SlugID: "collision", IssueIDs: []string{"missing"}},
				{ID: "collision"},
				{ID: "duplicate", IssueIDs: []string{"missing"}},
				{ID: "duplicate"},
				{ID: "open", SlugID: "current-slug", Description: strings.Repeat("large selected body ", 20000)},
				{ID: "public", SlugID: "public-slug", IssueIDs: []string{publicIssue.ID}},
				{ID: "private", SlugID: "private-slug", IssueIDs: []string{privateIssue.ID}},
				{ID: "both", IssueIDs: []string{privateIssue.ID, publicIssue.ID}},
				{ID: "typed\x00id", SlugID: "typed\x00slug"},
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "reviews")
		call := func(source *store.SQLiteStore, user domain.User, key *domain.APIKey, ref string, observe func(string)) bool {
			t.Helper()
			r := f.literalRequest(user, key, nil)
			r.URL.Path = "/api/favorites/review/" + ref
			if observe != nil {
				r = r.WithContext(store.WithSQLStatementObserver(r.Context(), observe))
			}
			return (&server{store: source}).preferenceReviewVisible(r, ref)
		}
		var counts [2]int
		for mode, source := range []*store.SQLiteStore{repo, f.probe} {
			if !call(source, f.data.Viewer, nil, "public", func(string) { counts[mode]++ }) {
				t.Fatal("admin SQL measurement did not see linked review")
			}
			t.Logf("review preference bounded=%t SQL statements=%d", source.BoundedMetadataEnabled(), counts[mode])
		}
		if counts[1] != counts[0] {
			t.Fatalf("installed review lookup added SQL: off=%d bounded=%d", counts[0], counts[1])
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, ref := range []string{"open", "current-slug", "public", "public-slug", "private", "private-slug", "both", "collision", "duplicate", "typed\x00id", "typed\x00slug", "typedid", "typedslug", "OPEN", "open ", "missing", ""} {
				legacy := call(repo, user, key, ref, nil)
				bounded := call(f.probe, user, key, ref, nil)
				if legacy != bounded {
					t.Fatalf("reference=%q visibility changed: off=%t bounded=%t", ref, legacy, bounded)
				}
				if (ref == "collision" || ref == "duplicate" || ref == "missing") && bounded {
					t.Fatalf("reference=%q skipped first match or invented missing review", ref)
				}
				if ref == "public" && key != nil && key.TeamRestriction == "selected" && len(key.TeamIDs) == 0 && bounded {
					t.Fatal("selected-empty key gained linked review visibility")
				}
				if ref == "private" && user.ID == guest.ID && key == nil && !bounded {
					t.Fatal("guest explicit issue grant lost review visibility")
				}
			}
		})
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			for _, spec := range []struct {
				ref  string
				want bool
			}{{"open", true}, {"public", false}} {
				r := f.literalRequest(f.data.Viewer, nil, nil)
				r.URL.Path = "/api/favorites/review/" + spec.ref
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				if got := (&server{store: source, authDisabled: true}).preferenceReviewVisible(r.WithContext(ctx), spec.ref); got != spec.want {
					t.Fatalf("auth-disabled canceled reference=%q visible=%t want=%t", spec.ref, got, spec.want)
				}
			}
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "Review foreign", idgen.New("p21-review-foreign-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		if err := repo.MutateWorkspace(t.Context(), foreign.Workspace.URLKey, "scale_p21_preference_review.foreign", "", nil, func(data *domain.Bootstrap) error {
			data.Reviews = []domain.CodeReview{{ID: "open"}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, foreign.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		member := f.principals[len(f.principals)-1].user
		for _, phase := range []string{"cross-workspace", "queued-revocation", "replay-revocation", "cancelled"} {
			key := workspace
			if phase == "cross-workspace" {
				key = foreign.Workspace.URLKey
			}
			if phase == "queued-revocation" {
				if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
					t.Fatal(err)
				}
			}
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r := f.literalRequest(member, nil, nil)
				r.Header.Set("X-Workspace-Key", key)
				r.URL.Path = "/api/favorites/review/open"
				if phase == "cancelled" {
					ctx, cancel := context.WithCancel(r.Context())
					cancel()
					r = r.WithContext(ctx)
				}
				if (&server{store: source}).preferenceReviewVisible(r, "open") {
					t.Fatalf("%s gained review visibility", phase)
				}
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 || before != scaleP21M3Fingerprint(t, repo, workspace, "reviews") {
			t.Fatal("review predicate decoded full metadata or changed canonical rows")
		}
	}
}
