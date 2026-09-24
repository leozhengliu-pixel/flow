import { useMemo, useState } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { Clock3, Copy, LoaderCircle, XCircle } from 'lucide-react'
import { toast } from 'sonner'
import { createComment, createRelation, updateIssue } from '@/lib/api'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue, Team, WorkflowState } from '@/types/flow'

type TriageAction = 'decline' | 'duplicate' | 'snooze'

/** Linear triage actions beside Accept: Decline, Mark as duplicate of…, Snooze until…. */
export function TriageActions({ issue, data, team, open, onOpenChange, onDone }: {
  issue: Issue
  data: BootstrapData
  team: Team
  /** Controlled panel so keyboard shortcuts (2 / 3 / H) can open it. */
  open?: TriageAction
  onOpenChange: (action?: TriageAction) => void
  onDone: (issue: Issue) => void
}) {
  const { t } = useI18n()
  const canceled = useMemo(() => canceledState(data.states, team.id), [data.states, team.id])
  const [comment, setComment] = useState('')
  const [busy, setBusy] = useState(false)
  const run = async (work: () => Promise<Issue>, message: string) => {
    setBusy(true)
    try {
      const updated = await work()
      if (comment.trim()) await createComment(issue.id, comment.trim())
      toast.success(t(message))
      setComment('')
      onOpenChange(undefined)
      onDone(updated)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not update issue'))
    } finally { setBusy(false) }
  }
  const decline = () => canceled && run(() => updateIssue(issue.id, { stateId: canceled.id, expectedVersion: issue.version }), 'Issue declined')
  const duplicate = (target: Issue) => canceled && run(async () => {
    await createRelation(issue.id, 'duplicate', target.id)
    return updateIssue(issue.id, { stateId: duplicateState(data.states, team.id)?.id ?? canceled.id, expectedVersion: issue.version })
  }, `Marked as duplicate of ${target.identifier}`)
  const snooze = (until: Date) => run(() => updateIssue(issue.id, { snoozedUntil: until.toISOString() }), 'Issue snoozed')

  return <div className="flow-triage-actions" role="group" aria-label={t('Triage actions')}>
    <ActionPopover label={t('Decline')} shortcut="3" icon={<XCircle size={14}/>} open={open === 'decline'} onOpenChange={value => onOpenChange(value ? 'decline' : undefined)}>
      <p className="flow-triage-actions__hint">{t('Declined issues move to')} <strong>{canceled?.name ?? t('Canceled')}</strong>.</p>
      <textarea aria-label={t('Comment for declining issue')} placeholder={t('Add an optional comment…')} rows={3} value={comment} onChange={event => setComment(event.target.value)}/>
      <button type="button" className="flow-triage-actions__confirm is-danger" disabled={busy || !canceled} onClick={() => void decline()}>{busy ? <LoaderCircle className="spin" size={14}/> : null}{t('Decline issue')}</button>
    </ActionPopover>
    <ActionPopover label={t('Mark as duplicate')} shortcut="2" icon={<Copy size={14}/>} open={open === 'duplicate'} onOpenChange={value => onOpenChange(value ? 'duplicate' : undefined)}>
      <DuplicatePicker issue={issue} issues={data.issues} disabled={busy} onPick={target => void duplicate(target)}/>
    </ActionPopover>
    <ActionPopover label={t('Snooze')} shortcut="H" icon={<Clock3 size={14}/>} open={open === 'snooze'} onOpenChange={value => onOpenChange(value ? 'snooze' : undefined)}>
      <div className="flow-triage-actions__presets" role="menu">
        {snoozePresets().map(preset => <button key={preset.label} role="menuitem" type="button" disabled={busy} onClick={() => void snooze(preset.until)}>{t(preset.label)}<small>{preset.until.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })}</small></button>)}
      </div>
    </ActionPopover>
  </div>
}

function ActionPopover({ label, shortcut, icon, open, onOpenChange, children }: { label: string; shortcut: string; icon: React.ReactNode; open: boolean; onOpenChange: (open: boolean) => void; children: React.ReactNode }) {
  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Popover.Trigger asChild><button type="button" className="flow-triage-actions__button" aria-keyshortcuts={shortcut}>{icon}<span>{label}</span><kbd>{shortcut}</kbd></button></Popover.Trigger>
    <Popover.Portal><Popover.Content data-flow-motion="floating" className="flow-triage-actions__popover" side="top" align="end" sideOffset={6} collisionPadding={10} aria-label={label}>{children}</Popover.Content></Popover.Portal>
  </Popover.Root>
}

function DuplicatePicker({ issue, issues, disabled, onPick }: { issue: Issue; issues: Issue[]; disabled: boolean; onPick: (issue: Issue) => void }) {
  const { t } = useI18n()
  const options = useMemo(() => issues.filter(item => item.id !== issue.id && !item.archivedAt).map(item => ({ id: item.id, label: `${item.identifier} ${item.title}` })), [issue.id, issues])
  const command = usePropertyCommand({ open: true, options, onOpenChange: () => undefined, closeOnSelect: false, onSelect: option => { const target = issues.find(item => item.id === option.id); if (target) onPick(target) } })
  return <div className="flow-triage-actions__search" onKeyDown={command.onKeyDown}>
    <input ref={command.inputRef} autoFocus aria-label={t('Search issues')} placeholder={t('Mark as duplicate of…')} value={command.query} disabled={disabled} onChange={event => command.onQueryChange(event.target.value)}/>
    <div role="listbox">{command.filteredOptions.slice(0, 8).map(option => <button key={option.id} type="button" role="option" aria-selected={command.activeId === option.id} disabled={disabled} onPointerMove={() => command.setActiveId(option.id)} onClick={() => command.choose(option)} data-i18n-ignore>{option.label}</button>)}</div>
  </div>
}

export function canceledState(states: WorkflowState[], teamId: string) {
  const scoped = states.filter(state => !state.teamId || state.teamId === teamId)
  return scoped.find(state => state.type === 'canceled' && !/duplicate/i.test(state.name)) ?? scoped.find(state => state.type === 'canceled')
}

function duplicateState(states: WorkflowState[], teamId: string) {
  return states.find(state => (!state.teamId || state.teamId === teamId) && state.type === 'canceled' && /duplicate/i.test(state.name))
}

export function snoozePresets(now = new Date()) {
  const at = (days: number) => { const date = new Date(now); date.setDate(date.getDate() + days); date.setHours(8, 0, 0, 0); return date }
  const monday = at(((8 - now.getDay()) % 7) || 7)
  return [
    { label: 'Tomorrow', until: at(1) },
    { label: 'Next week', until: monday },
    { label: 'In two weeks', until: at(14) },
    { label: 'In a month', until: at(30) },
  ]
}

export function isSnoozed(issue: Pick<Issue, 'snoozedUntil'>, now = Date.now()) {
  return Boolean(issue.snoozedUntil && Date.parse(issue.snoozedUntil) > now)
}
