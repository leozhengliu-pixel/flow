import type { PulseRouteView } from '@/lib/app-routes'
import type { SavedView } from '@/types/flow'

export const PULSE_TABS: { id: PulseRouteView; label: string }[] = [
  { id: 'following', label: 'For me' },
  { id: 'popular', label: 'Popular' },
  { id: 'all', label: 'Recent' },
]
/** Custom feeds use keys 4–9 then 0. */
export const PULSE_VIEW_KEYS = ['4', '5', '6', '7', '8', '9', '0']

export function orderPulseViews(views: SavedView[], order: string[]) {
  const rank = new Map(order.map((id, index) => [id, index]))
  return [...views].sort((left, right) => (rank.get(left.id) ?? Number.MAX_SAFE_INTEGER) - (rank.get(right.id) ?? Number.MAX_SAFE_INTEGER) || left.createdAt.localeCompare(right.createdAt))
}

/** Linear's empty copy: "Updates from initiatives, projects … will show here." */
export function emptyCopy(tab: PulseRouteView, initiativesEnabled: boolean, t: (source: string) => string) {
  const sources = initiativesEnabled ? t('initiatives, projects') : t('Projects') === 'Projects' ? 'projects' : t('Projects')
  const template = tab === 'following' ? 'Updates from {sources} that you’re a part of or subscribed to will show here.' : tab === 'popular' ? 'Popular updates from {sources} will show here.' : 'Updates from {sources} in your workspace will show here.'
  return t(template).replace('{sources}', sources)
}
