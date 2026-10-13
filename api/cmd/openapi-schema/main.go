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
	names   map[reflect.Type]string
	used    map[string]reflect.Type
	docs    map[string]string // "pkg.Type" and "pkg.Type.Field"
}

var (
	timeType    = reflect.TypeOf(time.Time{})
	rawJSONType = reflect.TypeOf(json.RawMessage{})
)

func main() {
	g := &generator{schemas: map[string]schema{}, names: map[reflect.Type]string{}, used: map[string]reflect.Type{}, docs: map[string]string{}}
	for _, dir := range []string{"internal/domain", "internal/publicapi", "internal/store"} {
		g.loadDocs(dir)
	}
	operations := map[string]schema{}
	for _, operation := range publicapi.Operations {
		key := strings.ToLower(operation.Method) + " " + operation.Path
		if _, exists := operations[key]; exists {
			fail(fmt.Errorf("duplicate contract operation %s", key))
		}
		operations[key] = g.operation(operation)
	}
	g.ref(reflect.TypeOf(publicapi.Error{}))
	g.ref(reflect.TypeOf(publicapi.PageInfo{}))
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
	output := map[string]any{"schemas": g.schemas, "operations": operations, "scopes": scopes}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(output); err != nil {
		fail(err)
	}
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
func (g *generator) schemaName(t reflect.Type) string {
	if name, ok := g.names[t]; ok {
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
	if other, taken := g.used[name]; taken && other != t {
		pkg := packageName(t)
		name = strings.ToUpper(pkg[:1]) + pkg[1:] + name
	}
	g.names[t] = name
	g.used[name] = t
	return name
}

func (g *generator) ref(t reflect.Type) schema {
	name := g.schemaName(t)
	if _, done := g.schemas[name]; !done {
		g.schemas[name] = schema{} // placeholder breaks recursion
		g.schemas[name] = g.structSchema(t)
	}
	return schema{"$ref": "#/components/schemas/" + name}
}

func (g *generator) schemaFor(t reflect.Type) schema {
	switch {
	case t == timeType:
		return schema{"type": "string", "format": "date-time"}
	case t == rawJSONType:
		return schema{}
	}
	switch t.Kind() {
	case reflect.Pointer:
		inner := g.schemaFor(t.Elem())
		return nullable(inner)
	case reflect.Struct:
		if t.Name() == "" {
			return g.structSchema(t)
		}
		return g.ref(t)
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return schema{"type": "string", "format": "byte"}
		}
		return schema{"type": "array", "items": g.schemaFor(t.Elem())}
	case reflect.Map:
		value := g.schemaFor(t.Elem())
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

func (g *generator) structSchema(t reflect.Type) schema {
	properties := schema{}
	required := []string{}
	g.collectFields(t, properties, &required)
	result := schema{"type": "object", "properties": properties}
	if len(required) > 0 {
		sort.Strings(required)
		result["required"] = required
	}
	if t.Name() != "" {
		if doc := g.docs[packageName(t)+"."+baseTypeName(t)]; doc != "" {
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

func (g *generator) collectFields(t reflect.Type, properties schema, required *[]string) {
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
				g.collectFields(embedded, properties, required)
				continue
			}
		}
		if name == "" {
			name = field.Name
		}
		property := g.schemaFor(field.Type)
		if strings.Contains(options, "string") {
			property = schema{"type": "string"}
		}
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
		}
	}
}

var pathParameter = regexp.MustCompile(`\{([^}]+)\}`)

func (g *generator) operation(operation publicapi.Operation) schema {
	result := schema{"summary": operation.Summary, "tags": []string{operation.Tag}, "operationId": operationID(operation.Method, operation.Path)}
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
		parameters = append(parameters,
			schema{"$ref": "#/components/parameters/first"}, schema{"$ref": "#/components/parameters/after"},
			schema{"$ref": "#/components/parameters/last"}, schema{"$ref": "#/components/parameters/before"},
			schema{"name": "orderBy", "in": "query", "description": "Sort field; results are newest first.", "schema": schema{"type": "string", "enum": orderings, "default": "createdAt"}},
			schema{"$ref": "#/components/parameters/includeArchived"})
		if len(operation.Filter) > 0 {
			parameters = append(parameters, schema{"name": "filter", "in": "query", "description": "filter object (JSON). Filterable fields: " + strings.Join(operation.Filter, ", ") + ". Comparators: eq, neq, in, nin, lt, lte, gt, gte, eqIgnoreCase, neqIgnoreCase, startsWith, startsWithIgnoreCase, notStartsWith, endsWith, notEndsWith, contains, notContains, containsIgnoreCase, notContainsIgnoreCase, null; combine with and/or.", "content": schema{"application/json": schema{"schema": schema{"type": "object", "additionalProperties": true}}}})
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
		result["requestBody"] = schema{"required": true, "content": schema{"application/json": schema{"schema": g.schemaFor(reflect.TypeOf(operation.Request))}}}
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
		body := g.schemaFor(reflect.TypeOf(operation.Response))
		if operation.Connection {
			body = g.connection(reflect.TypeOf(operation.Response))
		}
		if operation.Legacy != nil {
			body = schema{"oneOf": []any{body, g.schemaFor(reflect.TypeOf(operation.Legacy))}, "description": "A connection when first/last/after/before/orderBy is given, otherwise the legacy response."}
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
	nodeSchema := g.schemaFor(node)
	name := node.Name() + "Connection"
	if _, done := g.schemas[name]; !done {
		g.ref(reflect.TypeOf(publicapi.PageInfo{}))
		g.schemas[name] = schema{"type": "object", "required": []string{"nodes", "pageInfo"}, "properties": schema{
			"nodes":    schema{"type": "array", "items": nodeSchema, "description": "Nodes ordered newest first by orderBy."},
			"pageInfo": schema{"$ref": "#/components/schemas/PageInfo"},
		}}
	}
	return schema{"$ref": "#/components/schemas/" + name}
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
