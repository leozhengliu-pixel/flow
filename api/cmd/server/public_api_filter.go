package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// filter objects for the public list endpoints, e.g.
//
//	{"name":{"containsIgnoreCase":"bug"},"createdAt":{"gt":"-P2W"},
//	 "team":{"key":{"eq":"ENG"}},"or":[{...},{...}]}
//
// Each resource declares its filterable fields. A field value is a scalar
// (string, number, bool, time), a list (matches when any element matches) or
// a nested relation (map of sub-fields). Conditions in one object are ANDed.

var errFilter = errors.New("invalid filter")

type filterFields map[string]func(any) any

type filterNode map[string]json.RawMessage

func parseResourceFilter(raw string) (filterNode, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 16<<10 {
		return nil, fmt.Errorf("%w: filter is too large", errFilter)
	}
	var node filterNode
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		return nil, fmt.Errorf("%w: filter must be a JSON object", errFilter)
	}
	return node, nil
}

var filterComparators = []string{"eq", "neq", "in", "nin", "lt", "lte", "gt", "gte", "eqIgnoreCase", "neqIgnoreCase", "startsWith", "startsWithIgnoreCase", "notStartsWith", "endsWith", "notEndsWith", "contains", "notContains", "containsIgnoreCase", "notContainsIgnoreCase", "null"}

// validateFilter checks field names and comparators before evaluation so a
// typo is reported instead of silently matching nothing.
func validateFilter(node filterNode, fields filterFields, depth int) error {
	if depth > 8 {
		return fmt.Errorf("%w: filter is nested too deeply", errFilter)
	}
	for name, raw := range node {
		switch name {
		case "and", "or":
			var children []filterNode
			if err := json.Unmarshal(raw, &children); err != nil {
				return fmt.Errorf("%w: %s must be an array of filters", errFilter, name)
			}
			for _, child := range children {
				if err := validateFilter(child, fields, depth+1); err != nil {
					return err
				}
			}
			continue
		}
		if _, ok := fields[name]; !ok {
			names := make([]string, 0, len(fields))
			for field := range fields {
				names = append(names, field)
			}
			sort.Strings(names)
			return fmt.Errorf("%w: unknown field %q (filterable: %s)", errFilter, name, strings.Join(names, ", "))
		}
		if err := validateComparison(name, raw, depth); err != nil {
			return err
		}
	}
	return nil
}

func validateComparison(field string, raw json.RawMessage, depth int) error {
	if depth > 8 {
		return fmt.Errorf("%w: filter is nested too deeply", errFilter)
	}
	var comparison map[string]json.RawMessage
	if err := json.Unmarshal(raw, &comparison); err != nil {
		return fmt.Errorf("%w: %s must be an object of comparators", errFilter, field)
	}
	for comparator, value := range comparison {
		if slices.Contains(filterComparators, comparator) {
			continue
		}
		if comparator == "some" || comparator == "every" || isNestedFieldName(comparator) {
			if err := validateComparison(field+"."+comparator, value, depth+1); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("%w: unknown comparator %q on %s", errFilter, comparator, field)
	}
	return nil
}

var nestedFieldName = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)

func isNestedFieldName(value string) bool { return nestedFieldName.MatchString(value) }

func matchesFilter(item any, node filterNode, fields filterFields) bool {
	for name, raw := range node {
		switch name {
		case "and":
			var children []filterNode
			_ = json.Unmarshal(raw, &children)
			for _, child := range children {
				if !matchesFilter(item, child, fields) {
					return false
				}
			}
			continue
		case "or":
			var children []filterNode
			_ = json.Unmarshal(raw, &children)
			if len(children) == 0 {
				continue
			}
			if !slices.ContainsFunc(children, func(child filterNode) bool { return matchesFilter(item, child, fields) }) {
				return false
			}
			continue
		}
		if !matchesComparison(fields[name](item), raw) {
			return false
		}
	}
	return true
}

func matchesComparison(value any, raw json.RawMessage) bool {
	var comparison map[string]json.RawMessage
	if json.Unmarshal(raw, &comparison) != nil {
		return false
	}
	for comparator, operand := range comparison {
		if !matchesComparator(value, comparator, operand) {
			return false
		}
	}
	return true
}

