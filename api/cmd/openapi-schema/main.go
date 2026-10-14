// Command openapi-schema prints the typed part of Flow's OpenAPI document as
// JSON: component schemas reflected from the Go types the server encodes and
// decodes, and the operations of the public contract (internal/publicapi).
// scripts/generate-openapi.mjs merges it with the route inventory. Run from
// the api/ directory: go run ./cmd/openapi-schema
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/publicapi"
)

type schema = map[string]any

type generator struct {
	schemas map[string]schema
	names   map[schemaKey]string
	used    map[string]schemaKey
	docs    map[string]string // "pkg.Type" and "pkg.Type.Field"
}

// schemaKey identifies a component: a Go type in full or, for webhook
// payload entities, in its partial form (see partial struct tags).
type schemaKey struct {
	t       reflect.Type
	partial bool
}

var (
	timeType    = reflect.TypeOf(time.Time{})
	rawJSONType = reflect.TypeOf(json.RawMessage{})
)

func main() {
	g := newGenerator()
	operations := map[string]schema{}
	for _, operation := range publicapi.Operations {
		key := strings.ToLower(operation.Method) + " " + operation.Path
		if _, exists := operations[key]; exists {
			fail(fmt.Errorf("duplicate contract operation %s", key))
		}
		operations[key] = g.operation(operation)
	}
	g.ref(reflect.TypeOf(publicapi.Error{}), false)
	g.ref(reflect.TypeOf(publicapi.PageInfo{}), false)
	webhooks := g.webhooks()
	scopes := map[string]string{
		"read":   "Read workspace data",
		"write":  "Create and change workspace data",
		"admin":  "Workspace administration (webhooks, audit log, settings)",
		"openid": "OpenID Connect identity",
		"email":  "Email address of the user",
	}
	for _, scope := range domain.ApplicationScopes() {
		if _, ok := scopes[scope]; !ok {
			scopes[scope] = "Application scope " + scope
		}
	}
	output := map[string]any{"schemas": g.schemas, "operations": operations, "scopes": scopes, "webhooks": webhooks, "internalRoutes": publicapi.InternalRoutes}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(output); err != nil {
		fail(err)
	}
}

// newGenerator loads the type and field comments of the contract packages.
// It must run from the api/ directory (the tests chdir there).
func newGenerator() *generator {
	g := &generator{schemas: map[string]schema{}, names: map[schemaKey]string{}, used: map[string]schemaKey{}, docs: map[string]string{}}
	for _, dir := range []string{"internal/domain", "internal/publicapi", "internal/store"} {
		g.loadDocs(dir)
	}
	return g
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// loadDocs reads type and field comments so schemas carry descriptions.
func (g *generator) loadDocs(dir string) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		fail(err)
	}
	set := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(set, path, nil, parser.ParseComments)
		if err != nil {
			fail(err)
		}
		pkg := file.Name.Name
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, spec := range general.Specs {
				typeSpec := spec.(*ast.TypeSpec)
				doc := typeSpec.Doc
				if doc == nil && len(general.Specs) == 1 {
					doc = general.Doc
				}
				if text := cleanDoc(doc); text != "" {
					g.docs[pkg+"."+typeSpec.Name.Name] = text
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range structType.Fields.List {
					text := cleanDoc(field.Doc)
					if text == "" {
						text = cleanDoc(field.Comment)
					}
					for _, name := range field.Names {
						if text != "" {
							g.docs[pkg+"."+typeSpec.Name.Name+"."+name.Name] = text
						}
					}
				}
			}
		}
	}
}

var whitespace = regexp.MustCompile(`\s+`)

func cleanDoc(group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	return strings.TrimSpace(whitespace.ReplaceAllString(group.Text(), " "))
}

func packageName(t reflect.Type) string {
	return filepath.Base(t.PkgPath())
}

var genericName = regexp.MustCompile(`^([A-Za-z0-9_]+)\[(.+)\]$`)

// schemaName gives each named Go type a stable component name. Generic
// instantiations become <Arg><Base> (Connection[domain.Team] -> TeamConnection).
// The partial form of a type is named <Name>Partial.
func (g *generator) schemaName(t reflect.Type, partial bool) string {
	key := schemaKey{t, partial}
	if name, ok := g.names[key]; ok {
		return name
	}
	name := t.Name()
	if match := genericName.FindStringSubmatch(name); match != nil {
		argument := match[2]
		if index := strings.LastIndex(argument, "."); index >= 0 {
			argument = argument[index+1:]
		}
		name = argument + match[1]
	}
	if partial {
		name += "Partial"
	}
	if other, taken := g.used[name]; taken && other != key {
		pkg := packageName(t)
		name = strings.ToUpper(pkg[:1]) + pkg[1:] + name
	}
	g.names[key] = name
	g.used[name] = key
	return name
}

