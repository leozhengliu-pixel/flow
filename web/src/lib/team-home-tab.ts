import { useSyncExternalStore } from 'react'
import { teamDocumentsPath, teamHomePath, teamLoopsPath, teamMembersPath, type AppRoute } from './app-routes'

/** The tabs of a team's home page. */
export type TeamHomeTab = 'overview' | 'documents' | 'loops' | 'members'

const ROUTE_TABS: Partial<Record<AppRoute['kind'], TeamHomeTab>> = {
  'team-overview': 'overview',
  'team-documents': 'documents',
  'team-loops': 'loops',
  'team-members': 'members',
}
const TABS = new Set<string>(Object.values(ROUTE_TABS))
const listeners = new Set<() => void>()

function storageKey(viewerId: string) {
  return `flow:team-home-tab:${viewerId}`
}

function readTabs(viewerId: string): Record<string, string> {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(storageKey(viewerId)) ?? '{}')
    return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, string> : {}
  } catch {
    return {}
  }
}

/** The team home tab a route shows, if it is one. */
export function teamHomeTabOf(route: AppRoute): TeamHomeTab | undefined {
  return ROUTE_TABS[route.kind]
}

/** The tab of a team's home the viewer last opened (Overview until they open another). */
export function teamHomeTab(viewerId: string, teamId: string): TeamHomeTab {
  const tab = readTabs(viewerId)[teamId]
  return tab && TABS.has(tab) ? tab as TeamHomeTab : 'overview'
}

/** Remembers the team home tab the viewer opened, per team, so the team's Home link reopens it. */
export function rememberTeamHomeTab(viewerId: string, teamId: string, tab: TeamHomeTab) {
  const tabs = readTabs(viewerId)
  if (tabs[teamId] === tab) return
  try {
    localStorage.setItem(storageKey(viewerId), JSON.stringify({ ...tabs, [teamId]: tab }))
  } catch {
    return
  }
  for (const listener of listeners) listener()
}

/** The path of a team home tab; Loops falls back to Overview while the feature is off. */
export function teamHomeTabPath(workspaceSlug: string, teamKey: string, tab: TeamHomeTab, loopsEnabled = true) {
  if (tab === 'documents') return teamDocumentsPath(workspaceSlug, teamKey)
  if (tab === 'members') return teamMembersPath(workspaceSlug, teamKey)
  if (tab === 'loops' && loopsEnabled) return teamLoopsPath(workspaceSlug, teamKey)
  return teamHomePath(workspaceSlug, teamKey)
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  const onStorage = (event: StorageEvent) => { if (event.key?.startsWith('flow:team-home-tab:')) listener() }
  window.addEventListener('storage', onStorage)
  return () => {
    listeners.delete(listener)
    window.removeEventListener('storage', onStorage)
  }
}

/** The remembered team home tab, kept current as the viewer moves between tabs (here or in another window). */
export function useTeamHomeTab(viewerId: string, teamId: string): TeamHomeTab {
  return useSyncExternalStore(subscribe, () => teamHomeTab(viewerId, teamId), () => 'overview')
}
