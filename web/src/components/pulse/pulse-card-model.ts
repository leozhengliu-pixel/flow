import type { Project, PulseItem, User } from '@/types/flow'

export const HEALTH_LABELS: Record<Project['health'], string> = { onTrack: 'On track', atRisk: 'At risk', offTrack: 'Off track', noUpdate: 'No update' }
export const userName = (user: User) => user.displayName || user.name

/** Markdown copy of an update, as Linear's "Copy as markdown". */
export function pulseUpdateMarkdown(item: PulseItem) {
  return `## ${item.source.name}\n\n**${HEALTH_LABELS[item.update.health]}** · ${userName(item.update.user)} · ${new Date(item.update.createdAt).toDateString()}\n\n${item.update.body.trim()}\n`
}

export function pulseUpdateLink(item: PulseItem) {
  const base = item.source.url.startsWith('http') ? item.source.url : `${location.origin}${item.source.url}`
  return `${base}${base.includes('#') ? '' : `#update-${item.update.id}`}`
}
