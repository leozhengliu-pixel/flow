package domain

import (
	"fmt"
	"strings"
)

// Depth counts edges: a root and four generations of children form five levels.
const MaxTeamDepth = 4

func ValidateTeamParent(data *Bootstrap, teamID, parentID string) error {
	if parentID == "" {
		return nil
	}
	EnsureTeamDirectory(data)
	if index, ok := data.TeamByID[parentID]; !ok || data.Teams[index].RetiredAt != nil {
		return fmt.Errorf("parent team is unavailable")
	}
	seen := map[string]bool{teamID: true}
	depth := 0
	for id := parentID; id != ""; id = data.TeamSettings[id].ParentTeamID {
		if seen[id] {
			return fmt.Errorf("a team cannot be its own ancestor")
		}
		if index, ok := data.TeamByID[id]; !ok || data.Teams[index].RetiredAt != nil {
			return fmt.Errorf("parent team is unavailable")
		}
		seen[id] = true
		depth++
	}
	type node struct {
		id    string
		depth int
	}
	stack := []node{{teamID, depth}}
	seen = map[string]bool{}
	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[item.id] {
			return fmt.Errorf("team hierarchy contains a cycle")
		}
		seen[item.id] = true
		if item.depth > MaxTeamDepth {
			return fmt.Errorf("teams can only be nested five levels deep")
		}
		for _, child := range data.TeamChildren[item.id] {
			stack = append(stack, node{child, item.depth + 1})
		}
	}
	return nil
}

// Only expand through the supplied (already authorized) team metadata.
func TeamSubtreeIDs(data *Bootstrap, roots []string) []string {
	EnsureTeamDirectory(data)
	result := make([]string, 0, len(roots))
	seen := map[string]bool{}
	queue := append([]string(nil), roots...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		if index, ok := data.TeamByID[id]; ok && data.Teams[index].RetiredAt != nil && !containsString(roots, id) {
			continue
		}
		result = append(result, id)
		queue = append(queue, data.TeamChildren[id]...)
	}
	return result
}

func TeamDescendantIDs(data *Bootstrap, root string) []string {
	EnsureTeamDirectory(data)
	result := []string{}
	seen := map[string]bool{root: true}
	queue := append([]string(nil), data.TeamChildren[root]...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		if index, ok := data.TeamByID[id]; ok && data.Teams[index].RetiredAt != nil {
			continue
		}
		result = append(result, id)
		queue = append(queue, data.TeamChildren[id]...)
	}
	return result
}

func TeamKeyTaken(data *Bootstrap, key, exceptID string) bool {
	EnsureTeamDirectory(data)
	id, ok := data.TeamByKey[strings.ToLower(strings.TrimSpace(key))]
	return ok && id != exceptID
}

func TeamIndex(data *Bootstrap, id string) int {
	EnsureTeamDirectory(data)
	index, ok := data.TeamByID[id]
	if !ok {
		return -1
	}
	return index
}

func EnsureTeamDirectory(data *Bootstrap) {
	if data == nil {
		return
	}
	if data.TeamByID != nil && len(data.TeamByID) == len(data.Teams) {
		return
	}
	RebuildTeamDirectory(data)
}

func RebuildTeamDirectory(data *Bootstrap) {
	data.TeamByID = make(map[string]int, len(data.Teams))
	data.TeamByKey = make(map[string]string, len(data.Teams))
	data.TeamChildren = make(map[string][]string, len(data.Teams))
	data.LabelIndex = make(map[string][]int)
	for index, team := range data.Teams {
		data.TeamByID[team.ID] = index
		if team.Key != "" {
			data.TeamByKey[strings.ToLower(team.Key)] = team.ID
		}
		parent := ""
		if data.TeamSettings != nil {
			parent = data.TeamSettings[team.ID].ParentTeamID
		}
		if team.RetiredAt == nil {
			data.TeamChildren[parent] = append(data.TeamChildren[parent], team.ID)
		}
	}
	for index, label := range data.Labels {
		if label.Scope != "" {
			data.LabelIndex[label.Scope] = append(data.LabelIndex[label.Scope], index)
		}
	}
}

func NoteTeamAppended(data *Bootstrap, team Team) {
	if data.TeamByID == nil {
		RebuildTeamDirectory(data)
		return
	}
	data.TeamByID[team.ID] = len(data.Teams) - 1
	if team.Key != "" {
		data.TeamByKey[strings.ToLower(team.Key)] = team.ID
	}
	parent := ""
	if data.TeamSettings != nil {
		parent = data.TeamSettings[team.ID].ParentTeamID
	}
	if team.RetiredAt == nil {
		data.TeamChildren[parent] = append(data.TeamChildren[parent], team.ID)
	}
}

func NoteTeamParentChanged(data *Bootstrap, teamID, oldParent, newParent string) {
	if data.TeamChildren == nil || oldParent == newParent {
		return
	}
	data.TeamChildren[oldParent] = removeString(data.TeamChildren[oldParent], teamID)
	data.TeamChildren[newParent] = append(data.TeamChildren[newParent], teamID)
}

func NoteTeamKeyChanged(data *Bootstrap, teamID, oldKey, newKey string) {
	if data.TeamByKey == nil {
		return
	}
	if oldKey != "" {
		delete(data.TeamByKey, strings.ToLower(oldKey))
	}
	if newKey != "" {
		data.TeamByKey[strings.ToLower(newKey)] = teamID
	}
}

func SyncTeamAncestorMembers(data *Bootstrap) {
	guests := map[string]bool{}
	for _, member := range data.Members {
		if member.Role == "guest" {
			guests[member.User.ID] = true
		}
	}
	seen := make(map[string]bool, len(data.TeamMembers))
	for _, member := range data.TeamMembers {
		seen[member.TeamID+"\x00"+member.UserID] = true
	}
	for _, member := range data.TeamMembers {
		visited := map[string]bool{member.TeamID: true}
		if guests[member.UserID] {
			continue
		}
		for parent := data.TeamSettings[member.TeamID].ParentTeamID; parent != "" && !visited[parent]; parent = data.TeamSettings[parent].ParentTeamID {
			visited[parent] = true
			key := parent + "\x00" + member.UserID
			if !seen[key] {
				seen[key] = true
				data.TeamMembers = append(data.TeamMembers, TeamMember{TeamID: parent, UserID: member.UserID, Role: "member", JoinedAt: member.JoinedAt})
			}
		}
	}
}

func removeString(items []string, target string) []string {
	next := make([]string, 0, len(items))
	for _, item := range items {
		if item != target {
			next = append(next, item)
		}
	}
	return next
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
