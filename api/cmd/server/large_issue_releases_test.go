package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// SetIssueReleases on releases at the literal writer's maxima (a
// release with 300,000 linked issues, descriptions up to the size the
// literal transaction accepts and just above it), on both issue routes,
// against the literal writer byte for byte (response, release rows, root,
// issue rows, activities, events, webhooks and realtime), warm and
// SQL-reloaded, including a peer that commits to the large release row
// after the copies were installed (merged, or conflicting and retried).
func largeIssueReleasesRun(t *testing.T, names ...string) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	f, seed := issueReleasesSetup(t)
	owner := f.principals[0].user
	issue := f.triage.ID
	workspace := f.data.Workspace.URLKey
	large := seed.id("large")
	largeRow := func(t *testing.T, edit func(*domain.Release)) []byte {
		t.Helper()
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
		return raw
	}
	writeLarge := func(t *testing.T, raw []byte) {
		t.Helper()
		metadataReloadSourceExec(t, f.reader, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='releases' AND record_key=?`, raw, workspace, large)
	}
	// The peer rewrites the row after both copies were installed.
	peer := func(edit func(*domain.Release)) func(*testing.T) {
		return func(t *testing.T) { writeLarge(t, largeRow(t, edit)) }
	}
	editLarge := func(data *domain.Bootstrap, edit func(*domain.Release)) {
		for index := range data.Releases {
			if data.Releases[index].ID == large {
				edit(&data.Releases[index])
				return
			}
		}
	}
	type largeIssueReleasesCase struct {
		name                    string
		description, links, raw int
		linked, plain           bool
		peer                    func(*testing.T)
		ids                     []string
		code                    int
	}
	cases := []largeIssueReleasesCase{
		{name: "add-to-many-issues", description: 64 << 10, links: 300_000, ids: []string{large}, code: http.StatusOK},
		{name: "remove-from-many-issues", description: 64 << 10, links: 300_000, linked: true, ids: []string{}, code: http.StatusOK},
		{name: "add-large-description-7mib", description: 7 << 20, links: 1000, ids: []string{large, seed.id("add")}, code: http.StatusOK},
		{name: "add-large-description-at-max", description: 10 << 20, links: 1000, ids: []string{large}, code: http.StatusOK},
		{name: "remove-large-description-at-max", description: 10 << 20, links: 1000, linked: true, ids: []string{seed.id("add")}, code: http.StatusOK},
		{name: "add-plain-description-14mb", description: 14_000_000, links: 1000, plain: true, ids: []string{large}, code: http.StatusOK},
		{name: "add-large-description-over", description: 64 << 10, links: 1000, raw: 13 << 20, ids: []string{large}},
		{name: "peer-added-issue", description: 4 << 20, links: 50_000, ids: []string{large}, code: http.StatusOK,
			peer: peer(func(release *domain.Release) { release.IssueIDs = append(release.IssueIDs, "sw422-peer-issue") })},
		{name: "peer-renamed", description: 4 << 20, links: 1000, ids: []string{large}, code: http.StatusOK,
			peer: peer(func(release *domain.Release) { release.Name = "Peer renamed release" })},
	}
	for _, name := range names {
		index := slices.IndexFunc(cases, func(test largeIssueReleasesCase) bool { return test.name == name })
		if index < 0 {
			t.Fatal("unknown SW422 case", name)
		}
		test := cases[index]
		t.Run(name, func(t *testing.T) {
			var description string
			if test.plain {
				description = strings.Repeat("plain release notes ", test.description/20)
			} else {
				description = loopNextRunBody("legal release notes", test.description)
			}
			links := make([]string, test.links)
			for number := range links {
				links[number] = fmt.Sprintf("sw422-linked-%07d", number)
			}
			if test.linked {
				links = append(links, issue)
			}
			describe := func(release *domain.Release) {
				release.Description, release.PipelineID, release.Stage = description, seed.id("open"), "Open"
			}
			baseline := f.baseline
			defer func() {
				f.baseline, f.editBaseline, f.prepare = baseline, nil, nil
			}()
			const step = 50_000
			if test.links > step {
				// Grow the large index in batches once. Subsequent resets
				// restore only the link changed by the request.
				f.reader.query(t, `SELECT 1 FROM workspace_states WHERE workspace_key=?`, workspace).Close()
				t.Cleanup(func() {
					statement := `DELETE FROM release_issue_filter_records WHERE workspace_key=? AND release_id=?`
					if f.reader.config.Driver == "postgres" || f.reader.config.Driver == "postgresql" {
						statement = scaleDB{driver: "postgres"}.rebind(statement)
					}
					if _, err := f.reader.reader.ExecContext(context.Background(), statement, workspace, large); err != nil {
						t.Errorf("clean up large release index: %v", err)
					}
				})
				for start := 0; start < test.links; start += step {
					end := min(start+step, test.links)
					if end == test.links {
						end = len(links)
					}
					if err := f.repository.MutateWorkspace(t.Context(), workspace, "scale_p21.sw422_grow", "", nil, func(data *domain.Bootstrap) error {
						editLarge(data, func(release *domain.Release) {
							describe(release)
							release.IssueIDs = append(release.IssueIDs, links[start:end]...)
						})
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				grown, found := f.repository.BootstrapFor(workspace)
				if !found {
					t.Fatal("large release fixture missing")
				}
				var err error
				if f.baseline, err = json.Marshal(grown); err != nil {
					t.Fatal(err)
				}
			} else {
				// Reuse the description and links in every mode and route.
				// One reset writes the preimage with the case inputs already
				// installed instead of writing the base and growing it again.
				f.editBaseline = func(data *domain.Bootstrap) {
					editLarge(data, func(release *domain.Release) {
						describe(release)
						release.IssueIDs = slices.Clone(links)
					})
				}
			}
			if test.raw > 0 {
				// The literal writer cannot seed the oversized row. Keep
				// the earlier-write fixture and reuse its exact SQL bytes.
				var raw []byte
				f.prepare = func(t *testing.T) {
					if raw == nil {
						raw = largeRow(t, func(release *domain.Release) {
							release.Description = loopNextRunBody("legal release notes", test.raw)
						})
					}
					writeLarge(t, raw)
					if err := f.repository.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, alias := range []bool{true, false} {
				t.Run(fmt.Sprintf("alias=%t", alias), func(t *testing.T) {
					outcomes := f.issueReleasesDifferential(t, test.name, func(service *server) *httptest.ResponseRecorder {
						if test.peer != nil {
							test.peer(t)
						}
						service.authDisabled = !alias
						stopPeak := loopNextRunPeak(service.store)
						w := issueReleasesRequest(t, &f, service, owner, nil, issue, test.ids, alias, false)
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
		})
	}
}

// Separate the large indexes and description sizes so CI can shard them.
// Each fixture still exercises both routes in all three store modes.
func TestLargeIssueReleasesDifferential(t *testing.T) {
	largeIssueReleasesRun(t, "add-to-many-issues")
}

func TestLargeIssueReleasesRemoveDifferential(t *testing.T) {
	largeIssueReleasesRun(t, "remove-from-many-issues")
}

func TestLargeIssueReleasesDescriptionDifferential(t *testing.T) {
	largeIssueReleasesRun(t, "add-large-description-7mib")
}

func TestLargeIssueReleasesDescriptionAtMaxDifferential(t *testing.T) {
	largeIssueReleasesRun(t, "add-large-description-at-max")
}

func TestLargeIssueReleasesRemoveDescriptionAtMaxDifferential(t *testing.T) {
	largeIssueReleasesRun(t, "remove-large-description-at-max")
}

func TestLargeIssueReleasesPlainDescriptionDifferential(t *testing.T) {
	largeIssueReleasesRun(t, "add-plain-description-14mb")
}

func TestLargeIssueReleasesOverLimitDescriptionDifferential(t *testing.T) {
	largeIssueReleasesRun(t, "add-large-description-over")
}

func TestLargeIssueReleasesPeerDifferential(t *testing.T) {
	largeIssueReleasesRun(t, "peer-added-issue", "peer-renamed")
}
