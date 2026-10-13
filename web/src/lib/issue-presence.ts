import type { Presence } from '@/types/flow'

/**
 * People currently viewing an issue, as the issue header shows them: one entry per user (a person with the issue
 * open in several tabs or devices has one presence row per client), never the viewer themself, and only rows that
 * are actually on this issue. Order follows the first row seen for each user.
 */
export function issueViewers(presence: readonly Presence[], issueId: string, viewerId?: string): Presence[] {
  const seen = new Set<string>()
  const viewers: Presence[] = []
  for (const item of presence) {
    if (item.issueId !== issueId) continue
    if (viewerId && item.user.id === viewerId) continue
    if (seen.has(item.user.id)) continue
    seen.add(item.user.id)
    viewers.push(item)
  }
  return viewers
}