func matchesComparator(value any, comparator string, operand json.RawMessage) bool {
	switch comparator {
	case "null":
		var want bool
		if json.Unmarshal(operand, &want) != nil {
			return false
		}
		return filterValueEmpty(value) == want
	case "every":
		list, ok := value.([]any)
		if !ok {
			return matchesComparison(value, operand)
		}
		for _, element := range list {
			if !matchesComparison(element, operand) {
				return false
			}
		}
		return true
	case "some":
		list, ok := value.([]any)
		if !ok {
			return matchesComparison(value, operand)
		}
		return slices.ContainsFunc(list, func(element any) bool { return matchesComparison(element, operand) })
	}
	if list, ok := value.([]any); ok {
		// Lists match when any element does; negative comparators require that
		// no element matches the positive form.
		if negative, positive := negativeComparator(comparator); negative {
			return !slices.ContainsFunc(list, func(element any) bool { return matchesComparator(element, positive, operand) })
		}
		return slices.ContainsFunc(list, func(element any) bool { return matchesComparator(element, comparator, operand) })
	}
	if nested, ok := value.(map[string]any); ok {
		if !isNestedFieldName(comparator) || slices.Contains(filterComparators, comparator) {
			// Comparing a relation directly compares its id.
			return matchesComparator(nested["id"], comparator, operand)
		}
		return matchesComparison(nested[comparator], operand)
	}
	switch comparator {
	case "in", "nin":
		var options []json.RawMessage
		if json.Unmarshal(operand, &options) != nil {
			return false
		}
		found := slices.ContainsFunc(options, func(option json.RawMessage) bool { return compareFilterValue(value, option) == 0 })
		return found == (comparator == "in")
	case "eq":
		return compareFilterValue(value, operand) == 0
	case "neq":
		return compareFilterValue(value, operand) != 0
	case "lt", "lte", "gt", "gte":
		order := compareFilterValue(value, operand)
		if order == math.MinInt {
			return false
		}
		return comparator == "lt" && order < 0 || comparator == "lte" && order <= 0 || comparator == "gt" && order > 0 || comparator == "gte" && order >= 0
	}
	text, ok := value.(string)
	var target string
	if !ok || json.Unmarshal(operand, &target) != nil {
		return false
	}
	lowerText, lowerTarget := strings.ToLower(text), strings.ToLower(target)
	switch comparator {
	case "eqIgnoreCase":
		return lowerText == lowerTarget
	case "neqIgnoreCase":
		return lowerText != lowerTarget
	case "startsWith":
		return strings.HasPrefix(text, target)
	case "startsWithIgnoreCase":
		return strings.HasPrefix(lowerText, lowerTarget)
	case "notStartsWith":
		return !strings.HasPrefix(text, target)
	case "endsWith":
		return strings.HasSuffix(text, target)
	case "notEndsWith":
		return !strings.HasSuffix(text, target)
	case "contains":
		return strings.Contains(text, target)
	case "notContains":
		return !strings.Contains(text, target)
	case "containsIgnoreCase":
		return strings.Contains(lowerText, lowerTarget)
	case "notContainsIgnoreCase":
		return !strings.Contains(lowerText, lowerTarget)
	}
	return false
}

func negativeComparator(comparator string) (bool, string) {
	switch comparator {
	case "neq":
		return true, "eq"
	case "nin":
		return true, "in"
	case "neqIgnoreCase":
		return true, "eqIgnoreCase"
	case "notContains":
		return true, "contains"
	case "notContainsIgnoreCase":
		return true, "containsIgnoreCase"
	case "notStartsWith":
		return true, "startsWith"
	case "notEndsWith":
		return true, "endsWith"
	}
	return false, ""
}

func filterValueEmpty(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case time.Time:
		return typed.IsZero()
	case []any:
		return len(typed) == 0
	case map[string]any:
		id, _ := typed["id"].(string)
		return id == ""
	}
	return false
}

