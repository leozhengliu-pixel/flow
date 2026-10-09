import { useMemo, useState, type ReactNode } from 'react'
import { Command } from 'cmdk'
import { AlarmClockOff, CircleCheck, CircleX, Clock3, Copy, CornerDownLeft } from 'lucide-react'
import { toast } from 'sonner'
import { createComment, createRelation, updateIssue } from '@/lib/api'
import { MentionTextField } from '@/components/editor/mention-text-field'
import { PriorityPicker, StatusPicker } from '@/components/issue/core-property-pickers'
import { StatusIcon } from '@/components/issue/issue-icons'
import { useIssueSearch } from '@/components/issue/use-issue-search'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { DateTimeControl } from '@/components/ui/date-time-control'
import { FlowTooltip } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue, IssueRelationType, Team } from '@/types/flow'
import { canceledState, duplicateState, formatSnoozeDate, isSnoozed, parseSnoozeInput, resolveAcceptState, snoozePresets, teamStates, TRIAGE_SHORTCUTS, type TriageActionKind } from './triage-model'
import { useTriageShortcuts } from './use-triage-shortcuts'
import './triage.css'
import { GridLoader } from '@/components/ui/grid-loader'

/** Decline / Mark as duplicate / Snooze work shared by the triage list, the issue header and the context menu. */
function useTriageWork(issue: Issue, data: BootstrapData, team: Pick<Team, 'id'>, onDone: (issue: Issue) => void, onSettled: () => void) {
  const { t } = useI18n()
  const canceled = useMemo(() => canceledState(data.states, team.id), [data.states, team.id])
  const duplicate = useMemo(() => duplicateState(data.states, team.id) ?? canceled, [canceled, data.states, team.id])
  const [busy, setBusy] = useState(false)
  const run = async (work: () => Promise<Issue>, message: string, comment = '') => {
    setBusy(true)
    try {
      const updated = await work()
      if (comment.trim()) await createComment(issue.id, comment.trim())
      toast.success(message)
      onSettled()
      onDone(updated)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not update issue'))
    } finally { setBusy(false) }
  }
  return {
    canceled, duplicate, busy,
    decline: (comment: string) => canceled ? run(() => updateIssue(issue.id, { stateId: canceled.id, expectedVersion: issue.version }), t('Issue declined'), comment) : Promise.resolve(),
    markDuplicate: (target: Issue) => duplicate ? run(async () => {
      await createRelation(issue.id, 'duplicate', target.id)
      return updateIssue(issue.id, { stateId: duplicate.id, expectedVersion: issue.version })
    }, t('Marked as duplicate of {identifier}').replace('{identifier}', target.identifier)) : Promise.resolve(),
    relate: (type: IssueRelationType, target: Issue) => run(async () => { await createRelation(issue.id, type, target.id); return issue }, t('Relation added')),
    snooze: (until: Date) => run(() => updateIssue(issue.id, { snoozedUntil: until.toISOString() }), t('Snoozed until {date}').replace('{date}', formatSnoozeDate(until))),
    unsnooze: () => run(() => updateIssue(issue.id, { snoozedUntil: '' }), t('Issue unsnoozed')),
  }
}

type TriageWork = ReturnType<typeof useTriageWork>

/**
 * Linear's triage modals for one issue: Accept… / Decline… dialogs and the Mark as duplicate / Snooze command menus.
 * Controlled so the action bar, keyboard (1 / 2 / 3 / H) and the list's context menu can open them.
 */
export function TriageActionDialogs({ issue, data, open, onOpenChange, onDone }: {
  issue: Issue
  data: BootstrapData
  open?: TriageActionKind
  onOpenChange: (action?: TriageActionKind) => void
  onDone: (issue: Issue) => void
}) {
  const team = data.teams.find(item => item.id === issue.team.id) ?? issue.team
  const close = () => onOpenChange(undefined)
  const work = useTriageWork(issue, data, team, onDone, close)
  const toggle = (value: boolean) => { if (!value) close() }
  return <>
    {open === 'accept' && <AcceptDialog issue={issue} data={data} team={team} onOpenChange={toggle} onDone={updated => { close(); onDone(updated) }}/>}
    {open === 'decline' && <DeclineDialog issue={issue} work={work} onOpenChange={toggle}/>}
    {open === 'duplicate' && <DuplicateDialog issue={issue} data={data} work={work} onOpenChange={toggle}/>}
    {open === 'snooze' && <SnoozeDialog issue={issue} work={work} onOpenChange={toggle}/>}
  </>
}

