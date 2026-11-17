package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW210Request(t *testing.T, f *scaleP21TriageWriterFixture, service *server, actor domain.User, key *domain.APIKey, issue, target, kind string, alias, cancelled bool) *httptest.ResponseRecorder {
	t.Helper()
	r := f.literalRequest(actor, key, domain.RelationCreateInput{RelatedIssueID: target, Type: kind})
	family := "issues"
	if alias {
		family = "issue-records"
	}
	r.URL.Path = "/api/" + family + "/" + issue + "/relations"
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", issue)
	if cancelled {
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		r = r.WithContext(ctx)
	}
	r, closeOutput := beginRequestResourceReads(r)
	defer closeOutput()
	var statements atomic.Int64
	var peakWork atomic.Int64
	trace := os.Getenv("FLOW_P21_SW210_SQL_TRACE") != ""
	type queryTrace struct {
		sql  string
		work int64
	}
	var queries []queryTrace
	r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(query string) {
		statements.Add(1)
		work := int64(service.store.MetadataWorkUsedForTesting())
		for previous := peakWork.Load(); work > previous && !peakWork.CompareAndSwap(previous, work); previous = peakWork.Load() {
		}
		if trace {
			queries = append(queries, queryTrace{query, work})
		}
	}))
	service.resolveIssuePathReference(r)
	if _, resolved, _, found := issuePathReference(r.URL.Path); found {
		r.SetPathValue("id", resolved)
	}
	w := httptest.NewRecorder()
	if !service.authDisabled && !service.resourceAllowedLegacy(r, f.data.Workspace.URLKey, actor.ID) {
		writeError(w, http.StatusForbidden, "forbidden")
	} else if alias {
		service.issueRecordAlias(service.createRelation)(w, r)
	} else {
		service.createRelation(w, r)
	}
	t.Logf("relation bounded=%t alias=%t SQL=%d peak_work_at_SQL=%d", service.store.BoundedMetadataEnabled(), alias, statements.Load(), peakWork.Load())
	for index, query := range queries {
		t.Logf("SW210SQL bounded=%t alias=%t ordinal=%d work=%d query=%q", service.store.BoundedMetadataEnabled(), alias, index+1, query.work, query.sql)
	}
	return w
}

