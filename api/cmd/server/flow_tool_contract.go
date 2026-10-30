package main

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// The Flow tool contract: every Flow tool's input schema from flow_mcp_tools.json, resolved once. /mcp and the
// built-in agent (chat, loops, application agents: executeAgentTool) check a call's arguments against the same
// schema, so a call one path refuses the other refuses too, and a handler never silently ignores an argument it
// does not understand (a patch passed as one object, a list passed as a string, a misspelt field).
//
// Before the check, normalizeFlowToolArgs rewrites the few loose forms the handlers have always accepted into the
// form the schema describes. Both paths run it, so it widens the contract identically for both; anything it does not
// rewrite must match the schema as published.

// flowToolContract is the inventory as /mcp publishes it: names without the mcp__flow. prefix, additionalProperties
// false, and the delete tools' `ids` batch form.
var flowToolContract = sync.OnceValue(func() []flowMCPTool {
	var tools []flowMCPTool
	if err := json.Unmarshal(flowMCPToolInventory, &tools); err != nil {
		panic(fmt.Sprintf("parse Flow MCP tool inventory: %v", err))
	}
	for index := range tools {
		tools[index].Name = strings.TrimPrefix(tools[index].Name, "mcp__flow.")
		var raw map[string]any
		if err := json.Unmarshal(tools[index].InputSchema, &raw); err != nil {
			panic(err)
		}
		raw["additionalProperties"] = false
		if mcpBatchDeleteTools()[tools[index].Name] {
			mcpBatchDeleteSchema(raw)
			tools[index].Description += " To delete several items in one call, pass `ids` instead of `id`."
		}
		tools[index].InputSchema, _ = json.Marshal(raw)
		var schema jsonschema.Schema
		if err := json.Unmarshal(tools[index].InputSchema, &schema); err != nil {
			panic(err)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			panic(fmt.Sprintf("invalid MCP schema %s: %v", tools[index].Name, err))
		}
		tools[index].validator = resolved
		tools[index].scalars = map[string]string{}
		for key, property := range schema.Properties {
			if property == nil {
				continue
			}
			// "integer", or ["integer", "null"]: one scalar type, possibly nullable.
			types := slices.DeleteFunc(append([]string{property.Type}, property.Types...), func(kind string) bool { return kind == "" || kind == "null" })
			if len(types) == 1 && (types[0] == "integer" || types[0] == "number" || types[0] == "boolean") {
				tools[index].scalars[key] = types[0]
			}
		}
	}
	return tools
})

// flowToolByName finds a Flow tool of the contract ("save_issue" or "mcp__flow.save_issue").
func flowToolByName(name string) (flowMCPTool, bool) {
	name = strings.TrimPrefix(name, "mcp__flow.")
	for _, tool := range flowToolContract() {
		if tool.Name == name {
			return tool, true
		}
	}
	return flowMCPTool{}, false
}

// checkFlowToolArgs normalises args in place (normalizeFlowToolArgs) and validates them against the tool's input
// schema. The error is the tool error both paths return: it names the tool, the field and the expected shape, and
// asks for a corrected call. Internal arguments ("__flowBaseURL") are not part of the contract and are not checked.
func checkFlowToolArgs(tool flowMCPTool, args map[string]any) error {
	normalizeFlowToolArgs(tool, args)
	checked := args
	for key := range args {
		if strings.HasPrefix(key, "__") {
			checked = make(map[string]any, len(args))
			for name, value := range args {
				if !strings.HasPrefix(name, "__") {
					checked[name] = value
				}
			}
			break
		}
	}
	if err := tool.validator.Validate(checked); err != nil {
		return mcpInvalidArguments(tool, checked, err)
	}
	return nil
}

