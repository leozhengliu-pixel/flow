package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestDocumentSearchTermsSplitsKeywords(t *testing.T) {
	for query, want := range map[string][]string{
		"智能印控平台 UAT 的准出标准是什么？": {"智能印控平台", "UAT", "准出标准"},
		"印控UAT准出":                  {"印控", "UAT", "准出"},
		"UAT exit-criteria, v1.2.": {"UAT", "exit-criteria", "v1.2"},
		"uat UAT a":                {"uat"},
		"？":                        {},
	} {
		if got := documentSearchTerms(query); !slices.Equal(got, want) {
			t.Errorf("documentSearchTerms(%q) = %q, want %q", query, got, want)
		}
	}
}

// Runs on MySQL's ngram index too: FLOW_MCP_TEST_DATABASE_DRIVER=mysql FLOW_MCP_TEST_DATABASE_URL=mysql://….
func TestDocumentSnippetQuotesTheBestLineAndItsSection(t *testing.T) {
	content := "# 测试计划\n\n## 背景\n本计划覆盖 SIT 与 UAT。\n\n## 4. UAT 准出标准\n1. 用例通过率不低于 95%；\n2. 无 P0 缺陷。"
	snippet, section := documentSnippet(content, []string{"uat", "准出标准"})
	if section != "4. UAT 准出标准" || !strings.HasPrefix(snippet, "## 4. UAT 准出标准 1. 用例通过率不低于 95%；") {
		t.Fatalf("snippet %q in section %q", snippet, section)
	}
	long := strings.Repeat("前文", 100) + "关键词" + strings.Repeat("后文", 200)
	snippet, section = documentSnippet(long, []string{"关键词"})
	if section != "" || !strings.HasPrefix(snippet, "…"+strings.Repeat("前文", 30)+"关键词") || !strings.HasSuffix(snippet, "…") || len([]rune(snippet)) != 242 {
		t.Fatalf("long snippet %q", snippet)
	}
	if snippet, _ := documentSnippet("Only the title matched here.", []string{"absent"}); snippet != "Only the title matched here." {
		t.Fatalf("title-only snippet %q", snippet)
	}
}

