import type { IssueSuggestion } from '@/types/flow'

export function suggestionKind(suggestion: IssueSuggestion): string {
  switch (suggestion.type) {
    case 'assignee':
      return 'Assignee'
    case 'project':
      return 'Project'
    case 'label':
      return 'Label'
    case 'team':
      return 'Team'
    case 'similarIssue':
      return 'Similar'
    case 'relatedIssue':
      return 'Related'
    default:
      return 'Property'
  }
}

export function propertySuggestionsForEditor(
  suggestions: IssueSuggestion[],
  issueId: string,
): IssueSuggestion[] {
  return suggestions
    .filter(
      item =>
        item.issueId === issueId &&
        item.state === 'active' &&
        (item.type === 'assignee' ||
          item.type === 'project' ||
          item.type === 'label' ||
          item.type === 'team'),
    )
    .sort(
      (left, right) => Number(left.metadata.rank ?? 0) - Number(right.metadata.rank ?? 0),
    )
}
