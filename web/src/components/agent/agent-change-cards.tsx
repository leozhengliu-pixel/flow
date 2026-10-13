import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { FileText, Redo2, Trash2, Undo2 } from 'lucide-react'
import { NoAssigneeIcon, PriorityIcon, ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { healthColor } from '@/components/project-detail/health-color'
import { HealthGlyph } from '@/components/project-detail/health-glyph'
import { AppLink } from '@/components/ui/app-link'
import { Toggle } from '@/components/ui/toggle'
import { ScopedFlowTooltip as FlowTooltip } from '@/components/ui/tooltip'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import { restoreAgentMessageChanges, undoAgentMessageChanges } from '@/lib/api'
import { toast } from '@/lib/toast'
import type { AgentChange, AgentMessage, AgentSession, BootstrapData, Project } from '@/types/flow'
import { agentChangeCards, agentChangeErrorMessage, agentChangeHeader, agentChangeSkippedNote, agentChangeSummary, changedBlockTexts, diffMarkdownBlocks, markdownPlainText, type AgentChangeCard } from './agent-change-model'
import { highlightAgentChanges, type AgentChangeHighlight } from './agent-change-highlight'
import { chipParts } from './agent-entity-chip-parts'
import { agentEntityHref } from './agent-entity-data'
import { useAgentEntity } from './agent-entity-fetch'
import { AgentEntityHover } from './agent-entity-hover'
import type { AgentEntity, AgentEntityKind } from './agent-entity-refs'
import { AgentSkillsIcon } from './agent-icons'
import answerStyles from './agent-answer.module.css'
import styles from './agent-change-cards.module.css'

type CardsProps = {
  data?: BootstrapData
  message: Pick<AgentMessage, 'id' | 'parts'>
  /** The saved chat; without it (or for a reply still being written) Undo is unavailable. */
  sessionId?: string
  onSessionChange?: (session: AgentSession) => void
}

/**
 * The reference app's result cards for what an agent reply did in Flow: "Created DEV-50" over an issue card, "Updated project" with
 * a one-line summary, "Highlight changes" for description and document edits, and Undo, which becomes Restore.
 */
export function AgentChangeCards({ data, message, sessionId, onSessionChange }: CardsProps) {
  // Changes returned by Undo / Restore, shown right away even where the surface does not take the updated chat.
  const [local, setLocal] = useState<Record<string, AgentChange>>({})
  const merged = useMemo(() => ({
    parts: (message.parts ?? []).map(part => part.toolCall && local[part.id] ? { ...part, toolCall: { ...part.toolCall, change: local[part.id] } } : part),
  }), [message.parts, local])
  const cards = useMemo(() => groupCreatedIssues(agentChangeCards(merged)), [merged])
  if (!cards.length) return null
  const persisted = Boolean(sessionId) && !message.id.startsWith('pending-') && !message.id.startsWith('awaiting-')
  const act = async (card: AgentChangeCard, undo: boolean) => {
    const session = await (undo ? undoAgentMessageChanges : restoreAgentMessageChanges)(sessionId!, message.id, card.partIds)
    const reply = session.messages.find(item => item.id === message.id)
    const next: Record<string, AgentChange> = {}
    for (const part of reply?.parts ?? []) if (part.toolCall?.change && card.partIds.includes(part.id)) next[part.id] = part.toolCall.change
    setLocal(current => ({ ...current, ...next }))
    onSessionChange?.(session)
    return Object.values(next).flatMap(change => change.skipped ?? [])
  }
  return (
    <div className={styles.changes} data-agent-changes="">
      {cards.map(card => <AgentChangeCardView card={card} data={data} key={card.key} onAction={persisted ? undo => act(card, undo) : undefined}/>)}
    </div>
  )
}

type GroupedCard = AgentChangeCard & { issues?: AgentChangeCard[] }

/** Two or more issues created in one reply read as one list ("Created 3 issues")'s issue-list card. */
function groupCreatedIssues(cards: AgentChangeCard[]): GroupedCard[] {
  const created = cards.filter(card => card.entityType === 'issue' && card.action === 'created')
  if (created.length < 2) return cards
  const group: GroupedCard = {
    ...created[0],
    key: `issues:${created[0].key}`,
    label: String(created.length),
    partIds: created.flatMap(card => card.partIds),
    undoable: created.every(card => card.undoable),
    undone: created.every(card => card.undone),
    skipped: undefined,
    issues: created,
  }
  const result: GroupedCard[] = []
  for (const card of cards) {
    if (card === created[0]) result.push(group)
    else if (!created.includes(card)) result.push(card)
  }
  return result
}

function AgentChangeCardView({ card, data, onAction }: { card: GroupedCard; data?: BootstrapData; onAction?: (undo: boolean) => Promise<string[]> }) {
  const { locale, t } = useI18n()
  const [busy, setBusy] = useState(false)
  const header = card.issues ? t('Created {count} issues').replace('{count}', card.label) : agentChangeHeader(card, t)
  const summary = card.action === 'updated' ? agentChangeSummary(card, t, locale) : ''
  const run = async () => {
    if (!onAction || busy) return
    setBusy(true)
    try {
      const skipped = await onAction(!card.undone)
      if (skipped.length) toast(agentChangeSkippedNote(skipped, t, locale))
    } catch (error) {
      toast.error(agentChangeErrorMessage(error instanceof Error ? error.message : '', t))
    } finally {
      setBusy(false)
    }
  }
  const undoLabel = card.undone ? t('Restore') : t('Undo')
  const undoHint = card.undone ? t('Re-apply the agent’s changes. Overrides current changes.') : card.action === 'created' ? t('Remove what the agent created') : t('Revert the agent’s changes')
  return (
    <section aria-label={header} className={styles.change} data-action={card.action} data-agent-change={card.entityType} data-state={card.undone ? 'undone' : undefined}>
      <div className={styles.header} data-i18n-ignore>{header}</div>
      <div className={styles.card}>
        {card.issues
          ? <ul aria-label={t('Created issues')} className={answerStyles.references} style={{ margin: 0 }}>{card.issues.map(issue => <CreatedIssueRow card={issue} data={data} key={issue.key}/>)}</ul>
          : <ChangeEntity card={card} data={data}/>}
        {summary && <p className={styles.summary}>{summary}</p>}
        {card.skipped && <p className={styles.note}>{agentChangeSkippedNote(card.skipped, t, locale)}</p>}
        {(card.text || (card.undoable && onAction)) && (
          <ChangeFooter card={card}>
            {card.undoable && onAction && (
              <FlowTooltip label={undoHint}>
                <button aria-label={undoLabel} className={styles.undo} disabled={busy} onClick={() => void run()} type="button">
                  {card.undone ? <Redo2 aria-hidden="true"/> : <Undo2 aria-hidden="true"/>}
                  <span>{undoLabel}</span>
                </button>
              </FlowTooltip>
            )}
          </ChangeFooter>
        )}
      </div>
    </section>
  )
}

/** The footer row: "Highlight changes" for text edits (the live editor, else an in-card diff) and Undo / Restore. */
function ChangeFooter({ card, children }: { card: AgentChangeCard; children: ReactNode }) {
  const { t } = useI18n()
  const [highlight, setHighlight] = useState(false)
  const [diff, setDiff] = useState(false)
  const text = card.undone ? undefined : card.text
  useEffect(() => {
    setDiff(false)
    if (!highlight || !text) return
    const targets = changedBlockTexts(text.before, text.after)
    let active: AgentChangeHighlight | undefined
    let tries = 0
    let timer: number | undefined
    const attempt = () => {
      const next = highlightAgentChanges(targets)
      if (!next.matched) return false
      active = next
      setDiff(false)
      return true
    }
    // Without the edited editor on screen the card shows the diff; an editor that catches up (realtime) switches to bars.
    if (!attempt()) {
      setDiff(true)
      timer = window.setInterval(() => { if (++tries >= 15 || attempt()) window.clearInterval(timer) }, 1000)
    }
    return () => {
      window.clearInterval(timer)
      active?.stop()
    }
  }, [highlight, text])
  const lines = useMemo(() => diff && text ? diffMarkdownBlocks(text.before, text.after) : [], [diff, text])
  return (
    <>
      {lines.length > 0 && (
        <ul aria-label={t('Changes')} className={styles.diff}>
          {lines.map((line, index) => <li data-kind={line.kind} key={index}>{markdownPlainText(line.text) || line.text}</li>)}
        </ul>
      )}
      <div className={styles.footer}>
        {text && (
          <label className={styles.highlight}>
            <Toggle checked={highlight} label={t('Highlight changes')} onChange={setHighlight}/>
            <span>{t('Highlight changes')}</span>
          </label>
        )}
        {children}
      </div>
    </>
  )
}

function ChangeEntity({ card, data }: { card: AgentChangeCard; data?: BootstrapData }) {
  if (data && card.kind && card.entityId) return <ResolvedChangeEntity card={card} data={data} kind={card.kind}/>
  return <FallbackEntity card={card}/>
}

function fallbackIcon(card: AgentChangeCard) {
  if (card.action === 'deleted') return <Trash2 aria-hidden="true"/>
  if (card.entityType === 'skill') return <AgentSkillsIcon/>
  if (card.entityType === 'project') return <ProjectIcon size={16}/>
  if (card.entityType === 'initiative') return <ViewGlyph icon="Initiative"/>
  if (card.entityType === 'view') return <ViewGlyph/>
  if (card.entityType === 'issue') return <StatusIcon size={16} state={{ id: '', name: '', color: 'currentColor', type: 'backlog' }}/>
  if (card.entityType.endsWith('_update')) {
    const health = (card.preview?.health || 'onTrack') as Project['health']
    return <span style={{ color: healthColor(health), display: 'inline-flex' }}><HealthGlyph className="" health={health}/></span>
  }
  return <FileText aria-hidden="true"/>
}

/** A card for an entity Flow cannot resolve on the client (skills, updates, removed records): icon, name and preview. */
function FallbackEntity({ card }: { card: AgentChangeCard }) {
  const { t } = useI18n()
  const update = card.entityType.endsWith('_update')
  const title = update ? [card.preview?.parent, t('Update')].filter(Boolean).join(' › ') : card.label || t('Untitled')
  const content = <>{fallbackIcon(card)}<span className={styles.entityLabel}>{title}</span></>
  // A record created this turn may not be in the client's data yet: the card still links to it.
  const linked = card.url && !(card.undone && card.action === 'created') && !(card.action === 'deleted' && !card.undone)
  return (
    <>
      {linked ? <AppLink className={styles.entity} data-i18n-ignore href={card.url!}>{content}</AppLink> : <span className={styles.entity} data-i18n-ignore>{content}</span>}
      {(card.preview?.body || card.preview?.instructions) && <p className={styles.preview} data-i18n-ignore>{card.preview.body || card.preview.instructions}</p>}
    </>
  )
}

function ResolvedChangeEntity({ card, data, kind }: { card: AgentChangeCard; data: BootstrapData; kind: AgentEntityKind }) {
  // Issues resolve by identifier (the card's label), which also finds issues the client has not loaded yet.
  const state = useAgentEntity(data, { kind, id: kind === 'issue' && card.label ? card.label : card.entityId! })
  if (state.status !== 'ready') return <FallbackEntity card={card}/>
  return (
    <>
      <EntityRow data={data} entity={state.entity}/>
      <EntityMeta data={data} entity={state.entity}/>
      {(card.preview?.body || card.preview?.instructions) && <p className={styles.preview} data-i18n-ignore>{card.preview.body || card.preview.instructions}</p>}
    </>
  )
}

function EntityRow({ data, entity }: { data: BootstrapData; entity: AgentEntity }) {
  const { t } = useI18n()
  const parts = chipParts(entity, t)
  return (
    <AgentEntityHover data={data} entity={entity}>
      <AppLink className={styles.entity} data-agent-entity={entity.kind} data-i18n-ignore href={agentEntityHref(data, entity)}>
        {parts.icon}
        {parts.identifier && <span className={styles.entityIdentifier}>{parts.identifier}</span>}
        <span className={styles.entityLabel}>{parts.label}</span>
      </AppLink>
    </AgentEntityHover>
  )
}

/** The reference app's second line: an issue's status and priority pills, a project's or initiative's status, a view's owner. */
function EntityMeta({ data, entity }: { data: BootstrapData; entity: AgentEntity }) {
  const { formatDate, t } = useI18n()
  const date = (value: string) => formatDate(value, { month: 'short', day: 'numeric' })
  if (entity.kind === 'issue') {
    const { issue } = entity
    return (
      <div className={styles.pills}>
        <span className={styles.pill}><StatusIcon size={14} state={issue.state}/>{t(issue.state.name)}</span>
        <span className={styles.pill}><PriorityIcon priority={issue.priority} size={14}/>{t(issue.priorityLabel || 'No priority')}</span>
      </div>
    )
  }
  if (entity.kind === 'project' && entity.project.status?.name) {
    return <div className={styles.pills}><span className={styles.pill}>{t(entity.project.status.name)}</span></div>
  }
  if (entity.kind === 'initiative' && entity.initiative.status) {
    return <div className={styles.pills}><span className={styles.pill}>{t(entity.initiative.status.charAt(0).toUpperCase() + entity.initiative.status.slice(1))}</span></div>
  }
  if (entity.kind === 'view') {
    const owner = (data.users ?? []).find(user => user.id === entity.view.ownerId)
    return (
      <div className={styles.pills}>
        {owner && <span className={styles.pill} data-i18n-ignore><UserAvatar avatarUrl={owner.avatarUrl} className={answerStyles.referenceAvatar} name={owner.displayName || owner.name}/>{owner.displayName || owner.name}</span>}
        {entity.view.updatedAt && <span className={styles.pill}>{t('Last updated {date}').replace('{date}', date(entity.view.updatedAt))}</span>}
      </div>
    )
  }
  if (entity.kind === 'document' && entity.document.updatedAt) {
    return <div className={styles.pills}><span className={styles.pill}>{t('Last edited {date}').replace('{date}', date(entity.document.updatedAt))}</span></div>
  }
  return null
}

/** One row of the "Created N issues" list: priority, identifier, status, title and assignee's issue list. */
function CreatedIssueRow({ card, data }: { card: AgentChangeCard; data?: BootstrapData }) {
  if (!data) return <li><span className={answerStyles.referenceRow}><span className={answerStyles.referenceTitle}>{card.label}</span></span></li>
  return <ResolvedIssueRow card={card} data={data}/>
}

function ResolvedIssueRow({ card, data }: { card: AgentChangeCard; data: BootstrapData }) {
  const state = useAgentEntity(data, { kind: 'issue', id: card.label || card.entityId || '' })
  if (state.status !== 'ready' || state.entity.kind !== 'issue') {
    return <li><span className={answerStyles.referenceRow} data-i18n-ignore><span/><span className={answerStyles.entityIdentifier}>{card.label}</span></span></li>
  }
  const { issue } = state.entity
  const assignee = issue.assignee?.displayName || issue.assignee?.name
  return (
    <li>
      <AgentEntityHover data={data} entity={state.entity}>
        <AppLink className={answerStyles.referenceRow} data-agent-entity="issue" data-i18n-ignore href={agentEntityHref(data, state.entity)}>
          <PriorityIcon priority={issue.priority} size={14}/>
          <span className={answerStyles.entityIdentifier}>{issue.identifier}</span>
          <StatusIcon size={14} state={issue.state}/>
          <span className={answerStyles.referenceTitle}>{issue.title}</span>
          {assignee ? <UserAvatar avatarUrl={issue.assignee?.avatarUrl} className={answerStyles.referenceAvatar} name={assignee}/> : <NoAssigneeIcon className={answerStyles.dim} size={16}/>}
        </AppLink>
      </AgentEntityHover>
    </li>
  )
}
