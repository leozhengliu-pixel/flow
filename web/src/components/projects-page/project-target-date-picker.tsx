import * as Popover from '@radix-ui/react-popover'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode, type Ref } from 'react'
import { useI18n, type AppLocale } from '@/i18n/i18n'
import styles from './project-row-menus.module.css'

export type DateMode = 'day' | 'month' | 'quarter' | 'half-year' | 'year'
export type DateResolution = 'halfYear' | 'month' | 'quarter' | 'year'
const MODES: { id: DateMode; label: string }[] = [{ id: 'day', label: 'Day' }, { id: 'month', label: 'Month' }, { id: 'quarter', label: 'Quarter' }, { id: 'half-year', label: 'Half-year' }, { id: 'year', label: 'Year' }]
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

export function ProjectTargetDatePicker({ ariaLabel, buttonClassName = '', children, compactPeriods = false, defaultMode = 'day', displayValue, onChange, resolution, triggerRole = 'button', value }: {
  ariaLabel?: string
  buttonClassName?: string
  children: ReactNode
  compactPeriods?: boolean
  defaultMode?: DateMode
  displayValue?: string
  onChange: (value: string, resolution?: DateResolution) => void
  resolution?: DateResolution
  triggerRole?: 'button' | 'combobox'
  value?: string
}) {
  return <ProjectDatePicker ariaLabel={ariaLabel} buttonClassName={buttonClassName} compactPeriods={compactPeriods} defaultMode={defaultMode} displayValue={displayValue} label="Target date" onChange={onChange} resolution={resolution} triggerRole={triggerRole} value={value}>{children}</ProjectDatePicker>
}

export function ProjectDatePicker({ ariaLabel, buttonClassName = '', children, compactCalendar = false, compactPeriods = false, contentClassName = '', defaultMode = 'day', displayValue: _displayValue, label, max, min, onChange, onOpenChange, portalled = true, resolution, side, align = 'center', triggerRef, triggerRole = 'button', value }: {
  ariaLabel?: string
  buttonClassName?: string
  children: ReactNode
  compactPeriods?: boolean
  compactCalendar?: boolean
  contentClassName?: string
  defaultMode?: DateMode
  displayValue?: string
  label: 'Start date' | 'Target date'
  max?: string
  min?: string
  onChange: (value: string, resolution?: DateResolution) => void
  onOpenChange?: (open: boolean) => void
  portalled?: boolean
  resolution?: DateResolution
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
  triggerRef?: Ref<HTMLButtonElement>
  triggerRole?: 'button' | 'combobox'
  value?: string
}) {
  const { locale, t } = useI18n()
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<DateMode>('day')
  const [query, setQuery] = useState('')
  const [cursor, setCursor] = useState(() => parseDate(value) ?? startOfDay(new Date()))
  const selected = parseDate(value)
  const minDate = parseDate(min)
  const maxDate = parseDate(max)
  useEffect(() => {
    if (!open) return
    const date = parseDate(value) ?? startOfDay(new Date())
    const nextMode = resolution ? resolutionToMode(resolution) : defaultMode
    setCursor(date)
    setMode(nextMode)
    setQuery(value ? formatForMode(date, nextMode) : '')
  }, [defaultMode, open, resolution, value])

  const choose = (date: Date, nextResolution?: DateResolution) => {
    onChange(isoDate(date), nextResolution)
    setOpen(false)
    onOpenChange?.(false)
  }
  const submit = async () => {
    if (!query.trim()) { onChange(''); setOpen(false); onOpenChange?.(false); return }
    const parsed = await parseNaturalTarget(query, mode, label)
    if (parsed && !isDateDisabled(parsed, minDate, maxDate)) choose(parsed, modeToResolution(mode))
  }
  const selectMode = (nextMode: DateMode) => {
    setMode(nextMode)
    setQuery(formatForMode(cursor, nextMode))
  }
  const onModeKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
    event.preventDefault()
    const current = MODES.findIndex(item => item.id === mode)
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? MODES.length - 1 : (current + (event.key === 'ArrowRight' ? 1 : -1) + MODES.length) % MODES.length
    selectMode(MODES[next].id)
    requestAnimationFrame(() => event.currentTarget.querySelectorAll<HTMLButtonElement>('[role=tab]')[next]?.focus())
  }

  const content = <Popover.Content align={align} className={`${styles.datePicker}${compactPeriods && mode !== 'day' ? ` ${styles.compactPeriods}` : ''}${compactCalendar ? ` ${styles.compactCalendar}` : ''}${contentClassName ? ` ${contentClassName}` : ''}`} collisionPadding={8} onClick={event => event.stopPropagation()} onCloseAutoFocus={event => event.preventDefault()} side={side ?? (portalled ? 'bottom' : 'right')} sideOffset={4}>
    {!compactCalendar && <label className={styles.dateLabel}>{t(label)}<input autoFocus aria-label={t('Try: May 2027, Q4, 2027/05/20')} onChange={event => setQuery(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); void submit() } }} placeholder={t('Try: May 2027, Q4, 2027/05/20')} value={query}/></label>}
    {!compactCalendar && <div aria-label={t('Date precision')} className={styles.dateModes} onKeyDown={onModeKeyDown} role="tablist">{MODES.map(item => <button aria-selected={mode === item.id} key={item.id} onClick={() => selectMode(item.id)} role="tab" tabIndex={mode === item.id ? 0 : -1} type="button">{t(item.label)}</button>)}</div>}
    {compactCalendar || mode === 'day' ? <DayPanel cursor={cursor} locale={locale} max={maxDate} min={minDate} onChangeCursor={setCursor} onChoose={date => choose(date)} selected={selected}/> : <PeriodPanel cursor={cursor} label={label} locale={locale} max={maxDate} min={minDate} mode={mode} onChoose={(date, nextResolution) => choose(date, nextResolution)} selected={selected}/>}
  </Popover.Content>

  return <Popover.Root open={open} onOpenChange={next => { setOpen(next); onOpenChange?.(next) }}>
    <Popover.Trigger asChild><button aria-expanded={open} aria-label={t(ariaLabel ?? `Change project ${label.toLowerCase()}`)} className={`lp-project-property-trigger ${buttonClassName}`} ref={triggerRef} role={triggerRole === 'combobox' ? 'combobox' : undefined} type="button">{children}</button></Popover.Trigger>
    {portalled ? <Popover.Portal>{content}</Popover.Portal> : content}
  </Popover.Root>
}