// flowToolDecimal is a plain decimal number, the only quoted form normalizeFlowToolArgs reads as a number.
var flowToolDecimal = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// normalizeFlowToolArgs rewrites the loose argument forms the handlers accept into the form the schema describes.
// It is deliberately narrow: each rule keeps the value's meaning, and anything else is left for the schema check.
//
//   - A top-level argument whose schema type is only "integer" or "number" (or that and null), sent as a JSON string holding exactly
//     that number ("3", " 2.5 "), becomes the number; one whose type is only "boolean", sent as "true" or "false",
//     becomes the boolean. Models often quote scalars, and the handlers read numbers and booleans only, so the
//     value used to be dropped without a word.
//   - save_loop weekdays are compared case-insensitively ("Monday" is "monday"), as the handler does. The schema
//     itself lists the short and full day names it accepts.
//
// Forms the handlers tolerate but the schema does not describe stay errors on both paths: a patch passed as one
// object (the error names the list shape), a string where a list is expected, null for an argument that cannot be
// cleared, an unknown argument name.
func normalizeFlowToolArgs(tool flowMCPTool, args map[string]any) {
	if len(args) == 0 {
		return
	}
	// Keep the built-in agent and remote MCP clients on the same forgiving path.
	// Models commonly copy issue chips, resource links, or display names from a
	// previous result; the reference normalizer reduces those to the key before
	// handlers resolve it.
	normalizeAgentToolArgs(tool.Name, args)
	for key, value := range args {
		text, ok := value.(string)
		kind := tool.scalars[key]
		if key == "priority" && ok {
			if priority, found := normalizeFlowPriority(text); found {
				args[key] = float64(priority)
				continue
			}
		}
		if ok && isFlowStatusKey(tool.Name, key, args) {
			args[key] = normalizeFlowStatus(text)
			text = args[key].(string)
		}
		if ok && isFlowDateKey(key) {
			if date := normalizeFlowRelativeDate(text, time.Now().UTC()); date != "" {
				args[key] = date
				continue
			}
		}
		if !ok || kind == "" {
			continue
		}
		text = strings.TrimSpace(text)
		switch kind {
		case "integer", "number":
			if !flowToolDecimal.MatchString(text) {
				continue
			}
			number, err := strconv.ParseFloat(text, 64)
			if err == nil && !math.IsInf(number, 0) && (kind == "number" || number == math.Trunc(number)) {
				args[key] = number
			}
		case "boolean":
			if text == "true" || text == "false" {
				args[key] = text == "true"
			}
		}
	}
	if tool.Name == "save_loop" {
		if days, ok := args["weekdays"].([]any); ok {
			for index, day := range days {
				if text, ok := day.(string); ok {
					days[index] = strings.ToLower(strings.TrimSpace(text))
				}
			}
		}
	}
	normalizeFlowNestedArgs(args)
}

func normalizeFlowNestedArgs(args map[string]any) {
	for _, key := range []string{"filters", "advancedFilters"} {
		items, ok := args[key].([]any)
		if !ok {
			continue
		}
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			field, _ := item["field"].(string)
			if field == "priority" {
				if value, ok := item["value"].(string); ok {
					if priority, found := normalizeFlowPriority(value); found {
						item["value"] = strconv.Itoa(priority)
					}
				}
				if values, ok := item["values"].([]any); ok {
					for index, value := range values {
						if text, ok := value.(string); ok {
							if priority, found := normalizeFlowPriority(text); found {
								values[index] = strconv.Itoa(priority)
							}
						}
					}
				}
			}
			if field == "status" {
				if value, ok := item["value"].(string); ok {
					item["value"] = normalizeFlowStatus(value)
				}
				if values, ok := item["values"].([]any); ok {
					for index, value := range values {
						if text, ok := value.(string); ok {
							values[index] = normalizeFlowStatus(text)
						}
					}
				}
			}
		}
	}
}

