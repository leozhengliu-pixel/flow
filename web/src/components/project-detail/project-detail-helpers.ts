import { format } from 'date-fns'

import type { Initiative, Project } from '@/types/flow'
import { fiscalMonthOffset, fiscalYear } from '@/lib/runtime-preferences'
import { openMemberInvite } from '@/lib/invite-navigation'

type DateFormatter = (value: Date | string | number, options?: Intl.DateTimeFormatOptions) => string

export function formatProjectPropertyDate(
  value: string | undefined,
  resolution: Project['startDateResolution'],
  fallback: string,
  locale: string,
  formatDate: DateFormatter,
  monthYear: 'full' | 'short' = 'full',
) {
  if (!value) return fallback
  const date = new Date(`${value}T00:00:00`)
  if (resolution === 'month') {
    const year: Intl.DateTimeFormatOptions['year'] = monthYear === 'short' ? '2-digit' : 'numeric'
    return locale === 'en-US'
      ? format(date, monthYear === 'short' ? "MMM ''yy" : 'MMM yyyy')
      : formatDate(`${value}T00:00:00`, { month: 'short', year })
  }
  if (resolution === 'quarter') return `Q${Math.floor(fiscalMonthOffset(date) / 3) + 1} ${fiscalYear(date)}`
  if (resolution === 'halfYear') return `H${Math.floor(fiscalMonthOffset(date) / 6) + 1} ${fiscalYear(date)}`
  if (resolution === 'year') return String(fiscalYear(date))
  return locale === 'en-US'
    ? format(date, 'MMM do')
    : formatDate(`${value}T00:00:00`, { month: 'short', day: 'numeric' })
}

export const inviteProjectMember = openMemberInvite

export function initiativeStatusLabel(status: Initiative['status']) {
  return status === 'active' ? 'Active' : status === 'canceled' ? 'Canceled' : status.charAt(0).toUpperCase() + status.slice(1)
}

/** Link to the project's issues filtered to one milestone (what the reference app copies for a milestone). */
export function projectMilestoneLink(milestoneId: string) {
  return `${location.origin}${location.pathname.replace(/\/(overview|activity|issues)$/, '/issues')}?projectMilestoneId=${encodeURIComponent(milestoneId)}`
}
