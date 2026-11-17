package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// scaleP21SW200Outcome is the triage writer outcome plus every release and
// release pipeline metadata row in canonical collection order.
type scaleP21SW200Outcome struct {
	scaleP21TriageOutcome
	Releases, Pipelines [][]any
}

func scaleP21SW200Request(t *testing.T, f *scaleP21TriageWriterFixture, service *server, actor domain.User, key *domain.APIKey, ref string, ids []string, alias, cancelled bool) *httptest.ResponseRecorder {
	t.Helper()
	r := f.literalRequest(actor, key, issueReleasesInput{ReleaseIDs: ids})
	family := "issues"
	if alias {
		family = "issue-records"
	}
	r.Method = http.MethodPut
	r.URL.Path = "/api/" + family + "/" + ref + "/releases"
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", ref)
	if cancelled {
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		r = r.WithContext(ctx)
	}
	r, closeOutput := beginRequestResourceReads(r)
	defer closeOutput()
	service.resolveIssuePathReference(r)
	if _, resolved, _, found := issuePathReference(r.URL.Path); found {
		r.SetPathValue("id", resolved)
	}
	w := httptest.NewRecorder()
	if !service.authDisabled && !service.resourceAllowedLegacy(r, f.data.Workspace.URLKey, actor.ID) {
		writeError(w, http.StatusForbidden, "forbidden")
	} else if alias {
		service.issueRecordAlias(service.setIssueReleases)(w, r)
	} else {
		service.setIssueReleases(w, r)
	}
	return w
}

func (f *scaleP21TriageWriterFixture) sw200Run(t *testing.T, source string, call func(*server) *httptest.ResponseRecorder) (scaleP21SW200Outcome, time.Time) {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	var got scaleP21SW200Outcome
	var started time.Time
	got.scaleP21TriageOutcome, started = f.run(t, source, call)
	got.Releases = f.rows(t, `SELECT record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field='releases' ORDER BY collection_order,record_key`, workspace)
	got.Pipelines = f.rows(t, `SELECT record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field='releasePipelines' ORDER BY collection_order,record_key`, workspace)
	// Inline release collections live in the workspace root.
	root := f.rows(t, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace)
	got.Releases = append(got.Releases, root...)
	return got, started
}

