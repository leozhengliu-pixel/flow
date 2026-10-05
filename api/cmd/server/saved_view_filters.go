package main

import "encoding/json"

// Saved views store the filter bar's chips as raw JSON. Advanced-filter chips
// carry a tree ({conjunction, items}) that the web client caps at three levels
// (top level, group, nested group); reject anything deeper or oversized so a
// stored view can always be translated within the issue query budgets.
const (
	savedViewFilterLimit     = 200
	savedViewConditionLimit  = 100
	savedViewFilterTreeDepth = 3
)

type savedViewFilterNode struct {
	Conjunction string                `json:"conjunction"`
	Items       []savedViewFilterNode `json:"items"`
	Field       string                `json:"field"`
}

func validSavedViewFilters(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var chips []struct {
		Field string               `json:"field"`
		Tree  *savedViewFilterNode `json:"tree"`
	}
	if err := json.Unmarshal(raw, &chips); err != nil {
		// Other resources (Pulse, projects) may store an object; only arrays are checked.
		return true
	}
	if len(chips) > savedViewFilterLimit {
		return false
	}
	conditions := 0
	var walk func(node savedViewFilterNode, depth int) bool
	walk = func(node savedViewFilterNode, depth int) bool {
		if depth > savedViewFilterTreeDepth || (node.Conjunction != "" && node.Conjunction != "and" && node.Conjunction != "or") {
			return false
		}
		for _, item := range node.Items {
			if item.Items != nil || item.Conjunction != "" {
				if !walk(item, depth+1) {
					return false
				}
				continue
			}
			conditions++
			if item.Field == "" || conditions > savedViewConditionLimit {
				return false
			}
		}
		return true
	}
	for _, chip := range chips {
		if chip.Tree != nil && !walk(*chip.Tree, 1) {
			return false
		}
	}
	return true
}
