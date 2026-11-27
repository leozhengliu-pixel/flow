import type { BootstrapData, SyncManifest } from '@/types/flow'
import { cacheStringBytes } from '@/lib/realtime-cache-limits'
import { QUERY_WINDOWS_CAPABILITY } from '@/lib/query-window-api'

export function queryWindowCoreSupported(manifest: SyncManifest) {
  return manifest.capabilities.includes(QUERY_WINDOWS_CAPABILITY) && ['user', 'team', 'label', 'savedView', 'favorite'].every(kind => manifest.collections[kind]?.complete === true) && ['users', 'teams', 'labels', 'initiatives', 'projectStatuses', 'projectTemplates', 'projectRelations', 'favorites', 'savedViews', 'customers'].every(field => Array.isArray(manifest.core[field as keyof BootstrapData]))
}

export function projectWindowsEnabled(data: BootstrapData | null | undefined) {
  return import.meta.env.VITE_SYNC_V2 === 'true' && import.meta.env.VITE_PROJECT_QUERY_WINDOWS === 'true' && data?.sync?.capabilities.includes('sync-v2') === true
}

export function initiativeWindowsEnabled(data: BootstrapData | null | undefined) {
  return import.meta.env.VITE_SYNC_V2 === 'true' && import.meta.env.VITE_INITIATIVE_QUERY_WINDOWS === 'true' && data?.sync?.capabilities.includes('sync-v2') === true
}

export function queryWindowSyncSurface(kind: string) {
  if (import.meta.env.VITE_SYNC_V2 !== 'true') return undefined
  if (import.meta.env.VITE_PROJECT_QUERY_WINDOWS === 'true' && ['projects', 'team-projects', 'projects-new-view', 'projects-saved-view', 'team-projects-new-view', 'team-projects-saved-view'].includes(kind)) return 'projects-page' as const
  if (import.meta.env.VITE_INITIATIVE_QUERY_WINDOWS === 'true' && ['initiatives', 'team-initiatives'].includes(kind)) return 'initiatives-page' as const
  return undefined
}

export function queryCoreSupported(core: Partial<BootstrapData>, collections?: SyncManifest['collections']) {
  const arrays = ['users', 'teams', 'labels', 'labelGroups', 'projectStatuses', 'projectTemplates', 'projectRelations', 'initiatives', 'savedViews', 'favorites', 'favoriteFolders', 'subscriptions', 'agentSkills', 'invitations', 'customers', 'customerStatuses', 'customerTiers', 'customerRequests']
  return ['user', 'team', 'label', 'initiative', 'savedView', 'favorite'].every(kind => collections?.[kind]?.complete === true) && arrays.every(field => Array.isArray(core[field as keyof BootstrapData])) && Boolean(core.workspaceSettings && core.userSettings && core.teamSettings && core.settings) && new TextEncoder().encode(JSON.stringify(core)).byteLength <= 256 * 1024
}

export function initiativeCatalogSupported(core: Partial<BootstrapData>, collections?: SyncManifest['collections']) {
  return queryCoreSupported(core, collections) && collections?.project?.complete === true && Array.isArray(core.projects) && Boolean(core.projectUpdates) && core.initiatives!.every(item => Array.isArray(item.projectIds) && Array.isArray(item.parentInitiativeIds)) && cacheStringBytes(JSON.stringify(core.initiatives)) <= 4 * 1024 * 1024
}

export function queryWindowManifestMatches(current: SyncManifest, next: SyncManifest) {
  return next.schema === 2 && next.workspaceId === current.workspaceId && next.viewerId === current.viewerId && next.authzEpoch === current.authzEpoch
}
