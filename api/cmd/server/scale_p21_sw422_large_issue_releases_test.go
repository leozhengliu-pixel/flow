package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// SW422: setIssueReleases on releases at the literal writer's maxima (a
// release with 300,000 linked issues, descriptions up to the size the
// literal transaction accepts and just above it), on both issue routes,
// against the literal writer byte for byte (response, release rows, root,
// issue rows, activities, events, webhooks and realtime), warm and
// SQL-reloaded, including a peer that commits to the large release row
// after the copies were installed (merged, or conflicting and retried).
func TestScaleP21SW422LargeIssueReleasesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	f, seed := scaleP21SW200Setup(t)
	owner := f.principals[0].user
	issue := f.triage.ID
	workspace := f.data.Workspace.URLKey
	large := seed.id("large")
	// peer rewrites the large release row directly, as a writer in another
	// process would, after both copies were installed.
	peer := func(edit func(*domain.Release)) func(*testing.T) {
		return func(t *testing.T) {
			rows := f.rows(t, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='releases' AND record_key='`+large+`'`, workspace)
			if len(rows) != 1 {
				t.Fatal("large release row missing")
			}
			var release domain.Release
			if err := json.Unmarshal([]byte(rows[0][0].(string)), &release); err != nil {
				t.Fatal(err)
			}
			edit(&release)
			raw, err := json.Marshal(release)
			if err != nil {
				t.Fatal(err)
			}
			_ = f.rows(t, `SELECT 1 FROM workspace_states WHERE workspace_key=?`, workspace)
			if _, err := f.reader.reader.ExecContext(context.Background(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='releases' AND record_key=?`, raw, workspace, large); err != nil {
				t.Fatal(err)
			}
		}
	}
	var growPlain func(description, count int, linked bool, raw int, plain bool) func(*testing.T)
	// grow sets the large release's description and links count issues
	// over several transactions: the release issue index takes one row per
	// linked issue, so a release with very many issues grew in steps.
	grow := func(description, count int, linked bool, raw int) func(*testing.T) {
		return growPlain(description, count, linked, raw, false)
	}
	growPlain = func(description, count int, linked bool, raw int, plain bool) func(*testing.T) {
		return func(t *testing.T) {
			const step = 50_000
			for done := 0; ; done += step {
				upto := min(done+step, count)
				if err := f.repository.MutateWorkspace(t.Context(), workspace, "scale_p21.sw422_grow", "", nil, func(data *domain.Bootstrap) error {
					for index := range data.Releases {
						if data.Releases[index].ID != large {
							continue
						}
						release := &data.Releases[index]
						if done == 0 {
							release.Description = scaleP21SW421Body("legal release notes", description)
							if plain {
								release.Description = strings.Repeat("plain release notes ", description/20)
							}
							release.PipelineID, release.Stage = seed.id("open"), "Open"
						}
						for number := done; number < upto; number++ {
							release.IssueIDs = append(release.IssueIDs, fmt.Sprintf("sw422-linked-%07d", number))
						}
						if linked && upto >= count {
							release.IssueIDs = append(release.IssueIDs, issue)
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if upto >= count {
					break
				}
			}
			if raw > 0 {
				// Above what the literal writer accepts in one
				// transaction: an earlier write path left it.
				peer(func(release *domain.Release) {
					release.Description = scaleP21SW421Body("legal release notes", raw)
				})(t)
				if err := f.repository.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	cases := []struct {
		name    string
		prepare func(*testing.T)
		peer    func(*testing.T)
		ids     []string
		code    int
	}{
		{name: "add-to-many-issues", prepare: grow(64<<10, 300_000, false, 0), ids: []string{large}, code: http.StatusOK},
		{name: "remove-from-many-issues", prepare: grow(64<<10, 300_000, true, 0), ids: []string{}, code: http.StatusOK},
		{name: "add-large-description-7mib", prepare: grow(7<<20, 1000, false, 0), ids: []string{large, seed.id("add")}, code: http.StatusOK},
		{name: "add-large-description-at-max", prepare: grow(10<<20, 1000, false, 0), ids: []string{large}, code: http.StatusOK},
		{name: "remove-large-description-at-max", prepare: grow(10<<20, 1000, true, 0), ids: []string{seed.id("add")}, code: http.StatusOK},
		{name: "add-plain-description-14mb", prepare: growPlain(14_000_000, 1000, false, 0, true), ids: []string{large}, code: http.StatusOK},
		{name: "add-large-description-over", prepare: grow(64<<10, 1000, false, 13<<20), ids: []string{large}},
		{name: "peer-added-issue", prepare: grow(4<<20, 50_000, false, 0), ids: []string{large}, code: http.StatusOK,
			peer: peer(func(release *domain.Release) { release.IssueIDs = append(release.IssueIDs, "sw422-peer-issue") })},
		{name: "peer-renamed", prepare: grow(4<<20, 1000, false, 0), ids: []string{large}, code: http.StatusOK,
			peer: peer(func(release *domain.Release) { release.Name = "Peer renamed release" })},
	}
	for _, alias := range []bool{true, false} {
		for _, test := range cases {
			t.Run(fmt.Sprintf("%s/alias=%t", test.name, alias), func(t *testing.T) {
				f.prepare = test.prepare
				defer func() { f.prepare = nil }()
				outcomes := f.sw200Differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
					if test.peer != nil {
						test.peer(t)
					}
					service.authDisabled = !alias
					stopPeak := scaleP21SW421Peak(service.store)
					w := scaleP21SW200Request(t, &f, service, owner, nil, issue, test.ids, alias, false)
					t.Logf("%s alias=%t bounded=%t status=%d peak_work=%d", test.name, alias, service.store.BoundedMetadataEnabled(), w.Code, stopPeak())
					return w
				})
				got := outcomes[0]
				if test.code != 0 && got.Code != test.code {
					t.Fatalf("legacy status=%d want=%d body=%.300s", got.Code, test.code, got.Body)
				}
				if test.code == 0 && got.Code < http.StatusInternalServerError {
					t.Fatalf("legacy accepted the over-limit release: status=%d body=%.300s", got.Code, got.Body)
				}
				t.Logf("%s alias=%t legacy status=%d body=%.200s", test.name, alias, got.Code, got.Body)
			})
		}
	}
}
