import { useMemo, useState, type ReactNode } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { CircleCheck, CircleX, Clock3, Copy, LoaderCircle } from 'lucide-react'
import { toast } from 'sonner'
import { createComment, createRelation, updateIssue } from '@/lib/api'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { PriorityPicker } from '@/components/issue/core-property-pickers'
import { FlowTooltip } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue, Team } from '@/types/flow'
import { canceledState, duplicateState, resolveAcceptState, snoozePresets, teamStates, TRIAGE_SHORTCUTS, type TriageActionKind } from './triage-model'
import { useTriageShortcuts } from './use-triage-shortcuts'
import './triage.css'

type TriageAction = Exclude<TriageActionKind, 'accept'>

/** Decline / Mark as duplicate / Snooze work shared by the triage page and the issue page header. */
function useTriageWork(issue: Issue, data: BootstrapData, team: Team, onDone: (issue: Issue) => void, onSettled: () => void) {
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
      onSettled()
      onDone(updated)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not update issue'))
    } finally { setBusy(false) }
  }
  return {
    canceled, comment, setComment, busy, run,
    decline: () => canceled && run(() => updateIssue(issue.id, { stateId: canceled.id, expectedVersion: issue.version }), 'Issue declined'),
    duplicate: (target: Issue) => canceled && run(async () => {
      await createRelation(issue.id, 'duplicate', target.id)
      return updateIssue(issue.id, { stateId: duplicateState(data.states, team.id)?.id ?? canceled.id, expectedVersion: issue.version })
    }, `Marked as duplicate of ${target.identifier}`),
    snooze: (until: Date) => run(() => updateIssue(issue.id, { snoozedUntil: until.toISOString() }), 'Issue snoozed'),
  }
}

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
  const work = useTriageWork(issue, data, team, onDone, () => onOpenChange(undefined))

  return <div className="flow-triage-actions" role="group" aria-label={t('Triage actions')}>
    <ActionPopover label={t('Decline')} shortcut={TRIAGE_SHORTCUTS.decline} icon={<CircleX size={14}/>} open={open === 'decline'} onOpenChange={value => onOpenChange(value ? 'decline' : undefined)}>
      <DeclineForm work={work}/>
    </ActionPopover>
    <ActionPopover label={t('Mark as duplicate')} shortcut={TRIAGE_SHORTCUTS.duplicate} icon={<Copy size={14}/>} open={open === 'duplicate'} onOpenChange={value => onOpenChange(value ? 'duplicate' : undefined)}>
      <DuplicatePicker issue={issue} issues={data.issues} disabled={work.busy} onPick={target => void work.duplicate(target)}/>
    </ActionPopover>
    <ActionPopover label={t('Snooze')} shortcut={TRIAGE_SHORTCUTS.snooze} icon={<Clock3 size={14}/>} open={open === 'snooze'} onOpenChange={value => onOpenChange(value ? 'snooze' : undefined)}>
      <SnoozePresets disabled={work.busy} onSnooze={until => void work.snooze(until)}/>
    </ActionPopover>
  </div>
}

/**
 * Linear's issue-page header actions for an issue in Triage: Accept, Decline, then duplicate and snooze
 * icon buttons. Keyboard: 1 Accept, 2 Decline, 3 / M M Duplicate, H Snooze.
 */
export function TriageHeaderActions({ issue, data, onDone, shortcuts = true }: {
  issue: Issue
  data: BootstrapData
  onDone: (issue: Issue) => void
  /** Register the 1 / 2 / 3 / M M / H shortcuts (off where another surface owns them, e.g. Inbox). */
  shortcuts?: boolean
}) {
  const { t } = useI18n()
  const team = data.teams.find(item => item.id === issue.team.id) ?? issue.team
  const [open, setOpen] = useState<TriageActionKind>()
  const close = () => setOpen(undefined)
  const work = useTriageWork(issue, data, team, onDone, close)
  useTriageShortcuts(shortcuts, setOpen)
  const toggle = (kind: TriageActionKind) => (value: boolean) => setOpen(value ? kind : undefined)

  return <div className="issue-triage-header-actions" role="group" aria-label={t('Triage actions')}>
    <HeaderAction kind="accept" label={t('Accept')} tooltip={t('Accept issue')} icon={<CircleCheck size={14}/>} text open={open === 'accept'} onOpenChange={toggle('accept')}>
      <AcceptForm issue={issue} data={data} team={team} onDone={updated => { close(); onDone(updated) }}/>
    </HeaderAction>
    <HeaderAction kind="decline" label={t('Decline')} tooltip={t('Decline issue')} icon={<CircleX size={14}/>} text open={open === 'decline'} onOpenChange={toggle('decline')}>
      <DeclineForm work={work}/>
    </HeaderAction>
    <HeaderAction kind="duplicate" label={t('Mark as duplicate')} icon={<Copy size={14}/>} open={open === 'duplicate'} onOpenChange={toggle('duplicate')}>
      <DuplicatePicker issue={issue} issues={data.issues} disabled={work.busy} onPick={target => void work.duplicate(target)}/>
    </HeaderAction>
    <HeaderAction kind="snooze" label={t('Snooze')} icon={<Clock3 size={14}/>} open={open === 'snooze'} onOpenChange={toggle('snooze')}>
      <SnoozePresets disabled={work.busy} onSnooze={until => void work.snooze(until)}/>
    </HeaderAction>
  </div>
}

