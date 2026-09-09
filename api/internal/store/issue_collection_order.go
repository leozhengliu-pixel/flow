package store

import "flow/api/internal/domain"

// Creation and imports prepend/append records. Preserve the old ordinals in
// those cases instead of shifting every existing row (including its JSON image).
// Explicit reordering or insertion into the middle retains the legacy behavior.
func issueCollectionOrder(issues []domain.Issue, previous map[string]int) map[string]int {
	result := make(map[string]int, len(issues))
	first, last := -1, -1
	for i, issue := range issues {
		if _, ok := previous[issue.ID]; ok {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		for i, issue := range issues {
			result[issue.ID] = i
		}
		return result
	}
	stable := true
	for i := first; i <= last; i++ {
		position, ok := previous[issues[i].ID]
		if !ok {
			stable = false
			break
		}
		if i > first {
			prior := previous[issues[i-1].ID]
			if position < prior || position == prior && issues[i].ID < issues[i-1].ID {
				stable = false
				break
			}
		}
	}
	if !stable {
		for i, issue := range issues {
			result[issue.ID] = i
		}
		return result
	}
	for i, issue := range issues {
		switch {
		case i < first:
			result[issue.ID] = previous[issues[first].ID] - first + i
		case i > last:
			result[issue.ID] = previous[issues[last].ID] + i - last
		default:
			result[issue.ID] = previous[issue.ID]
		}
	}
	return result
}