func scaleP21SW210Setup(t *testing.T) (scaleP21TriageWriterFixture, string) {
	t.Helper()
	f := scaleP21TriageWriterSetup(t)
	prefix := idgen.New("sw210-")
	parentID := prefix + "old-parent"
	owner := f.principals[0].user
	stamp := time.Date(2026, 2, 3, 4, 5, 6, 7, time.UTC)
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw210_seed", "", nil, func(data *domain.Bootstrap) error {
		parent := f.visible
		parent.ID, parent.Identifier, parent.Title = parentID, prefix+"OLD", "Previous relation parent"
		parent.Relations, parent.ParentID, parent.SubIssueIDs = nil, nil, []string{f.visible.ID, f.visible.ID}
		data.Issues = append(data.Issues, parent)
		for index := range data.Issues {
			issue := &data.Issues[index]
			if issue.ID == f.triage.ID || issue.ID == f.visible.ID {
				issue.Relations = []domain.IssueRelation{{ID: prefix + "retained-" + issue.ID, Type: "related", IssueID: issue.ID, RelatedIssueID: parentID}}
				issue.Version, issue.UpdatedAt = 73, stamp
				issue.Description = "Retained full description"
				data.Comments[issue.ID] = []domain.Comment{{ID: prefix + "comment-" + issue.ID, Body: "Retain existing comments", User: owner, CreatedAt: stamp}}
				data.Activities[issue.ID] = []domain.ActivityEvent{{ID: prefix + "activity-" + issue.ID, Type: "issue.updated", Actor: owner, CreatedAt: stamp, Metadata: map[string]string{"title": "Retain history"}}}
			}
			if issue.ID == f.visible.ID {
				issue.ParentID = &parentID
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	full, found := f.repository.BootstrapFor(f.data.Workspace.URLKey)
	if !found {
		t.Fatal("relation fixture missing")
	}
	var err error
	f.baseline, err = json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	return f, prefix
}

func TestScaleP21SW210IssueRelationRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	// Reloaded oracle views live until their owning fixture closes. Keep each
	// scenario group's cleanup ahead of the next group's source allocations.
	for _, alias := range []bool{false, true} {
		name := "ordinary-development"
		if alias {
			name = "records"
		}
		t.Run(name, func(t *testing.T) {
			f, prefix := scaleP21SW210Setup(t)
			owner := f.principals[0].user
			for _, kind := range []string{"related", "blocks", "blocked_by", "duplicate", "parent_of", "sub_issue_of"} {
				t.Run(kind, func(t *testing.T) {
					outcomes := f.differential(t, kind, func(service *server) *httptest.ResponseRecorder {
						service.authDisabled = !alias
						return scaleP21SW210Request(t, &f, service, owner, nil, f.triage.ID, f.visible.ID, kind, alias, false)
					})
					if outcomes[0].Code != http.StatusCreated {
						t.Fatalf("legacy relation status=%d body=%s", outcomes[0].Code, outcomes[0].Body)
					}
					if len(outcomes[0].State.Events) != 1 || len(outcomes[0].Webhooks) != 1 || len(outcomes[0].Realtime) != 1 {
						t.Fatal("relation did not retain event/publication cardinality")
					}
				})
			}
			for _, test := range []struct {
				name, target, kind string
				cancelled          bool
			}{
				{"self", f.triage.ID, "related", false},
				{"invalid-kind", f.visible.ID, "not-a-relation", false},
				{"missing-target", prefix + "missing", "related", false},
				{"cancelled-before-auth", f.visible.ID, "related", true},
			} {
				t.Run(test.name, func(t *testing.T) {
					outcomes := f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
						service.authDisabled = !alias
						return scaleP21SW210Request(t, &f, service, owner, nil, f.triage.ID, test.target, test.kind, alias, test.cancelled)
					})
					if outcomes[0].Code == http.StatusCreated || len(outcomes[0].State.Events) != 0 {
						t.Fatal("refused relation wrote an event")
					}
				})
			}
		})
	}
	t.Run("ordinary-authenticated-legacy-gate", func(t *testing.T) {
		f, _ := scaleP21SW210Setup(t)
		owner := f.principals[0].user
		// The current authenticated legacy route projects a paged issue list
		// and refuses this path. Keep that behavior; development above reaches
		// the literal ordinary handler through its existing auth bypass.
		for _, kind := range []string{"related", "blocks", "blocked_by", "duplicate", "parent_of", "sub_issue_of"} {
			outcomes := f.differential(t, kind, func(service *server) *httptest.ResponseRecorder {
				return scaleP21SW210Request(t, &f, service, owner, nil, f.triage.ID, f.visible.ID, kind, false, false)
			})
			if outcomes[0].Code != http.StatusForbidden || len(outcomes[0].State.Events) != 0 {
				t.Fatal("authenticated ordinary legacy gate changed")
			}
		}
	})
	t.Run("parent-cycle", func(t *testing.T) {
		f, _ := scaleP21SW210Setup(t)
		owner := f.principals[0].user
		f.prepare = func(t *testing.T) {
			if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw210_cycle", "", nil, func(data *domain.Bootstrap) error {
				issue, err := issueByID(data, f.triage.ID)
				if err != nil {
					return err
				}
				id := f.visible.ID
				issue.ParentID = &id
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		defer func() { f.prepare = nil }()
		outcomes := f.differential(t, "parent-cycle", func(service *server) *httptest.ResponseRecorder {
			return scaleP21SW210Request(t, &f, service, owner, nil, f.triage.ID, f.visible.ID, "parent_of", true, false)
		})
		if outcomes[0].Code != http.StatusBadRequest || len(outcomes[0].State.Events) != 0 {
			t.Fatalf("legacy cycle status=%d body=%s", outcomes[0].Code, outcomes[0].Body)
		}
	})
	t.Run("legal-large-selected-record", func(t *testing.T) {
		f, _ := scaleP21SW210Setup(t)
		owner := f.principals[0].user
		f.prepare = func(t *testing.T) {
			if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw210_large", "", nil, func(data *domain.Bootstrap) error {
				issue, err := issueByID(data, f.triage.ID)
				if err != nil {
					return err
				}
				issue.Description = strings.Repeat("exact rich body ", 70000)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		defer func() { f.prepare = nil }()
		outcomes := f.differential(t, "legal-large-selected-record", func(service *server) *httptest.ResponseRecorder {
			return scaleP21SW210Request(t, &f, service, owner, nil, f.triage.ID, f.visible.ID, "related", true, false)
		})
		if outcomes[0].Code != http.StatusCreated {
			t.Fatalf("legacy large relation status=%d body=%s", outcomes[0].Code, outcomes[0].Body)
		}
	})
	t.Run("permission-matrix", func(t *testing.T) {
		f, _ := scaleP21SW210Setup(t)
		f.matrix(t, func(t *testing.T, actor domain.User, key *domain.APIKey) {
			for _, target := range []string{f.visible.ID, f.hidden.ID} {
				f.differential(t, "M/"+target, func(service *server) *httptest.ResponseRecorder {
					return scaleP21SW210Request(t, &f, service, actor, key, f.triage.ID, target, "related", true, false)
				})
			}
		})
	})
	for _, length := range []int{64, 65} {
		t.Run(fmt.Sprintf("literal-ancestor-boundary-%d", length), func(t *testing.T) {
			f, prefix := scaleP21SW210Setup(t)
			owner := f.principals[0].user
			f.prepare = func(t *testing.T) {
				if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw210_ancestors", "", nil, func(data *domain.Bootstrap) error {
					source, err := issueByID(data, f.triage.ID)
					if err != nil {
						return err
					}
					first := fmt.Sprintf("%sancestor-%d", prefix, 0)
					source.ParentID = &first
					for index := 0; index < length; index++ {
						ancestor := f.triage
						ancestor.ID = fmt.Sprintf("%sancestor-%d", prefix, index)
						ancestor.Identifier = fmt.Sprintf("%sANCESTOR-%d", prefix, index)
						ancestor.Relations, ancestor.ParentID, ancestor.SubIssueIDs = nil, nil, nil
						if index+1 < length {
							next := fmt.Sprintf("%sancestor-%d", prefix, index+1)
							ancestor.ParentID = &next
						}
						data.Issues = append(data.Issues, ancestor)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			defer func() { f.prepare = nil }()
			outcomes := f.differential(t, "literal-ancestor-boundary", func(service *server) *httptest.ResponseRecorder {
				// The middleware's independent policy walk has its own tested
				// limit. Exercise the writer's literal 64-level limit directly.
				r := f.literalRequest(owner, nil, domain.RelationCreateInput{RelatedIssueID: f.visible.ID, Type: "parent_of"})
				r.SetPathValue("id", f.triage.ID)
				r, closeOutput := beginRequestResourceReads(r)
				defer closeOutput()
				w := httptest.NewRecorder()
				service.createRelation(w, r)
				return w
			})
			want := http.StatusCreated
			if length == 65 {
				want = http.StatusInternalServerError
			}
			if outcomes[0].Code != want {
				t.Fatalf("literal ancestor limit status=%d want=%d body=%s", outcomes[0].Code, want, outcomes[0].Body)
			}
		})
	}
}
