package domain

import "fmt"

// Depth counts edges: a root and four generations of children form five levels.
const MaxTeamDepth = 4

func ValidateTeamParent(data *Bootstrap, teamID, parentID string) error {
	if parentID == "" {
		return nil
	}
	available := make(map[string]bool, len(data.Teams))
	children := make(map[string][]string)
	for _, team := range data.Teams {
		available[team.ID] = team.RetiredAt == nil
		parent := data.TeamSettings[team.ID].ParentTeamID
		children[parent] = append(children[parent], team.ID)
	}
	if !available[parentID] {
		return fmt.Errorf("parent team is unavailable")
	}
	seen := map[string]bool{teamID: true}
	depth := 0
	for id := parentID; id != ""; id = data.TeamSettings[id].ParentTeamID {
		if seen[id] {
			return fmt.Errorf("a team cannot be its own ancestor")
		}
		if !available[id] {
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
		for _, child := range children[item.id] {
			stack = append(stack, node{child, item.depth + 1})
		}
	}
	return nil
}

// Only expand through the supplied (already authorized) team metadata.
func TeamSubtreeIDs(data *Bootstrap, roots []string) []string {
	children := make(map[string][]string, len(data.Teams))
	for _, team := range data.Teams {
		if team.RetiredAt == nil {
			children[data.TeamSettings[team.ID].ParentTeamID] = append(children[data.TeamSettings[team.ID].ParentTeamID], team.ID)
		}
	}
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
		result = append(result, id)
		queue = append(queue, children[id]...)
	}
	return result
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