func TestMCPSearchDocumentsRanksAuthorizedFullTextMatches(t *testing.T) {
	f := newMCPContractFixture(t)
	repository, service, data := f.repository, f.service, f.data
	actor := mcpActor{WorkspaceKey: data.Workspace.URLKey, User: data.Viewer, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
	ctx := store.ContextWithActor(t.Context(), actor.User)
	team, project := data.Teams[0], data.Projects[0]
	now := time.Now().UTC()
	archived := now.Add(-time.Hour)
	documents := []domain.Document{
		{ID: "doc-plan", SlugID: "plan-1", Title: "智能印控平台 UAT 方案", Content: "# 测试方案\n\n## 准出标准\n\n所有 P0/P1 缺陷关闭，用例通过率不低于 98%，业务方签字确认。", TeamIDs: []string{team.ID}, ProjectIDs: []string{project.ID}, UpdatedAt: now.Add(-48 * time.Hour)},
		{ID: "doc-body", SlugID: "body-1", Title: "周会纪要", Content: "讨论了 UAT 的排期。\n\n另外，智能印控平台的准出标准沿用上一期。", TeamIDs: []string{team.ID}, UpdatedAt: now},
		{ID: "doc-partial", SlugID: "partial-1", Title: "智能印控平台介绍", Content: "产品概览。", TeamIDs: []string{team.ID}, UpdatedAt: now},
		{ID: "doc-english", SlugID: "english-1", Title: "Release checklist", Content: "Before go-live the UAT exit criteria apply: zero BLOCKER bugs and QA sign-off.", UpdatedAt: now},
		{ID: "doc-private", SlugID: "private-1", Title: "智能印控平台 UAT 准出标准（内部）", Content: "secret", TeamIDs: []string{"team-private-search"}, UpdatedAt: now},
		{ID: "doc-archived", SlugID: "archived-1", Title: "智能印控平台 UAT 准出标准（旧）", Content: "旧版", TeamIDs: []string{team.ID}, ArchivedAt: &archived, UpdatedAt: now},
	}
	if err := repository.MutateWorkspace(ctx, actor.WorkspaceKey, "test.documents", "doc-plan", nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, domain.Team{ID: "team-private-search", Name: "Private search", Key: "PSRCH", Private: true})
		next.TeamSettings["team-private-search"] = domain.TeamSettings{TeamID: "team-private-search"}
		author := next.Users[slices.IndexFunc(next.Users, func(user domain.User) bool { return user.ID != next.Viewer.ID })]
		for _, document := range documents {
			document.Creator = author
			document.CreatedAt = document.UpdatedAt
			next.Documents = append(next.Documents, document)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	search := func(args map[string]any) []map[string]any {
		t.Helper()
		result, err := service.callFlowTool(ctx, actor, "search_documents", args)
		if err != nil {
			t.Fatal(err)
		}
		return result.(map[string]any)["items"].([]map[string]any)
	}
	ids := func(items []map[string]any) []string {
		result := []string{}
		for _, item := range items {
			result = append(result, item["id"].(string))
		}
		return result
	}

	// Chinese and English keywords that are not adjacent; the title hit ranks above the
	// newer body-only match; private, archived and partial matches stay out.
	items := search(map[string]any{"queries": []any{"智能印控 UAT 准出标准"}})
	if got := ids(items); !slices.Equal(got, []string{"doc-plan", "doc-body"}) {
		t.Fatalf("ranked ids = %v", got)
	}
	first := items[0]
	if first["title"] != "智能印控平台 UAT 方案" || first["url"] != "/"+actor.WorkspaceKey+"/document/plan-1" || first["section"] != "准出标准" || !strings.Contains(first["snippet"].(string), "P0/P1 缺陷关闭") || first["allTermsMatched"] != true {
		t.Fatalf("first hit = %v", first)
	}
	if first["project"].(map[string]any)["id"] != project.ID || first["team"].(map[string]any)["key"] != team.Key {
		t.Fatalf("parents = %v %v", first["project"], first["team"])
	}
	if items[0]["score"].(int) <= items[1]["score"].(int) {
		t.Fatalf("scores not descending: %v", items)
	}
	if snippet := items[1]["snippet"].(string); !strings.HasPrefix(snippet, "另外，智能印控平台的准出标准") {
		t.Fatalf("snippet is not on the line with the most keywords: %q", snippet)
	}

	// English, case-insensitive, words apart.
	if got := ids(search(map[string]any{"queries": []any{"uat blocker criteria"}})); !slices.Equal(got, []string{"doc-english"}) {
		t.Fatalf("English ids = %v", got)
	}

	// No document has every keyword: fall back to any keyword, flagged as partial.
	fallback := search(map[string]any{"queries": []any{"智能印控 不存在的词"}})
	if got := ids(fallback); len(got) != 3 || slices.Contains(got, "doc-private") || slices.Contains(got, "doc-archived") || fallback[0]["allTermsMatched"] != false {
		t.Fatalf("fallback = %v", fallback)
	}

	// Several keyword sets merge; filters narrow to a team and archived documents can be included.
	if got := ids(search(map[string]any{"queries": []any{"准出标准", "exit criteria"}})); len(got) != 3 || !slices.Contains(got, "doc-english") {
		t.Fatalf("merged queries = %v", got)
	}
	if got := ids(search(map[string]any{"queries": []any{"UAT"}, "team": team.Key})); slices.Contains(got, "doc-english") || len(got) != 2 {
		t.Fatalf("team filter = %v", got)
	}
	if got := ids(search(map[string]any{"queries": []any{"智能印控 UAT 准出标准"}, "includeArchived": true})); !slices.Contains(got, "doc-archived") || slices.Contains(got, "doc-private") {
		t.Fatalf("includeArchived = %v", got)
	}

	// list_documents' query uses the same keyword matching.
	listed, err := service.callFlowTool(ctx, actor, "list_documents", map[string]any{"query": "UAT 智能印控"})
	if err != nil {
		t.Fatal(err)
	}
	var listedIDs []string
	for _, document := range listed.(map[string]any)["items"].([]domain.Document) {
		listedIDs = append(listedIDs, document.ID)
	}
	if !slices.Equal(listedIDs, []string{"doc-plan", "doc-body"}) {
		t.Fatalf("list_documents query = %v", listedIDs)
	}

	// A key limited to no teams sees only workspace-level documents, as list_documents does.
	restricted := actor
	restricted.APIKey = domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}
	result, err := service.callFlowTool(ctx, restricted, "search_documents", map[string]any{"queries": []any{"UAT"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(result.(map[string]any)["items"].([]map[string]any)); !slices.Equal(got, []string{"doc-english"}) {
		t.Fatalf("team-restricted search = %v", got)
	}
}
