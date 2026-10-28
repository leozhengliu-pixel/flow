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
	for key, value := range args {
		text, ok := value.(string)
		kind := tool.scalars[key]
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
}
