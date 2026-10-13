package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestMCPCustomerToolsCreateLogRequestAndDelete(t *testing.T) {
	f := newMCPContractFixture(t)
	issue := f.data.Issues[0]
	customer := mcpObject(t, f, "save_customer", map[string]any{"name": "QA fix – Acme", "domains": []string{"https://www.Acme.test/"}, "status": "Prospect", "owner": "me", "annualRevenue": 120000, "size": 40})
	if customer["created"] != true || customer["name"] != "QA fix – Acme" || customer["status"] != "Prospect" || customer["size"] != float64(40) || !strings.Contains(customer["url"].(string), "/customer/") {
		t.Fatalf("created: %v", customer)
	}
	if domains := customer["domains"].([]any); len(domains) != 1 || domains[0] != "acme.test" {
		t.Fatalf("domains: %v", domains)
	}
	if message := mcpToolError(t, f, "save_customer", map[string]any{"id": "acme.test", "tier": "Enterprise"}); !strings.Contains(message, "no customer tiers") {
		t.Fatalf("unknown tier: %s", message)
	}
	renamed := mcpObject(t, f, "save_customer", map[string]any{"id": "acme.test", "name": "QA fix – Acme Corp", "owner": nil})
	if renamed["created"] != false || renamed["id"] != customer["id"] || renamed["owner"] != "" {
		t.Fatalf("renamed: %v", renamed)
	}

	// Models send every optional field with an empty default; those must not wipe the customer.
	defaults := mcpObject(t, f, "save_customer", map[string]any{"id": customer["id"], "name": "", "status": "", "logoUrl": "", "tier": "", "owner": "", "annualRevenue": 0, "size": 0, "domains": []string{}, "externalIds": []string{}})
	if defaults["name"] != "QA fix – Acme Corp" || defaults["status"] != "Prospect" || defaults["size"] != float64(40) || defaults["annualRevenue"] != float64(120000) || len(defaults["domains"].([]any)) != 1 {
		t.Fatalf("empty defaults changed the customer: %v", defaults)
	}
	if created := mcpObject(t, f, "save_customer", map[string]any{"id": "", "name": "QA fix – Defaults", "status": "", "tier": "", "owner": nil, "size": 0, "externalIds": []string{}}); created["created"] != true || created["status"] != "Active" {
		t.Fatalf("create with empty defaults: %v", created)
	}
	need := mcpObject(t, f, "save_customer_request", map[string]any{"customer": "QA fix – Acme Corp", "issue": issue.Identifier, "body": "Needs exports of 50k issues.", "important": true})
	if need["created"] != true || need["name"] != "QA fix – Acme Corp" || need["important"] != true || need["body"] != "Needs exports of 50k issues." {
		t.Fatalf("request: %v", need)
	}
	if linked := need["issue"].(map[string]any); linked["identifier"] != issue.Identifier {
		t.Fatalf("request issue: %v", linked)
	}
	updated := mcpObject(t, f, "save_customer_request", map[string]any{"id": need["id"], "important": false, "body": "", "issue": "", "project": "", "sourceUrl": ""})
	if updated["important"] != false || updated["created"] != false || updated["body"] != "Needs exports of 50k issues." || updated["issue"] == nil {
		t.Fatalf("updated request: %v", updated)
	}
	got := mcpObject(t, f, "get_customer", map[string]any{"query": "acme.test"})
	requests := got["requests"].([]any)
	if got["name"] != "QA fix – Acme Corp" || got["requestCount"] != float64(1) || len(requests) != 1 || requests[0].(map[string]any)["issue"].(map[string]any)["identifier"] != issue.Identifier {
		t.Fatalf("get_customer: %v", got)
	}
	listed := mcpObject(t, f, "list_customers", map[string]any{"includeRequests": true})
	if items := mcpItems(t, listed); len(items) != 2 || items[0]["requestCount"] != float64(1) {
		t.Fatalf("list_customers: %v", listed)
	}

	for _, scenario := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"customer": "Globex", "issue": issue.Identifier, "body": "x"}, "create it with save_customer"},
		{map[string]any{"customer": "QA fix – Acme Corp", "body": "x"}, "issue or project is required"},
		{map[string]any{"issue": issue.Identifier, "body": "x"}, "customer is required"},
		{map[string]any{"customer": "QA fix – Acme Corp", "issue": "NOPE-999", "body": "x"}, "not found"},
		{map[string]any{"id": "customer_request_missing", "body": "x"}, "not found"},
	} {
		if message := mcpToolError(t, f, "save_customer_request", scenario.args); !strings.Contains(message, scenario.want) {
			t.Errorf("%v: %s", scenario.args, message)
		}
	}
	if message := mcpToolError(t, f, "save_customer", map[string]any{"size": 3}); !strings.Contains(message, "name is required") {
		t.Fatalf("create without name: %s", message)
	}
	readOnly := f.addMCPKey(t, "read-only-customers", "usr_admin", []string{"read"}, nil)
	mcpDeniedWith(t, f, readOnly, "save_customer", map[string]any{"name": "Denied"}, "write scope")
	mcpDeniedWith(t, f, readOnly, "delete_customer", map[string]any{"id": customer["id"]}, "write scope")

	deletedNeed := mcpObject(t, f, "delete_customer_request", map[string]any{"id": need["id"]})
	if deletedNeed["deleted"] != true || deletedNeed["restorable"] != true || deletedNeed["name"] != "QA fix – Acme Corp" {
		t.Fatalf("deleted request: %v", deletedNeed)
	}
	f.restoreFromTrash(t, "customer_request", need["id"].(string))
	deleted := mcpObject(t, f, "delete_customer", map[string]any{"id": "QA fix – Acme Corp"})
	if deleted["deleted"] != true || deleted["restorable"] != true || deleted["restorableUntil"] == nil || mcpAffected(t, deleted)["requests"] != float64(1) {
		t.Fatalf("deleted customer: %v", deleted)
	}
	if message := mcpToolError(t, f, "get_customer", map[string]any{"query": customer["id"]}); !strings.Contains(message, "not found") {
		t.Fatalf("deleted customer still found: %s", message)
	}
	f.restoreFromTrash(t, "customer", customer["id"].(string))
	if !slices.ContainsFunc(f.repository.Bootstrap().Customers, func(item domain.Customer) bool { return item.ID == customer["id"] }) {
		t.Fatal("customer not restored")
	}
}

