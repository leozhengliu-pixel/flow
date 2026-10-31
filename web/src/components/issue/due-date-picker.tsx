import * as Popover from '@radix-ui/react-popover'
import { X } from 'lucide-react'
import { useState, type ReactNode, type SyntheticEvent } from 'react'
import { CalendarIcon } from '@/components/issue/issue-icons'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { requestDueDate } from './due-date-dialog-service'
import { addDays, endOfWorkWeek, formatDate, formatShortDate, isoDate, parseNaturalDate } from './due-date-utils'
import './due-date-control.css'
import { useDesktopLayout } from '@/hooks/use-desktop-layout'
import { useI18n } from '@/i18n/i18n'

interface DueDatePickerProps {
  value?: string
  onChange: (value: string) => void | Promise<void>
  trigger?: ReactNode
  triggerClassName?: string
  ariaLabel?: string
  /** False hides "Remove due date" (a recurring issue's due date anchors its schedule). */
  allowRemove?: boolean
}

export function DueDatePicker({ value, onChange, trigger, triggerClassName = 'due-date-trigger', ariaLabel, allowRemove = true }: DueDatePickerProps) {
  const [open, setOpen] = useState(false)

  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild>
      <button className={triggerClassName} type="button" aria-label={ariaLabel ?? `Change due date. Current due date is ${value ? formatDate(value) : 'not set'}`}>
        {trigger ?? <><CalendarIcon size={14}/><span>{value ? formatDate(value) : 'Set due date'}</span></>}
      </button>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating" className="due-date-popover" side="bottom" align="start" sideOffset={4} onCloseAutoFocus={event => event.preventDefault()}>
        <DueDateCommand value={value} allowRemove={allowRemove} onSelect={async next => { await onChange(next); setOpen(false) }}/>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function DateIcon() {
  const desktop = useDesktopLayout()
  return desktop ? <CalendarIcon size={16}/> : <IssueActionGlyph label="Due date" fallback={<FlowGlyph name="dateAdd"/>}/>
}

export function DueDateCommand({ value, onSelect, className = '', autoFocus = true, extraOptions = [], allowRemove = true }: { value?: string; onSelect: (value: string) => Promise<void>; className?: string; autoFocus?: boolean; extraOptions?: {label: string; value: string}[]; allowRemove?: boolean }) {
  const { t } = useI18n()
  const desktop = useDesktopLayout()
  const [query, setQuery] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [dates] = useState(() => ({
    tomorrow: isoDate(addDays(new Date(), 1)),
    endOfWeek: isoDate(endOfWorkWeek(new Date())),
    week: isoDate(addDays(new Date(), 7)),
    today: isoDate(new Date()),
  }))
  const choose = async (next: string) => {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await onSelect(next)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t('Could not update due date.'))
    } finally {
      setBusy(false)
    }
  }
  const submitQuery = () => {
    const parsed = parseNaturalDate(query)
    if (parsed) void choose(parsed)
  }
  const openCustom = () => { void requestDueDate(value).then(next => { if (next) void choose(next) }) }
  // The reference app drops "End of this week" when it is the same day as tomorrow (or already past).
  const showEndOfWeek = desktop || dates.endOfWeek > dates.tomorrow && dates.endOfWeek !== dates.week

  return <div className={`due-date-command ${className}`} role="dialog" aria-label={t('Due date')}>
    <div className="due-date-head">
      <input autoFocus={autoFocus} className="due-date-search" aria-label={t('Try: 24h, 7 days, Feb 9')} placeholder={t('Try: 24h, 7 days, Feb 9')} value={query} onChange={event => setQuery(event.target.value)} onKeyDown={event => { if (event.key === 'Enter' && query.trim()) { event.preventDefault(); submitQuery() } }}/>
      <span className="due-date-shortcut" aria-hidden="true"><kbd>⇧</kbd><kbd>D</kbd></span>
    </div>
    {error ? <p className="due-date-error" role="alert">{error}</p> : null}
    <div className="due-date-options" role="listbox" aria-label={t('Due date options')}>
      {value && allowRemove && <button className="due-date-remove" type="button" role="option" aria-selected="false" disabled={busy} onPointerDown={stopCommandEvent} onClick={event => { stopCommandEvent(event); void choose('') }}><X size={16}/><span>{t('Remove due date')}</span><small>{formatShortDate(value)}</small></button>}
      <button type="button" role="option" aria-selected="false" onPointerDown={stopCommandEvent} onClick={event => { stopCommandEvent(event); openCustom() }}><DateIcon/><span>{t('Custom…')}</span></button>
      <button type="button" role="option" aria-selected="false" disabled={busy} onPointerDown={stopCommandEvent} onClick={event => { stopCommandEvent(event); void choose(dates.tomorrow) }}><DateIcon/><span>{t('Tomorrow')}</span><small>{formatShortDate(dates.tomorrow)}</small></button>
      {showEndOfWeek && <button type="button" role="option" aria-selected="false" disabled={busy} onPointerDown={stopCommandEvent} onClick={event => { stopCommandEvent(event); void choose(dates.endOfWeek) }}><DateIcon/><span>{t('End of this week')}</span><small>{formatShortDate(dates.endOfWeek)}</small></button>}
      <button type="button" role="option" aria-selected="false" disabled={busy} onPointerDown={stopCommandEvent} onClick={event => { stopCommandEvent(event); void choose(dates.week) }}><DateIcon/><span>{t('In one week')}</span><small>{formatShortDate(dates.week)}</small></button>
      {extraOptions.map(option => <button key={option.label} type="button" role="option" aria-selected="false" disabled={busy} onPointerDown={stopCommandEvent} onClick={event => { stopCommandEvent(event); void choose(option.value) }}><DateIcon/><span>{option.label}</span><small>{formatShortDate(option.value)}</small></button>)}
    </div>
  </div>
}

function stopCommandEvent(event: SyntheticEvent) { event.stopPropagation() }