func (g *generator) ref(t reflect.Type, partial bool) schema {
	name := g.schemaName(t, partial)
	if _, done := g.schemas[name]; !done {
		g.schemas[name] = schema{} // placeholder breaks recursion
		g.schemas[name] = g.structSchema(t, partial)
	}
	return schema{"$ref": "#/components/schemas/" + name}
}

// schemaFor describes a Go type. In partial form every named struct becomes
// its <Name>Partial component, where only id (when the struct has one) is
// required: webhook payloads carry the REST model of an entity, but older
// webhooks and nested objects carry a subset of it.
func (g *generator) schemaFor(t reflect.Type, partial bool) schema {
	switch {
	case t == timeType:
		return schema{"type": "string", "format": "date-time"}
	case t == rawJSONType:
		return schema{}
	}
	switch t.Kind() {
	case reflect.Pointer:
		inner := g.schemaFor(t.Elem(), partial)
		return nullable(inner)
	case reflect.Struct:
		if t.Name() == "" {
			return g.structSchema(t, partial)
		}
		return g.ref(t, partial)
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return schema{"type": "string", "format": "byte"}
		}
		return schema{"type": "array", "items": g.schemaFor(t.Elem(), partial)}
	case reflect.Map:
		value := g.schemaFor(t.Elem(), partial)
		if len(value) == 0 {
			return schema{"type": "object", "additionalProperties": true}
		}
		return schema{"type": "object", "additionalProperties": value}
	case reflect.Interface:
		return schema{}
	case reflect.Bool:
		return schema{"type": "boolean"}
	case reflect.Int, reflect.Int32, reflect.Int16, reflect.Int8, reflect.Uint, reflect.Uint32, reflect.Uint16, reflect.Uint8:
		return schema{"type": "integer"}
	case reflect.Int64, reflect.Uint64:
		return schema{"type": "integer", "format": "int64"}
	case reflect.Float32, reflect.Float64:
		return schema{"type": "number"}
	case reflect.String:
		return schema{"type": "string"}
	}
	return schema{}
}

func nullable(inner schema) schema {
	if ref, ok := inner["$ref"]; ok {
		return schema{"anyOf": []any{schema{"$ref": ref}, schema{"type": "null"}}}
	}
	if kind, ok := inner["type"].(string); ok {
		copied := schema{}
		for key, value := range inner {
			copied[key] = value
		}
		copied["type"] = []any{kind, "null"}
		return copied
	}
	return inner
}

func (g *generator) structSchema(t reflect.Type, partial bool) schema {
	properties := schema{}
	required := []string{}
	g.collectFields(t, properties, &required, partial)
	result := schema{"type": "object", "properties": properties}
	if partial {
		required = required[:0]
		if _, hasID := properties["id"]; hasID {
			required = append(required, "id")
		}
	}
	if len(required) > 0 {
		sort.Strings(required)
		result["required"] = slices.Compact(required)
	}
	if t.Name() != "" {
		if doc := g.docs[packageName(t)+"."+baseTypeName(t)]; doc != "" {
			if partial {
				doc += " In webhook payloads only id is guaranteed."
			}
			result["description"] = doc
		}
	}
	return result
}

func baseTypeName(t reflect.Type) string {
	name := t.Name()
	if index := strings.Index(name, "["); index >= 0 {
		return name[:index]
	}
	return name
}

// applyTags adds the validation keywords of the enum:"a,b" and const:"x"
// struct tags.
func applyTags(property schema, tag reflect.StructTag) schema {
	enum, hasEnum := tag.Lookup("enum")
	constant, hasConst := tag.Lookup("const")
	if !hasEnum && !hasConst {
		return property
	}
	copied := schema{}
	for key, value := range property {
		copied[key] = value
	}
	// A pointer field is nullable: the enum applies to its non-null value.
	if hasEnum {
		values := []any{}
		numeric := false
		if kind, ok := copied["type"]; ok {
			if text, isString := kind.(string); isString && (text == "integer" || text == "number") {
				numeric = true
			}
			if list, isList := kind.([]any); isList && len(list) > 0 && (list[0] == "integer" || list[0] == "number") {
				numeric = true
			}
		}
		for _, part := range strings.Split(enum, ",") {
			if numeric {
				number, err := strconv.Atoi(part)
				if err != nil {
					fail(fmt.Errorf("enum tag %q: %v", enum, err))
				}
				values = append(values, number)
			} else {
				values = append(values, part)
			}
		}
		if _, nullableType := copied["type"].([]any); nullableType {
			values = append(values, nil)
		}
		copied["enum"] = values
	}
	if hasConst {
		copied["const"] = constant
	}
	return copied
}

