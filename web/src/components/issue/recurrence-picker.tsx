import * as Dialog from '@radix-ui/react-dialog'
import { Repeat2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'

import { SelectControl } from '@/components/ui/select-control'
import { useI18n } from '@/i18n/i18n'
import {
  MAX_RECURRENCE_INTERVAL,
  anchorRecurrence,
  defaultFirstDue,
  defaultRecurrence,
  describeRepeats,
  isAnchoredRecurrence,
  nextRecurrenceDue,
  parseRecurrence,
  recurrenceDate,
  recurrenceDueDate,
  serializeRecurrenceFrom,
  toDateInput,
  weekdayOrdinal,
  type RecurrenceFrequency,
  type RecurrenceSchedule,
} from '@/lib/recurrence'
import './issue-options-select.css'
import './recurrence-picker.css'

const MONDAY_FIRST = [1, 2, 3, 4, 5, 6, 0]
const UNIT: Record<RecurrenceFrequency, [string, string]> = { daily: ['day', 'days'], weekly: ['week', 'weeks'], monthly: ['month', 'months'], yearly: ['year', 'years'] }
/** Linear's interval menu offers 1–12; longer stored intervals stay selectable. */
const LINEAR_INTERVALS = Array.from({ length: 12 }, (_, index) => index + 1)

/** Linear's "First due [date] repeats every [N] [unit]" row, shared by the dialog and the settings form. */
export function RecurrenceCadenceFields({ firstDue, frequency, interval, min, onFirstDue, onFrequency, onInterval }: {
  /** YYYY-MM-DD */
  firstDue: string
  frequency: RecurrenceFrequency
  interval: number
  min?: string
  onFirstDue: (value: string) => void
  onFrequency: (value: RecurrenceFrequency) => void
  onInterval: (value: number) => void
}) {
  const { t } = useI18n()
  const intervals = LINEAR_INTERVALS.includes(interval) ? LINEAR_INTERVALS : [...LINEAR_INTERVALS, interval].sort((a, b) => a - b)
  const plural = interval === 1 ? 0 : 1
  return <div className="recurrence-cadence">
    <label className="recurrence-cadence__due"><span>{t('First due')}</span><input aria-label={t('First due')} type="date" min={min} required value={firstDue} onChange={event => onFirstDue(event.target.value)}/></label>
    <span className="recurrence-cadence__every">
      <span>{t('repeats every')}</span>
      <SelectControl className="recurrence-cadence__select" label={t('Repeat interval')} value={String(interval)} onChange={value => onInterval(Math.min(MAX_RECURRENCE_INTERVAL, Math.max(1, Number(value) || 1)))} options={intervals.map(value => ({ value: String(value), label: String(value) }))}/>
      <SelectControl className="recurrence-cadence__select" label={t('Repeat unit')} value={frequency} onChange={value => onFrequency(value as RecurrenceFrequency)} options={(['daily', 'weekly', 'monthly', 'yearly'] as const).map(value => ({ value, label: t(UNIT[value][plural]) }))}/>
    </span>
  </div>
}

export interface RecurrencePickerProps {
  /** Stored schedule, or empty for a new schedule. */
  value?: string
  /** The issue's due date (YYYY-MM-DD): the current instance, which anchors the schedule. */
  dueDate?: string
  /** Legacy schedules without a due date: their next occurrence instant. */
  nextOccurrenceAt?: string
  /** Team timezone the API schedules occurrences in. */
  timeZone?: string
  busy?: boolean
  /** `firstDue` (YYYY-MM-DD) is sent as the issue's dueDate. */
  onSave: (recurrence: string, firstDue: string) => void | Promise<void>
  onStop?: () => void | Promise<void>
  onCancel: () => void
  /** Injected for tests; defaults to today. */
  today?: Date
}

/**
 * Linear-style schedule editor: the first due date and "repeats every N unit"
 * lead; Flow's extra day options (several weekdays, nth weekday, last day) sit
 * below and default to the first due date's own weekday / day.
 */
export function RecurrencePicker({ value, dueDate, nextOccurrenceAt, timeZone, busy = false, onSave, onStop, onCancel, today: todayInput }: RecurrencePickerProps) {
  const { t, locale, formatDate } = useI18n()
  const today = useMemo(() => recurrenceDate(todayInput ?? new Date()), [todayInput])
  const initial = useMemo(() => {
    const current = value ? recurrenceDueDate({ dueDate, nextOccurrenceAt }, timeZone) : dueDate ? recurrenceDate(dueDate.slice(0, 10)) : undefined
    const start = current && (value || current >= today) ? current : defaultFirstDue(today)
    const parsed = parseRecurrence(value, start)
    return { start, schedule: parsed ?? defaultRecurrence(start), customized: Boolean(parsed && !isAnchoredRecurrence(parsed, start)) }
  }, [dueDate, nextOccurrenceAt, timeZone, today, value])
  const [start, setStart] = useState(toDateInput(initial.start))
  const [schedule, setSchedule] = useState<RecurrenceSchedule>(initial.schedule)
  // Until a day option is changed, the schedule follows the first due date.
  const [customized, setCustomized] = useState(initial.customized)
  const startDate = recurrenceDate(start || toDateInput(initial.start))
  const shown = customized ? schedule : anchorRecurrence(schedule, startDate)
  const serialized = serializeRecurrenceFrom(shown, startDate)
  const next = nextRecurrenceDue(shown, startDate)
  const weekdayName = (day: number, width: 'short' | 'long' = 'long') => new Intl.DateTimeFormat(locale, { weekday: width }).format(new Date(2024, 0, 7 + day))
  const monthName = (month: number) => new Intl.DateTimeFormat(locale, { month: 'long' }).format(new Date(2024, month - 1, 1))
  const monthMode = shown.monthMode === 'weekday' ? 'weekday' : shown.monthDay < 0 ? 'last' : 'day'
  const invalid = shown.frequency === 'weekly' && shown.weekdays.length === 0 || !start

  const patch = (next: Partial<RecurrenceSchedule>) => setSchedule(current => ({ ...current, ...next }))
  const customize = (next: Partial<RecurrenceSchedule>) => { setSchedule({ ...shown, ...next }); setCustomized(true) }
  const setFrequency = (frequency: RecurrenceFrequency) => {
    if (!customized) return patch({ frequency })
    patch({ frequency, weekdays: shown.weekdays.length ? shown.weekdays : [startDate.getDay()], month: shown.month || startDate.getMonth() + 1 })
  }
  const toggleWeekday = (day: number) => customize({ weekdays: shown.weekdays.includes(day) ? shown.weekdays.filter(item => item !== day) : [...shown.weekdays, day] })

  return <div className="recurrence-picker">
    <RecurrenceCadenceFields firstDue={start} frequency={shown.frequency} interval={shown.interval} min={toDateInput(today)} onFirstDue={setStart} onFrequency={setFrequency} onInterval={interval => patch({ interval })}/>
    {shown.frequency === 'weekly' && <div className="recurrence-picker__options">
      <span className="recurrence-picker__caption">{t('Repeat on')}</span>
      <div className="recurrence-picker__weekdays" role="group" aria-label={t('Repeat on')}>
        {MONDAY_FIRST.map(day => <button type="button" key={day} aria-pressed={shown.weekdays.includes(day)} aria-label={weekdayName(day)} onClick={() => toggleWeekday(day)}>{weekdayName(day, 'short')}</button>)}
        <button type="button" className="recurrence-picker__shortcut" onClick={() => customize({ weekdays: [1, 2, 3, 4, 5], interval: 1 })}>{t('Weekdays')}</button>
      </div>
    </div>}
    {(shown.frequency === 'monthly' || shown.frequency === 'yearly') && <div className="recurrence-picker__options">
      <span className="recurrence-picker__caption">{t('Repeat on')}</span>
      <div className="recurrence-picker__row">
        {shown.frequency === 'yearly' && <label>{t('Month')}<SelectControl label={t('Month')} value={String(shown.month)} onChange={value => customize({ month: Number(value) })} options={Array.from({ length: 12 }, (_, index) => ({ value: String(index + 1), label: monthName(index + 1) }))}/></label>}
        <label>{t('On')}<SelectControl label={t('Repeat on')} value={monthMode} onChange={mode => customize(mode === 'weekday' ? { monthMode: 'weekday', ordinal: weekdayOrdinal(startDate), weekday: startDate.getDay() } : mode === 'last' ? { monthMode: 'day', monthDay: -1 } : { monthMode: 'day', monthDay: startDate.getDate() })} options={[{ value: 'day', label: t('Day of the month') }, { value: 'weekday', label: t('Weekday of the month') }, { value: 'last', label: t('Last day of the month') }]}/></label>
        {monthMode === 'day' && <label>{t('Day')}<input aria-label={t('Day of the month')} type="number" min={1} max={31} value={shown.monthDay} onChange={event => customize({ monthDay: Math.min(31, Math.max(1, Number(event.target.value) || 1)) })}/></label>}
        {monthMode === 'weekday' && <>
          <label>{t('Week')}<SelectControl label={t('Week of the month')} value={String(shown.ordinal)} onChange={value => customize({ ordinal: Number(value) })} options={[['1', 'First'], ['2', 'Second'], ['3', 'Third'], ['4', 'Fourth'], ['-1', 'Last']].map(([value, label]) => ({ value, label: t(label) }))}/></label>
          <label>{t('Weekday')}<SelectControl label={t('Weekday')} value={String(shown.weekday)} onChange={value => customize({ weekday: Number(value) })} options={MONDAY_FIRST.map(day => ({ value: String(day), label: weekdayName(day) }))}/></label>
        </>}
      </div>
    </div>}
    <p className="recurrence-picker__summary" aria-live="polite"><Repeat2 aria-hidden="true"/><span>{describeRepeats(serialized, { t, locale, anchor: startDate })}</span><small>{t('Next due {date}').replace('{date}', formatDate(next.toISOString(), { month: 'short', day: 'numeric', year: next.getFullYear() === today.getFullYear() ? undefined : 'numeric' }))}</small></p>
    <footer>
      {onStop && value && <button type="button" className="recurrence-picker__stop" disabled={busy} onClick={() => void onStop()}>{t('Stop recurring')}</button>}
      <button type="button" onClick={onCancel}>{t('Cancel')}</button>
      <button type="button" className="primary" disabled={busy || invalid} onClick={() => void onSave(serialized, start)}>{busy ? t('Saving…') : t('Save')}</button>
    </footer>
  </div>
}

/** Dialog wrapper used by the issue page, options menu, create dialog and settings. */
export function RecurrenceDialog({ open, onOpenChange, title, ...props }: Omit<RecurrencePickerProps, 'onCancel'> & { open: boolean; onOpenChange: (open: boolean) => void; title?: string }) {
  const { t } = useI18n()
  const [key, setKey] = useState(0)
  useEffect(() => { if (open) setKey(value => value + 1) }, [open])
  const heading = title ?? t(props.value ? 'Recurring issue' : 'Make recurring')
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="dialog-overlay"/><Dialog.Content data-flow-motion="dialog" className="issue-action-dialog recurrence-dialog" aria-label={heading} aria-describedby={undefined}><Dialog.Title>{heading}</Dialog.Title><RecurrencePicker key={key} {...props} onCancel={() => onOpenChange(false)}/></Dialog.Content></Dialog.Portal></Dialog.Root>
}
