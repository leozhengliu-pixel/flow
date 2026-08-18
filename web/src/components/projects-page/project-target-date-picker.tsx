import * as Popover from '@radix-ui/react-popover'
import { ChevronLeft, ChevronRight, CircleDot, X } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ReactNode, type Ref } from 'react'
import styles from './project-row-menus.module.css'

type DateMode = 'day' | 'month' | 'quarter' | 'half-year' | 'year'
const MODES: { id: DateMode; label: string }[] = [{ id: 'day', label: 'Day' }, { id: 'month', label: 'Month' }, { id: 'quarter', label: 'Quarter' }, { id: 'half-year', label: 'Half-year' }, { id: 'year', label: 'Year' }]
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

export function ProjectTargetDatePicker({ buttonClassName = '', children, displayValue, onChange, value }: {
  buttonClassName?: string
  children: ReactNode
  displayValue?: string
  onChange: (value: string) => void
  value?: string
}) {
  return <ProjectDatePicker buttonClassName={buttonClassName} displayValue={displayValue} label="Target date" onChange={onChange} value={value}>{children}</ProjectDatePicker>
}

export function ProjectDatePicker({ buttonClassName = '', children, contentClassName = '', displayValue, label, onChange, portalled = true, side, align = 'center', triggerRef, value }: {
  buttonClassName?: string
  children: ReactNode
  contentClassName?: string
  displayValue?: string
  label: 'Start date' | 'Target date'
  onChange: (value: string) => void
  portalled?: boolean
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
  triggerRef?: Ref<HTMLButtonElement>
  value?: string
}) {
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<DateMode>('day')
  const [query, setQuery] = useState('')
  const [cursor, setCursor] = useState(() => parseDate(value) ?? startOfDay(new Date()))
  const selected = parseDate(value)
  useEffect(() => {
    if (!open) return
    const date = parseDate(value) ?? startOfDay(new Date())
    setCursor(date)
    setMode('day')
    setQuery(value ? formatForMode(date, 'day') : '')
  }, [open, value])

  const choose = (date: Date) => {
    onChange(isoDate(date))
    setOpen(false)
  }
  const submit = () => {
    const parsed = parseNaturalTarget(query, mode)
    if (parsed) choose(parsed)
  }

  const content = <Popover.Content align={align} className={`${styles.datePicker}${contentClassName ? ` ${contentClassName}` : ''}`} collisionPadding={8} onClick={event => event.stopPropagation()} onCloseAutoFocus={event => event.preventDefault()} side={side ?? (portalled ? 'bottom' : 'right')} sideOffset={4}>
    <label className={styles.dateLabel}>{label}<input autoFocus aria-label="Try: May 2027, Q4, 2027/05/20" onChange={event => setQuery(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); submit() } }} placeholder="Try: May 2027, Q4, 2027/05/20" value={query}/></label>
    <div aria-label="Date precision" className={styles.dateModes} role="tablist">{MODES.map(item => <button aria-selected={mode === item.id} key={item.id} onClick={() => { setMode(item.id); setQuery(formatForMode(cursor, item.id)) }} role="tab" type="button">{item.label}</button>)}</div>
    {mode === 'day' ? <DayPanel cursor={cursor} onChangeCursor={setCursor} onChoose={choose} selected={selected}/> : <PeriodPanel cursor={cursor} mode={mode} onChoose={choose} selected={selected}/>} 
    {value && <button className={styles.removeDate} onClick={() => { onChange(''); setOpen(false) }} type="button"><X size={13}/>Remove {label.toLowerCase()} <span>{displayValue}</span></button>}
  </Popover.Content>

  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild><button aria-expanded={open} aria-label={`Change project ${label.toLowerCase()}`} className={`lp-project-property-trigger ${buttonClassName}`} ref={triggerRef} type="button">{children}</button></Popover.Trigger>
    {portalled ? <Popover.Portal>{content}</Popover.Portal> : content}
  </Popover.Root>
}

function DayPanel({ cursor, onChangeCursor, onChoose, selected }: { cursor: Date; onChangeCursor: (date: Date) => void; onChoose: (date: Date) => void; selected?: Date }) {
  const days = useMemo(() => calendarDays(cursor), [cursor])
  const today = startOfDay(new Date())
  return <div className={styles.dayPanel}>
    <header><strong>{new Intl.DateTimeFormat('en', { month: 'long', year: 'numeric' }).format(cursor)}</strong><span><button aria-label="Jump to today" onClick={() => onChangeCursor(today)} type="button"><CircleDot size={13}/></button><button aria-label="Previous month" onClick={() => onChangeCursor(new Date(cursor.getFullYear(), cursor.getMonth() - 1, 1))} type="button"><ChevronLeft size={14}/></button><button aria-label="Next month" onClick={() => onChangeCursor(new Date(cursor.getFullYear(), cursor.getMonth() + 1, 1))} type="button"><ChevronRight size={14}/></button></span></header>
    <div className={styles.weekdays}>{['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'].map(day => <span key={day}>{day}</span>)}</div>
    <div aria-label={new Intl.DateTimeFormat('en', { month: 'long', year: 'numeric' }).format(cursor)} className={styles.days} role="grid">{days.map(day => <button aria-label={day.toDateString()} className={sameDay(day, selected) ? styles.selectedDate : ''} data-outside={day.getMonth() !== cursor.getMonth()} key={isoDate(day)} onClick={() => onChoose(day)} role="gridcell" type="button">{day.getDate()}</button>)}</div>
  </div>
}

