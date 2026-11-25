package main

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"flow/api/internal/domain"
)

// SW494: reviews, releases, release pipelines, customers, customer requests
// and asks by ID, with their create/reorder roots and body references.
func scaleP21SW494Table(t *testing.T, part, parts int) {
	t.Helper()
	fx := scaleP21SW491Setup(t, nil)
	p, workspace := fx.prefix, fx.workspace
	host, err := url.Parse(fx.f.host.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	privateAdmin := fx.f.principals[1].user
	jar.SetCookies(host, []*http.Cookie{{Name: sessionCookieName, Value: fx.tokens[privateAdmin.ID]}})
	privateClient := &http.Client{Jar: jar}
	publicIssue := authRequest[domain.Issue](t, fx.f.admin, http.MethodPost, fx.f.host.URL+"/api/issue-records", map[string]any{"title": "SW494 public", "teamId": fx.public}, workspace, http.StatusCreated)
	privateIssue := authRequest[domain.Issue](t, privateClient, http.MethodPost, fx.f.host.URL+"/api/issue-records", map[string]any{"title": "SW494 private", "teamId": fx.privateTeam}, workspace, http.StatusCreated)
	if err := fx.repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw494_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Reviews = append(data.Reviews,
			domain.CodeReview{ID: p + "rev-public", SlugID: p + "rev-public-slug", Provider: "github", Title: "a", IssueIDs: []string{publicIssue.ID}},
			domain.CodeReview{ID: p + "rev-private", Provider: "github", Title: "b", IssueIDs: []string{privateIssue.ID}},
			domain.CodeReview{ID: p + "rev-mixed", Provider: "github", Title: "c", IssueIDs: []string{privateIssue.ID, publicIssue.ID}},
			domain.CodeReview{ID: p + "rev-gone", Provider: "github", Title: "d", IssueIDs: []string{p + "gone-issue"}},
			domain.CodeReview{ID: p + "rev-dup", Provider: "github", Title: "e", IssueIDs: []string{privateIssue.ID}},
			domain.CodeReview{ID: p + "rev-dup", Provider: "github", Title: "f"},
		)
		data.ReleasePipelines = append(data.ReleasePipelines,
			domain.ReleasePipeline{ID: p + "pipe-open", Name: "open", TeamIDs: []string{}},
			domain.ReleasePipeline{ID: p + "pipe-public", Name: "public", TeamIDs: []string{fx.public}},
			domain.ReleasePipeline{ID: p + "pipe-private", Name: "private", TeamIDs: []string{fx.privateTeam}},
			domain.ReleasePipeline{ID: p + "pipe-mixed", Name: "mixed", TeamIDs: []string{fx.privateTeam, fx.public}},
			domain.ReleasePipeline{ID: p + "pipe-dup", Name: "dup-private", TeamIDs: []string{fx.privateTeam}},
			domain.ReleasePipeline{ID: p + "pipe-dup", Name: "dup-public", TeamIDs: []string{fx.public}},
		)
		data.Releases = append(data.Releases,
			domain.Release{ID: p + "rel-open", Name: "open", PipelineID: ""},
			domain.Release{ID: p + "rel-public", Name: "public", PipelineID: p + "pipe-public"},
			domain.Release{ID: p + "rel-private", Name: "private", PipelineID: p + "pipe-private"},
			domain.Release{ID: p + "rel-dangling", Name: "dangling", PipelineID: p + "pipe-missing"},
			domain.Release{ID: p + "rel-dup-pipe", Name: "dup", PipelineID: p + "pipe-dup"},
		)
		data.Customers = append(data.Customers, domain.Customer{ID: p + "cust", Name: "cust"}, domain.Customer{ID: p + "cust-b", Name: "cust-b"})
		data.CustomerRequests = append(data.CustomerRequests,
			domain.CustomerRequest{ID: p + "cr-plain", CustomerID: p + "cust", Body: "x"},
			domain.CustomerRequest{ID: p + "cr-unknown", Body: "x"},
			domain.CustomerRequest{ID: p + "cr-missing-customer", CustomerID: p + "cust-gone", Body: "x"},
			domain.CustomerRequest{ID: p + "cr-public-issue", CustomerID: p + "cust", IssueID: publicIssue.ID, Body: "x"},
			domain.CustomerRequest{ID: p + "cr-private-issue", CustomerID: p + "cust", IssueID: privateIssue.ID, Body: "x"},
			domain.CustomerRequest{ID: p + "cr-public-project", CustomerID: p + "cust", ProjectID: fx.ids["pub-project"], Body: "x"},
			domain.CustomerRequest{ID: p + "cr-private-project", CustomerID: p + "cust", ProjectID: "m2-unrelated", Body: "x"},
			domain.CustomerRequest{ID: p + "cr-slug-project", CustomerID: p + "cust", ProjectID: p + "pub-current", Body: "x"},
		)
		data.Asks = append(data.Asks,
			domain.Ask{ID: p + "ask-open", Title: "open"},
			domain.Ask{ID: p + "ask-public", Title: "public", TeamID: fx.public},
			domain.Ask{ID: p + "ask-private", Title: "private", TeamID: fx.privateTeam},
			domain.Ask{ID: p + "ask-child", Title: "child", TeamID: fx.childTeam},
			domain.Ask{ID: p + "ask-dup", Title: "dup-private", TeamID: fx.privateTeam},
			domain.Ask{ID: p + "ask-dup", Title: "dup-open"},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	row := func(name, method, pattern, path, body string) scaleP21SW491Row {
		return scaleP21SW491Row{name, method, pattern, path, body}
	}
	rows := []scaleP21SW491Row{}
	for _, ref := range []string{"rev-public", "rev-private", "rev-mixed", "rev-gone", "rev-dup", "rev-public-slug", "rev-missing"} {
		rows = append(rows,
			row("review-get-"+ref, http.MethodGet, "GET /api/reviews/{id}", "/api/reviews/"+p+ref, ""),
			row("review-comment-"+ref, http.MethodPost, "POST /api/reviews/{id}/comments", "/api/reviews/"+p+ref+"/comments", `{}`),
		)
	}
	rows = append(rows,
		row("review-patch", http.MethodPatch, "PATCH /api/reviews/{id}", "/api/reviews/"+p+"rev-mixed", `{"title":"x"}`),
		row("review-previews", http.MethodPut, "PUT /api/reviews/{id}/previews", "/api/reviews/"+p+"rev-private/previews", `{}`),
		row("review-comment-patch", http.MethodPatch, "PATCH /api/reviews/{id}/comments/{commentId}", "/api/reviews/"+p+"rev-public/comments/c", `{}`),
	)
	for _, ref := range []string{"rel-open", "rel-public", "rel-private", "rel-dangling", "rel-dup-pipe", "rel-missing"} {
		rows = append(rows,
			row("release-get-"+ref, http.MethodGet, "GET /api/releases/{id}", "/api/releases/"+p+ref, ""),
			row("release-delete-"+ref, http.MethodDelete, "DELETE /api/releases/{id}", "/api/releases/"+p+ref, ""),
		)
	}
	rows = append(rows,
		row("release-history", http.MethodGet, "GET /api/releases/{id}/history", "/api/releases/"+p+"rel-private/history", ""),
		row("release-notes-post", http.MethodPost, "POST /api/releases/{id}/notes", "/api/releases/"+p+"rel-public/notes", `{}`),
		row("release-note-delete", http.MethodDelete, "DELETE /api/releases/{id}/notes/{noteId}", "/api/releases/"+p+"rel-open/notes/n", ""),
		row("release-patch-plain", http.MethodPatch, "PATCH /api/releases/{id}", "/api/releases/"+p+"rel-open", `{"name":"x"}`),
		row("release-patch-pipeline", http.MethodPatch, "PATCH /api/releases/{id}", "/api/releases/"+p+"rel-open", `{"pipelineId":" `+p+`pipe-private "}`),
		row("release-patch-projects", http.MethodPatch, "PATCH /api/releases/{id}", "/api/releases/"+p+"rel-public", `{"projectIds":["m2-unrelated",""]}`),
		row("release-patch-issues", http.MethodPatch, "PATCH /api/releases/{id}", "/api/releases/"+p+"rel-public", `{"issueIds":["`+privateIssue.ID+`"]}`),
		row("release-patch-invalid", http.MethodPatch, "PATCH /api/releases/{id}", "/api/releases/"+p+"rel-public", `{"issueIds":1}`),
		row("release-create-plain", http.MethodPost, "POST /api/releases", "/api/releases", `{"name":"x"}`),
		row("release-create-pipeline", http.MethodPost, "POST /api/releases", "/api/releases", `{"pipelineId":"`+p+`pipe-mixed"}`),
		row("release-create-project-slug", http.MethodPost, "POST /api/releases", "/api/releases", `{"projectIds":["`+p+`pub-old"]}`),
		row("release-create-issue", http.MethodPost, "POST /api/releases", "/api/releases", `{"issueIds":["`+publicIssue.ID+`"]}`),
		row("release-reorder", http.MethodPost, "POST /api/releases/reorder", "/api/releases/reorder", `{"ids":["`+p+`rel-open","`+p+`rel-private"]}`),
		row("release-reorder-pipeline", http.MethodPost, "POST /api/releases/reorder", "/api/releases/reorder", `{"pipelineId":"`+p+`pipe-public","ids":["`+p+`rel-public"]}`),
		row("release-reorder-invalid", http.MethodPost, "POST /api/releases/reorder", "/api/releases/reorder", `x`),
	)
	for _, ref := range []string{"pipe-open", "pipe-public", "pipe-private", "pipe-mixed", "pipe-dup", "pipe-missing"} {
		rows = append(rows,
			row("pipeline-get-"+ref, http.MethodGet, "GET /api/release-pipelines/{id}", "/api/release-pipelines/"+p+ref, ""),
			row("pipeline-key-"+ref, http.MethodPost, "POST /api/release-pipelines/{id}/access-key", "/api/release-pipelines/"+p+ref+"/access-key", ""),
		)
	}
	rows = append(rows,
		row("pipeline-delete", http.MethodDelete, "DELETE /api/release-pipelines/{id}", "/api/release-pipelines/"+p+"pipe-private", ""),
		row("pipeline-key-delete", http.MethodDelete, "DELETE /api/release-pipelines/{id}/access-key", "/api/release-pipelines/"+p+"pipe-public/access-key", ""),
		row("pipeline-patch-teams", http.MethodPatch, "PATCH /api/release-pipelines/{id}", "/api/release-pipelines/"+p+"pipe-open", `{"teamIds":["`+fx.privateTeam+`"," "]}`),
		row("pipeline-patch-plain", http.MethodPatch, "PATCH /api/release-pipelines/{id}", "/api/release-pipelines/"+p+"pipe-public", `{"name":"x"}`),
		row("pipeline-create-public", http.MethodPost, "POST /api/release-pipelines", "/api/release-pipelines", `{"teamIds":["`+fx.public+`"]}`),
		row("pipeline-create-private", http.MethodPost, "POST /api/release-pipelines", "/api/release-pipelines", `{"teamIds":["`+fx.privateTeam+`"]}`),
		row("pipeline-reorder", http.MethodPost, "POST /api/release-pipelines/reorder", "/api/release-pipelines/reorder", `{"ids":["`+p+`pipe-open","`+p+`pipe-mixed"]}`),
	)
	for _, ref := range []string{"cust", "cust-missing"} {
		rows = append(rows,
			row("customer-get-"+ref, http.MethodGet, "GET /api/customers/{id}", "/api/customers/"+p+ref, ""),
			row("customer-patch-"+ref, http.MethodPatch, "PATCH /api/customers/{id}", "/api/customers/"+p+ref, `{}`),
			row("customer-delete-"+ref, http.MethodDelete, "DELETE /api/customers/{id}", "/api/customers/"+p+ref, ""),
			row("customer-merge-"+ref, http.MethodPost, "POST /api/customers/{id}/merge", "/api/customers/"+p+ref+"/merge", `{}`),
		)
	}
	for _, ref := range []string{"cr-plain", "cr-unknown", "cr-missing-customer", "cr-public-issue", "cr-private-issue", "cr-public-project", "cr-private-project", "cr-slug-project", "cr-missing"} {
		rows = append(rows,
			row("request-delete-"+ref, http.MethodDelete, "DELETE /api/customer-requests/{id}", "/api/customer-requests/"+p+ref, ""),
		)
	}
	rows = append(rows,
		row("request-archive", http.MethodPost, "POST /api/customer-requests/{id}/archive", "/api/customer-requests/"+p+"cr-public-issue/archive", ""),
		row("request-unarchive", http.MethodDelete, "DELETE /api/customer-requests/{id}/archive", "/api/customer-requests/"+p+"cr-private-issue/archive", ""),
		row("request-attach", http.MethodPost, "POST /api/customer-requests/{id}/attachments", "/api/customer-requests/"+p+"cr-plain/attachments", `{}`),
		row("request-detach", http.MethodDelete, "DELETE /api/customer-requests/{id}/attachments/{attachmentId}", "/api/customer-requests/"+p+"cr-unknown/attachments/a", ""),
		row("request-patch-plain", http.MethodPatch, "PATCH /api/customer-requests/{id}", "/api/customer-requests/"+p+"cr-plain", `{"body":"y"}`),
		row("request-patch-customer", http.MethodPatch, "PATCH /api/customer-requests/{id}", "/api/customer-requests/"+p+"cr-plain", `{"customerId":"`+p+`cust-missing"}`),
		row("request-patch-issue", http.MethodPatch, "PATCH /api/customer-requests/{id}", "/api/customer-requests/"+p+"cr-plain", `{"issueId":"`+privateIssue.ID+`"}`),
		row("request-patch-project", http.MethodPatch, "PATCH /api/customer-requests/{id}", "/api/customer-requests/"+p+"cr-unknown", `{"projectId":"m2-unrelated"}`),
		row("request-patch-invalid", http.MethodPatch, "PATCH /api/customer-requests/{id}", "/api/customer-requests/"+p+"cr-unknown", `[`),
		row("request-create-plain", http.MethodPost, "POST /api/customer-requests", "/api/customer-requests", `{"body":"x"}`),
		row("request-create-customer", http.MethodPost, "POST /api/customer-requests", "/api/customer-requests", `{"customerId":" `+p+`cust "}`),
		row("request-create-issue", http.MethodPost, "POST /api/customer-requests", "/api/customer-requests", `{"issueId":"`+publicIssue.ID+`"}`),
		row("request-create-project", http.MethodPost, "POST /api/customer-requests", "/api/customer-requests", `{"projectId":"`+p+`pub-old"}`),
	)
	for _, ref := range []string{"ask-open", "ask-public", "ask-private", "ask-child", "ask-dup", "ask-missing"} {
		rows = append(rows,
			row("ask-delete-"+ref, http.MethodDelete, "DELETE /api/asks/{id}", "/api/asks/"+p+ref, ""),
			row("ask-decision-"+ref, http.MethodPost, "POST /api/asks/{id}/decision", "/api/asks/"+p+ref+"/decision", `{}`),
		)
	}
	rows = append(rows,
		row("ask-patch", http.MethodPatch, "PATCH /api/asks/{id}", "/api/asks/"+p+"ask-private", `{}`),
		row("ask-create-open", http.MethodPost, "POST /api/asks", "/api/asks", `{"title":"x"}`),
		row("ask-create-private", http.MethodPost, "POST /api/asks", "/api/asks", `{"teamId":"`+fx.privateTeam+`"}`),
		row("ask-create-invalid", http.MethodPost, "POST /api/asks", "/api/asks", `{"teamId":3}`),
	)
	fx.runPart(t, "SW494", rows, []scaleP21SW491Control{
		{"review-get-rev-public/member-private-outsider/session", true},
		{"review-get-rev-private/member-private-outsider/session", false},
		{"review-get-rev-private/admin/session", true},
		{"review-get-rev-mixed/member-private-outsider/session", true},
		{"review-get-rev-dup/member-private-outsider/session", true},
		{"review-get-rev-gone/admin/session", false},
		{"review-get-rev-public-slug/admin/session", false},
		{"release-get-rel-open/guest-explicit/session", true},
		{"release-get-rel-private/member-private-outsider/session", false},
		{"release-get-rel-private/admin/session", true},
		{"release-get-rel-dangling/admin/session", false},
		{"release-get-rel-dup-pipe/member-private-outsider/session", true},
		{"pipeline-get-pipe-mixed/member-private-outsider/session", true},
		{"pipeline-get-pipe-private/member-private-outsider/session", false},
		{"customer-get-cust/member-private-outsider/session", true},
		{"customer-get-cust/guest-explicit/session", false},
		{"request-delete-cr-plain/member-private-outsider/session", true},
		{"request-delete-cr-private-issue/member-private-outsider/session", false},
		{"request-delete-cr-private-project/member-private-outsider/session", false},
		{"request-delete-cr-missing-customer/admin/session", false},
		{"ask-delete-ask-open/guest-explicit/session", true},
		{"ask-delete-ask-private/member-private-outsider/session", false},
		{"ask-delete-ask-dup/member-private-outsider/session", true},
		{"ask-create-private/member-private-outsider/session", false},
	}, part, parts)
}

// The SW494 table runs in 3 top-level tests (every third row each) so the
// CI shards spread it: on PostgreSQL and MySQL the whole table took 15-21 minutes.
func TestScaleP21SW494RecordKindAuthorizationDifferential(t *testing.T) { scaleP21SW494Table(t, 0, 3) }

func TestScaleP21SW494RecordKindAuthorizationDifferentialPart2(t *testing.T) {
	scaleP21SW494Table(t, 1, 3)
}

func TestScaleP21SW494RecordKindAuthorizationDifferentialPart3(t *testing.T) {
	scaleP21SW494Table(t, 2, 3)
}
