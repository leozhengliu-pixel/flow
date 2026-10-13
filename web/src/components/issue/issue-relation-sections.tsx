import * as Tooltip from '@radix-ui/react-tooltip'
import { useState, type MouseEvent } from 'react'
import { useI18n } from '@/i18n/i18n'
import { deleteRelation } from '@/lib/api'
import { toast } from '@/lib/toast'
import { FlowTooltip } from '@/components/ui/tooltip'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { PriorityIcon, StatusIcon } from '@/components/issue/issue-icons'
import { useIssuesById } from '@/components/issue/use-issues-by-id'
import type { ActivityEvent, BootstrapData, Issue } from '@/types/flow'
import { issueRelationEntries, type IssueRelationEntry, type IssueRelationSectionKind } from './issue-relation-model'
import './issue-relation-sections.css'

/** The reference app's sidebar order: Blocked by, Blocking, Related, then duplicates. */
const SECTION_ORDER: IssueRelationSectionKind[] = ['blockedBy', 'blocking', 'related', 'duplicateOf', 'duplicates']
const SECTION_TITLE: Record<IssueRelationSectionKind, string> = { blockedBy: 'Blocked by', blocking: 'Blocking', related: 'Related', duplicateOf: 'Duplicate of', duplicates: 'Duplicates' }
const REMOVE_LABEL: Record<IssueRelationSectionKind, string> = { blockedBy: 'Remove blocking issue {identifier}', blocking: 'Remove blocked issue {identifier}', related: 'Remove related issue {identifier}', duplicateOf: 'Remove canonical issue {identifier}', duplicates: 'Remove duplicate of {identifier}' }
const PRIORITY_NAMES = ['No priority', 'Urgent', 'High', 'Medium', 'Low']

/** On light surfaces the reference app darkens bright status colours to L 67.969 (dark theme keeps them; see --issue-rel-status-max-l). */
function tintedState(state: Issue['state']): Issue['state'] {
  return state.color.startsWith('var(') ? state : { ...state, color: `lch(from ${state.color} min(l, var(--issue-rel-status-max-l)) c h)` }
}

/** The reference app's issue sidebar relation sections (Blocked by, Blocking, Related, Duplicates): shown only when non-empty. */
export function IssueRelationSections({ issue, data, activities, onOpenIssue, onDeleteRelation }: {
  issue: Issue
  data: BootstrapData
  activities?: ActivityEvent[]
  onOpenIssue: (issue: Issue) => void
  onDeleteRelation: (relationId: string) => Promise<void>
}) {
  const { t } = useI18n()
  const [removed, setRemoved] = useState<Set<string>>(() => new Set())
  const entries = issueRelationEntries(issue, data.issues, activities).filter(entry => !removed.has(entry.id))
  const targets = useIssuesById(entries.map(entry => entry.targetId), data)
  const remove = async (entry: IssueRelationEntry) => {
    setRemoved(current => new Set(current).add(entry.id))
    try {
      if (entry.ownerId === issue.id) await onDeleteRelation(entry.id)
      else await deleteRelation(entry.ownerId, entry.id)
    } catch (error) {
      setRemoved(current => { const next = new Set(current); next.delete(entry.id); return next })
      toast.error(error instanceof Error ? error.message : t('Could not remove relation'))
    }
  }
  return <>
    {SECTION_ORDER.map(kind => {
      const rows = entries.flatMap(entry => {
        const target = entry.kind === kind ? targets.get(entry.targetId) : undefined
        return target ? [{ entry, target }] : []
      })
      if (!rows.length) return null
      return <section key={kind} className="issue-relation-section" data-relation-section={kind} aria-label={t(SECTION_TITLE[kind])}>
        <div className="issue-relation-section__header"><h3>{t(SECTION_TITLE[kind])}</h3></div>
        <div className="issue-relation-section__rows">
          {rows.map(({ entry, target }) => <IssueRelationRow key={entry.id} kind={kind} issue={target} data={data} onOpen={() => onOpenIssue(target)} onRemove={() => void remove(entry)}/>)}
        </div>
      </section>
    })}
  </>
}

function IssueRelationRow({ kind, issue, data, onOpen, onRemove }: { kind: IssueRelationSectionKind; issue: Issue; data: BootstrapData; onOpen: () => void; onRemove: () => void }) {
  const { t } = useI18n()
  const removeLabel = t(REMOVE_LABEL[kind]).replace('{identifier}', issue.identifier)
  const open = (event: MouseEvent<HTMLAnchorElement>) => {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    onOpen()
  }
  return <div className="issue-relation-row" data-relation-row={issue.identifier}>
    <Tooltip.Provider delayDuration={500} skipDelayDuration={300}>
      <Tooltip.Root>
        <Tooltip.Trigger asChild>
          <a className="issue-relation-row__link" href={`/${data.workspace.urlKey}/issue/${issue.identifier}`} onClick={open}>
            <StatusIcon state={tintedState(issue.state)} size={14}/>
            <span data-i18n-ignore>{issue.title}</span>
          </a>
        </Tooltip.Trigger>
        <Tooltip.Portal>
          <Tooltip.Content data-flow-motion="tooltip" className="issue-relation-card" side="left" align="center" sideOffset={8} collisionPadding={8}>
            <IssueRelationCard issue={issue} onOpen={onOpen}/>
          </Tooltip.Content>
        </Tooltip.Portal>
      </Tooltip.Root>
    </Tooltip.Provider>
    <span className="issue-relation-row__remove">
      <FlowTooltip label={removeLabel} contentClassName="issue-relation-remove-tip" sideOffset={8}>
        <button type="button" aria-label={removeLabel} onClick={onRemove}><FlowGlyph name="closeChat" size={14}/></button>
      </FlowTooltip>
    </span>
  </div>
}

/** The reference app's issue hover card: identifier, title, then status and priority. */
function IssueRelationCard({ issue, onOpen }: { issue: Issue; onOpen: () => void }) {
  const { t } = useI18n()
  return <div className="issue-relation-card__body">
    <div className="issue-relation-card__head">
      <span className="issue-relation-card__identifier" data-i18n-ignore>{issue.identifier}</span>
      <button type="button" className="issue-relation-card__title" onClick={onOpen} data-i18n-ignore>{issue.title}</button>
    </div>
    <div className="issue-relation-card__properties">
      <span className="issue-relation-card__status"><StatusIcon state={tintedState(issue.state)} size={14}/><span data-i18n-ignore>{issue.state.name}</span></span>
      <span className="issue-relation-card__priority"><PriorityIcon priority={issue.priority} size={16}/><span>{t(PRIORITY_NAMES[Math.max(0, Math.min(4, issue.priority))])}</span></span>
    </div>
  </div>
}
