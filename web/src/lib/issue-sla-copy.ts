import { getSlaState } from './issue-sla'
import type { IssueSLA } from '@/types/flow'

type Translate = (source: string) => string

/** The countdown rounds short intervals to minutes and long intervals to one or two units. */
export function slaTimeLeft(milliseconds: number, t: Translate = value => value): string {
  const minutes = Math.abs(milliseconds) / 60_000
  const unit = (count: number, suffix: string) => t(`{count}${suffix}`).replace('{count}', String(count))
  if (minutes < 1) return t('<1min')
  if (minutes < 60) return Math.round(minutes) === 60 ? unit(1, 'h') : unit(Math.round(minutes), 'min')
  if (minutes < 480) {
    const hours = Math.floor(minutes / 60), rest = Math.round(minutes % 60)
    return rest === 60 ? unit(hours + 1, 'h') : [unit(hours, 'h'), rest ? unit(rest, 'min') : ''].filter(Boolean).join(' ')
  }
  if (minutes < 1440) return unit(Math.round(minutes / 60), 'h')
  if (minutes < 7200) {
    const days = Math.floor(minutes / 1440), hours = Math.round(minutes % 1440 / 60)
    return hours === 24 ? unit(days + 1, 'd') : [unit(days, 'd'), hours ? unit(hours, 'h') : ''].filter(Boolean).join(' ')
  }
  if (minutes < 43200) {
    const days = Math.round(minutes / 1440)
    return days % 7 === 0 ? unit(days / 7, 'w') : unit(days, 'd')
  }
  const rounded = (value: number) => Math.floor(value) + Number(value % 1 >= .66)
  if (minutes < 100800) return unit(rounded(minutes / 10080), 'w')
  if (minutes < 525600) return unit(rounded(minutes / 43200), 'mo')
  return unit(rounded(minutes / 525600), 'y')
}

export function slaDisplayText(sla: IssueSLA, now: number, compact: boolean, t: Translate): string {
  const state = getSlaState(sla, now)
  const due = Date.parse(sla.dueAt)
  const finished = sla.completedAt ? Date.parse(sla.completedAt) : now
  const remaining = state === 'paused' ? sla.remainingMinutes * 60_000 : due - finished
  const duration = slaTimeLeft(remaining, t)
  if (state === 'failed') return t('Failed by {duration}').replace('{duration}', duration)
  if (state === 'achieved') return t('Achieved {duration} early').replace('{duration}', duration)
  if (state === 'paused') return t('Paused · {duration}').replace('{duration}', duration)
  if (compact) return duration
  return t(state === 'breached' ? 'Breached by {duration}' : '{duration} remaining').replace('{duration}', duration)
}

export function slaTooltipText(sla: IssueSLA, now: number, t: Translate): string {
  const state = getSlaState(sla, now)
  const date = new Date(sla.dueAt).toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' })
  if (state === 'achieved' && sla.completedAt) return t('Completed in {duration}').replace('{duration}', slaTimeLeft(Date.parse(sla.completedAt) - Date.parse(sla.startedAt), t))
  const template = Date.parse(sla.dueAt) > now ? 'Due on {date}' : 'Was due on {date}'
  return t(template).replace('{date}', date)
}
