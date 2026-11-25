package store

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A document edit in a workspace with many large projects: the edit of an
// unlinked document, of a linked one (its project resource follows the
// title) and an unlink. Legacy, bounded warm and bounded SQL-reloaded writers
// must leave the same rows, events and installed values.
func scaleP21HotDocumentSeed(t *testing.T, f scaleP09Fixture, projects int) {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	clock := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	for batch := 0; batch < projects; batch += 50 {
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.hot_document_seed", "", nil, func(data *domain.Bootstrap) error {
			for i := batch; i < min(batch+50, projects); i++ {
				project := domain.Project{ID: fmt.Sprintf("hot-project-%04d", i), Name: fmt.Sprintf("Project %d", i),
					Description: strings.Repeat(fmt.Sprintf("project %d body <>& ", i), 200), TeamIDs: []string{}, Resources: []domain.ProjectResource{}, CreatedAt: clock}
				if i%17 == 7 {
					project.Resources = []domain.ProjectResource{{ID: "hot-linked", ProjectID: project.ID, Type: "document", Title: "Linked", URL: "/linked", PinnedTeamIDs: []string{}, CreatedAt: clock}}
				}
				data.Projects = append(data.Projects, project)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.hot_document_seed", "", nil, func(data *domain.Bootstrap) error {
		for i := range 30 {
			data.Documents = append(data.Documents, domain.Document{ID: fmt.Sprintf("hot-doc-%02d", i), SlugID: fmt.Sprintf("hot-doc-%02d", i),
				Title: "Other", Content: strings.Repeat("document body <>& ", 4000), Creator: data.Viewer, Version: 1, CreatedAt: clock, UpdatedAt: clock})
		}
		var linked []string
		for _, project := range data.Projects {
			if len(project.Resources) != 0 {
				linked = append(linked, project.ID)
			}
		}
		data.Documents = append(data.Documents, domain.Document{ID: "hot-linked", SlugID: "hot-linked", Title: "Linked", ProjectIDs: linked,
			Creator: data.Viewer, Version: 1, CreatedAt: clock, UpdatedAt: clock})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func scaleP21HotDocumentRows(t *testing.T, f scaleP09Fixture) []string {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	var result []string
	rows, err := f.db.QueryContext(t.Context(), `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('documents','projects','favorites') ORDER BY field,record_key`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var field, key string
		var order int64
		var raw []byte
		if err := rows.Scan(&field, &key, &order, &raw); err != nil {
			t.Fatal(err)
		}
		value, _ := json.Marshal([]any{field, key, order, json.RawMessage(raw)})
		result = append(result, string(value))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	events, err := f.db.QueryContext(t.Context(), `SELECT event_type,aggregate_id,payload FROM domain_events WHERE workspace_id=? AND event_type='document.updated' ORDER BY created_at,id`, f.data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	for events.Next() {
		var kind, aggregate string
		var raw []byte
		if err := events.Scan(&kind, &aggregate, &raw); err != nil {
			t.Fatal(err)
		}
		value, _ := json.Marshal([]any{kind, aggregate, json.RawMessage(raw)})
		result = append(result, string(value))
	}
	if err := events.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21HotDocumentInstalled(t *testing.T, repo *SQLiteStore, workspace string) map[string]any {
	t.Helper()
	names := []string{"documents", "projects", "favorites"}
	raw := map[string]json.RawMessage{}
	if repo.BoundedMetadataEnabled() {
		read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("installed postimage missing", err)
		}
		defer read.Close()
		for _, name := range names {
			var output strings.Builder
			if err := read.StreamFieldJSON(t.Context(), name, &output); err != nil {
				t.Fatal(err)
			}
			raw[name] = json.RawMessage(output.String())
		}
	} else {
		data, found := repo.WorkspaceMetadataFields(workspace, names...)
		if !found {
			t.Fatal("legacy postimage missing")
		}
		encoded, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &raw); err != nil {
			t.Fatal(err)
		}
	}
	result := map[string]any{}
	for _, name := range names {
		var value any
		if err := json.Unmarshal(raw[name], &value); err != nil {
			t.Fatal(name, err)
		}
		result[name] = value
	}
	return result
}

func TestScaleP21HotDocumentWriteDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	clock := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	edits := []struct {
		ref, title string
		projects   []string
	}{
		{ref: "hot-doc-03", title: "Unlinked edit"},
		{ref: "hot-linked", title: "Linked edit"},
		{ref: "hot-linked", title: "Unlinked again", projects: []string{}},
		{ref: "hot-doc-04", title: "Second unlinked edit"},
	}
	var outcomes [][]any
	modes := []string{"legacy-warm", "bounded-warm", "legacy-reloaded", "bounded-reloaded"}
	for _, mode := range modes {
		f := scaleP09Database(t)
		scaleP21HotDocumentSeed(t, f, 120)
		workspace := f.data.Workspace.URLKey
		repo := f.SQLiteStore
		if strings.HasPrefix(mode, "bounded") {
			var err error
			repo, err = f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.releaseMetadataLogicalSources()
		}
		if strings.HasSuffix(mode, "reloaded") {
			// A real SQL decode (with its load normalization) before the edits.
			if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		var outcome []any
		for index, edit := range edits {
			var closes []func()
			ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
				closes = append(closes, close)
				return true
			})
			ctx = WithMutationScope(ctx, MutationScope{Resources: []string{edit.ref}})
			ctx = WithDocumentUpdateWrite(ctx, DocumentUpdateWriteRequest{Ref: edit.ref, ProjectIDs: edit.projects})
			at := clock.Add(time.Duration(index) * time.Minute)
			err := repo.MutateWorkspace(ctx, workspace, "document.updated", edit.ref, map[string]any{"title": edit.title}, func(data *domain.Bootstrap) error {
				position := slices.IndexFunc(data.Documents, func(document domain.Document) bool { return document.ID == edit.ref })
				if position < 0 {
					return fmt.Errorf("document %s not supplied", edit.ref)
				}
				document := &data.Documents[position]
				document.Title, document.UpdatedAt = edit.title, at
				document.Version++
				if edit.projects != nil {
					document.ProjectIDs = edit.projects
				}
				for i := range data.Projects {
					project := &data.Projects[i]
					linked := slices.Contains(document.ProjectIDs, project.ID)
					at := slices.IndexFunc(project.Resources, func(resource domain.ProjectResource) bool { return resource.ID == document.ID })
					switch {
					case !linked && at >= 0:
						project.Resources = slices.Delete(project.Resources, at, at+1)
					case linked && at >= 0:
						project.Resources[at].Title = document.Title
					}
				}
				return nil
			})
			for _, close := range closes {
				close()
			}
			if err != nil {
				t.Fatal(mode, edit.ref, err)
			}
			if strings.HasPrefix(mode, "bounded") && repo.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("bounded document edit hydrated the workspace", mode)
			}
			outcome = append(outcome, scaleP21HotDocumentInstalled(t, repo, workspace))
		}
		outcome = append(outcome, scaleP21HotDocumentRows(t, f))
		// Every mode then decodes its rows again: the SQL postimage itself.
		if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
			t.Fatal(err)
		}
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		outcome = append(outcome, scaleP21HotDocumentInstalled(t, repo, workspace))
		if strings.HasPrefix(mode, "bounded") && repo.metadataResidency.work.used != 0 {
			t.Fatal("bounded document edit leaked work", mode)
		}
		// Each mode owns a fresh fixture: its owner and workspace identities
		// are the only expected differences.
		raw, err := json.Marshal(outcome)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for name, value := range map[string]string{"<owner>": f.data.Viewer.ID, "<workspace-id>": f.data.Workspace.ID, "<workspace>": workspace, "<email>": f.data.Viewer.Email} {
			if value != "" {
				text = strings.ReplaceAll(text, value, name)
			}
		}
		var normalized []any
		if err := json.Unmarshal([]byte(text), &normalized); err != nil {
			t.Fatal(err)
		}
		outcomes = append(outcomes, normalized)
	}
	for index := 1; index < len(outcomes); index += 2 {
		for step := range outcomes[index-1] {
			if !reflect.DeepEqual(outcomes[index-1][step], outcomes[index][step]) {
				t.Fatalf("%s step %d differs from %s: %s", modes[index], step, modes[index-1], scaleP21HotDocumentDiff(outcomes[index-1][step], outcomes[index][step], ""))
			}
		}
	}
}

