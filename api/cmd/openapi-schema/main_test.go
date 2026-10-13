package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"flow/api/internal/publicapi"
)

// serverRoutes scans cmd/server for every registered route, like
// scripts/generate-openapi.mjs.
func serverRoutes(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("cmd/server/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("server sources not found: %v", err)
	}
	pattern := regexp.MustCompile(`mux\.Handle(?:Func)?\(\s*"([A-Z]+)\s+([^"?]+)(?:\?[^" ]*)?"`)
	seen := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(raw), -1) {
			seen[match[1]+" "+match[2]] = true
		}
	}
	routes := make([]string, 0, len(seen))
	for route := range seen {
		routes = append(routes, route)
	}
	sort.Strings(routes)
	return routes
}

func TestEveryRouteIsTypedOrInternal(t *testing.T) {
	routes := serverRoutes(t)
	typed := map[string]bool{}
	for _, operation := range publicapi.Operations {
		typed[operation.Method+" "+operation.Path] = true
	}
	internal := map[string]bool{}
	for _, route := range publicapi.InternalRoutes {
		if internal[route] {
			t.Errorf("internal route listed twice: %s", route)
		}
		internal[route] = true
		if typed[route] {
			t.Errorf("%s has an Operation: remove it from InternalRoutes", route)
		}
	}
	known := map[string]bool{}
	var missing []string
	for _, route := range routes {
		known[route] = true
		if !typed[route] && !internal[route] {
			missing = append(missing, fmt.Sprintf("\t%q,", route))
		}
	}
	if len(missing) > 0 {
		t.Errorf("routes without an Operation in internal/publicapi/contract.go that are not listed in InternalRoutes (internal_routes.go); give them a contract or add these lines:\n%s", strings.Join(missing, "\n"))
	}
	for route := range typed {
		if !known[route] {
			t.Errorf("Operation %s matches no server route", route)
		}
	}
	for route := range internal {
		if !known[route] {
			t.Errorf("InternalRoutes lists %s, which is no server route any more", route)
		}
	}
	if len(typed) < 80 {
		t.Errorf("only %d typed operations", len(typed))
	}
}

func generate(t *testing.T) (*generator, map[string]schema, map[string]any) {
	t.Helper()
	g := newGenerator()
	operations := map[string]schema{}
	for _, operation := range publicapi.Operations {
		operations[strings.ToLower(operation.Method)+" "+operation.Path] = g.operation(operation)
	}
	return g, operations, g.webhooks()
}

// toJSON round-trips a generated value into plain JSON types.
func toJSON(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGeneratedReferencesResolveAndOperationIDsAreUnique(t *testing.T) {
	g, operations, webhooks := generate(t)
	document := toJSON(t, map[string]any{"schemas": g.schemas, "operations": operations, "webhooks": webhooks})
	schemas := toJSON(t, g.schemas).(map[string]any)
	var walk func(path string, value any)
	walk = func(path string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			if ref, ok := typed["$ref"].(string); ok && strings.HasPrefix(ref, "#/components/schemas/") {
				if _, exists := schemas[strings.TrimPrefix(ref, "#/components/schemas/")]; !exists {
					t.Errorf("%s: dangling reference %s", path, ref)
				}
			}
			for key, child := range typed {
				walk(path+"/"+key, child)
			}
		case []any:
			for index, child := range typed {
				walk(fmt.Sprintf("%s/%d", path, index), child)
			}
		}
	}
	walk("", document)
	ids := map[string]string{}
	for key, operation := range operations {
		id, _ := operation["operationId"].(string)
		if other, taken := ids[id]; taken {
			t.Errorf("operationId %s is used by %s and %s", id, other, key)
		}
		ids[id] = key
		if operation["summary"] == "" {
			t.Errorf("%s has no summary", key)
		}
	}
}

