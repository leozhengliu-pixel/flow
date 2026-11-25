import { useCallback, useMemo, useState, type KeyboardEvent } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { ChevronLeft, ChevronRight, CornerUpRight, X } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import { firstWeekday } from '@/lib/runtime-preferences'
import { datePeriod, datePeriodValue, localDay, parseDateFilterValue, type DateFilterKind, type DateFilterOperator, type DatePeriodUnit } from './issue-date-filter'
import { dayString, MONTH_NAMES, MONTH_SHORT, parseDateFilterInput, targetText, type DateFilterTarget } from './date-filter-input'
import { useCurrentTimeMs } from '@/hooks/use-current-time'
import styles from './date-filter-dialog.module.css'

type Mode = 'day' | DatePeriodUnit

const MODES: { mode: Mode; label: string }[] = [{ mode: 'day', label: 'Day' }, { mode: 'month', label: 'Month' }, { mode: 'quarter', label: 'Quarter' }, { mode: 'half', label: 'Half-year' }, { mode: 'year', label: 'Year' }]
const DAY_OPERATORS: DateFilterOperator[] = ['on', 'before', 'after']
const WEEKDAYS = ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa']
const WEEKDAYS_ZH = ['日', '一', '二', '三', '四', '五', '六']
/** Years the reference app lists: period grids from four years back, the year list from six, both to five years ahead (earlier when the chosen year is older). */
const GRID_YEARS_BACK = 4, YEAR_LIST_BACK = 6, YEARS_AHEAD = 5

function sameTarget(target: DateFilterTarget | undefined, unit: Mode, year: number, index: number) {
  if (!target?.period || target.period.unit !== unit) return false
  return target.period.year === year && target.period.index === index
}