function HeaderAction({ kind, label, tooltip, icon, text = false, open, onOpenChange, children }: { kind: TriageActionKind; label: string; tooltip?: string; icon: ReactNode; text?: boolean; open: boolean; onOpenChange: (open: boolean) => void; children: ReactNode }) {
  const shortcut = TRIAGE_SHORTCUTS[kind]
  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <FlowTooltip disabled={open} label={tooltip ?? label} shortcut={shortcut}>
      <Popover.Trigger asChild>
        <button type="button" className={text ? 'issue-triage-header-button' : 'issue-triage-header-button is-icon'} data-triage-action={kind} aria-label={text ? undefined : label} aria-keyshortcuts={shortcut}>
          {icon}{text ? <span>{label}</span> : null}
        </button>
      </Popover.Trigger>
    </FlowTooltip>
    <Popover.Portal><Popover.Content data-flow-motion="floating" className="flow-triage-actions__popover" side="bottom" align="end" sideOffset={6} collisionPadding={10} aria-label={tooltip ?? label}>{children}</Popover.Content></Popover.Portal>
  </Popover.Root>
}

function AcceptForm({ issue, data, team, onDone }: { issue: Issue; data: BootstrapData; team: Team; onDone: (issue: Issue) => void }) {
  const { t } = useI18n()
  const teamSettings = data.teamSettings?.[team.id]
  const target = useMemo(() => resolveAcceptState(teamStates(data.states, team.id), teamSettings?.defaultStateId), [data.states, team.id, teamSettings?.defaultStateId])
  const [priority, setPriority] = useState(issue.priority)
  const [comment, setComment] = useState('')
  const [busy, setBusy] = useState(false)
  const needsPriority = Boolean(teamSettings?.triageRequirePriority) && priority === 0
  const accept = async () => {
    if (!target || needsPriority) return
    setBusy(true)
    try {
      const updated = await updateIssue(issue.id, { ...(priority !== issue.priority ? { priority } : {}), stateId: target.id, expectedVersion: issue.version })
      if (comment.trim()) await createComment(issue.id, comment.trim())
      toast.success(t('Issue accepted'))
      setComment('')
      onDone(updated)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not accept issue'))
    } finally { setBusy(false) }
  }
  return <>
    <p className="flow-triage-actions__hint">{t('Accepted issues move to')} <strong>{target?.name ?? t('Todo')}</strong>.</p>
    {teamSettings?.triageRequirePriority ? <div className="flow-triage-actions__priority"><PriorityPicker value={priority} onChange={setPriority}/></div> : null}
    {needsPriority ? <p className="flow-fast-triage-accept__warning" role="status">{t('Set a priority before moving this issue out of triage.')}</p> : null}
    <textarea aria-label={t('Comment for accepting issue')} placeholder={t('Add an optional comment…')} rows={3} value={comment} onChange={event => setComment(event.target.value)}/>
    <button type="button" className="flow-triage-actions__confirm is-primary" disabled={busy || !target || needsPriority} onClick={() => void accept()}>{busy ? <LoaderCircle className="spin" size={14}/> : null}{t('Accept issue')}</button>
  </>
}

function DeclineForm({ work }: { work: ReturnType<typeof useTriageWork> }) {
  const { t } = useI18n()
  return <>
    <p className="flow-triage-actions__hint">{t('Declined issues move to')} <strong>{work.canceled?.name ?? t('Canceled')}</strong>.</p>
    <textarea aria-label={t('Comment for declining issue')} placeholder={t('Add an optional comment…')} rows={3} value={work.comment} onChange={event => work.setComment(event.target.value)}/>
    <button type="button" className="flow-triage-actions__confirm is-danger" disabled={work.busy || !work.canceled} onClick={() => void work.decline()}>{work.busy ? <LoaderCircle className="spin" size={14}/> : null}{t('Decline issue')}</button>
  </>
}

function SnoozePresets({ disabled, onSnooze }: { disabled: boolean; onSnooze: (until: Date) => void }) {
  const { t } = useI18n()
  return <div className="flow-triage-actions__presets" role="menu" aria-label={t('Snooze until')}>
    {snoozePresets().map(preset => <button key={preset.label} role="menuitem" type="button" disabled={disabled} onClick={() => onSnooze(preset.until)}>{t(preset.label)}<small>{preset.until.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })}</small></button>)}
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