func (g *generator) collectFields(t reflect.Type, properties schema, required *[]string, partial bool) {
	for index := 0; index < t.NumField(); index++ {
		field := t.Field(index)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, options, _ := strings.Cut(tag, ",")
		if field.Anonymous && name == "" {
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				g.collectFields(embedded, properties, required, partial)
				continue
			}
		}
		if name == "" {
			name = field.Name
		}
		property := g.schemaFor(field.Type, partial || field.Tag.Get("partial") == "true")
		if strings.Contains(options, "string") {
			property = schema{"type": "string"}
		}
		property = applyTags(property, field.Tag)
		if doc := g.docs[packageName(t)+"."+baseTypeName(t)+"."+field.Name]; doc != "" {
			if _, isRef := property["$ref"]; isRef {
				property = schema{"allOf": []any{property}, "description": doc}
			} else {
				copied := schema{}
				for key, value := range property {
					copied[key] = value
				}
				copied["description"] = doc
				property = copied
			}
		}
		properties[name] = property
		if !strings.Contains(options, "omitempty") && field.Type.Kind() != reflect.Pointer && !strings.Contains(options, "omitzero") {
			*required = append(*required, name)
		} else if i := slices.Index(*required, name); i >= 0 {
			// An outer field shadows an embedded one that was required.
			*required = slices.Delete(*required, i, i+1)
		}
	}
}

var pathParameter = regexp.MustCompile(`\{([^}]+)\}`)

