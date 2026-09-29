import * as Dialog from '@radix-ui/react-dialog'
import { Repeat2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'

import { SelectControl } from '@/components/ui/select-control'
import { useI18n } from '@/i18n/i18n'
import {
  MAX_RECURRENCE_INTERVAL,
  defaultRecurrence,
  describeRecurrence,
  firstRecurrenceOnOrAfter,
  parseRecurrence,
  recurrenceDate,
  serializeRecurrence,
  toDateInput,
  weekdayOrdinal,
  type RecurrenceFrequency,
  type RecurrenceSchedule,
} from '@/lib/recurrence'
import './issue-options-select.css'
import './recurrence-picker.css'

const MONDAY_FIRST = [1, 2, 3, 4, 5, 6, 0]
const UNIT: Record<RecurrenceFrequency, [string, string]> = { daily: ['day', 'days'], weekly: ['week', 'weeks'], monthly: ['month', 'months'], yearly: ['year', 'years'] }

export interface RecurrencePickerProps {
  /** Stored schedule, or empty for a new schedule. */
  value?: string
  /** Current next occurrence (ISO instant or YYYY-MM-DD). */
  nextOccurrenceAt?: string
  /** Team timezone the API schedules occurrences in. */
  timeZone?: string
  busy?: boolean
  onSave: (recurrence: string, startDate: string) => void | Promise<void>
  onStop?: () => void | Promise<void>
  onCancel: () => void
  /** Injected for tests; defaults to today. */
  today?: Date
}

function tomorrow(today: Date) {
  return new Date(today.getFullYear(), today.getMonth(), today.getDate() + 1)
}

/** Linear-style schedule editor: frequency, interval, days and start date. */
export function RecurrencePicker({ value, nextOccurrenceAt, timeZone, busy = false, onSave, onStop, onCancel, today: todayInput }: RecurrencePickerProps) {
  const { t, locale, formatDate } = useI18n()
  const today = useMemo(() => recurrenceDate(todayInput ?? new Date()), [todayInput])
  const current = value && nextOccurrenceAt ? recurrenceDate(nextOccurrenceAt, timeZone) : undefined
  const initialStart = current ?? tomorrow(today)
  const [start, setStart] = useState(toDateInput(initialStart < today ? today : initialStart))
  const [schedule, setSchedule] = useState<RecurrenceSchedule>(() => parseRecurrence(value, current ?? today) ?? defaultRecurrence(today))
  const patch = (next: Partial<RecurrenceSchedule>) => setSchedule(current => ({ ...current, ...next }))
  const startDate = recurrenceDate(start || toDateInput(tomorrow(today)))
  const serialized = serializeRecurrence(schedule)
  const next = firstRecurrenceOnOrAfter(schedule, startDate < today ? today : startDate)
  const weekdayName = (day: number, width: 'short' | 'long' = 'long') => new Intl.DateTimeFormat(locale, { weekday: width }).format(new Date(2024, 0, 7 + day))
  const monthName = (month: number) => new Intl.DateTimeFormat(locale, { month: 'long' }).format(new Date(2024, month - 1, 1))
  const monthMode = schedule.monthMode === 'weekday' ? 'weekday' : schedule.monthDay < 0 ? 'last' : 'day'
  const invalid = schedule.frequency === 'weekly' && schedule.weekdays.length === 0 || !start

  const setFrequency = (frequency: RecurrenceFrequency) => {
    const anchor = startDate
    patch({ frequency, weekdays: schedule.weekdays.length ? schedule.weekdays : [anchor.getDay()], month: schedule.month || anchor.getMonth() + 1 })
  }
  const toggleWeekday = (day: number) => patch({ weekdays: schedule.weekdays.includes(day) ? schedule.weekdays.filter(item => item !== day) : [...schedule.weekdays, day] })

  return <div className="recurrence-picker">
    <div className="recurrence-picker__row">
      <label>{t('Repeat')}<SelectControl label={t('Repeat')} value={schedule.frequency} onChange={value => setFrequency(value as RecurrenceFrequency)} options={[{ value: 'daily', label: t('Daily') }, { value: 'weekly', label: t('Weekly') }, { value: 'monthly', label: t('Monthly') }, { value: 'yearly', label: t('Yearly') }]}/></label>
      <label>{t('Every')}<span className="recurrence-picker__interval"><input aria-label={t('Interval')} type="number" min={1} max={MAX_RECURRENCE_INTERVAL} value={schedule.interval} onChange={event => patch({ interval: Math.min(MAX_RECURRENCE_INTERVAL, Math.max(1, Number(event.target.value) || 1)) })}/><span>{t(UNIT[schedule.frequency][schedule.interval === 1 ? 0 : 1])}</span></span></label>
    </div>
    {schedule.frequency === 'weekly' && <div className="recurrence-picker__weekdays" role="group" aria-label={t('Repeat on')}>
      {MONDAY_FIRST.map(day => <button type="button" key={day} aria-pressed={schedule.weekdays.includes(day)} aria-label={weekdayName(day)} onClick={() => toggleWeekday(day)}>{weekdayName(day, 'short')}</button>)}
      <button type="button" className="recurrence-picker__shortcut" onClick={() => patch({ weekdays: [1, 2, 3, 4, 5], interval: 1 })}>{t('Weekdays')}</button>
    </div>}
    {(schedule.frequency === 'monthly' || schedule.frequency === 'yearly') && <div className="recurrence-picker__row">
      {schedule.frequency === 'yearly' && <label>{t('Month')}<SelectControl label={t('Month')} value={String(schedule.month)} onChange={value => patch({ month: Number(value) })} options={Array.from({ length: 12 }, (_, index) => ({ value: String(index + 1), label: monthName(index + 1) }))}/></label>}
      <label>{t('On')}<SelectControl label={t('Repeat on')} value={monthMode} onChange={mode => patch(mode === 'weekday' ? { monthMode: 'weekday', ordinal: weekdayOrdinal(startDate), weekday: startDate.getDay() } : mode === 'last' ? { monthMode: 'day', monthDay: -1 } : { monthMode: 'day', monthDay: startDate.getDate() })} options={[{ value: 'day', label: t('Day of the month') }, { value: 'weekday', label: t('Weekday of the month') }, { value: 'last', label: t('Last day of the month') }]}/></label>
      {monthMode === 'day' && <label>{t('Day')}<input aria-label={t('Day of the month')} type="number" min={1} max={31} value={schedule.monthDay} onChange={event => patch({ monthDay: Math.min(31, Math.max(1, Number(event.target.value) || 1)) })}/></label>}
      {monthMode === 'weekday' && <>
        <label>{t('Week')}<SelectControl label={t('Week of the month')} value={String(schedule.ordinal)} onChange={value => patch({ ordinal: Number(value) })} options={[['1', 'First'], ['2', 'Second'], ['3', 'Third'], ['4', 'Fourth'], ['-1', 'Last']].map(([value, label]) => ({ value, label: t(label) }))}/></label>
        <label>{t('Weekday')}<SelectControl label={t('Weekday')} value={String(schedule.weekday)} onChange={value => patch({ weekday: Number(value) })} options={MONDAY_FIRST.map(day => ({ value: String(day), label: weekdayName(day) }))}/></label>
      </>}
    </div>}
    <label>{t('Starts')}<input aria-label={t('Start date')} type="date" min={toDateInput(today)} value={start} onChange={event => setStart(event.target.value)}/></label>
    <p className="recurrence-picker__summary" aria-live="polite"><Repeat2 aria-hidden="true"/><span>{describeRecurrence(serialized, { t, locale })}</span><small>{t('Next')}: {formatDate(next.toISOString(), { month: 'short', day: 'numeric', year: next.getFullYear() === today.getFullYear() ? undefined : 'numeric' })}</small></p>
    <footer>
      {onStop && value && <button type="button" className="recurrence-picker__stop" disabled={busy} onClick={() => void onStop()}>{t('Stop recurring')}</button>}
      <button type="button" onClick={onCancel}>{t('Cancel')}</button>
      <button type="button" className="primary" disabled={busy || invalid} onClick={() => void onSave(serialized, start)}>{busy ? t('Saving…') : t('Save')}</button>
    </footer>
  </div>
}

/** Dialog wrapper used by the issue page, options menu and create dialog. */
export function RecurrenceDialog({ open, onOpenChange, title, ...props }: Omit<RecurrencePickerProps, 'onCancel'> & { open: boolean; onOpenChange: (open: boolean) => void; title?: string }) {
  const { t } = useI18n()
  const [key, setKey] = useState(0)
  useEffect(() => { if (open) setKey(value => value + 1) }, [open])
  const heading = title ?? t(props.value ? 'Recurring issue' : 'Make recurring')
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="dialog-overlay"/><Dialog.Content data-flow-motion="dialog" className="issue-action-dialog recurrence-dialog" aria-label={heading} aria-describedby={undefined}><Dialog.Title>{heading}</Dialog.Title><RecurrencePicker key={key} {...props} onCancel={() => onOpenChange(false)}/></Dialog.Content></Dialog.Portal></Dialog.Root>
}
