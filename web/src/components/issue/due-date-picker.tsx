import * as Popover from '@radix-ui/react-popover'
import { CalendarRange, X } from 'lucide-react'
import { useMemo, useState, type ReactNode, type SyntheticEvent } from 'react'
import { CalendarIcon } from '@/components/issue/issue-icons'
import { DateTimeControl } from '@/components/ui/date-time-control'
import './due-date-control.css'

interface DueDatePickerProps {
  value?: string
  onChange: (value: string) => void | Promise<void>
  trigger?: ReactNode
  triggerClassName?: string
  ariaLabel?: string
}

export function DueDatePicker({ value, onChange, trigger, triggerClassName = 'due-date-trigger', ariaLabel }: DueDatePickerProps) {
  const [open, setOpen] = useState(false)

  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild>
      <button className={triggerClassName} type="button" aria-label={ariaLabel ?? `Change due date. Current due date is ${value ? formatDate(value) : 'not set'}`}>
        {trigger ?? <><CalendarIcon size={14}/><span>{value ? formatDate(value) : 'Set due date'}</span></>}
      </button>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Content className="due-date-popover" side="bottom" align="start" sideOffset={4} onCloseAutoFocus={event => event.preventDefault()}>
        <DueDateCommand value={value} onSelect={async next => { await onChange(next); setOpen(false) }}/>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

export function DueDateCommand({ value, onSelect, className = '' }: { value?: string; onSelect: (value: string) => Promise<void>; className?: string }) {
  const [query, setQuery] = useState('')
  const [custom, setCustom] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const dates = useMemo(() => ({
    tomorrow: isoDate(addDays(new Date(), 1)),
    endOfWeek: isoDate(endOfWorkWeek(new Date())),
    week: isoDate(addDays(new Date(), 7)),
  }), [])
  const choose = async (next: string) => {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await onSelect(next)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not update due date.')
    } finally {
      setBusy(false)
    }
  }
  const submitQuery = () => {
    const parsed = parseNaturalDate(query)
    if (parsed) void choose(parsed)
  }

  return <div className={`due-date-command ${className}`} role="dialog" aria-label="Due date">
    <input autoFocus className="due-date-search" aria-label="Try: 24h, 7 days, Feb 9" placeholder="Try: 24h, 7 days, Feb 9" value={query} onChange={event => setQuery(event.target.value)} onKeyDown={event => { if (event.key === 'Enter' && query.trim()) { event.preventDefault(); submitQuery() } }}/>
    {error ? <p className="due-date-error" role="alert">{error}</p> : null}
    {custom ? <div className="due-date-custom">
      <label><CalendarRange size={15}/><DateTimeControl label="Custom due date" value={value ?? isoDate(new Date())} onChange={next => void choose(next)}/></label>
      <button type="button" onPointerDown={stopCommandEvent} onClick={event => { stopCommandEvent(event); setCustom(false) }}>Back</button>
    </div> : <div className="due-date-options" role="listbox" aria-label="Due date options">
      <button type="button" role="option" aria-selected="false" onPointerDown={stopCommandEvent} onClick={event => { stopCommandEvent(event); setCustom(true) }}><span>Custom…</span></button>
      <button type="button" role="option" aria-selected="false" disabled={busy} onPointerDown={stopCommandEvent} onClick={event => { stopCommandEvent(event); void choose(dates.tomorrow) }}><span>Tomorrow</span><small>{formatShortDate(dates.tomorrow)}</small></button>
      <button type="button" role="option" aria-selected="false" disabled={busy} onPointerDown={stopCommandEvent} onClick={event => { stopCommandEvent(event); void choose(dates.endOfWeek) }}><span>End of this week</span><small>{formatShortDate(dates.endOfWeek)}</small></button>
      <button type="button" role="option" aria-selected="false" disabled={busy} onPointerDown={stopCommandEvent} onClick={event => { stopCommandEvent(event); void choose(dates.week) }}><span>In one week</span><small>{formatShortDate(dates.week)}</small></button>
      {value && <button className="due-date-remove" type="button" role="option" aria-selected="false" disabled={busy} onPointerDown={stopCommandEvent} onClick={event => { stopCommandEvent(event); void choose('') }}><X size={14}/><span>Remove due date</span></button>}
    </div>}
  </div>
}

function addDays(date: Date, days: number) { const next = new Date(date); next.setDate(next.getDate() + days); return next }
function endOfWorkWeek(date: Date) { return addDays(date, (5 - date.getDay() + 7) % 7) }
function isoDate(date: Date) { const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000); return local.toISOString().slice(0, 10) }
function parseNaturalDate(input: string) {
  const value = input.trim().toLowerCase()
  if (!value) return undefined
  if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return value
  if (value === 'tomorrow' || value === '24h') return isoDate(addDays(new Date(), 1))
  const days = value.match(/^(?:in\s+)?(\d+)\s*d(?:ays?)?$/)
  if (days) return isoDate(addDays(new Date(), Number(days[1])))
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? undefined : isoDate(parsed)
}
function formatDate(value: string) { return new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric', year: 'numeric' }).format(new Date(`${value}T12:00:00`)) }
function formatShortDate(value: string) {
  const date = new Date(`${value}T12:00:00`)
  const weekday = new Intl.DateTimeFormat('en-GB', { weekday: 'short' }).format(date)
  const dayAndMonth = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short' }).format(date)
  return `${weekday}, ${dayAndMonth}`
}
function stopCommandEvent(event: SyntheticEvent) { event.stopPropagation() }
