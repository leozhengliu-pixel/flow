import { useI18n } from '@/i18n/i18n'
import { describeRepeats, nextRecurrenceDue, parseRecurrence, recurrenceDueDate } from '@/lib/recurrence'

/**
 * "Repeats every week · Next due Oct 17" label shared by issue surfaces. The
 * schedule is anchored on the issue's due date (the current instance); the next
 * instance is created once that date passes, due on the following cadence date.
 */
export function useRecurrenceSummary(issue: { recurrence?: string; dueDate?: string; nextOccurrenceAt?: string }, timeZone?: string, options: { firstDue?: boolean } = {}) {
  const { t, locale, formatDate } = useI18n()
  if (!issue.recurrence) return ''
  const due = recurrenceDueDate(issue, timeZone)
  const repeats = describeRepeats(issue.recurrence, { t, locale, anchor: due })
  const dateLabel = (date: Date) => formatDate(date.toISOString(), { month: 'short', day: 'numeric' })
  // While composing, the first due date is the useful anchor ("Repeats every week · Due Oct 10").
  if (options.firstDue) return [repeats, due ? t('Due {date}').replace('{date}', dateLabel(due)) : ''].filter(Boolean).join(' · ')
  const schedule = due ? parseRecurrence(issue.recurrence, due) : null
  const next = due && schedule ? nextRecurrenceDue(schedule, due) : undefined
  const nextLabel = next ? t('Next due {date}').replace('{date}', dateLabel(next)) : ''
  return [repeats, nextLabel].filter(Boolean).join(' · ')
}
