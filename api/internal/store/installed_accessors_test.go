package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// resident image readers differentials: the store accessors that read a record field from the
// installed image (which a bounded store holds without it) answer, bounded,
// what the legacy store answers. A flag-off fixture holds a cycle with a
// calendar token, a document, a project with an update, a trash entry and
// its member directory; a bounded test view of it is taken from the
// owner's installed generation (warm) and again after the owner reloaded the
// workspace from SQL (reloaded). Every accessor must encode the same answer.
func TestInstalledAccessorsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := journalScaleDatabase(t)
	ctx := t.Context()
	workspace := f.data.Workspace.URLKey
	team := f.data.Teams[0].ID
	now := time.Now().UTC().Truncate(time.Second)
	cycleID, documentID, projectID, trashID := idgen.New("sw559-cycle-"), idgen.New("sw559-document-"), idgen.New("sw559-project-"), idgen.New("sw559-trash-")
	const token = "flow_cycle_sw559_calendar"
	if err := f.MutateWorkspace(WithMetadataFields(ctx, "cycles", "documents", "projects", "projectUpdates", "trash"), workspace, "scale_p21_sw559.accessors", "", nil, func(data *domain.Bootstrap) error {
		data.Cycles = append(data.Cycles, domain.Cycle{ID: cycleID, TeamID: team, Number: 59, Name: "SW559 cycle", Description: "calendar; feed", StartsAt: now, EndsAt: now.Add(14 * 24 * time.Hour), CalendarToken: token})
		data.Documents = append(data.Documents, domain.Document{ID: documentID, Title: "SW559 document", Content: "body", TeamIDs: []string{team}, CreatedAt: now, UpdatedAt: now})
		data.Projects = append(data.Projects, domain.Project{ID: projectID, Name: "SW559 project", TeamIDs: []string{team}, CreatedAt: now, UpdatedAt: now})
		if data.ProjectUpdates == nil {
			data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
		}
		data.ProjectUpdates[projectID] = append(data.ProjectUpdates[projectID], domain.ProjectUpdate{ID: idgen.New("sw559-update-"), ProjectID: projectID, Body: "on track @P09", Health: "onTrack", CreatedAt: now})
		data.Trash = append(data.Trash, domain.TrashEntry{ID: trashID, ResourceType: "project", ResourceID: idgen.New("sw559-trashed-"), Title: "SW559 trashed", DeletedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	encode := func(value any) string {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	answers := func(s *SQLiteStore) map[string]string {
		result := map[string]string{}
		for _, ref := range []struct{ id, token string }{{cycleID, token}, {cycleID, "wrong"}, {"missing", token}} {
			cycle, found := s.CycleForCalendar(ref.id, ref.token)
			result["CycleForCalendar "+ref.id+" "+ref.token] = fmt.Sprint(found, encode(cycle))
		}
		for _, key := range []string{workspace, "", workspace + "-missing"} {
			result["TrashSnapshot "+key] = encode(s.TrashSnapshot(key))
			for _, id := range []string{documentID, "missing"} {
				document, found := s.DocumentByID(key, id)
				result["DocumentByID "+key+" "+id] = fmt.Sprint(found, encode(document))
			}
			for _, id := range []string{projectID, "missing"} {
				project, found := s.ProjectByID(key, id)
				result["ProjectByID "+key+" "+id] = fmt.Sprint(found, encode(project))
			}
		}
		current, found := s.memberDirectorySource(ctx, workspace)
		missing, err := s.membersMissingFromDirectory(ctx, current, nil)
		if err != nil {
			t.Fatal(err)
		}
		result["memberDirectory"] = fmt.Sprint(found, encode(missing))
		pending := []bool{}
		for _, key := range []string{workspace, workspace + "-missing"} {
			pending = append(pending, s.legacyAuditLogPending(ctx, key))
		}
		result["legacyAuditLogPending"] = fmt.Sprint(pending)
		return result
	}
	legacy := answers(f.SQLiteStore)
	for _, key := range []string{"CycleForCalendar " + cycleID + " " + token, "DocumentByID " + workspace + " " + documentID, "ProjectByID " + workspace + " " + projectID} {
		if !strings.HasPrefix(legacy[key], "true") {
			t.Fatalf("legacy fixture lacks %s: %s", key, legacy[key])
		}
	}
	if !strings.Contains(legacy["TrashSnapshot "+workspace], trashID) {
		t.Fatalf("legacy fixture lacks the trash entry: %s", legacy["TrashSnapshot "+workspace])
	}
	for _, stage := range []string{"warm", "reloaded"} {
		if stage == "reloaded" {
			f.mu.Lock()
			delete(f.copyRevisions, workspace)
			f.mu.Unlock()
			if err := f.ReloadWorkspace(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			legacy = answers(f.SQLiteStore)
		}
		probe, err := f.BoundedMetadataViewForTesting(ctx)
		if err != nil {
			t.Fatal(err)
		}
		bounded := answers(probe)
		for key, want := range legacy {
			if bounded[key] != want {
				t.Errorf("%s %s: bounded %s, legacy %s", stage, key, bounded[key], want)
			}
		}
		// The skeleton itself still holds none of them: the answers came
		// from the installed source.
		skeleton, _ := scaleResidentOracle(probe, workspace)
		if len(skeleton.Cycles)+len(skeleton.Documents)+len(skeleton.Projects)+len(skeleton.Trash)+len(skeleton.Users) != 0 {
			t.Fatal("the bounded view holds record fields in its image")
		}
		// A directory that holds every member writes nothing bounded either.
		if added, err := probe.syncMemberDirectory(ctx, workspace); err != nil || added != 0 {
			t.Fatalf("%s bounded directory sync added %d members of a complete directory: %v", stage, added, err)
		}
	}
}

// The storage maintenance pass moves audit entries an older server left
// in the workspace document into the audit table. Bounded, the image holds no
// audit log, so the pass never moved them. One database seeded flag off with
// two such entries is copied; the copies opened flag off and flag on (the
// production bounded store) must move the same entries and leave none.
func TestLegacyAuditLogMigrationDifferential(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	ctx := t.Context()
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed.db")
	open := func(path string, bounded bool) *SQLiteStore {
		t.Helper()
		value := "off"
		if bounded {
			value = "on"
		}
		t.Setenv("FLOW_BOUNDED_METADATA", value)
		repo, err := OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		if repo.BoundedMetadataEnabled() != bounded {
			t.Fatalf("store opened with bounded=%v, want %v", repo.BoundedMetadataEnabled(), bounded)
		}
		return repo
	}
	repo := open(seed, false)
	base := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	entries := []domain.AuditLogEntry{
		{ID: "audit_sw559_newer", Action: "updated", ResourceType: "project", ResourceID: "p1", CreatedAt: base.Add(time.Hour), Actor: domain.User{ID: "usr_a"}, Metadata: map[string]any{"ip": "198.51.100.7"}},
		{ID: "audit_sw559_older", Action: "created", ResourceType: "project", ResourceID: "p1", CreatedAt: base, Actor: domain.User{ID: "usr_a"}},
	}
	repo.writeMu.Lock()
	before, _ := repo.storedWorkspace(accessWorkspace)
	after := before
	after.AuditLog = entries
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.persistMetadataTx(ctx, tx, accessWorkspace, before, &after); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	repo.installWorkspace(accessWorkspace, after, false)
	repo.writeMu.Unlock()
	if err := repo.CompleteMetadataCatalogForTesting(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	type outcome struct{ moved, left string }
	results := map[bool]outcome{}
	for _, bounded := range []bool{false, true} {
		path := filepath.Join(dir, fmt.Sprintf("copy-%v.db", bounded))
		copyDatabase(t, seed, path)
		repo := open(path, bounded)
		work := ctx
		if bounded {
			work = WithColdWork(ctx, "sw559")
			if !repo.legacyAuditLogPending(ctx, accessWorkspace) {
				t.Fatal("bounded store does not see the document's audit entries")
			}
		}
		if err := repo.MigrateLegacyAuditLog(work); err != nil {
			t.Fatalf("bounded=%v: %v", bounded, err)
		}
		// A second pass finds nothing left.
		if err := repo.MigrateLegacyAuditLog(work); err != nil {
			t.Fatalf("bounded=%v second pass: %v", bounded, err)
		}
		listed, err := repo.ListAuditEntries(ctx, auditTestWorkspaceID(t, repo), AuditQuery{})
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, entry := range listed {
			ids = append(ids, fmt.Sprint(entry.ID, " ", entry.Seq, " ", entry.IP, " ", entry.Action))
		}
		slices.Sort(ids)
		data, found := repo.WorkspaceMetadataFields(accessWorkspace, "auditLog")
		if !found {
			t.Fatal("workspace missing")
		}
		results[bounded] = outcome{moved: fmt.Sprint(ids), left: fmt.Sprint(len(data.AuditLog), repo.legacyAuditLogPending(ctx, accessWorkspace))}
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if results[false] != results[true] {
		t.Fatalf("bounded audit log migration %+v, legacy %+v", results[true], results[false])
	}
	if results[false].left != "0 false" || results[false].moved == "[]" {
		t.Fatalf("legacy migration did not move the entries: %+v", results[false])
	}
}
