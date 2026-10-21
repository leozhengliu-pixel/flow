package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// SCIM filters (RFC 7644 §3.4.2.2) are parsed into a small expression tree
// and evaluated against the JSON shape of a resource. Attribute names and
// operator keywords are case-insensitive; string comparisons are
// case-insensitive except for caseExact attributes (id, externalId,
// members.value, meta.version).

type scimFilterError struct{ detail string }

func (e scimFilterError) Error() string { return e.detail }

func scimFilterErrorf(format string, args ...any) error {
	return scimFilterError{detail: fmt.Sprintf(format, args...)}
}

type scimFilter interface {
	matches(resource map[string]any) bool
}

type scimLogicalFilter struct {
	and         bool
	left, right scimFilter
}

func (f scimLogicalFilter) matches(resource map[string]any) bool {
	if f.and {
		return f.left.matches(resource) && f.right.matches(resource)
	}
	return f.left.matches(resource) || f.right.matches(resource)
}

type scimNotFilter struct{ inner scimFilter }

func (f scimNotFilter) matches(resource map[string]any) bool { return !f.inner.matches(resource) }

// scimCompareFilter is `attrPath op value` or `attrPath pr`. path holds the
// lower-cased segments relative to the evaluated object; full is the
// resource-level path used to decide case and dateTime rules.
type scimCompareFilter struct {
	path  []string
	full  string
	op    string
	value any // string, float64, bool or nil
}

func (f scimCompareFilter) matches(resource map[string]any) bool {
	values := scimResolve(resource, f.path)
	switch f.op {
	case "pr":
		return slices.ContainsFunc(values, scimPresent)
	case "eq":
		if f.value == nil {
			return !slices.ContainsFunc(values, scimPresent)
		}
	case "ne":
		if f.value == nil {
			return slices.ContainsFunc(values, scimPresent)
		}
		return !slices.ContainsFunc(values, func(value any) bool { return scimCompare(value, "eq", f.value, f.full) })
	}
	return slices.ContainsFunc(values, func(value any) bool { return scimCompare(value, f.op, f.value, f.full) })
}

// scimValuePathFilter is `attr[filter]`, optionally followed by
// `.sub op value` (emails[type eq "work"].value eq "a@b.c").
type scimValuePathFilter struct {
	attr  string
	inner scimFilter
	sub   *scimCompareFilter
}

func (f scimValuePathFilter) matches(resource map[string]any) bool {
	for _, value := range scimResolve(resource, []string{f.attr}) {
		element, ok := value.(map[string]any)
		if !ok || !f.inner.matches(element) {
			continue
		}
		if f.sub == nil || f.sub.matches(element) {
			return true
		}
	}
	return false
}

func scimPresent(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return typed != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	}
	return true
}

// scimResolve walks a lower-cased attribute path through objects and
// multi-valued attributes, returning every value it reaches.
func scimResolve(node any, path []string) []any {
	if len(path) == 0 {
		if list, ok := node.([]any); ok {
			return list
		}
		return []any{node}
	}
	switch typed := node.(type) {
	case []any:
		result := []any{}
		for _, item := range typed {
			result = append(result, scimResolve(item, path)...)
		}
		return result
	case map[string]any:
		for key, value := range typed {
			if strings.EqualFold(key, path[0]) {
				return scimResolve(value, path[1:])
			}
		}
	}
	return nil
}

var scimCaseExactPaths = map[string]bool{"id": true, "externalid": true, "members.value": true, "meta.version": true}
var scimDateTimePaths = map[string]bool{"meta.created": true, "meta.lastmodified": true}