var flowPriorityAliases = map[string]int{
	"none": 0, "no priority": 0, "无优先级": 0, "无优先": 0, "不重要": 0,
	"urgent": 1, "紧急": 1, "最高": 1,
	"high": 2, "高": 2, "高优先级": 2,
	"medium": 3, "normal": 3, "中": 3, "中优先级": 3,
	"low": 4, "低": 4, "低优先级": 4,
}

func normalizeFlowPriority(value string) (int, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	priority, ok := flowPriorityAliases[value]
	return priority, ok
}

func isFlowStatusKey(tool, key string, args map[string]any) bool {
	if key == "state" || key == "status" {
		return true
	}
	// `value` is overloaded by save_loop: it can be a label, person, team, or
	// project name. Only normalize it for the status event trigger.
	return key == "value" && tool == "save_loop" && strings.EqualFold(strings.TrimSpace(stringArg(args, "event")), "status")
}

// normalizeFlowStatus maps common localized labels to the stable workflow
// types/names understood by every tool handler. Unknown labels are preserved.
func normalizeFlowStatus(value string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	aliases := map[string]string{
		"待办": "backlog", "待处理": "backlog", "未开始": "unstarted", "未启动": "unstarted",
		"进行中": "started", "处理中": "started", "已开始": "started", "开始": "started",
		"完成": "completed", "已完成": "completed", "已解决": "completed", "完了": "completed",
		"取消": "canceled", "已取消": "canceled", "已关闭": "canceled", "关闭": "canceled",
		"planned": "planned", "计划中": "planned", "规划中": "planned",
		"active": "active", "活跃": "active", "进行": "active",
		"completed": "completed", "done": "completed", "finished": "completed",
		"canceled": "canceled", "cancelled": "canceled", "backlog": "backlog",
		"unstarted": "unstarted", "started": "started", "in progress": "started",
	}
	if normalized, ok := aliases[key]; ok {
		return normalized
	}
	return strings.TrimSpace(value)
}

func isFlowDateKey(key string) bool {
	switch key {
	case "dueDate", "startDate", "targetDate", "completedAt", "createdAt", "startedAt", "remindAt", "snoozedUntil":
		return true
	default:
		return false
	}
}

// normalizeFlowRelativeDate accepts the short natural-language date forms
// models use and emits the ISO date expected by the REST handlers.
func normalizeFlowRelativeDate(value string, now time.Time) string {
	value = strings.ToLower(strings.TrimSpace(value))
	base := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if value == "today" || value == "今天" {
		return base.Format(time.DateOnly)
	}
	if value == "tomorrow" || value == "明天" {
		return base.AddDate(0, 0, 1).Format(time.DateOnly)
	}
	if value == "day after tomorrow" || value == "后天" {
		return base.AddDate(0, 0, 2).Format(time.DateOnly)
	}
	if value == "yesterday" || value == "昨天" {
		return base.AddDate(0, 0, -1).Format(time.DateOnly)
	}
	if value == "next week" || value == "下周" {
		return base.AddDate(0, 0, 7).Format(time.DateOnly)
	}
	if matches := regexp.MustCompile(`^(?:in\s+)?([0-9]+)\s*(day|days|week|weeks|month|months)\s*(?:later)?$`).FindStringSubmatch(value); matches != nil {
		n, _ := strconv.Atoi(matches[1])
		if strings.HasPrefix(matches[2], "month") {
			return base.AddDate(0, n, 0).Format(time.DateOnly)
		}
		if strings.HasPrefix(matches[2], "week") {
			n *= 7
		}
		return base.AddDate(0, 0, n).Format(time.DateOnly)
	}
	if matches := regexp.MustCompile(`^([0-9]+)\s*(天|周|个月)后$`).FindStringSubmatch(value); matches != nil {
		n, _ := strconv.Atoi(matches[1])
		if matches[2] == "周" {
			n *= 7
		} else if matches[2] == "个月" {
			return base.AddDate(0, n, 0).Format(time.DateOnly)
		}
		return base.AddDate(0, 0, n).Format(time.DateOnly)
	}
	return ""
}