const ACTIONS: { kind: TriageActionKind; tooltip: string; label: string; icon: ReactNode }[] = [
  { kind: 'accept', tooltip: 'Accept issue', label: 'Accept issue from triage', icon: <CircleCheck size={16}/> },
  { kind: 'decline', tooltip: 'Decline issue', label: 'Decline triage issue', icon: <CircleX size={16}/> },
  { kind: 'duplicate', tooltip: 'Mark issue as duplicate of', label: 'Mark triage issue as duplicate', icon: <Copy size={16}/> },
  { kind: 'snooze', tooltip: 'Snooze issue until', label: 'Snooze triage issue', icon: <Clock3 size={16}/> },
]

/**
 * Linear's issue header actions for an issue in Triage: four round icon buttons (Accept 1, Decline 2,
 * Mark as duplicate 3, Snooze H), plus Unsnooze while the issue is snoozed.
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
  const work = useTriageWork(issue, data, team, onDone, () => setOpen(undefined))
  useTriageShortcuts(shortcuts && !open, setOpen)
  const snoozed = isSnoozed(issue)
  return <div className="issue-triage-header-actions" role="group" aria-label={t('Triage actions')}>
    {snoozed && issue.snoozedUntil ? <FlowTooltip label={`${t('Snoozed until')} ${formatSnoozeDate(new Date(issue.snoozedUntil))}`}>
      <button type="button" className="issue-triage-header-button is-text" disabled={work.busy} onClick={() => void work.unsnooze()}><AlarmClockOff size={14}/><span>{t('Unsnooze')}</span></button>
    </FlowTooltip> : null}
    {ACTIONS.map(action => <FlowTooltip key={action.kind} disabled={open === action.kind} label={t(action.tooltip)} shortcut={TRIAGE_SHORTCUTS[action.kind]}>
      <button type="button" className="issue-triage-header-button" data-triage-action={action.kind} data-state={open === action.kind ? 'open' : undefined} aria-label={t(action.label)} aria-keyshortcuts={TRIAGE_SHORTCUTS[action.kind]} onClick={() => setOpen(action.kind)}>{action.icon}</button>
    </FlowTooltip>)}
    <TriageActionDialogs issue={issue} data={data} open={open} onOpenChange={setOpen} onDone={onDone}/>
  </div>
}

function IssueChip({ issue }: { issue: Issue }) {
  return <div className="command-context triage-command-context"><span className="command-context-chip" data-i18n-ignore title={`${issue.identifier} ${issue.title}`}>{issue.identifier} · {issue.title}</span></div>
}

function AcceptDialog({ issue, data, team, onOpenChange, onDone }: { issue: Issue; data: BootstrapData; team: Pick<Team, 'id'>; onOpenChange: (open: boolean) => void; onDone: (issue: Issue) => void }) {
  const { t } = useI18n()
  const teamSettings = data.teamSettings?.[team.id]
  const states = useMemo(() => teamStates(data.states, team.id), [data.states, team.id])
  const target = useMemo(() => resolveAcceptState(states, teamSettings?.defaultStateId), [states, teamSettings?.defaultStateId])
  const [stateId, setStateId] = useState(target?.id)
  const [priority, setPriority] = useState(issue.priority)
  const [comment, setComment] = useState('')
  const [busy, setBusy] = useState(false)
  const selected = states.find(state => state.id === stateId) ?? target
  const needsPriority = Boolean(teamSettings?.triageRequirePriority) && priority === 0
  const accept = async () => {
    if (!selected || needsPriority || busy) return
    setBusy(true)
    try {
      const updated = await updateIssue(issue.id, { ...(priority !== issue.priority ? { priority } : {}), stateId: selected.id, expectedVersion: issue.version })
      if (comment.trim()) await createComment(issue.id, comment.trim())
      toast.success(t('Issue accepted'))
      onDone(updated)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not accept issue'))
    } finally { setBusy(false) }
  }
  return <Dialog open onOpenChange={onOpenChange}>
    <DialogContent aria-describedby={undefined} className="action-dialog triage-action-dialog" overlayClassName="action-dialog-overlay" onKeyDown={event => { if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) { event.preventDefault(); void accept() } }}>
      <IssueChip issue={issue}/>
      <DialogTitle>{t('Accept issue…')}</DialogTitle>
      <p>{t('Move the issue out of triage into the team workflow.')}</p>
      <div className="triage-action-dialog__properties">
        {selected ? <StatusPicker value={selected} states={states.filter(state => state.type !== 'backlog')} onChange={setStateId}/> : null}
        <PriorityPicker value={priority} onChange={setPriority}/>
      </div>
      {!selected ? <p className="action-dialog-error" role="alert">{t('This team has no status to accept issues into.')}</p> : null}
      {needsPriority ? <p className="action-dialog-error" role="status">{t('Set a priority before moving this issue out of triage.')}</p> : null}
      <MentionTextField className="triage-action-dialog__comment" ariaLabel={t('Comment for accepting issue')} placeholder={t('Add an optional comment…')} value={comment} onChange={setComment}/>
      <footer>
        <button type="button" onClick={() => onOpenChange(false)}>{t('Cancel')}</button>
        <button type="button" className="primary" disabled={busy || !selected || needsPriority} onClick={() => void accept()}>{busy ? <GridLoader size={14}/> : null}{t('Accept')}</button>
      </footer>
    </DialogContent>
  </Dialog>
}

function DeclineDialog({ issue, work, onOpenChange }: { issue: Issue; work: TriageWork; onOpenChange: (open: boolean) => void }) {
  const { t } = useI18n()
  const [comment, setComment] = useState('')
  const decline = () => { if (work.canceled && !work.busy) void work.decline(comment) }
  return <Dialog open onOpenChange={onOpenChange}>
    <DialogContent aria-describedby={undefined} className="action-dialog triage-action-dialog" overlayClassName="action-dialog-overlay" onKeyDown={event => { if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) { event.preventDefault(); decline() } }}>
      <IssueChip issue={issue}/>
      <DialogTitle>{t('Decline issue…')}</DialogTitle>
      {work.canceled
        ? <p>{t('Declined issues move to')} <strong data-i18n-ignore>{work.canceled.name}</strong>.</p>
        : <p className="action-dialog-error" role="alert">{t('This team has no canceled status. Add one in the team workflow settings to decline issues.')}</p>}
      <MentionTextField className="triage-action-dialog__comment" ariaLabel={t('Comment for declining issue')} placeholder={t('Add an optional comment…')} value={comment} onChange={setComment}/>
      <footer>
        <button type="button" onClick={() => onOpenChange(false)}>{t('Cancel')}</button>
        <button type="button" className="danger" disabled={work.busy || !work.canceled} onClick={decline}>{work.busy ? <GridLoader size={14}/> : null}{t('Decline')}</button>
      </footer>
    </DialogContent>
  </Dialog>
}

/** Linear's command-menu footer: ↵ Select on the left, ⌥↵ More actions on the right. */
function CommandFooter({ moreActions }: { moreActions?: boolean }) {
  const { t } = useI18n()
  return <footer className="triage-command-footer">
    <span><kbd><CornerDownLeft size={11}/></kbd>{t('Select')}</span>
    {moreActions ? <span><kbd>⌥</kbd><kbd><CornerDownLeft size={11}/></kbd>{t('More actions')}</span> : null}
  </footer>
}