func scimCompare(value any, op string, literal any, full string) bool {
	// A complex value compared directly ("members eq x", "emails co y") is
	// compared on its value sub-attribute.
	if object, ok := value.(map[string]any); ok {
		for key, sub := range object {
			if strings.EqualFold(key, "value") {
				return scimCompare(sub, op, literal, full+".value")
			}
		}
		return false
	}
	switch want := literal.(type) {
	case bool:
		got, ok := value.(bool)
		if !ok {
			if text, isText := value.(string); isText {
				parsed, err := strconv.ParseBool(text)
				got, ok = parsed, err == nil
			}
		}
		if !ok {
			return false
		}
		return op == "eq" && got == want
	case float64:
		got, ok := value.(float64)
		if !ok {
			return false
		}
		return scimOrdered(scimCompareFloat(got, want), op)
	case string:
		var got string
		switch typed := value.(type) {
		case string:
			got = typed
		case bool:
			got = strconv.FormatBool(typed)
		case float64:
			got = strconv.FormatFloat(typed, 'f', -1, 64)
		default:
			return false
		}
		if scimDateTimePaths[full] {
			left, leftErr := time.Parse(time.RFC3339Nano, got)
			right, rightErr := time.Parse(time.RFC3339Nano, want)
			if leftErr == nil && rightErr == nil {
				return scimOrdered(left.Compare(right), op)
			}
		}
		if !scimCaseExactPaths[full] {
			got, want = strings.ToLower(got), strings.ToLower(want)
		}
		switch op {
		case "co":
			return strings.Contains(got, want)
		case "sw":
			return strings.HasPrefix(got, want)
		case "ew":
			return strings.HasSuffix(got, want)
		}
		return scimOrdered(strings.Compare(got, want), op)
	}
	return false
}

func scimCompareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func scimOrdered(cmp int, op string) bool {
	switch op {
	case "eq":
		return cmp == 0
	case "ne":
		return cmp != 0
	case "gt":
		return cmp > 0
	case "ge":
		return cmp >= 0
	case "lt":
		return cmp < 0
	case "le":
		return cmp <= 0
	}
	return false
}

// Parsing.

type scimToken struct {
	kind  byte // '(' ')' '[' ']' 's' (string literal) 'w' (word)
	text  string
	value string
}