func TestMCPCustomerToolsRespectManualEditsInApp(t *testing.T) {
	f := newMCPContractFixture(t)
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "test.customer_settings", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.FeatureSettings.CustomerManualEdits = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	inApp := mcpActor{WorkspaceKey: f.data.Workspace.URLKey, User: f.data.Viewer, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
	if _, err := f.service.callFlowTool(t.Context(), inApp, "save_customer", map[string]any{"name": "Synced elsewhere"}); err == nil || !strings.Contains(err.Error(), "manual customer attribute edits are disabled") {
		t.Fatalf("in-app create with manual edits off: %v", err)
	}
	// Integration keys may still sync customers, as on the REST API.
	if created := mcpObject(t, f, "save_customer", map[string]any{"name": "Synced elsewhere"}); created["created"] != true {
		t.Fatalf("API key create: %v", created)
	}
}

func TestMCPApplicationScopesGateCustomerAndInitiativeTools(t *testing.T) {
	tools := map[string]flowMCPTool{}
	for _, tool := range mustFlowMCPTools(t) {
		tools[tool.Name] = tool
	}
	app := func(scopes ...string) domain.APIKey { return domain.APIKey{Actor: "app", Scopes: scopes} }
	for _, scenario := range []struct {
		tool     string
		key      domain.APIKey
		readonly bool
		allowed  bool
	}{
		{"get_customer", app("customer:read"), false, true},
		{"save_customer", app("customer:read"), false, false},
		{"save_customer", app("customer:write"), false, true},
		{"save_customer", app("customer:write"), true, false},
		{"save_customer_request", app("customer:write"), false, true},
		{"delete_customer", app("write"), false, false},
		{"delete_customer", app("customer:write"), false, true},
		{"delete_initiative", app("initiative:read"), false, false},
		{"delete_initiative", app("initiative:write"), false, true},
		{"delete_project", app("write"), false, true},
		{"delete_project", domain.APIKey{Scopes: []string{"read"}}, false, false},
		{"save_cycle", domain.APIKey{Scopes: []string{"read", "write"}}, false, true},
	} {
		tool, ok := tools[scenario.tool]
		if !ok {
			t.Fatalf("%s missing from the inventory", scenario.tool)
		}
		if got := mcpToolAllowed(tool, scenario.key, scenario.readonly, map[string]any{}); got != scenario.allowed {
			t.Errorf("%s with %v (readonly %v) allowed=%v, want %v", scenario.tool, scenario.key.Scopes, scenario.readonly, got, scenario.allowed)
		}
	}
}

func TestMCPSaveCycleEditsNameDescriptionAndLinksOnly(t *testing.T) {
	f := newMCPContractFixture(t)
	saved := mcpObject(t, f, "save_cycle", map[string]any{"id": "current", "team": "TST", "name": "QA fix – Sprint", "description": "Ship exports", "links": []map[string]any{{"url": "https://example.test/plan", "title": "Plan"}}})
	if saved["number"] != float64(48) || saved["name"] != "QA fix – Sprint" || saved["description"] != "Ship exports" || len(saved["linksAdded"].([]any)) != 1 {
		t.Fatalf("saved: %v", saved)
	}
	cycle := f.repository.Bootstrap().Cycles[slices.IndexFunc(f.repository.Bootstrap().Cycles, func(item domain.Cycle) bool { return item.ID == "cycle_48" })]
	if cycle.Name != "QA fix – Sprint" || len(cycle.Resources) != 1 || cycle.Resources[0].URL != "https://example.test/plan" {
		t.Fatalf("stored cycle: %+v", cycle)
	}
	if byNumber := mcpObject(t, f, "save_cycle", map[string]any{"id": "49", "description": "Next"}); byNumber["number"] != float64(49) {
		t.Fatalf("by number: %v", byNumber)
	}
	for _, scenario := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"id": "48"}, "provide name, description, or links"},
		{map[string]any{"id": "99", "name": "x"}, "not found"},
		{map[string]any{"id": "48", "links": []map[string]any{{"url": "notaurl"}}}, "valid link"},
		{map[string]any{"name": "New cycle"}, "id"},
	} {
		if message := mcpToolError(t, f, "save_cycle", scenario.args); !strings.Contains(message, scenario.want) {
			t.Errorf("%v: %s", scenario.args, message)
		}
	}
}

func mustFlowMCPTools(t *testing.T) []flowMCPTool {
	t.Helper()
	var tools []flowMCPTool
	if err := json.Unmarshal(flowMCPToolInventory, &tools); err != nil {
		t.Fatal(err)
	}
	for index := range tools {
		tools[index].Name = strings.TrimPrefix(tools[index].Name, "mcp__flow.")
	}
	return tools
}