// A document edit must not compare every project of the workspace byte by
// byte or rewrite the collection when no project changes: the spool bytes an
// edit reads (cached or not) grow with the project bytes only by the passes
// that re-frame the image. A linked edit also rewrites the collection.
func TestScaleP21HotDocumentWriteBoundedReads(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	metadataLogicalSpoolCounting.Store(true)
	t.Cleanup(func() { metadataLogicalSpoolCounting.Store(false) })
	measure := func(projects int) (unlinked, linked, projectBytes int64) {
		f := scaleP09Database(t)
		scaleP21HotDocumentSeed(t, f, projects)
		workspace := f.data.Workspace.URLKey
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer view.releaseMetadataLogicalSources()
		edit := func(ref, title string) int64 {
			var closes []func()
			ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
				closes = append(closes, close)
				return true
			})
			ctx = WithMutationScope(ctx, MutationScope{Resources: []string{ref}})
			ctx = WithDocumentUpdateWrite(ctx, DocumentUpdateWriteRequest{Ref: ref})
			before := metadataLogicalSpoolReadBytes.Load()
			if err := view.MutateWorkspace(ctx, workspace, "document.updated", ref, map[string]any{"title": title}, func(data *domain.Bootstrap) error {
				for i := range data.Documents {
					if data.Documents[i].ID == ref {
						data.Documents[i].Title = title
					}
				}
				for i := range data.Projects {
					for j := range data.Projects[i].Resources {
						if data.Projects[i].Resources[j].ID == ref {
							data.Projects[i].Resources[j].Title = title
						}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			read := metadataLogicalSpoolReadBytes.Load() - before
			for _, close := range closes {
				close()
			}
			return read
		}
		edit("hot-doc-05", "Warm")
		unlinked = edit("hot-doc-06", "Unlinked")
		linked = edit("hot-linked", "Linked edit")
		read, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		defer read.Close()
		index, _, _ := metadataLogicalField("projects")
		return unlinked, linked, read.source.fields[index].size
	}
	smallUnlinked, smallLinked, smallProjects := measure(100)
	largeUnlinked, largeLinked, largeProjects := measure(400)
	t.Logf("projects %d -> %d bytes: unlinked edit read %d -> %d, linked edit read %d -> %d", smallProjects, largeProjects, smallUnlinked, largeUnlinked, smallLinked, largeLinked)
	// Before, an edit forced every project through a canonical rewrite and
	// byte comparisons of copies: well over eight times the project bytes.
	if growth := largeProjects - smallProjects; largeUnlinked-smallUnlinked > growth*4 {
		t.Fatalf("an unlinked document edit read %d more bytes for %d more bytes of projects", largeUnlinked-smallUnlinked, growth)
	}
	if growth := largeProjects - smallProjects; largeLinked-smallLinked > growth*12 {
		t.Fatalf("a linked document edit read %d more bytes for %d more bytes of projects", largeLinked-smallLinked, growth)
	}
}

func scaleP21HotDocumentDiff(left, right any, path string) string {
	switch l := left.(type) {
	case map[string]any:
		r, ok := right.(map[string]any)
		if !ok {
			break
		}
		for key, value := range l {
			if !reflect.DeepEqual(value, r[key]) {
				return scaleP21HotDocumentDiff(value, r[key], path+"."+key)
			}
		}
		for key, value := range r {
			if _, found := l[key]; !found {
				return fmt.Sprintf("%s.%s: legacy missing, bounded=%.300v", path, key, value)
			}
		}
	case []any:
		r, ok := right.([]any)
		if !ok || len(l) != len(r) {
			break
		}
		for index := range l {
			if !reflect.DeepEqual(l[index], r[index]) {
				return scaleP21HotDocumentDiff(l[index], r[index], fmt.Sprintf("%s[%d]", path, index))
			}
		}
	case []string:
		r, ok := right.([]string)
		if !ok || len(l) != len(r) {
			break
		}
		for index := range l {
			if l[index] != r[index] {
				return fmt.Sprintf("%s[%d]: legacy=%.400s bounded=%.400s", path, index, l[index], r[index])
			}
		}
	}
	return fmt.Sprintf("%s: legacy=%.400v bounded=%.400v", path, left, right)
}
