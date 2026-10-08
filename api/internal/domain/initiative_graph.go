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

// MaxInitiativeNesting is Linear's max-sub-initiative-nesting limit: an initiative tree has at most
// five levels.
const MaxInitiativeNesting = 5

// InitiativeNestingTooDeep reports whether making child a sub-initiative of any candidate would take
// the tree past MaxInitiativeNesting levels. Like Linear's canMoveInitiative it adds the longest chain
// above the candidate to the longest chain below the child (both counted in edges) plus the two
// initiatives themselves.
func InitiativeNestingTooDeep(parents map[string][]string, child string, candidates []string) bool {
	if len(candidates) == 0 {
		return false
	}
	children := map[string][]string{}
	for id, ids := range parents {
		for _, parent := range ids {
			children[parent] = append(children[parent], id)
		}
	}
	below := longestInitiativeChain(children, child, map[string]bool{})
	for _, candidate := range candidates {
		if longestInitiativeChain(parents, candidate, map[string]bool{child: true})+below+2 > MaxInitiativeNesting {
			return true
		}
	}
	return false
}

func longestInitiativeChain(edges map[string][]string, id string, visiting map[string]bool) int {
	visiting[id] = true
	defer delete(visiting, id)
	longest := 0
	for _, next := range edges[id] {
		if visiting[next] {
			continue
		}
		if depth := longestInitiativeChain(edges, next, visiting) + 1; depth > longest {
			longest = depth
		}
	}
	return longest
}