func (g *generator) operation(operation publicapi.Operation) schema {
	result := schema{"summary": operation.Summary, "tags": []string{operation.Tag}, "operationId": operationID(operation.Method, operation.Path), "x-flow-typed": true}
	if operation.Description != "" {
		result["description"] = operation.Description
	}
	parameters := []any{}
	for _, match := range pathParameter.FindAllStringSubmatch(operation.Path, -1) {
		parameters = append(parameters, schema{"name": match[1], "in": "path", "required": true, "schema": schema{"type": "string"}})
	}
	if operation.Connection {
		orderings := operation.Orderings
		if len(orderings) == 0 {
			orderings = []string{"createdAt", "updatedAt"}
		}
		first, last := schema{"$ref": "#/components/parameters/first"}, schema{"$ref": "#/components/parameters/last"}
		if operation.MaxPageSize > 0 {
			size := func(name, description string, withDefault bool) schema {
				value := schema{"type": "integer", "minimum": 1, "maximum": operation.MaxPageSize}
				if withDefault {
					value["default"] = 50
				}
				return schema{"name": name, "in": "query", "description": description, "schema": value}
			}
			first = size("first", fmt.Sprintf("Page size counting forward from `after` (1-%d, default 50). Alias: limit.", operation.MaxPageSize), true)
			last = size("last", fmt.Sprintf("Page size counting backward from `before` (1-%d); without `before` the final page.", operation.MaxPageSize), false)
		}
		parameters = append(parameters, first, schema{"$ref": "#/components/parameters/after"}, last, schema{"$ref": "#/components/parameters/before"},
			schema{"name": "orderBy", "in": "query", "description": "Sort field; timestamps are newest first.", "schema": schema{"type": "string", "enum": orderings, "default": "createdAt"}},
			schema{"$ref": "#/components/parameters/includeArchived"})
		if operation.Legacy != nil {
			parameters = append(parameters, schema{"$ref": "#/components/parameters/connection"})
		}
		explicitFilter := slices.ContainsFunc(operation.Params, func(param publicapi.Param) bool { return param.Name == "filter" })
		switch {
		case explicitFilter:
		case operation.FilterType != nil:
			parameters = append(parameters, schema{"name": "filter", "in": "query", "description": "Filter tree (JSON), see IssueFilter.", "content": schema{"application/json": schema{"schema": g.schemaFor(reflect.TypeOf(operation.FilterType), false)}}})
		case len(operation.Filter) > 0:
			parameters = append(parameters, schema{"name": "filter", "in": "query", "description": "Filter object (JSON). Filterable fields: " + strings.Join(operation.Filter, ", ") + ". Comparators: eq, neq, in, nin, lt, lte, gt, gte, eqIgnoreCase, neqIgnoreCase, startsWith, startsWithIgnoreCase, notStartsWith, endsWith, notEndsWith, contains, notContains, containsIgnoreCase, notContainsIgnoreCase, null; combine with and/or.", "content": schema{"application/json": schema{"schema": schema{"type": "object", "additionalProperties": true}}}})
		}
	}
	for _, param := range operation.Params {
		in := param.In
		if in == "" {
			in = "query"
		}
		kind := param.Type
		if kind == "" {
			kind = "string"
		}
		value := schema{"type": kind}
		if len(param.Enum) > 0 {
			value["enum"] = param.Enum
		}
		if param.Array {
			value = schema{"type": "array", "items": value}
		}
		item := schema{"name": param.Name, "in": in, "schema": value}
		if param.JSON != nil {
			delete(item, "schema")
			item["content"] = schema{"application/json": schema{"schema": param.JSON}}
		}
		if param.Form != "" {
			item["x-flow-form"] = param.Form
		}
		if param.Required {
			item["required"] = true
		}
		if param.Description != "" {
			item["description"] = param.Description
		}
		parameters = append(parameters, item)
	}
	if len(parameters) > 0 {
		result["parameters"] = parameters
	}
	switch {
	case operation.RequestBinary:
		result["requestBody"] = schema{"required": true, "content": schema{"application/octet-stream": schema{"schema": schema{"type": "string", "format": "binary"}}}}
	case operation.Request != nil:
		result["requestBody"] = schema{"required": true, "content": schema{"application/json": schema{"schema": g.schemaFor(reflect.TypeOf(operation.Request), false)}}}
	}
	responses := schema{}
	status := operation.Status
	if status == 0 {
		status = 200
	}
	rateHeaders := schema{}
	for _, name := range []string{"X-RateLimit-Requests-Limit", "X-RateLimit-Requests-Remaining", "X-RateLimit-Requests-Reset", "X-Complexity", "X-RateLimit-Complexity-Limit", "X-RateLimit-Complexity-Remaining", "X-RateLimit-Complexity-Reset"} {
		rateHeaders[name] = schema{"$ref": "#/components/headers/" + name}
	}
	switch {
	case operation.ResponseBinary:
		responses[strconv.Itoa(status)] = schema{"description": "File contents", "content": schema{"application/octet-stream": schema{"schema": schema{"type": "string", "format": "binary"}}}}
	case operation.Response == nil:
		if operation.Status == 0 {
			status = 204
		}
		responses[strconv.Itoa(status)] = schema{"description": "No content", "headers": rateHeaders}
	default:
		body := g.schemaFor(reflect.TypeOf(operation.Response), false)
		if operation.Connection {
			body = g.connection(reflect.TypeOf(operation.Response))
		}
		if operation.Legacy != nil {
			// The response depends on the arguments, so both shapes are
			// documented: oneOf for readers of the schema and the
			// x-flow-response-shapes extension for generators, which can
			// then emit one method per shape.
			legacy := g.schemaFor(reflect.TypeOf(operation.Legacy), false)
			result["x-flow-response-shapes"] = schema{"connection": body, "legacy": legacy, "connectionWhen": "first, last, after, before, orderBy or connection=1"}
			body = schema{"oneOf": []any{body, legacy}, "description": "A connection when first, last, after, before, orderBy or connection=1 is given, otherwise the legacy response."}
		}
		responses[strconv.Itoa(status)] = schema{"description": http200(operation), "headers": rateHeaders, "content": schema{"application/json": schema{"schema": body}}}
		if operation.AltStatus != 0 {
			responses[strconv.Itoa(operation.AltStatus)] = schema{"description": "Updated existing resource", "headers": rateHeaders, "content": schema{"application/json": schema{"schema": body}}}
		}
	}
	for code, ref := range map[string]string{"400": "InvalidInput", "401": "AuthenticationError", "403": "Forbidden", "404": "NotFound", "429": "RateLimited"} {
		responses[code] = schema{"$ref": "#/components/responses/" + ref}
	}
	responses["default"] = schema{"$ref": "#/components/responses/Error"}
	result["responses"] = responses
	if operation.Public {
		result["security"] = []any{}
	} else if len(operation.Scopes) > 0 {
		result["security"] = []any{schema{"sessionCookie": []string{}}, schema{"bearerApiKey": []string{}}, schema{"oauth2": operation.Scopes}}
	}
	return result
}

func http200(operation publicapi.Operation) string {
	if operation.Connection {
		return "A page of results"
	}
	return "Success"
}