function DayPanel({ cursor, locale, max, min, onChangeCursor, onChoose, selected }: { cursor: Date; locale: AppLocale; max?: Date; min?: Date; onChangeCursor: (date: Date) => void; onChoose: (date: Date) => void; selected?: Date }) {
  const days = useMemo(() => calendarDays(cursor), [cursor])
  const monthLabel = new Intl.DateTimeFormat(locale, { month: 'long', year: 'numeric' }).format(cursor)
  const weekdays = locale === 'zh-CN' ? ['一', '二', '三', '四', '五', '六', '日'] : ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su']
  return <div className={styles.dayPanel}>
    <header><strong>{monthLabel}</strong><span><button aria-label={locale === 'zh-CN' ? '上个月' : 'Previous month'} onClick={() => onChangeCursor(new Date(cursor.getFullYear(), cursor.getMonth() - 1, 1))} type="button"><ChevronLeft size={14}/></button><button aria-label={locale === 'zh-CN' ? '下个月' : 'Next month'} onClick={() => onChangeCursor(new Date(cursor.getFullYear(), cursor.getMonth() + 1, 1))} type="button"><ChevronRight size={14}/></button></span></header>
    <div className={styles.weekdays}>{weekdays.map(day => <span key={day}>{day}</span>)}</div>
    <div aria-label={monthLabel} className={styles.days} role="grid">{days.map(day => <button aria-label={new Intl.DateTimeFormat(locale, { dateStyle: 'full' }).format(day)} aria-selected={sameDay(day, selected)} className={sameDay(day, selected) ? styles.selectedDate : ''} data-outside={day.getMonth() !== cursor.getMonth()} disabled={isDateDisabled(day, min, max)} key={isoDate(day)} onClick={() => onChoose(day)} role="gridcell" type="button">{day.getDate()}</button>)}</div>
  </div>
}

function PeriodPanel({ cursor: _cursor, label, locale, max, min, mode, onChoose, selected }: { cursor: Date; label: 'Start date' | 'Target date'; locale: AppLocale; max?: Date; min?: Date; mode: Exclude<DateMode, 'day'>; onChoose: (date: Date, resolution: DateResolution) => void; selected?: Date }) {
  const scrollerRef = useRef<HTMLDivElement>(null)
  const currentYear = new Date().getFullYear()
  const years = Array.from({ length: 10 }, (_, index) => currentYear - 4 + index)
  useEffect(() => {
    const scroller = scrollerRef.current
    const active = scroller?.querySelector<HTMLElement>('[data-selected="true"]')
    if (scroller && active) scroller.scrollTop = Math.max(0, active.offsetTop - scroller.clientHeight / 2)
  }, [mode, selected])
  return <div className={styles.periodScroller} ref={scrollerRef}>{years.map(year => <section key={year}><h3>{year}</h3><div className={styles.periodGrid} data-mode={mode}>{periodsFor(mode, locale).map((periodLabel, index) => {
    const date = dateForPeriod(year, mode, index, label)
    const isSelected = periodSelected(selected, year, mode, index)
    return <button className={isSelected ? styles.selectedDate : ''} data-selected={isSelected} disabled={isDateDisabled(date, min, max)} key={periodLabel} onClick={() => onChoose(date, modeToResolution(mode)!)} type="button">{periodLabel}</button>
  })}</div></section>)}</div>
}

