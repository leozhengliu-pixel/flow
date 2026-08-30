import { format } from 'date-fns'

import type { Initiative, Project } from '@/types/flow'

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
  if (resolution === 'quarter') return `Q${Math.floor(date.getMonth() / 3) + 1} ${date.getFullYear()}`
  if (resolution === 'halfYear') return `H${Math.floor(date.getMonth() / 6) + 1} ${date.getFullYear()}`
  if (resolution === 'year') return String(date.getFullYear())
  return locale === 'en-US'
    ? format(date, 'MMM do')
    : formatDate(`${value}T00:00:00`, { month: 'short', day: 'numeric' })
}

export function inviteProjectMember() {
  const workspace = location.pathname.split('/').filter(Boolean)[0]
  if (workspace) location.assign(`/${workspace}/members?invite=1`)
}

export function initiativeStatusLabel(status: Initiative['status']) {
  return status === 'active' ? 'Active' : status === 'canceled' ? 'Canceled' : status.charAt(0).toUpperCase() + status.slice(1)
}
