import { useMemo } from 'react'
import { CalendarIcon, NoAssigneeIcon, PriorityIcon, ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import { DocumentGlyph } from '@/components/documents/document-icon'
import { AppLink } from '@/components/ui/app-link'
import { UserAvatar } from '@/components/ui/user-avatar'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'
import { linkAgentEntities } from './agent-answer-content'
import { AgentEntityDataContext, agentEntityHref } from './agent-entity-data'
import { useAgentEntity } from './agent-entity-fetch'
import { AgentEntityHover } from './agent-entity-hover'
import type { AgentEntity, AgentEntityTarget } from './agent-entity-refs'
import { AgentRichText } from './agent-rich-text'
import styles from './agent-answer.module.css'

/**
 * Assistant markdown with every resource reference (issues, projects, initiatives, documents, people, teams, cycles,
 * labels, milestones, customers, releases, views, reviews, updates and comments) rendered as inline entity chips.
 * Accepts raw markdown or markdown already linked by `parseAgentAnswer`; linking is idempotent.
 */
export function AgentAnswerText({ ariaLabel, className, data, markdown }: { ariaLabel?: string; className: string; data?: BootstrapData; markdown: string }) {
  const linked = useMemo(() => data ? linkAgentEntities(markdown, data, { all: true }).markdown : markdown, [data, markdown])
  return (
    <AgentEntityDataContext.Provider value={data}>
      <AgentRichText ariaLabel={ariaLabel} className={className} content={linked} />
    </AgentEntityDataContext.Provider>
  )
}

/** The resources an answer references, as Linear lists them under the text: issues as rows, documents and views as cards. */
export function AgentReferencedResources({ data, references }: { data?: BootstrapData; references: AgentEntityTarget[] }) {
  if (!data) return null
  const issues = references.filter(reference => reference.kind === 'issue')
  const cards = references.filter(reference => reference.kind === 'document' || reference.kind === 'view')
  if (issues.length < 2 && !cards.length) return null
  return (
    <>
      {issues.length >= 2 && <AgentReferencedIssues data={data} references={issues}/>}
      {cards.map(reference => <AgentReferencedCard data={data} key={`${reference.kind}:${reference.id}`} reference={reference}/>)}
    </>
  )
}

/** Linear's list of the issues an answer references (shown when it names two or more). */
function AgentReferencedIssues({ data, references }: { data: BootstrapData; references: AgentEntityTarget[] }) {
  const { t } = useI18n()
  return (
    <ul aria-label={t('Referenced issues')} className={styles.references}>
      {references.map(reference => <AgentReferencedIssueRow data={data} key={reference.id} reference={reference}/>)}
    </ul>
  )
}

function AgentReferencedIssueRow({ data, reference }: { data: BootstrapData; reference: AgentEntityTarget }) {
  const state = useAgentEntity(data, reference)
  if (state.status !== 'ready' || state.entity.kind !== 'issue') return null
  const { issue } = state.entity
  const assignee = issue.assignee?.displayName || issue.assignee?.name
  return (
    <li>
      <AgentEntityHover data={data} entity={state.entity}>
        <AppLink className={styles.referenceRow} data-agent-entity="issue" data-i18n-ignore href={agentEntityHref(data, state.entity)}>
          <PriorityIcon priority={issue.priority} size={16}/>
          <span className={styles.entityIdentifier}>{issue.identifier}</span>
          <StatusIcon size={16} state={issue.state}/>
          <span className={styles.referenceTitle}>{issue.title}</span>
          {assignee ? <UserAvatar avatarUrl={issue.assignee?.avatarUrl} className={styles.referenceAvatar} name={assignee}/> : <NoAssigneeIcon className={styles.dim} size={16}/>}
        </AppLink>
      </AgentEntityHover>
    </li>
  )
}

function cardParts(entity: AgentEntity, data: BootstrapData, t: (source: string) => string, formatDate: (value: Date | string | number, options?: Intl.DateTimeFormatOptions) => string) {
  const date = (value: string) => formatDate(value, { month: 'short', day: 'numeric' })
  if (entity.kind === 'document') {
    const { document } = entity
    const project = (data.projects ?? []).find(item => (document.projectIds ?? []).includes(item.id))
    const creator = document.creator?.displayName || document.creator?.name
    return {
      icon: <DocumentGlyph document={document}/>,
      title: document.title,
      project,
      detail: (creator ? t('Last edited {date} by {name}').replace('{name}', creator) : t('Last edited {date}')).replace('{date}', date(document.updatedAt)),
    }
  }
  if (entity.kind === 'view') {
    const { view } = entity
    const owner = (data.users ?? []).find(user => user.id === view.ownerId)
    return {
      icon: <ViewGlyph color={view.color} icon={view.icon}/>,
      title: view.name,
      owner,
      detail: t('Last updated {date}').replace('{date}', date(view.updatedAt)),
    }
  }
  return undefined
}

/** Linear's embedded document / view card: icon and title, then where it lives and when it last changed. */
function AgentReferencedCard({ data, reference }: { data: BootstrapData; reference: AgentEntityTarget }) {
  const { formatDate, t } = useI18n()
  const state = useAgentEntity(data, reference)
  if (state.status !== 'ready') return null
  const parts = cardParts(state.entity, data, t, formatDate)
  if (!parts) return null
  const owner = 'owner' in parts ? parts.owner : undefined
  const project = 'project' in parts ? parts.project : undefined
  return (
    <AgentEntityHover data={data} entity={state.entity}>
      <AppLink className={styles.referenceCard} data-agent-entity={state.entity.kind} data-i18n-ignore href={agentEntityHref(data, state.entity)}>
        <span className={styles.referenceCardTitle}>{parts.icon}<span>{parts.title}</span></span>
        <span className={styles.referenceCardMeta}>
          {project && <span className={styles.referenceCardRow}><ProjectIcon size={16} style={{ color: project.color }}/>{project.name}</span>}
          {owner && <span className={styles.referenceCardRow}><UserAvatar avatarUrl={owner.avatarUrl} className={styles.referenceAvatar} name={owner.displayName || owner.name}/>{owner.displayName || owner.name}</span>}
          <span className={styles.referenceCardRow}><CalendarIcon className={styles.dim} size={16} variant="start"/>{parts.detail}</span>
        </span>
      </AppLink>
    </AgentEntityHover>
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
