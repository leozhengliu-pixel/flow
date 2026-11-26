package store

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestInitiativeProjectLinksBatch(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	const first, second, third = "ci-project-00", "ci-project-01", "ci-project-03"
	type row struct {
		data           []byte
		version        int64
		search         string
		catalogVersion string
		catalogBytes   int
		catalogLinks   []string
		relations      []string
	}
	read := func(id string) (r row) {
		t.Helper()
		var summary []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT m.data,v.version,d.content,c.version,c.body_bytes,c.summary
			FROM workspace_metadata_records m
			JOIN record_versions v ON v.workspace_key=m.workspace_key AND v.field=m.field AND v.record_key=m.record_key
			JOIN metadata_search_documents d ON d.workspace_key=m.workspace_key AND d.field=m.field AND d.record_key=m.record_key
			JOIN metadata_catalog c ON c.workspace_key=m.workspace_key AND c.field=m.field AND c.record_key=m.record_key
			WHERE m.workspace_key=? AND m.field='projects' AND m.record_key=?`, workspace, id).
			Scan(&r.data, &r.version, &r.search, &r.catalogVersion, &r.catalogBytes, &summary); err != nil {
			t.Fatal(err)
		}
		var project domain.Project
		if err := json.Unmarshal(summary, &project); err != nil {
			t.Fatal(err)
		}
		r.catalogLinks = project.Initiatives
		relations, err := f.db.QueryContext(t.Context(), `SELECT value FROM metadata_catalog_relations
			WHERE workspace_key=? AND field='projects' AND record_key=? AND relation='initiatives' ORDER BY value`, workspace, id)
		if err != nil {
			t.Fatal(err)
		}
		defer relations.Close()
		for relations.Next() {
			var value string
			if err := relations.Scan(&value); err != nil {
				t.Fatal(err)
			}
			r.relations = append(r.relations, value)
		}
		if err := relations.Err(); err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, id := range []string{first, second, third} {
		// A startup repair or SQL writer can leave trigger-formatted search
		// content. A link write must leave the same projection as a legacy edit.
		if _, err := f.db.ExecContext(t.Context(), `UPDATE metadata_search_documents SET content=? WHERE workspace_key=? AND field='projects' AND record_key=?`,
			"old index content", workspace, id); err != nil {
			t.Fatal(err)
		}
	}
	check := func(event string, links map[string][]string, wantBatches int) {
		t.Helper()
		previous := map[string]row{}
		for _, id := range []string{first, second, third} {
			previous[id] = read(id)
		}
		batches := 0
		ctx := WithSQLStatementObserver(WithColdWork(t.Context(), "initiative-project-links"), func(query string) {
			if strings.HasPrefix(query, "UPDATE workspace_metadata_records SET data=") {
				batches++
			}
		})
		err := view.coldLegacyMutation(ctx, workspace, event, nil, func(data *domain.Bootstrap) (string, error) {
			projectIDs := []string{}
			for i, project := range data.Projects {
				if ids, ok := links[project.ID]; ok {
					data.Projects[i].Initiatives = ids
				}
				if slices.Contains(data.Projects[i].Initiatives, "ci-link") {
					projectIDs = append(projectIDs, project.ID)
				}
			}
			if index := slices.IndexFunc(data.Initiatives, func(i domain.Initiative) bool { return i.ID == "ci-link" }); index >= 0 {
				data.Initiatives[index].ProjectIDs = projectIDs
				data.Initiatives[index].Name += " updated"
			} else {
				data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "ci-link", Name: "Linked", ProjectIDs: projectIDs})
			}
			return "ci-link", nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if batches != wantBatches {
			t.Fatalf("%s issued %d link batches, want %d", event, batches, wantBatches)
		}
		exact := coldIncrementalAssertExact(t, f, view, workspace, event)
		for _, project := range exact.Projects {
			old, ok := previous[project.ID]
			if !ok {
				continue
			}
			if want, changed := links[project.ID]; changed && !reflect.DeepEqual(project.Initiatives, want) {
				t.Fatalf("%s left incorrect links for %s", event, project.ID)
			}
			now := read(project.ID)
			var before domain.Project
			if err := json.Unmarshal(old.data, &before); err != nil {
				t.Fatal(err)
			}
			if reflect.DeepEqual(before.Initiatives, project.Initiatives) {
				if !reflect.DeepEqual(now, old) {
					t.Fatalf("%s rewrote an unchanged project %s", event, project.ID)
				}
				continue
			}
			oldJSON, _ := json.Marshal(before.Initiatives)
			newJSON, _ := json.Marshal(project.Initiatives)
			want := bytes.Replace(old.data, []byte(`,"initiatives":`+string(oldJSON)+`,`), []byte(`,"initiatives":`+string(newJSON)+`,`), 1)
			if now.version != old.version+1 || wantBatches > 0 && !bytes.Equal(now.data, want) {
				t.Fatalf("%s changed fields outside the links of %s, or bumped its version incorrectly", event, project.ID)
			}
			wantRelations := slices.Clone(project.Initiatives)
			slices.Sort(wantRelations)
			if now.catalogVersion != metadataFingerprint(now.data) || now.catalogBytes != len(now.data) ||
				!reflect.DeepEqual(now.catalogLinks, project.Initiatives) || !slices.Equal(now.relations, slices.Compact(wantRelations)) {
				t.Fatalf("%s left stale catalog metadata for %s", event, project.ID)
			}
			raw, err := json.Marshal(project)
			if err != nil {
				t.Fatal(err)
			}
			if want := capSearchText(metadataSearchText(StripJSONNUL(raw)), f.db.searchMaxChars); now.search != want {
				t.Fatalf("%s changed the legacy search projection for %s", event, project.ID)
			}
		}
	}
	check("initiative.created", map[string][]string{first: {"ci-link"}, second: {"ci-link", "other"}}, 1)
	check("initiative.updated", map[string][]string{first: {}, second: {"other", "ci-link"}, third: {"ci-link"}}, 1)
	check("initiative.updated", map[string][]string{third: {"ci-link"}}, 0)

	// A peer changes an unrelated project field after the COLD base was
	// captured. The legacy merge keeps that edit alongside the link update.
	before := read(third)
	coldIncrementalRun(t, view, workspace, "initiative.updated", func(data *domain.Bootstrap) error {
		var peer domain.Project
		if err := json.Unmarshal(before.data, &peer); err != nil {
			return err
		}
		peer.Summary = "Peer summary"
		raw, err := json.Marshal(peer)
		if err != nil {
			return err
		}
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND record_key=?`, raw, workspace, third); err != nil {
			return err
		}
		for i, p := range data.Projects {
			if p.ID == third {
				data.Projects[i].Initiatives = []string{}
			}
		}
		return nil
	})
	got := coldIncrementalAssertExact(t, f, view, workspace, "peer edit")
	i := slices.IndexFunc(got.Projects, func(p domain.Project) bool { return p.ID == third })
	if i < 0 || got.Projects[i].Summary != "Peer summary" || len(got.Projects[i].Initiatives) != 0 {
		t.Fatal("initiative update lost the peer edit or did not remove the link")
	}

	// A case alias can override the canonical member when Go decodes the
	// row. Replacing just the canonical member would leave the old links.
	old := read(first)
	raw := append(bytes.TrimSuffix(old.data, []byte("}")), []byte(`,"INITIATIVES":[]}`)...)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND record_key=?`, raw, workspace, first); err != nil {
		t.Fatal(err)
	}
	view = coldIncrementalReloadedView(t, f, workspace)
	check("initiative.updated", map[string][]string{first: {"ci-link"}}, 0)
}
