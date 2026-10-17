import type { Project, PulseItem, User } from '@/types/flow'

export const HEALTH_LABELS: Record<Project['health'], string> = { onTrack: 'On track', atRisk: 'At risk', offTrack: 'Off track', noUpdate: 'No update' }
/** The feed names the source kind in the status line: "Project on track". */
export const PULSE_HEALTH_LABELS: Record<PulseItem['kind'], Record<Project['health'], string>> = {
  project: { onTrack: 'Project on track', atRisk: 'Project at risk', offTrack: 'Project off track', noUpdate: 'Project no update' },
  initiative: { onTrack: 'Initiative on track', atRisk: 'Initiative at risk', offTrack: 'Initiative off track', noUpdate: 'Initiative no update' },
}
export const userName = (user: User) => user.displayName || user.name

/** Markdown copy of an update, as the reference app's "Copy as markdown". */
export function pulseUpdateMarkdown(item: PulseItem) {
  return `## ${item.source.name}\n\n**${HEALTH_LABELS[item.update.health]}** · ${userName(item.update.user)} · ${new Date(item.update.createdAt).toDateString()}\n\n${item.update.body.trim()}\n`
}

export function pulseUpdateLink(item: PulseItem) {
  const base = item.source.url.startsWith('http') ? item.source.url : `${location.origin}${item.source.url}`
  return `${base}${base.includes('#') ? '' : `#update-${item.update.id}`}`
}
