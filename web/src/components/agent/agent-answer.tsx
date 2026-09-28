import { StatusIcon } from '@/components/issue/issue-icons'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue } from '@/types/flow'
import { AgentEntityDataContext, agentEntityHref } from './agent-entity-data'
import { AgentRichText } from './agent-rich-text'
import styles from './agent-answer.module.css'

/** Assistant markdown with issue / project references rendered as inline entity chips. */
export function AgentAnswerText({ ariaLabel, className, data, markdown }: { ariaLabel?: string; className: string; data?: BootstrapData; markdown: string }) {
  return (
    <AgentEntityDataContext.Provider value={data}>
      <AgentRichText ariaLabel={ariaLabel} className={className} content={markdown} />
    </AgentEntityDataContext.Provider>
  )
}

/** Linear's list of the issues an answer references (shown when it names two or more). */
export function AgentReferencedIssues({ data, issues }: { data?: BootstrapData; issues: Issue[] }) {
  const { t } = useI18n()
  if (!data || issues.length < 2) return null
  return (
    <ul aria-label={t('Referenced issues')} className={styles.references}>
      {issues.map(issue => (
        <li key={issue.id}>
          <a className={styles.referenceRow} data-i18n-ignore href={agentEntityHref(data, { kind: 'issue', issue })}>
            <StatusIcon size={14} state={issue.state} />
            <span className={styles.entityIdentifier}>{issue.identifier}</span>
            <span className={styles.entityTitle}>{issue.title}</span>
          </a>
        </li>
      ))}
    </ul>
  )
}

/** Follow-up suggestion pills under the latest answer; clicking one sends it as the next message. */
export function AgentSuggestionChips({ disabled = false, onSelect, suggestions }: { disabled?: boolean; onSelect: (suggestion: string) => void; suggestions: string[] }) {
  const { t } = useI18n()
  if (!suggestions.length) return null
  return (
    <div aria-label={t('Suggested follow-ups')} className={styles.suggestions} role="group">
      {suggestions.map(suggestion => (
        <button className={styles.suggestion} data-i18n-ignore disabled={disabled} key={suggestion} onClick={() => onSelect(suggestion)} type="button">
          {suggestion}
        </button>
      ))}
    </div>
  )
}