function DuplicateDialog({ issue, data, work, onOpenChange }: { issue: Issue; data: BootstrapData; work: TriageWork; onOpenChange: (open: boolean) => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [active, setActive] = useState('')
  const [more, setMore] = useState<Issue>()
  const [moreQuery, setMoreQuery] = useState('')
  // Paged workspaces only hold the issues on screen: the hook adds a server search for the query.
  const candidates = useIssueSearch(query, data.issues)
  const results = useMemo(() => {
    const text = query.trim().toLowerCase()
    return candidates.filter(item => item.id !== issue.id && !item.archivedAt && (!text || `${item.identifier} ${item.title}`.toLowerCase().includes(text))).slice(0, 50)
  }, [candidates, issue.id, query])
  const pick = (target: Issue) => { if (!work.busy) void work.markDuplicate(target) }
  // ⌥↵ on a result: what else to do with that issue (Linear's "More actions").
  const moreActions = more ? [
    { id: 'duplicate', label: t('Mark as duplicate'), icon: <Copy/>, disabled: !work.duplicate || work.busy, run: () => pick(more) },
    { id: 'related', label: t('Mark as related'), icon: <StatusIcon state={more.state}/>, disabled: work.busy, run: () => void work.relate('related', more) },
    { id: 'blocked', label: t('Mark as blocked by'), icon: <StatusIcon state={more.state}/>, disabled: work.busy, run: () => void work.relate('blocked_by', more) },
  ].filter(action => action.label.toLowerCase().includes(moreQuery.trim().toLowerCase())) : []
  return <Dialog open onOpenChange={value => { if (!value && more) { setMore(undefined); return } onOpenChange(value) }}>
    <DialogContent aria-describedby={undefined} className="command-dialog triage-command-dialog" overlayClassName="command-overlay" onOpenAutoFocus={event => event.preventDefault()} onEscapeKeyDown={event => { if (more) { event.preventDefault(); setMore(undefined) } }}>
      <DialogTitle className="sr-only">{t('Mark issue as duplicate of')}</DialogTitle>
      <Command shouldFilter={false} loop value={active} onValueChange={setActive}>
        <IssueChip issue={issue}/>
        {more ? <>
          <div className="command-input"><Command.Input autoFocus aria-label={t('More actions')} placeholder={`${more.identifier} · ${more.title}`} value={moreQuery} onValueChange={setMoreQuery}/></div>
          <Command.List>
            {moreActions.map(action => <Command.Item key={action.id} value={action.id} disabled={action.disabled} onSelect={action.run}><span className="command-item-icon">{action.icon}</span><span>{action.label}</span></Command.Item>)}
            <Command.Empty>{t('No results found.')}</Command.Empty>
          </Command.List>
        </> : <>
          <div className="command-input"><Command.Input autoFocus aria-label={t('Search for issue to mark as duplicate of…')} placeholder={t('Search for issue to mark as duplicate of…')} value={query} onValueChange={setQuery} onKeyDown={event => {
            if (event.key !== 'Enter' || !event.altKey) return
            const target = results.find(item => item.id === active)
            if (!target) return
            event.preventDefault()
            setMore(target)
          }}/></div>
          {!work.duplicate ? <p className="triage-command-notice" role="alert">{t('This team has no canceled status to move duplicates to. Add one in the team workflow settings.')}</p> : null}
          <Command.List>
            {results.map(item => <Command.Item key={item.id} value={item.id} disabled={work.busy || !work.duplicate} onSelect={() => pick(item)}>
              <span className="command-item-icon"><StatusIcon state={item.state}/></span>
              <span className="triage-command-identifier" data-i18n-ignore>{item.identifier}</span>
              <span className="command-option-label" data-i18n-ignore>{item.title}</span>
            </Command.Item>)}
            <Command.Empty>{t('No issues found')}</Command.Empty>
          </Command.List>
        </>}
        <CommandFooter moreActions={!more}/>
      </Command>
    </DialogContent>
  </Dialog>
}

function SnoozeDialog({ issue, work, onOpenChange }: { issue: Issue; work: TriageWork; onOpenChange: (open: boolean) => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [custom, setCustom] = useState(false)
  const [customValue, setCustomValue] = useState(() => localInputValue(snoozePresets()[1].until))
  const [now] = useState(() => new Date())
  const presets = useMemo(() => snoozePresets(now), [now])
  const parsed = useMemo(() => parseSnoozeInput(query, new Date()), [query])
  const text = query.trim().toLowerCase()
  const options = parsed
    ? [{ id: 'parsed', label: t('Snooze until'), until: parsed }]
    : presets.map(preset => ({ ...preset, label: t(preset.label) })).filter(preset => !text || preset.label.toLowerCase().includes(text))
  const snooze = (until: Date) => { if (!work.busy) void work.snooze(until) }
  return <Dialog open onOpenChange={value => { if (!value && custom) { setCustom(false); return } onOpenChange(value) }}>
    <DialogContent aria-describedby={undefined} className="command-dialog triage-command-dialog triage-snooze-dialog" overlayClassName="command-overlay" onOpenAutoFocus={event => event.preventDefault()} onEscapeKeyDown={event => { if (custom) { event.preventDefault(); setCustom(false) } }}>
      <DialogTitle className="sr-only">{t('Snooze issue until')}</DialogTitle>
      {custom ? <div className="triage-snooze-custom">
        <IssueChip issue={issue}/>
        <label><span>{t('Snooze until')}</span><DateTimeControl label={t('Custom snooze date and time')} mode="datetime" value={customValue} min={localInputValue(new Date()).slice(0, 10)} onChange={setCustomValue}/></label>
        <footer>
          <button type="button" onClick={() => setCustom(false)}>{t('Back')}</button>
          <button type="button" className="primary" disabled={work.busy || !(new Date(customValue).getTime() > Date.now())} onClick={() => snooze(new Date(customValue))}>{t('Snooze')}</button>
        </footer>
      </div> : <Command shouldFilter={false} loop>
        <IssueChip issue={issue}/>
        <div className="command-input"><Command.Input autoFocus aria-label={t('Snooze until')} placeholder={t('Try: 4 pm, 2 days, in 5 weeks…')} value={query} onValueChange={setQuery}/></div>
        <Command.List>
          {options.map(option => <Command.Item key={option.id} value={option.id} disabled={work.busy} onSelect={() => snooze(option.until)}>
            <span className="command-option-label">{option.label}</span>
            <span className="triage-snooze-date">{formatSnoozeDate(option.until)}</span>
          </Command.Item>)}
          {!parsed && (!text || t('Custom…').toLowerCase().includes(text) || 'custom'.includes(text)) ? <Command.Item value="custom" onSelect={() => setCustom(true)}><span className="command-option-label">{t('Custom…')}</span></Command.Item> : null}
          <Command.Empty>{t('No matching time. Try "4 pm" or "in 2 days".')}</Command.Empty>
        </Command.List>
        <CommandFooter/>
      </Command>}
    </DialogContent>
  </Dialog>
}

function localInputValue(date: Date) {
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}
