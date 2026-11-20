package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// teamScopedFixture seeds one record of every team-scoped settings and
// content collection per team: the workspace ("ws"), the public team ("pub"),
// the private team ("priv") and the private team's sub-team ("child").
type teamScopedFixture struct {
	scaleP21M2Fixture
	teams map[string]string // label -> team id
}

func teamScopedSeed(t *testing.T, repo *store.SQLiteStore) teamScopedFixture {
	t.Helper()
	f := scaleP21M2Setup(t, repo)
	result := teamScopedFixture{scaleP21M2Fixture: f, teams: map[string]string{"ws": "", "pub": f.public}}
	at := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	restore := store.ForceFullMutationsForTesting(true)
	err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "team_scoped.seed", "", nil, func(data *domain.Bootstrap) error {
		for _, team := range data.Teams {
			switch {
			case strings.HasPrefix(team.ID, "m2-private-"):
				result.teams["priv"] = team.ID
			case strings.HasPrefix(team.ID, "m2-child-"):
				result.teams["child"] = team.ID
			}
		}
		for _, label := range []string{"ws", "pub", "priv", "child"} {
			team := result.teams[label]
			data.DocumentTemplates = append(data.DocumentTemplates, domain.DocumentTemplate{ID: "tsc-doctpl-" + label, TeamID: team, Name: "Doc template " + label, Content: "secret " + label, Creator: data.Viewer, CreatedAt: at, UpdatedAt: at})
			scope := "workspace"
			if team != "" {
				scope = "team"
			}
			data.IssueTemplates = append(data.IssueTemplates, domain.IssueTemplate{ID: "tsc-tpl-" + label, TeamID: team, Scope: scope, Name: "Template " + label, Title: "From " + label, LabelIDs: []string{}, Creator: data.Viewer, CreatedAt: at, UpdatedAt: at})
			data.WorkflowDefinitions = append(data.WorkflowDefinitions, domain.WorkflowDefinition{ID: "tsc-workflow-" + label, Name: "Workflow " + label, TeamID: team, Trigger: "manual", Conditions: map[string]string{}, Actions: []domain.WorkflowAction{}, CreatorID: data.Viewer.ID, CreatedAt: at, UpdatedAt: at})
			pipelineTeams := []string{}
			if team != "" {
				pipelineTeams = []string{team}
			}
			data.ReleasePipelines = append(data.ReleasePipelines, domain.ReleasePipeline{ID: "tsc-pipeline-" + label, SlugID: "tsc-pipeline-" + label, Name: "Pipeline " + label, TeamIDs: pipelineTeams, Type: "continuous", Stages: []string{}, StageStatuses: map[string]string{}, PathFilters: []string{}})
			data.Releases = append(data.Releases, domain.Release{ID: "tsc-release-" + label, SlugID: "tsc-release-" + label, Name: "Release " + label, Status: "planned", PipelineID: "tsc-pipeline-" + label, IssueIDs: []string{}, ProjectIDs: []string{}, CreatedAt: at, UpdatedAt: at})
			data.ReleaseNotes = append(data.ReleaseNotes, domain.ReleaseNote{ID: "tsc-note-" + label, ReleaseID: "tsc-release-" + label, Title: "Notes " + label, Body: "secret notes " + label, Creator: data.Viewer, CreatedAt: at, UpdatedAt: at})
			data.ReleaseHistory = append(data.ReleaseHistory, domain.ReleaseHistory{ID: "tsc-history-" + label, ReleaseID: "tsc-release-" + label, Actor: data.Viewer, Action: "created", Metadata: map[string]any{"name": "Release " + label}, CreatedAt: at})
			if team == "" {
				continue
			}
			data.EmailIntakeAddresses = append(data.EmailIntakeAddresses, domain.EmailIntakeAddress{ID: "tsc-intake-" + label, TeamID: team, LocalPart: "tsc-" + label, Domain: "intake.example.test", Address: "tsc-" + label + "@intake.example.test", VerificationState: "verified", Aliases: []domain.EmailIntakeAlias{}, Enabled: true, Type: "team", CreatedAt: at, UpdatedAt: at})
			data.TriageResponsibilities = append(data.TriageResponsibilities, domain.TriageResponsibility{ID: "tsc-responsibility-" + label, TeamID: team, Name: "Responsibility " + label, Mode: "notify", UserIDs: []string{}, CreatedAt: at, UpdatedAt: at})
			data.TriageRoutingRules = append(data.TriageRoutingRules, domain.TriageRoutingRule{ID: "tsc-rule-" + label, TeamID: team, Name: "Rule " + label, Conditions: map[string]string{}, LabelIDs: []string{}, CreatedAt: at, UpdatedAt: at})
			data.TeamResourceSections = append(data.TeamResourceSections, domain.TeamResourceSection{ID: "tsc-section-" + label, TeamID: team, Name: "Section " + label, CreatedAt: at, UpdatedAt: at})
			data.TeamPinnedResources = append(data.TeamPinnedResources, domain.TeamPinnedResource{ID: "tsc-pinned-" + label, TeamID: team, SectionID: "tsc-section-" + label, ResourceType: "link", URL: "https://example.test/" + label, Title: "Pinned " + label, CreatedAt: at, UpdatedAt: at})
		}
		// Workspace documents (no team of their own): unlinked, and attached
		// to an issue of the public and of the private team. Like every
		// document created (or migrated, 1041) they carry their creator's
		// owner grant.
		owner := func(id string) []domain.DocumentPermission {
			return []domain.DocumentPermission{{ID: "document_permission_" + data.Viewer.ID, DocumentID: id, SubjectType: "user", SubjectID: data.Viewer.ID, Role: "owner", CreatedAt: at, UpdatedAt: at}}
		}
		data.Documents = append(data.Documents,
			domain.Document{ID: "tsc-doc-ws", Title: "Workspace doc", Content: "workspace", Creator: data.Viewer, Version: 1, Permissions: owner("tsc-doc-ws"), CreatedAt: at, UpdatedAt: at},
			domain.Document{ID: "tsc-doc-inherited-pub", Title: "Public issue doc", Content: "public", Creator: data.Viewer, Version: 1, InheritedTeamIDs: []string{f.public}, Permissions: owner("tsc-doc-inherited-pub"), CreatedAt: at, UpdatedAt: at},
			domain.Document{ID: "tsc-doc-inherited-priv", Title: "Private issue doc", Content: "private", Creator: data.Viewer, Version: 1, InheritedTeamIDs: []string{result.teams["priv"]}, Permissions: owner("tsc-doc-inherited-priv"), CreatedAt: at, UpdatedAt: at},
		)
		// A workspace template whose default team is private, a template
		// narrowed to the private team and a public template whose sub-issue
		// lands in the private team: each names the private team.
		data.IssueTemplates = append(data.IssueTemplates,
			domain.IssueTemplate{ID: "tsc-tpl-wsdefault-priv", TeamID: result.teams["priv"], Scope: "workspace", Name: "Workspace default private", LabelIDs: []string{}, Creator: data.Viewer, CreatedAt: at, UpdatedAt: at},
			domain.IssueTemplate{ID: "tsc-tpl-narrowed-priv", TeamID: f.public, VisibilityTeamID: result.teams["priv"], Scope: "team", Name: "Narrowed private", LabelIDs: []string{}, Creator: data.Viewer, CreatedAt: at, UpdatedAt: at},
			domain.IssueTemplate{ID: "tsc-tpl-subissue-priv", TeamID: f.public, Scope: "team", Name: "Sub-issue private", LabelIDs: []string{}, SubIssues: []domain.TemplateSubIssue{{ID: "tsc-sub", Title: "Private follow-up", TeamID: result.teams["priv"]}}, Creator: data.Viewer, CreatedAt: at, UpdatedAt: at},
		)
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	return result
}

// teamScopedReach lists, per principal, the teams whose rows (settings) and
// content they reach: workspace admins see every team row but read a private
// team's content only as a member; a member of the private team also reaches
// its sub-team.
var teamScopedReach = map[string]struct{ rows, content []string }{
	"admin-private-outsider":  {rows: []string{"pub", "priv", "child"}, content: []string{"pub"}},
	"admin":                   {rows: []string{"pub", "priv", "child"}, content: []string{"pub", "priv", "child"}},
	"member-private-outsider": {rows: []string{"pub"}, content: []string{"pub"}},
	"guest-explicit":          {rows: []string{"pub"}, content: []string{"pub"}},
	"sub-team-inheritance":    {rows: []string{"pub", "priv", "child"}, content: []string{"pub", "priv", "child"}},
}

type teamScopedCollections struct {
	IssueTemplates         []struct{ ID string } `json:"issueTemplates"`
	DocumentTemplates      []struct{ ID string } `json:"documentTemplates"`
	EmailIntakeAddresses   []struct{ ID string } `json:"emailIntakeAddresses"`
	TriageResponsibilities []struct{ ID string } `json:"triageResponsibilities"`
	TriageRoutingRules     []struct{ ID string } `json:"triageRoutingRules"`
	WorkflowDefinitions    []struct{ ID string } `json:"workflowDefinitions"`
	TeamResourceSections   []struct{ ID string } `json:"teamResourceSections"`
	TeamPinnedResources    []struct{ ID string } `json:"teamPinnedResources"`
	ReleaseNotes           []struct{ ID string } `json:"releaseNotes"`
	ReleaseHistory         []struct{ ID string } `json:"releaseHistory"`
	Documents              []struct{ ID string } `json:"documents"`
}

func (c teamScopedCollections) ids() map[string][]string {
	result := map[string][]string{}
	add := func(name string, items []struct{ ID string }) {
		ids := []string{}
		for _, item := range items {
			if strings.HasPrefix(item.ID, "tsc-") {
				ids = append(ids, item.ID)
			}
		}
		sort.Strings(ids)
		result[name] = ids
	}
	add("issueTemplates", c.IssueTemplates)
	add("documentTemplates", c.DocumentTemplates)
	add("emailIntakeAddresses", c.EmailIntakeAddresses)
	add("triageResponsibilities", c.TriageResponsibilities)
	add("triageRoutingRules", c.TriageRoutingRules)
	add("workflowDefinitions", c.WorkflowDefinitions)
	add("teamResourceSections", c.TeamResourceSections)
	add("teamPinnedResources", c.TeamPinnedResources)
	add("releaseNotes", c.ReleaseNotes)
	add("releaseHistory", c.ReleaseHistory)
	add("documents", c.Documents)
	return result
}

// teamScopedExpected is the projection rule: settings collections follow the
// team rows, team page resources follow team content, release notes and
// history follow the visible releases (whose pipelines follow team content).
// A team-restricted key narrows both to its teams; workspace-level records
// stay visible.
func teamScopedExpected(principal string, keyTeams []string, restricted bool) map[string][]string {
	reach := teamScopedReach[principal]
	in := func(set []string, label string) bool {
		if label == "ws" {
			return true
		}
		if !slices.Contains(set, label) {
			return false
		}
		return !restricted || slices.Contains(keyTeams, label)
	}
	rows := func(label string) bool { return in(reach.rows, label) }
	content := func(label string) bool { return in(reach.content, label) }
	result := map[string][]string{}
	add := func(name, id string, visible bool) {
		if _, ok := result[name]; !ok {
			result[name] = []string{}
		}
		if visible {
			result[name] = append(result[name], id)
		}
	}
	for _, label := range []string{"ws", "pub", "priv", "child"} {
		add("issueTemplates", "tsc-tpl-"+label, rows(label))
		add("documentTemplates", "tsc-doctpl-"+label, rows(label))
		add("workflowDefinitions", "tsc-workflow-"+label, rows(label))
		add("releaseNotes", "tsc-note-"+label, content(label))
		add("releaseHistory", "tsc-history-"+label, content(label))
		if label == "ws" {
			continue
		}
		add("emailIntakeAddresses", "tsc-intake-"+label, rows(label))
		add("triageResponsibilities", "tsc-responsibility-"+label, rows(label))
		add("triageRoutingRules", "tsc-rule-"+label, rows(label))
		add("teamResourceSections", "tsc-section-"+label, content(label))
		add("teamPinnedResources", "tsc-pinned-"+label, content(label))
	}
	add("issueTemplates", "tsc-tpl-wsdefault-priv", rows("priv"))
	add("issueTemplates", "tsc-tpl-narrowed-priv", rows("pub") && rows("priv"))
	add("issueTemplates", "tsc-tpl-subissue-priv", rows("pub") && rows("priv"))
	for name := range result {
		sort.Strings(result[name])
	}
	return result
}

func teamScopedBootstrap(t *testing.T, f teamScopedFixture, source *store.SQLiteStore, user domain.User, key *domain.APIKey, path string, lean bool) teamScopedCollections {
	t.Helper()
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	service := &server{store: source}
	request := f.literalRequest(user, key, nil)
	request.Method, request.URL.Path = http.MethodGet, path
	if lean {
		request.Header.Set("X-Flow-Projection", "lean")
	}
	request, closeOutput := beginRequestResourceReads(request)
	defer closeOutput()
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.Handle("GET /api/bootstrap", serializeLegacyBootstrap(http.HandlerFunc(service.bootstrap)))
	mux.HandleFunc("GET /api/issue-records/bootstrap", service.issueRecordsBootstrap)
	service.signedFileURLs(mux).ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("%s bounded=%t: %d %.300s", path, source.BoundedMetadataEnabled(), w.Code, w.Body.String())
	}
	var result teamScopedCollections
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// TestTeamScopedCollectionsBootstrap: every bootstrap mode (the full outline,
// its lean form, the paged issue-records bootstrap, each with the bounded
// reads off and on) projects the team-scoped settings and content collections
// by the viewer's team access and the key's teams, identically.
func TestTeamScopedCollectionsBootstrap(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := teamScopedSeed(t, repo)
		credentials := []struct {
			name  string
			key   *domain.APIKey
			teams []string
		}{
			{"session", nil, nil},
			{"unrestricted", &domain.APIKey{ID: "tsc-key-all", Scopes: []string{"read"}, TeamRestriction: "all"}, nil},
			{"selected-public", &domain.APIKey{ID: "tsc-key-public", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}, []string{"pub"}},
			{"selected-empty", &domain.APIKey{ID: "tsc-key-empty", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}, []string{}},
		}
		modes := []struct {
			name string
			path string
			lean bool
		}{
			{"outline", "/api/bootstrap", false},
			{"lean", "/api/bootstrap", true},
			{"paged", "/api/issue-records/bootstrap", false},
		}
		for _, principal := range f.principals {
			for _, credential := range credentials {
				want := teamScopedExpected(principal.name, credential.teams, credential.teams != nil)
				for _, mode := range modes {
					var literal map[string][]string
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						got := teamScopedBootstrap(t, f, source, principal.user, credential.key, mode.path, mode.lean).ids()
						label := principal.name + "/" + credential.name + "/" + mode.name
						if source.BoundedMetadataEnabled() {
							label += "/bounded"
							if !jsonEqual(got, literal) {
								t.Errorf("%s differs from flag off:\n off=%v\n  on=%v", label, literal, got)
							}
						} else {
							literal = got
						}
						for name, ids := range want {
							if !slices.Equal(got[name], ids) {
								t.Errorf("%s %s = %v, want %v", label, name, got[name], ids)
							}
						}
					}
				}
			}
		}
	}
}

