/**
 * LS-0569 SuggestedIssueProperties — detail-editor accept/dismiss chrome.
 * Complements create-dialog quickSuggestions + TriageIntelligenceSuggestions.
 */
import { Check, Sparkles, X } from 'lucide-react'
import { useMemo, useState } from 'react'
import type { BootstrapData, Issue, IssueSuggestion } from '@/types/flow'
import styles from './suggested-issue-properties.module.css'

export interface SuggestedIssuePropertiesProps {
  issue: Issue
  data: BootstrapData
  suggestions: IssueSuggestion[]
  loading?: boolean
  onAccept: (suggestion: IssueSuggestion) => void | Promise<void>
  onDismiss: (suggestion: IssueSuggestion) => void | Promise<void>
  onAcceptAll?: (suggestions: IssueSuggestion[]) => void | Promise<void>
}

function suggestionLabel(suggestion: IssueSuggestion, data: BootstrapData): string {
  if (suggestion.suggestedLabelId) {
    return data.labels.find(label => label.id === suggestion.suggestedLabelId)?.name ?? 'Label'
  }
  if (suggestion.suggestedUserId) {
    const user = data.users.find(item => item.id === suggestion.suggestedUserId)
    return user?.displayName ?? user?.name ?? 'Assignee'
  }
  if (suggestion.suggestedProjectId) {
    return data.projects.find(project => project.id === suggestion.suggestedProjectId)?.name ?? 'Project'
  }
  if (suggestion.suggestedTeamId) {
    return data.teams.find(team => team.id === suggestion.suggestedTeamId)?.name ?? 'Team'
  }
  if (suggestion.suggestedIssueId) {
    return data.issues.find(item => item.id === suggestion.suggestedIssueId)?.title ?? 'Related issue'
  }
  const reason = suggestion.metadata.reasons?.[0]
  return reason ?? suggestion.type
}

function suggestionKind(suggestion: IssueSuggestion): string {
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

export function SuggestedIssueProperties({
  issue,
  data,
  suggestions,
  loading = false,
  onAccept,
  onDismiss,
  onAcceptAll,
}: SuggestedIssuePropertiesProps) {
  const [pending, setPending] = useState<string>()
  const visible = useMemo(
    () => propertySuggestionsForEditor(suggestions, issue.id),
    [suggestions, issue.id],
  )

  if (!loading && visible.length === 0) return null

  const run = async (id: string, action: () => void | Promise<void>) => {
    setPending(id)
    try {
      await action()
    } finally {
      setPending(undefined)
    }
  }

  return (
    <section
      className={styles.root}
      data-suggested-issue-properties=""
      aria-label="Suggested properties"
    >
      <header className={styles.header}>
        <Sparkles size={14} aria-hidden="true" />
        <span>{loading && visible.length === 0 ? 'Analyzing…' : 'Suggested properties'}</span>
        {visible.length > 1 && onAcceptAll ? (
          <button
            type="button"
            className={styles.acceptAll}
            disabled={Boolean(pending)}
            onClick={() => void run('all', () => onAcceptAll(visible))}
          >
            Accept all
          </button>
        ) : null}
      </header>
      <ul className={styles.list}>
        {visible.map(suggestion => (
          <li key={suggestion.id} className={styles.item}>
            <div className={styles.meta}>
              <span className={styles.kind}>{suggestionKind(suggestion)}</span>
              <span className={styles.label} data-i18n-ignore>
                {suggestionLabel(suggestion, data)}
              </span>
              {typeof suggestion.metadata.score === 'number' ? (
                <span className={styles.confidence}>
                  {Math.round(suggestion.metadata.score * 100)}%
                </span>
              ) : null}
            </div>
            <div className={styles.actions}>
              <button
                type="button"
                className={styles.accept}
                aria-label="Accept suggestion"
                disabled={pending === suggestion.id}
                onClick={() => void run(suggestion.id, () => onAccept(suggestion))}
              >
                <Check size={14} />
              </button>
              <button
                type="button"
                className={styles.dismiss}
                aria-label="Dismiss suggestion"
                disabled={pending === suggestion.id}
                onClick={() => void run(suggestion.id, () => onDismiss(suggestion))}
              >
                <X size={14} />
              </button>
            </div>
          </li>
        ))}
      </ul>
    </section>
  )
}