// compareFilterValue returns -1/0/1, or math.MinInt when the operand cannot be
// compared with the value.
func compareFilterValue(value any, operand json.RawMessage) int {
	switch typed := value.(type) {
	case nil:
		if string(operand) == "null" {
			return 0
		}
		return math.MinInt
	case string:
		var target string
		if json.Unmarshal(operand, &target) != nil {
			return math.MinInt
		}
		return strings.Compare(typed, target)
	case bool:
		var target bool
		if json.Unmarshal(operand, &target) != nil {
			return math.MinInt
		}
		if typed == target {
			return 0
		}
		if !typed {
			return -1
		}
		return 1
	case float64, int, int64:
		number := toFloat(typed)
		var target float64
		if json.Unmarshal(operand, &target) != nil {
			var text string
			if json.Unmarshal(operand, &text) != nil {
				return math.MinInt
			}
			parsed, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return math.MinInt
			}
			target = parsed
		}
		switch {
		case number < target:
			return -1
		case number > target:
			return 1
		}
		return 0
	case time.Time:
		var text string
		if json.Unmarshal(operand, &text) != nil {
			return math.MinInt
		}
		target, ok := parseFilterTime(text, time.Now())
		if !ok || typed.IsZero() {
			return math.MinInt
		}
		return typed.Compare(target)
	}
	return math.MinInt
}

func toFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	}
	return 0
}

// parseFilterTime accepts RFC 3339 timestamps, YYYY-MM-DD dates and ISO 8601
// durations relative to now ("-P2W" is two weeks ago, "P1D" tomorrow).
func parseFilterTime(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, true
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed, true
	}
	if duration, ok := parseISODuration(value); ok {
		return duration(now), true
	}
	return time.Time{}, false
}

var isoDuration = regexp.MustCompile(`^([+-])?P(?:(\d+)Y)?(?:(\d+)M)?(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

func parseISODuration(value string) (func(time.Time) time.Time, bool) {
	match := isoDuration.FindStringSubmatch(value)
	if match == nil || value == "P" || value == "-P" || value == "+P" || strings.HasSuffix(value, "T") {
		return nil, false
	}
	number := func(index int) int {
		parsed, _ := strconv.Atoi(match[index])
		return parsed
	}
	sign := 1
	if match[1] == "-" {
		sign = -1
	}
	return func(now time.Time) time.Time {
		shifted := now.AddDate(sign*number(2), sign*number(3), sign*(number(4)*7+number(5)))
		return shifted.Add(time.Duration(sign) * (time.Duration(number(6))*time.Hour + time.Duration(number(7))*time.Minute + time.Duration(number(8))*time.Second))
	}, true
}

// Helpers used by field accessors.
func filterList(values []string) any {
	list := make([]any, 0, len(values))
	for _, value := range values {
		list = append(list, value)
	}
	return list
}

func filterRelation(id string, fields map[string]any) any {
	if id == "" {
		return nil
	}
	result := map[string]any{"id": id}
	for name, value := range fields {
		result[name] = value
	}
	return result
}

func filterRelations(relations []map[string]any) any {
	list := make([]any, 0, len(relations))
	for _, relation := range relations {
		list = append(list, relation)
	}
	return list
}

func filterTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func filterDate(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	if parsed, ok := parseFilterTime(*value, time.Now()); ok {
		return parsed
	}
	return nil
}

// applyResourceFilter parses the `filter` query parameter and keeps matching
// items.
func applyResourceFilter[T any](items []T, raw string, fields filterFields) ([]T, error) {
	node, err := parseResourceFilter(raw)
	if err != nil || node == nil {
		return items, err
	}
	if err := validateFilter(node, fields, 0); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(items, func(item T) bool { return !matchesFilter(item, node, fields) }), nil
}

// typedFields adapts typed accessors to the untyped filter evaluator.
func typedFields[T any](fields map[string]func(T) any) filterFields {
	result := filterFields{}
	for name, accessor := range fields {
		result[name] = func(item any) any {
			typed, ok := item.(T)
			if !ok {
				return nil
			}
			return accessor(typed)
		}
	}
	return result
}
