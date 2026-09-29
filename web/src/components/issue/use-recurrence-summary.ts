import { useI18n } from '@/i18n/i18n'
import { describeRecurrence, recurrenceDate } from '@/lib/recurrence'

/** "Recurring · Weekly on Mon · Next: Oct 5" label shared by issue surfaces. */
export function useRecurrenceSummary(recurrence?: string, nextOccurrenceAt?: string, timeZone?: string) {
  const { t, locale, formatDate } = useI18n()
  if (!recurrence) return ''
  const nextDate = nextOccurrenceAt ? recurrenceDate(nextOccurrenceAt, timeZone) : undefined
  const schedule = describeRecurrence(recurrence, { t, locale, anchor: nextDate })
  const next = nextDate ? `${t('Next')}: ${formatDate(nextDate.toISOString(), { month: 'short', day: 'numeric' })}` : ''
  return [t('Recurring'), schedule, next].filter(Boolean).join(' · ')
}
