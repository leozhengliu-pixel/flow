import type { BootstrapData, SyncManifest } from '@/types/flow'
import { cacheStringBytes } from '@/lib/scale-p08'

export function scaleP19ProjectsEnabled(data: BootstrapData | null | undefined) {
  return import.meta.env.VITE_SYNC_V2 === 'true' && import.meta.env.VITE_PROJECT_QUERY_WINDOWS === 'true' && data?.sync?.capabilities.includes('sync-v2') === true
}

export function scaleP19InitiativesEnabled(data: BootstrapData | null | undefined) {
  return import.meta.env.VITE_SYNC_V2 === 'true' && import.meta.env.VITE_INITIATIVE_QUERY_WINDOWS === 'true' && data?.sync?.capabilities.includes('sync-v2') === true
}

export function scaleP19Surface(kind: string) {
  if (import.meta.env.VITE_SYNC_V2 !== 'true') return undefined
  if (import.meta.env.VITE_PROJECT_QUERY_WINDOWS === 'true' && ['projects', 'team-projects', 'projects-new-view', 'projects-saved-view', 'team-projects-new-view', 'team-projects-saved-view'].includes(kind)) return 'projects-page' as const
  if (import.meta.env.VITE_INITIATIVE_QUERY_WINDOWS === 'true' && ['initiatives', 'team-initiatives'].includes(kind)) return 'initiatives-page' as const
  return undefined
}

export function scaleP19CoreSupported(core: Partial<BootstrapData>, collections?: SyncManifest['collections']) {
  return ['user', 'team', 'label', 'initiative', 'savedView', 'favorite'].every(kind => collections?.[kind]?.complete === true) && ['users', 'teams', 'labels', 'labelGroups', 'projectStatuses', 'initiatives', 'savedViews', 'favorites', 'subscriptions'].every(field => Array.isArray(core[field as keyof BootstrapData])) && Boolean(core.workspaceSettings && core.userSettings) && new TextEncoder().encode(JSON.stringify(core)).byteLength <= 256 * 1024
}

export function scaleP19InitiativeCatalogSupported(core: Partial<BootstrapData>, collections?: SyncManifest['collections']) {
  return scaleP19CoreSupported(core, collections) && collections?.project?.complete === true && Array.isArray(core.projects) && Boolean(core.projectUpdates) && core.initiatives!.every(item => Array.isArray(item.projectIds) && Array.isArray(item.parentInitiativeIds)) && cacheStringBytes(JSON.stringify(core.initiatives)) <= 4 * 1024 * 1024
}

export function scaleP19ManifestMatches(current: SyncManifest, next: SyncManifest) {
  return next.schema === 2 && next.workspaceId === current.workspaceId && next.viewerId === current.viewerId && next.authzEpoch === current.authzEpoch
}