function periodsFor(mode: Exclude<DateMode, 'day'>, locale: AppLocale) {
  if (mode === 'month') return locale === 'zh-CN' ? MONTHS.map((_, index) => `${index + 1}月`) : MONTHS
  if (mode === 'quarter') return ['Q1', 'Q2', 'Q3', 'Q4']
  if (mode === 'half-year') return ['H1', 'H2']
  return [locale === 'zh-CN' ? '年末' : 'Year end']
}
function dateForPeriod(year: number, mode: Exclude<DateMode, 'day'>, index: number, label: 'Start date' | 'Target date') {
  const start = label === 'Start date'
  if (mode === 'month') return start ? new Date(year, index, 1) : new Date(year, index + 1, 0)
  if (mode === 'quarter') return start ? new Date(year, index * 3, 1) : new Date(year, (index + 1) * 3, 0)
  if (mode === 'half-year') return start ? new Date(year, index * 6, 1) : new Date(year, (index + 1) * 6, 0)
  return start ? new Date(year, 0, 1) : new Date(year, 11, 31)
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
async function parseNaturalTarget(input: string, mode: DateMode, label: 'Start date' | 'Target date') {
  const value = input.trim()
  if (!value) return undefined
  const slash = value.match(/^(\d{4})[/-](\d{1,2})[/-](\d{1,2})$/)
  if (slash) return new Date(Number(slash[1]), Number(slash[2]) - 1, Number(slash[3]))
  const quarter = value.match(/^Q([1-4])(?:\s+(\d{4}))?$/i)
  if (quarter) return dateForPeriod(Number(quarter[2] ?? new Date().getFullYear()), 'quarter', Number(quarter[1]) - 1, label)
  const half = value.match(/^H([12])(?:\s+(\d{4}))?$/i)
  if (half) return dateForPeriod(Number(half[2] ?? new Date().getFullYear()), 'half-year', Number(half[1]) - 1, label)
  if (/^\d{4}$/.test(value)) return dateForPeriod(Number(value), 'year', 0, label)
  const parsed = new Date(value)
  let natural = parsed
  if (Number.isNaN(natural.getTime())) {
    const { parseDate } = await import('chrono-node')
    natural = parseDate(value, new Date(), { forwardDate: true }) ?? new Date(Number.NaN)
  }
  if (Number.isNaN(natural.getTime())) return undefined
  if (mode !== 'day') return dateForPeriod(natural.getFullYear(), mode, mode === 'month' ? natural.getMonth() : mode === 'quarter' ? Math.floor(natural.getMonth() / 3) : mode === 'half-year' ? Math.floor(natural.getMonth() / 6) : 0, label)
  return startOfDay(natural)
}
function formatForMode(date: Date, mode: DateMode) {
  if (mode === 'day') return `${date.getFullYear()}/${String(date.getMonth() + 1).padStart(2, '0')}/${String(date.getDate()).padStart(2, '0')}`
  if (mode === 'month') return `${MONTHS[date.getMonth()]} ${date.getFullYear()}`
  if (mode === 'quarter') return `Q${Math.floor(date.getMonth() / 3) + 1} ${date.getFullYear()}`
  if (mode === 'half-year') return `H${Math.floor(date.getMonth() / 6) + 1} ${date.getFullYear()}`
  return String(date.getFullYear())
}
function parseDate(value?: string) { if (!value) return undefined; const date = new Date(`${value}T00:00:00`); return Number.isNaN(date.getTime()) ? undefined : date }
function isDateDisabled(date: Date, min?: Date, max?: Date) { return Boolean((min && date < min) || (max && date > max)) }
function modeToResolution(mode: DateMode): DateResolution | undefined { return mode === 'day' ? undefined : mode === 'half-year' ? 'halfYear' : mode }
function resolutionToMode(resolution?: DateResolution): DateMode { return resolution === 'halfYear' ? 'half-year' : resolution ?? 'day' }
function isoDate(date: Date) { return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
function startOfDay(date: Date) { return new Date(date.getFullYear(), date.getMonth(), date.getDate()) }
function sameDay(left?: Date, right?: Date) { return Boolean(left && right && isoDate(left) === isoDate(right)) }
