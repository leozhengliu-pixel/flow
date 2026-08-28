import { FLOW_VIEW_ICON_ALIASES, FLOW_VIEW_ICON_NAMES } from './flow-view-icon-data'

const PROJECT_ICON_NAMES = new Set<string>(FLOW_VIEW_ICON_NAMES)

export function normalizeProjectIcon(icon?: string) {
  const value = icon?.trim()
  if (!value) return 'Project'
  const assetIcon = FLOW_VIEW_ICON_ALIASES[value] ?? value
  return PROJECT_ICON_NAMES.has(assetIcon) || assetIcon === 'Team' || /\p{Extended_Pictographic}/u.test(value) ? value : 'Project'
}