// TestKeyTeamScopeWorkspaceDocuments: a team-restricted key reads a document
// without a team of its own by one rule in the bootstrap, the document list
// and the document itself: outside the key, except for an application reading
// the documents of its teams' issues and projects that no private team
// outside the key scopes. The bounded reads answer the same.
func TestKeyTeamScopeWorkspaceDocuments(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := teamScopedSeed(t, repo)
		documents := []string{"tsc-doc-ws", "tsc-doc-inherited-pub", "tsc-doc-inherited-priv"}
		for _, principal := range f.principals {
			// Both admins read every one of these documents as a session: the
			// admin of the private team as its member, the admin outside it
			// as the documents' creator (the owner grant is a person grant,
			// which an unrestricted key keeps and a restricted key does not
			// widen).
			if principal.name != "admin" && principal.name != "admin-private-outsider" {
				continue
			}
			session := documents
			for _, credential := range []struct {
				key     *domain.APIKey
				visible []string
			}{
				{&domain.APIKey{ID: "tsc-doc-key-public", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}, nil},
				{&domain.APIKey{ID: "tsc-doc-key-empty", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}, nil},
				{&domain.APIKey{ID: "tsc-doc-key-app", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}, []string{"tsc-doc-inherited-pub"}},
				{&domain.APIKey{ID: "tsc-doc-key-all", Scopes: []string{"read"}, TeamRestriction: "all"}, session},
			} {
				key := credential.key
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					label := principal.name + "/" + key.ID + map[bool]string{false: "", true: "/bounded"}[source.BoundedMetadataEnabled()]
					bootstrap := teamScopedBootstrap(t, f, source, principal.user, key, "/api/bootstrap", false).ids()["documents"]
					list := keyTeamScopeServe(f.scaleP21M2Fixture, source, "", principal.user, key, http.MethodGet, "/api/documents", nil)
					for _, id := range documents {
						visible := slices.Contains(credential.visible, id)
						detail := keyTeamScopeServe(f.scaleP21M2Fixture, source, "", principal.user, key, http.MethodGet, "/api/documents/"+id, nil)
						if got := detail.code == http.StatusOK; got != visible {
							t.Errorf("%s: detail of %s = %d, want visible=%t", label, id, detail.code, visible)
						}
						if got := slices.Contains(bootstrap, id); got != visible {
							t.Errorf("%s: bootstrap has %s = %t, want %t", label, id, got, visible)
						}
						if got := strings.Contains(list.body, id); got != visible {
							t.Errorf("%s: list has %s = %t, want %t (%d)", label, id, got, visible, list.code)
						}
					}
				}
			}
		}
	}
}