function PeriodPanel({ cursor: _cursor, mode, onChoose, selected }: { cursor: Date; mode: Exclude<DateMode, 'day'>; onChoose: (date: Date) => void; selected?: Date }) {
  const scrollerRef = useRef<HTMLDivElement>(null)
  const currentYear = new Date().getFullYear()
  const years = Array.from({ length: 7 }, (_, index) => currentYear - 1 + index)
  useEffect(() => {
    const scroller = scrollerRef.current
    const active = scroller?.querySelector<HTMLElement>('[data-selected="true"]')
    if (scroller && active) scroller.scrollTop = Math.max(0, active.offsetTop - scroller.clientHeight / 2)
  }, [mode, selected])
  return <div className={styles.periodScroller} ref={scrollerRef}>{years.map(year => <section key={year}><h3>{year}</h3><div className={styles.periodGrid} data-mode={mode}>{periodsFor(mode).map((label, index) => {
    const date = endOfPeriod(year, mode, index)
    const isSelected = periodSelected(selected, year, mode, index)
    return <button className={isSelected ? styles.selectedDate : ''} data-selected={isSelected} key={label} onClick={() => onChoose(date)} type="button">{label}</button>
  })}</div></section>)}</div>
}

function periodsFor(mode: Exclude<DateMode, 'day'>) {
  if (mode === 'month') return MONTHS
  if (mode === 'quarter') return ['Q1', 'Q2', 'Q3', 'Q4']
  if (mode === 'half-year') return ['H1', 'H2']
  return ['Year end']
}
function endOfPeriod(year: number, mode: Exclude<DateMode, 'day'>, index: number) {
  if (mode === 'month') return new Date(year, index + 1, 0)
  if (mode === 'quarter') return new Date(year, (index + 1) * 3, 0)
  if (mode === 'half-year') return new Date(year, (index + 1) * 6, 0)
  return new Date(year, 11, 31)
}
function periodSelected(date: Date | undefined, year: number, mode: Exclude<DateMode, 'day'>, index: number) {
  if (!date || date.getFullYear() !== year) return false
  if (mode === 'month') return date.getMonth() === index
  if (mode === 'quarter') return Math.floor(date.getMonth() / 3) === index
  if (mode === 'half-year') return Math.floor(date.getMonth() / 6) === index
  return true
}
function calendarDays(cursor: Date) {
  const first = new Date(cursor.getFullYear(), cursor.getMonth(), 1)
  const start = new Date(first)
  start.setDate(first.getDate() - ((first.getDay() + 6) % 7))
  return Array.from({ length: 42 }, (_, index) => new Date(start.getFullYear(), start.getMonth(), start.getDate() + index))
}
function parseNaturalTarget(input: string, mode: DateMode) {
  const value = input.trim()
  if (!value) return undefined
  const slash = value.match(/^(\d{4})[/-](\d{1,2})[/-](\d{1,2})$/)
  if (slash) return new Date(Number(slash[1]), Number(slash[2]) - 1, Number(slash[3]))
  const quarter = value.match(/^Q([1-4])(?:\s+(\d{4}))?$/i)
  if (quarter) return endOfPeriod(Number(quarter[2] ?? new Date().getFullYear()), 'quarter', Number(quarter[1]) - 1)
  const half = value.match(/^H([12])(?:\s+(\d{4}))?$/i)
  if (half) return endOfPeriod(Number(half[2] ?? new Date().getFullYear()), 'half-year', Number(half[1]) - 1)
  if (/^\d{4}$/.test(value)) return endOfPeriod(Number(value), 'year', 0)
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return undefined
  return mode === 'month' ? new Date(parsed.getFullYear(), parsed.getMonth() + 1, 0) : startOfDay(parsed)
}
function formatForMode(date: Date, mode: DateMode) {
  if (mode === 'day') return `${date.getFullYear()}/${String(date.getMonth() + 1).padStart(2, '0')}/${String(date.getDate()).padStart(2, '0')}`
  if (mode === 'month') return `${MONTHS[date.getMonth()]} ${date.getFullYear()}`
  if (mode === 'quarter') return `Q${Math.floor(date.getMonth() / 3) + 1} ${date.getFullYear()}`
  if (mode === 'half-year') return `H${Math.floor(date.getMonth() / 6) + 1} ${date.getFullYear()}`
  return String(date.getFullYear())
}
function parseDate(value?: string) { if (!value) return undefined; const date = new Date(`${value}T00:00:00`); return Number.isNaN(date.getTime()) ? undefined : date }
function isoDate(date: Date) { return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
function startOfDay(date: Date) { return new Date(date.getFullYear(), date.getMonth(), date.getDate()) }
function sameDay(left?: Date, right?: Date) { return Boolean(left && right && isoDate(left) === isoDate(right)) }
