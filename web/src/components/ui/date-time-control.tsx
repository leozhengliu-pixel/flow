import * as Popover from '@radix-ui/react-popover'
import { useMemo, useState } from 'react'

import { SelectControl } from './select-control'
import './date-time-control.css'

type Mode = 'date' | 'datetime'

export function DateTimeControl({
  className = '',
  label,
  min,
  mode = 'date',
  onChange,
  value,
}: {
  className?: string
  label: string
  min?: string
  mode?: Mode
  onChange: (value: string) => void
  value: string
}) {
  const initial = parseValue(value)
  const [open, setOpen] = useState(false)
  const [view, setView] = useState(() => new Date(initial.year, initial.month - 1, 1))
  const [draftDate, setDraftDate] = useState(initial.date)
  const [draftTime, setDraftTime] = useState(initial.time)
  const days = useMemo(() => calendarDays(view), [view])
  const choose = (date: string) => {
    setDraftDate(date)
    if (mode === 'date') {
      onChange(date)
      setOpen(false)
    }
  }
  const apply = () => {
    onChange(`${draftDate}T${draftTime}`)
    setOpen(false)
  }
  return <Popover.Root open={open} onOpenChange={next => {
    setOpen(next)
    if (next) {
      const current = parseValue(value)
      setDraftDate(current.date)
      setDraftTime(current.time)
      setView(new Date(current.year, current.month - 1, 1))
    }
  }}>
    <Popover.Trigger asChild>
      <button aria-label={label} className={`date-time-control ${className}`.trim()} type="button">
        <span>{displayValue(value, mode)}</span><CalendarIcon/>
      </button>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Content align="start" className="date-time-popover" collisionPadding={8} sideOffset={4}>
        <header><button aria-label="Previous month" onClick={() => setView(new Date(view.getFullYear(), view.getMonth() - 1, 1))}><ChevronIcon direction="left"/></button><strong>{view.toLocaleDateString(undefined, { month: 'long', year: 'numeric' })}</strong><button aria-label="Next month" onClick={() => setView(new Date(view.getFullYear(), view.getMonth() + 1, 1))}><ChevronIcon direction="right"/></button></header>
        <div className="date-time-weekdays">{['S','M','T','W','T','F','S'].map((day,index)=><span key={`${day}-${index}`}>{day}</span>)}</div>
        <div className="date-time-grid">{days.map(day => {
          const iso = formatDate(day)
          const outside = day.getMonth() !== view.getMonth()
          const disabled = Boolean(min && iso < min.slice(0, 10))
          return <button aria-label={day.toLocaleDateString()} data-outside={outside || undefined} data-selected={iso === draftDate || undefined} disabled={disabled} key={iso} onClick={() => choose(iso)}>{day.getDate()}</button>
        })}</div>
        {mode === 'datetime' && <footer><TimeControl label="Time" value={draftTime} onChange={setDraftTime}/><button className="date-time-apply" disabled={!draftDate} onClick={apply}>Apply</button></footer>}
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

export function TimeControl({ className = '', label, onChange, value }: { className?: string; label: string; onChange: (value: string) => void; value: string }) {
  return <SelectControl className={`time-control ${className}`.trim()} label={label} value={normalizeTime(value)} onChange={onChange} options={timeOptions()}/>
}

function parseValue(value: string) {
  const match = /^(\d{4})-(\d{2})-(\d{2})(?:T(\d{2}):(\d{2}))?/.exec(value)
  const now = new Date()
  const year = Number(match?.[1] ?? now.getFullYear())
  const month = Number(match?.[2] ?? now.getMonth() + 1)
  const day = Number(match?.[3] ?? now.getDate())
  return { year, month, date: `${year}-${String(month).padStart(2,'0')}-${String(day).padStart(2,'0')}`, time: `${match?.[4] ?? '10'}:${match?.[5] ?? '00'}` }
}
function displayValue(value: string, mode: Mode) {
  const parsed = parseValue(value)
  const date = parsed.date.replaceAll('-', '/')
  return mode === 'datetime' ? `${date} ${parsed.time}` : date
}
function calendarDays(view: Date) {
  const first = new Date(view.getFullYear(), view.getMonth(), 1)
  const start = new Date(view.getFullYear(), view.getMonth(), 1 - first.getDay())
  return Array.from({ length: 42 }, (_, index) => new Date(start.getFullYear(), start.getMonth(), start.getDate() + index))
}
function formatDate(value: Date) { return `${value.getFullYear()}-${String(value.getMonth()+1).padStart(2,'0')}-${String(value.getDate()).padStart(2,'0')}` }
function normalizeTime(value: string) { return /^\d{2}:\d{2}$/.test(value) ? value : '10:00' }
function timeOptions() { return Array.from({ length: 48 }, (_, index) => { const hour = Math.floor(index / 2), minute = index % 2 ? 30 : 0, value = `${String(hour).padStart(2,'0')}:${String(minute).padStart(2,'0')}`; return { value, label: value } }) }

function CalendarIcon() { return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path fillRule="evenodd" d="M4.25 1a.75.75 0 0 1 .75.75V2h6v-.25a.75.75 0 0 1 1.5 0V2h.25A2.25 2.25 0 0 1 15 4.25v8.5A2.25 2.25 0 0 1 12.75 15h-9.5A2.25 2.25 0 0 1 1 12.75v-8.5A2.25 2.25 0 0 1 3.25 2h.25v-.25A.75.75 0 0 1 4.25 1M2.5 6v6.75c0 .414.336.75.75.75h9.5a.75.75 0 0 0 .75-.75V6zm.75-2.5a.75.75 0 0 0-.75.75v.25h11v-.25a.75.75 0 0 0-.75-.75z" clipRule="evenodd"/></svg> }
function ChevronIcon({ direction }: { direction: 'left'|'right' }) { return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path d={direction === 'left' ? 'M10.53 3.47a.75.75 0 0 1 0 1.06L7.06 8l3.47 3.47a.75.75 0 1 1-1.06 1.06l-4-4a.75.75 0 0 1 0-1.06l4-4a.75.75 0 0 1 1.06 0Z' : 'M5.47 3.47a.75.75 0 0 0 0 1.06L8.94 8l-3.47 3.47a.75.75 0 1 0 1.06 1.06l4-4a.75.75 0 0 0 0-1.06l-4-4a.75.75 0 0 0-1.06 0Z'}/></svg> }