func scimTokenize(input string) ([]scimToken, error) {
	tokens := []scimToken{}
	runes := []rune(input)
	for index := 0; index < len(runes); {
		char := runes[index]
		switch {
		case unicode.IsSpace(char):
			index++
		case char == '(' || char == ')' || char == '[' || char == ']':
			tokens = append(tokens, scimToken{kind: byte(char), text: string(char)})
			index++
		case char == '"':
			end := index + 1
			for end < len(runes) && runes[end] != '"' {
				if runes[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(runes) {
				return nil, scimFilterErrorf("unterminated string in filter")
			}
			raw := string(runes[index : end+1])
			var value string
			if err := json.Unmarshal([]byte(raw), &value); err != nil {
				return nil, scimFilterErrorf("invalid string %s in filter", raw)
			}
			tokens = append(tokens, scimToken{kind: 's', text: raw, value: value})
			index = end + 1
		default:
			end := index
			for end < len(runes) && !unicode.IsSpace(runes[end]) && !strings.ContainsRune("()[]\"", runes[end]) {
				end++
			}
			tokens = append(tokens, scimToken{kind: 'w', text: string(runes[index:end])})
			index = end
		}
	}
	return tokens, nil
}

type scimFilterParser struct {
	tokens   []scimToken
	position int
	// prefix is the enclosing attribute inside a value path, for case rules.
	prefix string
}

var scimCompareOperators = map[string]bool{"eq": true, "ne": true, "co": true, "sw": true, "ew": true, "gt": true, "ge": true, "lt": true, "le": true, "pr": true}

// parseSCIMFilter parses a filter expression. An empty filter returns nil.
// Errors are scimFilterError and map to 400 invalidFilter.
func parseSCIMFilter(input string) (scimFilter, error) {
	if strings.TrimSpace(input) == "" {
		return nil, nil
	}
	tokens, err := scimTokenize(input)
	if err != nil {
		return nil, err
	}
	parser := &scimFilterParser{tokens: tokens}
	filter, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if parser.position != len(parser.tokens) {
		return nil, scimFilterErrorf("unexpected %q in filter", parser.tokens[parser.position].text)
	}
	return filter, nil
}

func (p *scimFilterParser) peek() (scimToken, bool) {
	if p.position >= len(p.tokens) {
		return scimToken{}, false
	}
	return p.tokens[p.position], true
}

func (p *scimFilterParser) peekWord(word string) bool {
	token, ok := p.peek()
	return ok && token.kind == 'w' && strings.EqualFold(token.text, word)
}

func (p *scimFilterParser) expect(kind byte) error {
	token, ok := p.peek()
	if !ok || token.kind != kind {
		found := "end of filter"
		if ok {
			found = strconv.Quote(token.text)
		}
		return scimFilterErrorf("expected %q in filter, found %s", string(kind), found)
	}
	p.position++
	return nil
}

func (p *scimFilterParser) parseOr() (scimFilter, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peekWord("or") {
		p.position++
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = scimLogicalFilter{left: left, right: right}
	}
	return left, nil
}

func (p *scimFilterParser) parseAnd() (scimFilter, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.peekWord("and") {
		p.position++
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = scimLogicalFilter{and: true, left: left, right: right}
	}
	return left, nil
}

func (p *scimFilterParser) parseNot() (scimFilter, error) {
	if p.peekWord("not") {
		p.position++
		if err := p.expect('('); err != nil {
			return nil, err
		}
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(')'); err != nil {
			return nil, err
		}
		return scimNotFilter{inner: inner}, nil
	}
	return p.parseAtom()
}

func (p *scimFilterParser) parseAtom() (scimFilter, error) {
	token, ok := p.peek()
	if !ok {
		return nil, scimFilterErrorf("filter ended unexpectedly")
	}
	if token.kind == '(' {
		p.position++
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(')'); err != nil {
			return nil, err
		}
		return inner, nil
	}
	if token.kind != 'w' {
		return nil, scimFilterErrorf("expected an attribute in filter, found %q", token.text)
	}
	p.position++
	path, err := scimAttributePath(token.text)
	if err != nil {
		return nil, err
	}
	if next, ok := p.peek(); ok && next.kind == '[' {
		if p.prefix != "" {
			return nil, scimFilterErrorf("nested value filters are not supported")
		}
		if len(path) != 1 {
			return nil, scimFilterErrorf("value filter on %q is not supported", token.text)
		}
		p.position++
		inner := &scimFilterParser{tokens: p.tokens, position: p.position, prefix: path[0]}
		filter, err := inner.parseOr()
		if err != nil {
			return nil, err
		}
		p.position = inner.position
		if err := p.expect(']'); err != nil {
			return nil, err
		}
		result := scimValuePathFilter{attr: path[0], inner: filter}
		if sub, ok := p.peek(); ok && sub.kind == 'w' && strings.HasPrefix(sub.text, ".") {
			p.position++
			subPath, err := scimAttributePath(strings.TrimPrefix(sub.text, "."))
			if err != nil || len(subPath) != 1 {
				return nil, scimFilterErrorf("invalid sub-attribute %q in filter", sub.text)
			}
			compare, err := p.parseComparison(subPath, path[0]+"."+subPath[0])
			if err != nil {
				return nil, err
			}
			result.sub = &compare
		}
		return result, nil
	}
	full := strings.Join(path, ".")
	if p.prefix != "" {
		full = p.prefix + "." + full
	}
	return p.parseComparison(path, full)
}

func (p *scimFilterParser) parseComparison(path []string, full string) (scimCompareFilter, error) {
	token, ok := p.peek()
	if !ok || token.kind != 'w' || !scimCompareOperators[strings.ToLower(token.text)] {
		found := "end of filter"
		if ok {
			found = strconv.Quote(token.text)
		}
		return scimCompareFilter{}, scimFilterErrorf("expected an operator after %q, found %s", full, found)
	}
	p.position++
	op := strings.ToLower(token.text)
	filter := scimCompareFilter{path: path, full: full, op: op}
	if op == "pr" {
		return filter, nil
	}
	valueToken, ok := p.peek()
	if !ok {
		return scimCompareFilter{}, scimFilterErrorf("expected a value after %q", op)
	}
	p.position++
	switch valueToken.kind {
	case 's':
		filter.value = valueToken.value
	case 'w':
		switch strings.ToLower(valueToken.text) {
		case "true":
			filter.value = true
		case "false":
			filter.value = false
		case "null":
			filter.value = nil
		default:
			number, err := strconv.ParseFloat(valueToken.text, 64)
			if err != nil {
				return scimCompareFilter{}, scimFilterErrorf("invalid value %q in filter (strings must be quoted)", valueToken.text)
			}
			filter.value = number
		}
	default:
		return scimCompareFilter{}, scimFilterErrorf("expected a value after %q, found %q", op, valueToken.text)
	}
	switch filter.value.(type) {
	case bool:
		if op != "eq" && op != "ne" {
			return scimCompareFilter{}, scimFilterErrorf("operator %q cannot compare a boolean", op)
		}
	case nil:
		if op != "eq" && op != "ne" {
			return scimCompareFilter{}, scimFilterErrorf("operator %q cannot compare null", op)
		}
	case float64:
		if op == "co" || op == "sw" || op == "ew" {
			return scimCompareFilter{}, scimFilterErrorf("operator %q needs a string value", op)
		}
	}
	return filter, nil
}

// scimAttributePath splits an attribute path into lower-cased segments. A
// core schema URN prefix is dropped; an extension URN becomes the first
// segment (extension attributes live under their URN in the resource).
func scimAttributePath(raw string) ([]string, error) {
	text := strings.ToLower(strings.TrimSpace(raw))
	var segments []string
	if strings.HasPrefix(text, "urn:") {
		cut := strings.LastIndex(text, ":")
		schema, attr := text[:cut], text[cut+1:]
		if attr == "" {
			return nil, scimFilterErrorf("invalid attribute %q in filter", raw)
		}
		if schema != strings.ToLower(scimSchemaUser) && schema != strings.ToLower(scimSchemaGroup) {
			segments = append(segments, schema)
		}
		text = attr
	}
	for _, part := range strings.Split(text, ".") {
		if part == "" || !scimValidAttributeName(part) {
			return nil, scimFilterErrorf("invalid attribute %q in filter", raw)
		}
		segments = append(segments, part)
	}
	if len(segments) > 3 {
		return nil, scimFilterErrorf("invalid attribute %q in filter", raw)
	}
	return segments, nil
}

func scimValidAttributeName(name string) bool {
	for index, char := range name {
		if unicode.IsLetter(char) || char == '$' || (index > 0 && (unicode.IsDigit(char) || char == '_' || char == '-')) {
			continue
		}
		return false
	}
	return true
}

// scimEqualityPushdown finds a top-level (or and-joined) `attr eq "value"`
// on one of the given attributes, so a store can narrow the candidate set
// before the full filter is evaluated in memory.
func scimEqualityPushdown(filter scimFilter, attributes ...string) (string, string, bool) {
	switch typed := filter.(type) {
	case scimCompareFilter:
		value, isString := typed.value.(string)
		if typed.op != "eq" || !isString {
			return "", "", false
		}
		for _, attribute := range attributes {
			if typed.full == attribute {
				return attribute, value, true
			}
		}
	case scimLogicalFilter:
		if typed.and {
			if attribute, value, ok := scimEqualityPushdown(typed.left, attributes...); ok {
				return attribute, value, true
			}
			return scimEqualityPushdown(typed.right, attributes...)
		}
	}
	return "", "", false
}