/** `value` and `onApply` use the part after `<kind>:` (`2026-11-05`, `2022-02`, `2026-Q4`, `2026-H2`, `2026`). */
export function DateFilterDialog({ open, fieldLabel, kind, value, operator, onApply, onClose, now: nowProp }: {
  open: boolean
  fieldLabel: string
  kind: DateFilterKind
  value?: string
  operator?: DateFilterOperator
  onApply: (value: string, operator: DateFilterOperator) => void
  onClose: () => void
  now?: number
}) {
  const { t, locale } = useI18n()
  // The reference app's phone dialog: no search field, one month at a time, roomier cells and buttons.
  const phone = usePhoneLayout()
  const zh = locale.startsWith('zh')
  const clock = useCurrentTimeMs()
  const now = nowProp ?? clock
  const today = localDay(now)
  const thisYear = Number(today.slice(0, 4)), thisMonth = Number(today.slice(5, 7))
  const weekStart = firstWeekday(undefined, locale)
  const applied = useMemo<DateFilterTarget | undefined>(() => {
    const parsed = value ? parseDateFilterValue(`${kind}:${value}`) : undefined
    return parsed?.date ? { date: parsed.date } : parsed?.period ? { period: parsed.period } : undefined
  }, [kind, value])
  const [mode, setMode] = useState<Mode>('quarter')
  const [text, setText] = useState('')
  const [preview, setPreview] = useState<DateFilterTarget>()
  const [dayOperator, setDayOperator] = useState<DateFilterOperator>(kind === 'due' ? 'before' : 'after')
  const [month, setMonth] = useState({ year: thisYear, month: thisMonth })
  const [seen, setSeen] = useState<{ open: boolean; value?: string }>()
  if (seen?.open !== open || seen.value !== value) {
    setSeen({ open, value })
    if (open) {
      const start = applied?.date ?? applied?.period?.start
      setMode(applied?.date ? 'day' : applied?.period?.unit ?? 'quarter')
      setText(applied ? targetText(applied) : '')
      setPreview(undefined)
      setDayOperator(applied?.date && operator && DAY_OPERATORS.includes(operator) ? operator : kind === 'due' ? 'before' : 'after')
      setMonth(start ? { year: Number(start.slice(0, 4)), month: Number(start.slice(5, 7)) } : { year: thisYear, month: thisMonth })
    }
  }
  const focus = preview ?? applied
  const focusYear = focus?.date ? Number(focus.date.slice(0, 4)) : focus?.period?.year ?? thisYear
  // Scroll the period grid so the chosen (or current) year leads; the year list keeps two earlier years in view.
  // A ref callback: the dialog's content mounts after this component's effects, and it re-runs only when the mode or year changes.
  const scroller = useCallback((list: HTMLDivElement | null) => {
    if (!list || mode === 'day') return
    const anchor = list.querySelector<HTMLElement>(`[data-year="${mode === 'year' ? focusYear - 2 : focusYear}"]`)
    if (anchor) list.scrollTop = anchor.offsetTop - 12
  }, [mode, focusYear])

  const apply = (target: DateFilterTarget, dayOp = dayOperator) => {
    if (target.date) onApply(target.date, dayOp)
    else onApply(datePeriodValue(target.period!), 'within')
  }
  const onText = (next: string) => {
    setText(next)
    const parsed = parseDateFilterInput(next, now)
    setPreview(parsed)
    if (!parsed) return
    setMode(parsed.date ? 'day' : parsed.period!.unit)
    const start = parsed.date ?? parsed.period!.start
    if (parsed.date) setMonth({ year: Number(start.slice(0, 4)), month: Number(start.slice(5, 7)) })
  }
  const submit = () => { const target = preview ?? applied; if (target) apply(target) }
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => { if (event.key === 'Enter') { event.preventDefault(); submit() } }

  const firstYear = Math.min(thisYear, focusYear) - (mode === 'year' ? YEAR_LIST_BACK : GRID_YEARS_BACK)
  const years = Array.from({ length: thisYear + YEARS_AHEAD - firstYear + 1 }, (_, index) => firstYear + index)
  const periodCells = (unit: DatePeriodUnit, year: number) => {
    const count = unit === 'month' ? 12 : unit === 'quarter' ? 4 : unit === 'half' ? 2 : 1
    return Array.from({ length: count }, (_, index) => {
      const label = unit === 'month' ? (zh ? `${index + 1}月` : MONTH_SHORT[index]) : unit === 'quarter' ? `Q${index + 1}` : unit === 'half' ? `H${index + 1}` : String(year)
      const selected = sameTarget(applied, unit, year, unit === 'year' ? 1 : index + 1)
      const previewed = !selected && sameTarget(preview, unit, year, unit === 'year' ? 1 : index + 1)
      return <button key={label} type="button" className={styles.cell} data-selected={selected || undefined} data-preview={previewed || undefined} onClick={() => apply({ period: datePeriod(unit, year, index + 1) })}>{label}</button>
    })
  }
  const secondMonth = month.month === 12 ? { year: month.year + 1, month: 1 } : { year: month.year, month: month.month + 1 }
  const shiftMonth = (delta: number) => setMonth(current => { const index = current.year * 12 + current.month - 1 + delta; return { year: Math.floor(index / 12), month: index % 12 + 1 } })
  const calendar = ({ year, month: monthNumber }: { year: number; month: number }) => {
    // The week starts on the account's First day of the week (Sunday by default, Monday for Chinese).
    const leading = (new Date(year, monthNumber - 1, 1).getDay() - weekStart + 7) % 7
    const days = new Date(year, monthNumber, 0).getDate()
    // Desktop leaves the month's other cells blank; the phone fills six weeks with the neighbouring months' days, dimmed.
    const rows = phone ? 6 : Math.ceil((leading + days) / 7)
    const columns = Array.from({ length: 7 }, (_, column) => Array.from({ length: rows }, (_, row) => 1 - leading + column + row * 7))
    return <div className={styles.month} key={`${year}-${monthNumber}`}>
      <div className={styles.monthTitle}>{zh ? `${year}年${monthNumber}月` : `${MONTH_NAMES[monthNumber - 1]} ${year}`}</div>
      <div className={styles.weeks}>
        {columns.map((cells, column) => { const weekday = (weekStart + column) % 7; return <div key={column} className={styles.weekday} data-weekend={weekday === 0 || weekday === 6 || undefined}>
          <div className={styles.weekdayName}>{(zh ? WEEKDAYS_ZH : WEEKDAYS)[weekday]}</div>
          {cells.map((offset, row) => {
            const inMonth = offset >= 1 && offset <= days
            if (!inMonth && !phone) return <span key={row} className={styles.dayGap}/>
            const shown = new Date(year, monthNumber - 1, offset)
            const day = shown.getDate()
            const date = dayString(shown.getFullYear(), shown.getMonth() + 1, day)
            const selected = applied?.date === date
            return <button key={row} type="button" className={styles.day} aria-label={date} data-outside={!inMonth || undefined} data-today={date === today || undefined} data-selected={selected || undefined} data-preview={(!selected && preview?.date === date) || undefined} onClick={() => apply({ date })}>{day}</button>
          })}
        </div> })}
      </div>
    </div>
  }
  const atToday = month.year === thisYear && month.month === thisMonth

  return <Dialog.Root open={open} onOpenChange={next => { if (!next) onClose() }}><Dialog.Portal>
    <Dialog.Overlay data-flow-motion="backdrop" className={styles.overlay}/>
    {/* Window-centred like the reference app's date dialog (it does not shift over the content area). */}
    <Dialog.Content data-flow-motion="dialog" data-dialog-placement="window" data-phone={phone || undefined} className={styles.dialog} aria-describedby={undefined} onOpenAutoFocus={event => { event.preventDefault(); requestAnimationFrame(() => document.querySelector<HTMLInputElement>(`.${styles.input}`)?.focus()) }}>
        <form onSubmit={event => { event.preventDefault(); submit() }}>
          <div className={styles.head}>
            <div className={styles.titleRow}>
              <Dialog.Title className={styles.title}>{t(fieldLabel)}</Dialog.Title>
              {mode === 'day'
                ? DAY_OPERATORS.map(choice => <button key={choice} type="button" className={styles.pill} aria-pressed={dayOperator === choice} onClick={() => setDayOperator(choice)}>{t(choice)}</button>)
                : <span className={styles.operator}>{t('in')}</span>}
            </div>
            {!phone && <div className={styles.field}>
              <input className={styles.input} aria-label={t(fieldLabel)} placeholder={t('Try: May 2027, Q4, 05/20/2027')} value={text} onChange={event => onText(event.target.value)} onKeyDown={onKeyDown}/>
              {text && <button type="button" className={styles.clear} aria-label={t('Clear')} onClick={() => onText('')}><X size={9} strokeWidth={3}/></button>}
            </div>}
            <div className={styles.modes} role="tablist">
              {MODES.map(item => <button key={item.mode} type="button" role="tab" className={styles.pill} aria-selected={mode === item.mode} onClick={() => setMode(item.mode)}>{t(item.label)}</button>)}
            </div>
          </div>
          <div className={styles.body} ref={scroller} data-mode={mode}>
            {mode === 'day'
              ? <div className={styles.calendars}>
                {calendar(month)}{!phone && calendar(secondMonth)}
                <div className={styles.nav}>
                  {!atToday && !phone && <button type="button" aria-label={t('Today')} onClick={() => setMonth({ year: thisYear, month: thisMonth })}><CornerUpRight size={14} strokeWidth={1.75}/></button>}
                  <button type="button" aria-label={t('Previous month')} onClick={() => shiftMonth(-1)}><ChevronLeft size={14} strokeWidth={1.75}/></button>
                  <button type="button" aria-label={t('Next month')} onClick={() => shiftMonth(1)}><ChevronRight size={14} strokeWidth={1.75}/></button>
                </div>
              </div>
              : mode === 'year'
                ? <div className={styles.yearList}>{years.map(year => <div key={year} data-year={year}>{periodCells('year', year)}</div>)}</div>
                : years.map(year => <section key={year} className={styles.yearGroup} data-year={year}>
                  <div className={styles.yearLabel}>{year}</div>
                  <div className={styles.grid} data-unit={mode}>{periodCells(mode, year)}</div>
                </section>)}
          </div>
          <footer className={styles.footer}>
            <button type="button" className={styles.cancel} onClick={onClose}>{t('Cancel')}</button>
            <button type="submit" className={styles.apply}>{t('Apply')}</button>
          </footer>
        </form>
    </Dialog.Content>
  </Dialog.Portal></Dialog.Root>
}

