import * as Popover from '@radix-ui/react-popover'
import { AlarmClock } from 'lucide-react'
import { useState } from 'react'

import { PhoneMenu } from '@/components/ui/phone-menu'
import { FlowTooltip } from '@/components/ui/tooltip'
import { useCurrentTimeMs } from '@/hooks/use-current-time'
import { useIssueRowActions } from '@/components/my-issues/use-issue-row-actions'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import { useI18n } from '@/i18n/i18n'
import { parseNaturalDateInput } from '@/lib/natural-date'
import { IssueSLAIndicator, SlaTooltipContent } from './issue-sla-indicator'
import type { IssueSLA, IssueUpdateInput } from '@/types/flow'
import './issue-sla-picker.css'

const PRESETS = [
  { label: '12 hours', minutes: 720 }, { label: '24 hours', minutes: 1440 },
  { label: '48 hours', minutes: 2880 }, { label: '1 week', minutes: 10080 },
  { label: '2 weeks', minutes: 20160 }, { label: '4 weeks', minutes: 40320 },
]

export function IssueSLAPicker({ sla, disabled = false, compact = false, onChange }: { sla?: IssueSLA; disabled?: boolean; compact?: boolean; onChange: (input: IssueUpdateInput) => Promise<unknown> }) {
  const { t } = useI18n()
  const phone = usePhoneLayout()
  const [open, setOpen] = useState(false)
  const now = useCurrentTimeMs()
  const label = t(sla ? 'Change SLA…' : 'Set SLA…')
  const button = <button type="button" className="issue-sla-trigger" disabled={disabled} aria-label={label} aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(value => !value)}>
    {sla ? <IssueSLAIndicator sla={sla} compact={compact} focusable={false} tooltip={false}/> : <><AlarmClock size={14}/><span>{t('Set SLA…')}</span></>}
  </button>
  const trigger = sla ? <FlowTooltip label={<SlaTooltipContent sla={sla} now={now}/>} disabled={open} side="top" align="start">{button}</FlowTooltip> : button
  const choose = async (deadline: string) => {
    await onChange({ slaBreachesAt: deadline, slaType: 'all' })
    setOpen(false)
  }
  const content = <SlaCommand hasSla={Boolean(sla)} onChoose={choose} onClose={() => setOpen(false)}/>
  if (phone) return <>{trigger}<PhoneMenu open={open} onOpenChange={setOpen} label={label}>{content}</PhoneMenu></>
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Anchor asChild><span className="issue-sla-anchor">{trigger}</span></Popover.Anchor>
    <Popover.Portal><Popover.Content className="issue-sla-popover" data-flow-motion="floating" side="bottom" align="start" sideOffset={4} collisionPadding={8} onCloseAutoFocus={event => event.preventDefault()}>{content}</Popover.Content></Popover.Portal>
  </Popover.Root>
}

/** Lists and boards share the same issue mutation context, including paged records. */
export function IssueRowSLA({ sla }: { sla: IssueSLA }) {
  const actions = useIssueRowActions()
  if (!actions) return <IssueSLAIndicator compact sla={sla}/>
  const issue = actions.data.issues.find(issue => issue.id === sla.issueId)
  return <IssueSLAPicker compact sla={sla} disabled={Boolean(issue?.archivedAt || sla.completedAt || sla.status === 'completed')} onChange={input => actions.onUpdateIssue(sla.issueId, input)}/>
}

function SlaCommand({ hasSla, onChoose, onClose }: { hasSla: boolean; onChoose: (deadline: string) => Promise<void>; onClose: () => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const now = useCurrentTimeMs(1000)
  const choose = async (deadline: string) => {
    if (busy) return
    setBusy(true); setError('')
    try { await onChoose(deadline) }
    catch (cause) { setError(cause instanceof Error ? cause.message : t('Could not update SLA.')) }
    finally { setBusy(false) }
  }
  const date = query.trim() ? parseNaturalDateInput(query, new Date(now)) : undefined
  const { inputRef, ...command } = usePropertyCommand({
    open: true,
    options: [
      ...(date ? [{ id: 'typed', label: date.toLocaleString(), minutes: 0, searchOnly: true }] : []),
      ...(hasSla && !query ? [{ id: 'remove', label: t('Remove SLA'), minutes: 0 }] : []),
      ...PRESETS.map(option => ({ ...option, id: String(option.minutes), label: t(option.label) })),
    ],
    pinnedIds: date ? ['typed'] : [],
    selectedIds: [],
    closeOnSelect: false,
    onOpenChange: next => { if (!next) onClose() },
    onSelect: option => {
      void choose(option.id === 'remove' ? '' : option.id === 'typed' && date ? date.toISOString() : new Date(now + option.minutes * 60_000).toISOString())
    },
  })
  return <div className="issue-sla-command" role="dialog" aria-label={t('Set SLA')}>
    <input ref={inputRef} aria-label={t('Try: 24h, 7 days, Feb 9')} placeholder={t(hasSla ? 'Change SLA…' : 'Try: 24h, 7 days, Feb 9')} value={command.query} onChange={event => { setQuery(event.target.value); command.onQueryChange(event.target.value) }} onKeyDown={command.onKeyDown}/>
    {error && <p role="alert">{error}</p>}
    <div role="listbox" aria-label={t('SLA')}>
      {command.filteredOptions.map(option => <button key={option.id} role="option" aria-selected={command.activeId === option.id} disabled={busy} onMouseMove={() => command.setActiveId(option.id)} onClick={() => command.choose(option)}>{option.id === 'typed' ? date?.toLocaleString() : option.label}</button>)}
      {!command.filteredOptions.length && <p>{t('No results found.')}</p>}
    </div>
  </div>
}