func TestListRoutesWithTwoShapesDocumentBoth(t *testing.T) {
	_, operations, _ := generate(t)
	for _, operation := range publicapi.Operations {
		if operation.Legacy == nil {
			continue
		}
		key := strings.ToLower(operation.Method) + " " + operation.Path
		generated := toJSON(t, operations[key]).(map[string]any)
		shapes, ok := generated["x-flow-response-shapes"].(map[string]any)
		if !ok || shapes["connection"] == nil || shapes["legacy"] == nil {
			t.Errorf("%s lacks x-flow-response-shapes", key)
		}
		success := generated["responses"].(map[string]any)["200"].(map[string]any)
		body := success["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
		if variants, _ := body["oneOf"].([]any); len(variants) != 2 {
			t.Errorf("%s: response is not a oneOf of both shapes: %v", key, body)
		}
		hasSwitch := false
		for _, parameter := range generated["parameters"].([]any) {
			if ref, _ := parameter.(map[string]any)["$ref"].(string); ref == "#/components/parameters/connection" {
				hasSwitch = true
			}
		}
		if !hasSwitch {
			t.Errorf("%s lacks the connection switch parameter", key)
		}
	}
}

func TestIssueListDeclaresPagingLimitsAndTypedFilter(t *testing.T) {
	_, operations, _ := generate(t)
	generated := toJSON(t, operations["get /api/issue-records"]).(map[string]any)
	byName := map[string]map[string]any{}
	for _, parameter := range generated["parameters"].([]any) {
		item := parameter.(map[string]any)
		if name, ok := item["name"].(string); ok {
			byName[name] = item
		}
	}
	for _, name := range []string{"first", "last"} {
		if byName[name]["schema"].(map[string]any)["maximum"] != float64(publicapi.MaxIssuePageSize) {
			t.Errorf("%s does not declare the maximum page size: %v", name, byName[name])
		}
	}
	filter := byName["filter"]["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if filter["$ref"] != "#/components/schemas/IssueFilter" {
		t.Errorf("filter is not typed: %v", filter)
	}
	archived := byName["archived"]["schema"].(map[string]any)["enum"].([]any)
	if len(archived) != 3 {
		t.Errorf("archived values = %v", archived)
	}
}

// docsWebhookTypes are the payload types of the events table in docs/webhooks.md.
func docsWebhookTypes(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../docs/webhooks.md")
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	row := regexp.MustCompile("(?m)^\\| `[a-z_]+` \\| `([A-Za-z]+)` \\|")
	for _, match := range row.FindAllStringSubmatch(string(raw), -1) {
		types = append(types, match[1])
	}
	if len(types) < 20 {
		t.Fatalf("docs/webhooks.md event table not found (%d rows)", len(types))
	}
	return types
}

func TestWebhookEventsCoverEveryDocumentedAndServedType(t *testing.T) {
	_, _, webhooks := generate(t)
	for _, name := range docsWebhookTypes(t) {
		if _, ok := webhooks[name]; !ok {
			t.Errorf("docs/webhooks.md documents %s but the webhooks section does not describe it", name)
		}
	}
	// The server's resource type table is the other source of truth.
	raw, err := os.ReadFile("cmd/server/webhook_delivery.go")
	if err != nil {
		t.Fatal(err)
	}
	entry := regexp.MustCompile(`\{"([a-z_]+)", "([A-Za-z]+)"\},`)
	served := entry.FindAllStringSubmatch(string(raw), -1)
	if len(served) < 20 {
		t.Fatalf("server webhookResourceTypes not found (%d)", len(served))
	}
	for _, match := range served {
		event, ok := webhooks[match[2]]
		if !ok {
			t.Errorf("server delivers %s (%s) but the webhooks section does not describe it", match[2], match[1])
			continue
		}
		_ = event
		var declared *publicapi.WebhookEvent
		for index := range publicapi.WebhookEvents {
			if publicapi.WebhookEvents[index].Type == match[2] {
				declared = &publicapi.WebhookEvents[index]
			}
		}
		if declared == nil || declared.Resource != match[1] {
			t.Errorf("%s: resource type %q does not match the server's %q", match[2], declared.Resource, match[1])
		}
	}
	if _, ok := webhooks["WebhookTest"]; !ok {
		t.Error("WebhookTest event missing")
	}
}

// docsJSONExamples returns the parsed ```json blocks of a Markdown file.
func docsJSONExamples(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var examples []map[string]any
	block := regexp.MustCompile("(?s)```json\n(.*?)```")
	for _, match := range block.FindAllStringSubmatch(string(raw), -1) {
		var value map[string]any
		if json.Unmarshal([]byte(match[1]), &value) == nil {
			examples = append(examples, value)
		}
	}
	return examples
}

func TestDocumentedWebhookPayloadsValidateAgainstTheirSchemas(t *testing.T) {
	g, _, webhooks := generate(t)
	schemas := toJSON(t, g.schemas).(map[string]any)
	validated := map[string]bool{}
	for _, example := range docsJSONExamples(t, "../docs/webhooks.md") {
		typeName, _ := example["type"].(string)
		action, _ := example["action"].(string)
		if typeName == "" || action == "" {
			continue
		}
		event, ok := webhooks[typeName].(schema)
		if !ok {
			// Legacy (version 1) examples use resource names.
			continue
		}
		payload := toJSON(t, event).(map[string]any)["post"].(map[string]any)["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"]
		if problems := validateSchema(schemas, payload, example, typeName); len(problems) > 0 {
			t.Errorf("docs/webhooks.md %s example does not match its schema:\n  %s", typeName, strings.Join(problems, "\n  "))
		}
		validated[typeName] = true
	}
	for _, name := range []string{"Issue", "IssueSLA", "OAuthApp", "PermissionChange", "AppUserNotification", "AgentSessionEvent"} {
		if !validated[name] {
			t.Errorf("no documented %s example was validated", name)
		}
	}
}

func TestWebhookSchemasRejectBrokenPayloads(t *testing.T) {
	g, _, webhooks := generate(t)
	schemas := toJSON(t, g.schemas).(map[string]any)
	payload := toJSON(t, webhooks["Issue"]).(map[string]any)["post"].(map[string]any)["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"]
	valid := map[string]any{"action": "update", "type": "Issue", "createdAt": "2026-10-12T16:28:47.818Z", "data": map[string]any{"id": "issue_1", "title": "x"}, "organizationId": "w", "webhookId": "h", "webhookTimestamp": float64(1)}
	if problems := validateSchema(schemas, payload, valid, "Issue"); len(problems) > 0 {
		t.Fatalf("minimal payload rejected: %v", problems)
	}
	for name, mutate := range map[string]func(map[string]any){
		"unknown action":      func(m map[string]any) { m["action"] = "created" },
		"wrong type":          func(m map[string]any) { m["type"] = "Comment" },
		"data without id":     func(m map[string]any) { m["data"] = map[string]any{"title": "x"} },
		"missing webhookId":   func(m map[string]any) { delete(m, "webhookId") },
		"timestamp as string": func(m map[string]any) { m["webhookTimestamp"] = "1" },
	} {
		broken := map[string]any{}
		for key, value := range valid {
			broken[key] = value
		}
		mutate(broken)
		if len(validateSchema(schemas, payload, broken, "Issue")) == 0 {
			t.Errorf("%s was accepted", name)
		}
	}
}

// validateSchema is a small JSON Schema checker for the keywords the
// generator emits: $ref, type, properties, required, items,
// additionalProperties, enum, const, anyOf, oneOf, allOf.
func validateSchema(schemas map[string]any, schemaValue any, value any, path string) []string {
	node, ok := schemaValue.(map[string]any)
	if !ok {
		return nil
	}
	if ref, ok := node["$ref"].(string); ok {
		target := schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
		if target == nil {
			return []string{path + ": dangling " + ref}
		}
		return validateSchema(schemas, target, value, path)
	}
	var problems []string
	if variants, ok := node["allOf"].([]any); ok {
		for _, variant := range variants {
			problems = append(problems, validateSchema(schemas, variant, value, path)...)
		}
	}
	for _, keyword := range []string{"anyOf", "oneOf"} {
		variants, ok := node[keyword].([]any)
		if !ok {
			continue
		}
		matches := 0
		var attempts []string
		for _, variant := range variants {
			if issues := validateSchema(schemas, variant, value, path); len(issues) == 0 {
				matches++
			} else {
				attempts = append(attempts, issues...)
			}
		}
		if matches == 0 || keyword == "oneOf" && matches > 1 {
			problems = append(problems, fmt.Sprintf("%s: matches %d of the %s variants (%s)", path, matches, keyword, strings.Join(attempts, "; ")))
		}
	}
	if constant, ok := node["const"]; ok && fmt.Sprint(constant) != fmt.Sprint(value) {
		problems = append(problems, fmt.Sprintf("%s: %v is not %v", path, value, constant))
	}
	if enum, ok := node["enum"].([]any); ok && !slices.ContainsFunc(enum, func(item any) bool { return fmt.Sprint(item) == fmt.Sprint(value) }) {
		problems = append(problems, fmt.Sprintf("%s: %v is not one of %v", path, value, enum))
	}
	if kind, ok := node["type"]; ok {
		kinds := []string{}
		switch typed := kind.(type) {
		case string:
			kinds = append(kinds, typed)
		case []any:
			for _, item := range typed {
				kinds = append(kinds, fmt.Sprint(item))
			}
		}
		if !slices.ContainsFunc(kinds, func(kind string) bool { return jsonTypeMatches(kind, value) }) {
			return append(problems, fmt.Sprintf("%s: %T is not %v", path, value, kinds))
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		properties, _ := node["properties"].(map[string]any)
		if required, ok := node["required"].([]any); ok {
			for _, name := range required {
				if _, present := typed[fmt.Sprint(name)]; !present {
					problems = append(problems, fmt.Sprintf("%s: missing required %s", path, name))
				}
			}
		}
		for name, child := range typed {
			if schemaFor, known := properties[name]; known {
				problems = append(problems, validateSchema(schemas, schemaFor, child, path+"."+name)...)
			} else if node["additionalProperties"] == false {
				problems = append(problems, fmt.Sprintf("%s: unexpected property %s", path, name))
			} else if additional, ok := node["additionalProperties"].(map[string]any); ok {
				problems = append(problems, validateSchema(schemas, additional, child, path+"."+name)...)
			}
		}
	case []any:
		if items, ok := node["items"]; ok {
			for index, child := range typed {
				problems = append(problems, validateSchema(schemas, items, child, fmt.Sprintf("%s[%d]", path, index))...)
			}
		}
	}
	return problems
}

func jsonTypeMatches(kind string, value any) bool {
	switch kind {
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == float64(int64(number))
	case "number":
		_, ok := value.(float64)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	}
	return true
}

func TestMain(m *testing.M) {
	// The generator reads comments relative to the api/ directory.
	if err := os.Chdir("../.."); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
