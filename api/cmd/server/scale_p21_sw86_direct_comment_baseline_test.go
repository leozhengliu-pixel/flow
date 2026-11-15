package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// The direct endpoints and issue-record aliases intentionally retain their
// different production middleware topology in this baseline.
func TestScaleP21SW86DirectCommentBaseline(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner := f.principals[0].user
	_, session, err := repo.Login(t.Context(), owner.Email, "p03-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "SW86 direct comment baseline", "teamId": f.public}, workspace, http.StatusCreated)
	commentID := idgen.New("sw86-comment-")
	stamp := time.Now().UTC().Add(-time.Minute)
	restore := store.ForceFullMutationsForTesting(true)
	err = repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw86_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Comments[issue.ID] = []domain.Comment{{
			ID: commentID, Version: 1, Body: "Baseline comment", User: owner,
			CreatedAt: stamp, Reactions: map[string][]string{},
		}}
		data.Activities[issue.ID] = []domain.ActivityEvent{}
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("owned comment baseline workspace missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	reset := func(t *testing.T) {
		t.Helper()
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
		}
		var data domain.Bootstrap
		if err := json.Unmarshal(saved, &data); err != nil {
			t.Fatal(err)
		}
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw86_reset", "", nil, func(next *domain.Bootstrap) error {
			*next = data
			return nil
		})
		restore()
		if err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
	}
	type outcome struct {
		Code int
		Body any
	}
	for _, test := range []struct {
		name, method, tail, pattern string
		body                        any
		handler                     func(*server, http.ResponseWriter, *http.Request)
		aliasCode                   int
	}{
		{"create", http.MethodPost, "", "", map[string]any{"body": "Created baseline comment"}, (*server).createComment, http.StatusCreated},
		{"update", http.MethodPatch, "/" + commentID, "/{commentId}", map[string]any{"body": "Updated baseline comment", "expectedVersion": 1}, (*server).updateComment, http.StatusOK},
		{"delete", http.MethodDelete, "/" + commentID, "/{commentId}", nil, (*server).deleteComment, http.StatusNoContent},
		{"reaction", http.MethodPost, "/" + commentID + "/reactions", "/{commentId}/reactions", map[string]any{"emoji": "like"}, (*server).toggleCommentReaction, http.StatusOK},
	} {
		for _, family := range []string{"issues", "issue-records"} {
			t.Run(test.name+"/"+family, func(t *testing.T) {
				var results []outcome
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					reset(t)
					before := scaleP21SW59SQL(t, f)
					decodes := source.WorkspaceMetadataDecodes()
					work := source.MetadataWorkUsedForTesting()
					request := f.literalRequest(owner, nil, test.body)
					request.Method = test.method
					request.URL.Path = "/api/" + family + "/" + issue.ID + "/comments" + test.tail
					request.Header.Set("Content-Type", "application/json")
					request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
					service := &server{store: source}
					mux := http.NewServeMux()
					call := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { test.handler(service, w, r) })
					if family == "issue-records" {
						call = service.issueRecordAlias(call)
					}
					mux.Handle(test.method+" /api/"+family+"/{id}/comments"+test.pattern, call)
					response := httptest.NewRecorder()
					started := time.Now().UTC()
					service.authenticate(mux).ServeHTTP(response, request)
					finished := time.Now().UTC()
					wantCode := test.aliasCode
					if family == "issues" {
						wantCode = http.StatusForbidden
					}
					if response.Code != wantCode {
						t.Fatalf("bounded=%t status=%d want=%d body=%s", source.BoundedMetadataEnabled(), response.Code, wantCode, response.Body.String())
					}
					var body any
					if response.Body.Len() > 0 {
						if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
							t.Fatal(err)
						}
					}
					if family == "issues" {
						if !reflect.DeepEqual(body, map[string]any{"error": "This resource is outside your teams"}) {
							t.Fatalf("direct refusal body changed: %s", response.Body.String())
						}
						if after := scaleP21SW59SQL(t, f); !reflect.DeepEqual(before, after) {
							t.Fatal("refused direct request changed issue, content or subscription rows")
						}
					} else if test.name == "delete" {
						if response.Body.Len() != 0 {
							t.Fatal("successful alias deletion returned a body")
						}
					} else {
						var result domain.Comment
						if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
							t.Fatal(err)
						}
						if result.User.ID != owner.ID {
							t.Fatal("alias response actor differs from authenticated owner")
						}
						object, ok := body.(map[string]any)
						if !ok {
							t.Fatal("alias response is not a comment object")
						}
						switch test.name {
						case "create":
							if !strings.HasPrefix(result.ID, "comment_") || result.Version != 1 || result.Body != "Created baseline comment" ||
								result.CreatedAt.Before(started) || result.CreatedAt.After(finished) {
								t.Fatal("alias create response changed", response.Body.String())
							}
							object["id"], object["createdAt"] = "<generated-comment>", "<generated-time>"
						case "update":
							if result.ID != commentID || result.Version != 2 || result.Body != "Updated baseline comment" ||
								result.EditedAt == nil || result.EditedAt.Before(started) || result.EditedAt.After(finished) {
								t.Fatal("alias update response changed", response.Body.String())
							}
							object["editedAt"] = "<generated-time>"
						case "reaction":
							if result.ID != commentID || result.Version != 1 || result.Body != "Baseline comment" ||
								!reflect.DeepEqual(result.Reactions, map[string][]string{"like": {owner.ID}}) {
								t.Fatal("alias reaction response changed", response.Body.String())
							}
						}
					}
					results = append(results, outcome{Code: response.Code, Body: body})
					if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != work || source.WorkspaceMetadataDecodes() != decodes) {
						t.Fatal("bounded HTTP baseline retained work or decoded full metadata")
					}
				}
				left, err := json.Marshal(results[0])
				if err != nil {
					t.Fatal(err)
				}
				right, err := json.Marshal(results[1])
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(left, right) {
					t.Fatalf("legacy/bounded baseline differs: %s / %s", left, right)
				}
				t.Logf("direct family=%s operation=%s status=%d matched exactly", family, test.name, results[0].Code)
			})
		}
	}
}
