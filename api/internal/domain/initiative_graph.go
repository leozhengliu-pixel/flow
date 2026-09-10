package domain

// Include historical relation-only records alongside materialized parent IDs.
func InitiativeParents(data *Bootstrap) map[string][]string {
	parents := make(map[string][]string, len(data.Initiatives))
	for _, item := range data.Initiatives {
		parents[item.ID] = append([]string(nil), item.ParentInitiativeIDs...)
	}
	for _, relation := range data.InitiativeRelations {
		if relation.Type != "parent" {
			continue
		}
		found := false
		for _, id := range parents[relation.InitiativeID] {
			if id == relation.RelatedInitiativeID {
				found = true
				break
			}
		}
		if !found {
			parents[relation.InitiativeID] = append(parents[relation.InitiativeID], relation.RelatedInitiativeID)
		}
	}
	return parents
}

func InitiativeParentCycle(parents map[string][]string, child string, candidates []string) bool {
	stack := append([]string(nil), candidates...)
	visited := map[string]bool{}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == child {
			return true
		}
		if visited[id] {
			continue
		}
		visited[id] = true
		stack = append(stack, parents[id]...)
	}
	return false
}