func (f *scaleP21TriageWriterFixture) sw200Differential(t *testing.T, name string, call func(*server) *httptest.ResponseRecorder) []scaleP21SW200Outcome {
	t.Helper()
	var normalized []string
	var outcomes []scaleP21SW200Outcome
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started := f.sw200Run(t, source, call)
		outcomes = append(outcomes, got)
		normalized = append(normalized, scaleP21TriageNormalize(t, got, started))
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Fatalf("%s: %s changed: %s", name, source, scaleP21M2Diff(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

type scaleP21SW200Seed struct {
	prefix string
	at     time.Time
}

func (s scaleP21SW200Seed) id(name string) string { return s.prefix + name }

// scaleP21SW200Setup adds release pipelines and releases around the triage
// fixture's issues: open, frozen-stage and own-frozen releases, archived and
// private-pipeline releases, previously linked releases, a release whose ID
// is the issue ID, large descriptions and existing issue history.
func scaleP21SW200Setup(t *testing.T) (scaleP21TriageWriterFixture, scaleP21SW200Seed) {
	t.Helper()
	f := scaleP21TriageWriterSetup(t)
	seed := scaleP21SW200Seed{prefix: idgen.New("sw200-"), at: time.Date(2026, 2, 3, 4, 5, 6, 7, time.UTC)}
	owner := f.principals[0].user
	issue := f.triage.ID
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw200_seed", "", nil, func(data *domain.Bootstrap) error {
		at := seed.at
		data.ReleasePipelines = append(data.ReleasePipelines,
			domain.ReleasePipeline{ID: seed.id("open"), Name: "Open pipeline", TeamIDs: []string{f.public}, FrozenStages: []string{}, Stages: []string{"Open", "Frozen"}, CreatedAt: at, UpdatedAt: at},
			domain.ReleasePipeline{ID: seed.id("frozen"), Name: "Frozen pipeline", TeamIDs: []string{f.public}, FrozenStages: []string{"Frozen"}, Stages: []string{"Open", "Frozen"}, CreatedAt: at, UpdatedAt: at},
			domain.ReleasePipeline{ID: seed.id("private"), Name: "Private pipeline", TeamIDs: []string{f.private}, CreatedAt: at, UpdatedAt: at},
		)
		release := func(id, name string) domain.Release {
			return domain.Release{ID: id, Name: name, Creator: owner, CreatedAt: at, UpdatedAt: at, IssueIDs: []string{}}
		}
		add := release(seed.id("add"), "New release")
		add.PipelineID, add.Stage = seed.id("open"), "Open"
		second := release(seed.id("second"), "Second release")
		second.PipelineID, second.Stage = seed.id("open"), "Open"
		remove := release(seed.id("remove"), "Old release")
		remove.PipelineID, remove.Stage = seed.id("open"), "Open"
		remove.IssueIDs = []string{f.visible.ID, issue, f.hidden.ID}
		frozen := release(seed.id("frozen"), "Frozen stage")
		frozen.PipelineID, frozen.Stage = seed.id("frozen"), "Frozen"
		alreadyFrozen := release(seed.id("already-frozen"), "Existing frozen stage")
		alreadyFrozen.PipelineID, alreadyFrozen.Stage = seed.id("frozen"), "Frozen"
		alreadyFrozen.IssueIDs = []string{issue}
		ownFrozen := release(seed.id("own-frozen"), "Frozen release")
		ownFrozen.StageFrozenAt = &at
		archived := release(seed.id("archived"), "Archived release")
		archived.ArchivedAt = &at
		hidden := release(seed.id("private-release"), "Private pipeline release")
		hidden.PipelineID, hidden.Stage = seed.id("private"), "Open"
		large := release(seed.id("large"), "Large legal record")
		large.Description = strings.Repeat("legal selected release <>& ", 1<<12)
		unrelated := release(seed.id("unrelated"), "Unrelated")
		unrelated.Description = strings.Repeat("unrelated release ", 1<<12)
		unrelated.IssueIDs = []string{f.hidden.ID}
		collision := release(issue, "Aggregate id collision")
		data.Releases = append(data.Releases, collision, add, second, remove, frozen, alreadyFrozen, ownFrozen, archived, hidden, large, unrelated)
		for index := range data.Issues {
			if data.Issues[index].ID == issue {
				data.Activities[issue] = append(data.Activities[issue], domain.ActivityEvent{ID: seed.id("history"), Type: "issue.updated", Actor: owner,
					CreatedAt: time.Now().UTC().Add(-time.Minute), Metadata: map[string]string{"title": "Retain history"}})
				data.Comments[issue] = append(data.Comments[issue], domain.Comment{ID: seed.id("comment"), Body: "Retain comments", User: owner, CreatedAt: at})
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	full, found := f.repository.BootstrapFor(f.data.Workspace.URLKey)
	if !found {
		t.Fatal("release fixture missing")
	}
	var err error
	if f.baseline, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
	return f, seed
}

// Reloaded oracle views live until their owning fixture closes, so each
// scenario group runs on its own fixture.
func TestScaleP21SW200IssueReleasesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	for _, group := range []string{"records", "ordinary-development", "misc"} {
		t.Run(group, func(t *testing.T) { scaleP21SW200Group(t, group, -1) })
	}
	for principal := range 5 {
		t.Run(fmt.Sprintf("permission-matrix-%d", principal), func(t *testing.T) { scaleP21SW200Group(t, "matrix", principal) })
	}
}

func scaleP21SW200Group(t *testing.T, group string, principal int) {
	f, seed := scaleP21SW200Setup(t)
	owner := f.principals[0].user
	issue := f.triage.ID
	type releaseCase struct {
		name    string
		ref     string
		ids     []string
		code    int
		prepare func(*testing.T)
	}
	mutate := func(name string, edit func(*domain.Bootstrap)) func(*testing.T) {
		return func(t *testing.T) {
			if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw200_"+name, "", nil, func(data *domain.Bootstrap) error {
				edit(data)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []releaseCase{
		{name: "add", ids: []string{seed.id("remove"), seed.id("already-frozen"), seed.id("add")}, code: http.StatusOK},
		{name: "remove", ids: []string{seed.id("already-frozen")}, code: http.StatusOK},
		{name: "add-and-remove", ids: []string{seed.id("add"), seed.id("second")}, code: http.StatusOK},
		{name: "reorder-only", ids: []string{seed.id("already-frozen"), seed.id("remove")}, code: http.StatusOK},
		{name: "clear", ids: []string{}, code: http.StatusOK},
		{name: "null-list", ids: nil, code: http.StatusOK},
		{name: "frozen-stage-refused", ids: []string{seed.id("frozen")}, code: http.StatusConflict},
		{name: "own-frozen-refused", ids: []string{seed.id("own-frozen")}, code: http.StatusConflict},
		{name: "already-frozen-kept", ids: []string{seed.id("already-frozen"), seed.id("add")}, code: http.StatusOK},
		{name: "archived", ids: []string{seed.id("archived")}, code: http.StatusBadRequest},
		{name: "missing", ids: []string{seed.id("missing")}},
		{name: "duplicate-ids", ids: []string{seed.id("add"), seed.id("add")}, code: http.StatusBadRequest},
		{name: "duplicate-after-trim", ids: []string{seed.id("add"), " " + seed.id("add") + " "}},
		{name: "empty-id", ids: []string{""}},
		{name: "trimmed-id", ids: []string{" " + seed.id("add") + "\t"}},
		{name: "private-pipeline-release", ids: []string{seed.id("private-release")}},
		{name: "large-release", ids: []string{seed.id("large")}, code: http.StatusOK},
		{name: "aggregate-collision", ids: []string{issue}, code: http.StatusOK},
		{name: "missing-issue", ref: f.gone.ID, ids: []string{seed.id("add")}},
		{name: "many-releases", ids: func() []string {
			ids := []string{}
			for index := range 60 {
				ids = append(ids, seed.id(fmt.Sprintf("many-%02d", index)))
			}
			return ids
		}(), code: http.StatusOK, prepare: mutate("many", func(data *domain.Bootstrap) {
			for index := range 80 {
				release := domain.Release{ID: seed.id(fmt.Sprintf("many-%02d", index)), Name: fmt.Sprintf("Many %02d", index), CreatedAt: seed.at, UpdatedAt: seed.at,
					PipelineID: seed.id("open"), Stage: "Open"}
				if index%2 == 1 {
					release.IssueIDs = []string{issue}
				}
				data.Releases = append(data.Releases, release)
			}
		})},
		{name: "duplicate-occurrences", ids: []string{seed.id("add")}, code: http.StatusOK, prepare: mutate("duplicates", func(data *domain.Bootstrap) {
			// The first archived occurrence is skipped by validation but is
			// the response; the first live one decides freezing. Every
			// occurrence is edited and every old link removed.
			data.Releases = append([]domain.Release{{ID: seed.id("add"), Name: "First archived duplicate", ArchivedAt: &seed.at,
				PipelineID: seed.id("open"), CreatedAt: seed.at, UpdatedAt: seed.at}}, data.Releases...)
			data.Releases = append(data.Releases,
				domain.Release{ID: seed.id("remove"), Name: "Later duplicate", IssueIDs: []string{issue, issue}, CreatedAt: seed.at, UpdatedAt: seed.at},
				domain.Release{ID: "", Name: "Empty previous id", IssueIDs: []string{issue}, CreatedAt: seed.at, UpdatedAt: seed.at},
				domain.Release{ID: strings.Repeat("overlong", 600), Name: "Overlong previous id", IssueIDs: []string{issue}, CreatedAt: seed.at, UpdatedAt: seed.at},
			)
			data.ReleasePipelines = append(data.ReleasePipelines, domain.ReleasePipeline{ID: seed.id("open"), FrozenStages: []string{"Open"}})
		})},
		{name: "duplicate-pipeline-first-wins", ids: []string{seed.id("add")}, code: http.StatusConflict, prepare: mutate("pipelines", func(data *domain.Bootstrap) {
			data.ReleasePipelines = append([]domain.ReleasePipeline{{ID: seed.id("open"), FrozenStages: []string{"Open"}}}, data.ReleasePipelines...)
		})},
		{name: "recent-update-history", ids: []string{seed.id("add")}, code: http.StatusOK, prepare: mutate("history", func(data *domain.Bootstrap) {
			data.Activities[issue] = append(data.Activities[issue], domain.ActivityEvent{ID: seed.id("recent"), Type: "issue.releases_updated", Actor: owner,
				CreatedAt: seed.at.Add(time.Hour), Metadata: map[string]string{"releaseIds": seed.id("remove"), "added": "Old release", "removed": ""}})
		})},
	}
	for _, alias := range []bool{true, false} {
		family := "records"
		if !alias {
			family = "ordinary-development"
		}
		if family != group {
			continue
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				f.prepare = test.prepare
				defer func() { f.prepare = nil }()
				ref := test.ref
				if ref == "" {
					ref = issue
				}
				outcomes := f.sw200Differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
					service.authDisabled = !alias
					return scaleP21SW200Request(t, &f, service, owner, nil, ref, test.ids, alias, false)
				})
				if test.code != 0 && outcomes[0].Code != test.code {
					t.Fatalf("legacy status=%d want=%d body=%s", outcomes[0].Code, test.code, outcomes[0].Body)
				}
				written := 0
				if outcomes[0].Code == http.StatusOK && test.name != "reorder-only" {
					written = 1
				}
				if got := outcomes[0]; len(got.State.Events) != written || len(got.Webhooks) != written || len(got.Realtime) != written {
					t.Fatalf("legacy events/webhooks/realtime %d/%d/%d, want %d", len(got.State.Events), len(got.Webhooks), len(got.Realtime), written)
				}
			})
		}
	}
	if group == "matrix" {
		if principal >= len(f.principals) {
			return
		}
		name := f.principals[principal].name
		f.matrix(t, func(t *testing.T, actor domain.User, key *domain.APIKey) {
			if actor.ID != f.principals[principal].user.ID {
				return
			}
			for _, target := range []string{issue, f.hidden.ID} {
				for _, ids := range [][]string{{seed.id("add")}, {seed.id("private-release")}, {}} {
					f.sw200Differential(t, fmt.Sprintf("M/%s/%s/%v", name, target, ids), func(service *server) *httptest.ResponseRecorder {
						return scaleP21SW200Request(t, &f, service, actor, key, target, ids, true, false)
					})
				}
			}
		})
		return
	}
	if group != "misc" {
		return
	}
	t.Run("old-identifier", func(t *testing.T) {
		f.prepare = mutate("old_identifier", func(data *domain.Bootstrap) {
			for index := range data.Issues {
				if data.Issues[index].ID == issue {
					data.Issues[index].PreviousIdentifiers = []string{"SWOLD-200"}
				}
			}
		})
		defer func() { f.prepare = nil }()
		f.sw200Differential(t, "old-identifier", func(service *server) *httptest.ResponseRecorder {
			return scaleP21SW200Request(t, &f, service, owner, nil, "SWOLD-200", []string{seed.id("add")}, true, false)
		})
	})
	t.Run("cancelled", func(t *testing.T) {
		f.sw200Differential(t, "cancelled", func(service *server) *httptest.ResponseRecorder {
			return scaleP21SW200Request(t, &f, service, owner, nil, issue, []string{seed.id("add")}, true, true)
		})
	})
	t.Run("legal-large-issue", func(t *testing.T) {
		f.prepare = mutate("large_issue", func(data *domain.Bootstrap) {
			for index := range data.Issues {
				if data.Issues[index].ID == issue {
					data.Issues[index].Description = strings.Repeat("exact rich body ", 70000)
				}
			}
		})
		defer func() { f.prepare = nil }()
		outcomes := f.sw200Differential(t, "legal-large-issue", func(service *server) *httptest.ResponseRecorder {
			return scaleP21SW200Request(t, &f, service, owner, nil, issue, []string{seed.id("add")}, true, false)
		})
		if outcomes[0].Code != http.StatusOK {
			t.Fatalf("legacy large issue status=%d body=%s", outcomes[0].Code, outcomes[0].Body)
		}
	})
	t.Run("ordinary-authenticated-legacy-gate", func(t *testing.T) {
		outcomes := f.sw200Differential(t, "gate", func(service *server) *httptest.ResponseRecorder {
			return scaleP21SW200Request(t, &f, service, owner, nil, issue, []string{seed.id("add")}, false, false)
		})
		if len(outcomes[0].State.Events) != 0 {
			t.Fatal("authenticated ordinary route gate changed")
		}
	})
}

// Concurrent release writers on one issue serialize: the bounded state equals
// one of the legacy sequential orders.
func TestScaleP21SW200IssueReleasesConcurrent(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, seed := scaleP21SW200Setup(t)
	owner := f.principals[0].user
	first := func(service *server) *httptest.ResponseRecorder {
		return scaleP21SW200Request(t, &f, service, owner, nil, f.triage.ID, []string{seed.id("add")}, true, false)
	}
	second := func(service *server) *httptest.ResponseRecorder {
		return scaleP21SW200Request(t, &f, service, owner, nil, f.visible.ID, []string{seed.id("add"), seed.id("second")}, true, false)
	}
	expected := map[string]bool{}
	for _, order := range [][2]func(*server) *httptest.ResponseRecorder{{first, second}, {second, first}} {
		got, started := f.sw200Run(t, "legacy", func(service *server) *httptest.ResponseRecorder {
			order[0](service)
			order[1](service)
			return nil
		})
		got.Webhooks, got.Realtime = nil, nil
		expected[scaleP21TriageNormalize(t, got, started)] = true
	}
	for _, source := range []string{"warm", "reloaded"} {
		for attempt := range 3 {
			got, started := f.sw200Run(t, source, func(service *server) *httptest.ResponseRecorder {
				done := make(chan int, 2)
				for _, call := range []func(*server) *httptest.ResponseRecorder{first, second} {
					go func() { done <- call(service).Code }()
				}
				for range 2 {
					if code := <-done; code != http.StatusOK {
						t.Errorf("concurrent release writer status %d", code)
					}
				}
				return nil
			})
			got.Webhooks, got.Realtime = nil, nil
			if !expected[scaleP21TriageNormalize(t, got, started)] {
				t.Fatalf("%s attempt %d: concurrent state matches no sequential legacy order", source, attempt)
			}
		}
	}
}