// connection builds (once) the <Node>Connection component for a node type.
func (g *generator) connection(node reflect.Type) schema {
	elem := node
	for elem.Kind() == reflect.Slice || elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}
	nodeSchema := g.schemaFor(node, false)
	name := elem.Name() + "Connection"
	if _, done := g.schemas[name]; !done {
		g.ref(reflect.TypeOf(publicapi.PageInfo{}), false)
		g.schemas[name] = schema{"type": "object", "required": []string{"nodes", "pageInfo"}, "properties": schema{
			"nodes":      schema{"type": "array", "items": nodeSchema, "description": "Nodes ordered by orderBy (timestamps newest first)."},
			"pageInfo":   schema{"$ref": "#/components/schemas/PageInfo"},
			"totalCount": schema{"type": "integer", "format": "int64", "description": "Number of matching nodes, on endpoints that report it (issues, with includeTotal=true)."},
		}}
	}
	return schema{"$ref": "#/components/schemas/" + name}
}

// webhooks describes the payload of every webhook event: the OpenAPI 3.1
// `webhooks` section. Each entry is a POST that Flow sends to the receiver.
func (g *generator) webhooks() map[string]any {
	result := map[string]any{}
	headers := schema{
		"Flow-Delivery":         schema{"description": "Unique id of the delivery; a redelivery gets a new one.", "schema": schema{"type": "string"}},
		"Flow-Event":            schema{"description": "The payload type.", "schema": schema{"type": "string"}},
		"Flow-Timestamp":        schema{"description": "Unix time in milliseconds of the attempt (also webhookTimestamp in the body).", "schema": schema{"type": "string"}},
		"Flow-Attempt":          schema{"description": "1 for the first attempt, up to 4.", "schema": schema{"type": "string"}},
		"Flow-Signature":        schema{"description": "Hex HMAC-SHA256 of the raw body, keyed with the signing secret.", "schema": schema{"type": "string"}},
		"Flow-Signature-Status": schema{"description": "unsigned when the webhook has no signing secret.", "schema": schema{"type": "string"}},
	}
	for _, event := range publicapi.WebhookEvents {
		payload := g.schemaFor(reflect.TypeOf(event.Payload), false)
		parameters := []any{}
		for _, name := range []string{"Flow-Delivery", "Flow-Event", "Flow-Timestamp", "Flow-Attempt", "Flow-Signature", "Flow-Signature-Status"} {
			header := schema{"name": name, "in": "header", "required": name != "Flow-Signature-Status"}
			for key, value := range headers[name].(schema) {
				header[key] = value
			}
			parameters = append(parameters, header)
		}
		description := g.docs["publicapi."+reflect.TypeOf(event.Payload).Name()]
		subscription := "Subscribe with resourceTypes " + strconv.Quote(event.Resource) + " (or the payload type name)."
		switch {
		case event.Resource == "":
			subscription = "Sent by the test endpoint to the tested webhook only."
		case event.ApplicationOnly:
			subscription += " Application webhooks only."
		}
		result[event.Type] = schema{"post": schema{
			"operationId": "webhook_" + event.Type,
			"tags":        []string{"webhooks"},
			"summary":     event.Type + " event",
			"description": strings.TrimSpace(description + " " + subscription + " Answer with a 2xx status within 5 seconds."),
			"parameters":  parameters,
			"requestBody": schema{"required": true, "content": schema{"application/json": schema{"schema": payload}}},
			"responses":   schema{"2XX": schema{"description": "Delivery accepted."}},
		}}
	}
	legacy := g.schemaFor(reflect.TypeOf(publicapi.LegacyWebhookEvent{}), false)
	result["Legacy"] = schema{"post": schema{
		"operationId": "webhook_Legacy",
		"tags":        []string{"webhooks"},
		"summary":     "Version 1 event",
		"description": g.docs["publicapi.LegacyWebhookEvent"],
		"requestBody": schema{"required": true, "content": schema{"application/json": schema{"schema": legacy}}},
		"responses":   schema{"2XX": schema{"description": "Delivery accepted."}},
	}}
	return result
}

func operationID(method, routePath string) string {
	parts := []string{strings.ToLower(method)}
	for _, part := range strings.Split(routePath, "/") {
		if part == "" || part == "api" {
			continue
		}
		parts = append(parts, strings.Trim(part, "{}"))
	}
	return regexp.MustCompile(`[^a-zA-Z0-9_]`).ReplaceAllString(strings.Join(parts, "_"), "_")
}
