/**
 * Compact relative times for lists ("just now", "31min ago", "9d ago") in the
 * active locale. Lists show these next to a full-date tooltip.
 */
export type RelativeTimeLocale = 'en-US' | 'zh-CN'

export function formatRelativeTime(value: Date | string | number, locale: RelativeTimeLocale = 'en-US', now: number = Date.now()): string {
  const time = new Date(value).getTime()
  if (!Number.isFinite(time)) return ''
  const seconds = Math.max(0, Math.floor((now - time) / 1000))
  const zh = locale === 'zh-CN'
  if (seconds < 60) return zh ? '刚刚' : 'just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return zh ? `${minutes}分钟前` : `${minutes}min ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return zh ? `${hours}小时前` : `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 30) return zh ? `${days}天前` : `${days}d ago`
  const months = Math.floor(days / 30)
  if (months < 12) return zh ? `${months}个月前` : `${months}mo ago`
  const years = Math.floor(days / 365)
  return zh ? `${Math.max(1, years)}年前` : `${Math.max(1, years)}y ago`
}
