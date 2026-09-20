import type { BootstrapData } from '@/types/flow'
import { normalizeEntityType, type WorkspaceEntityType } from './entity-type'

export type EntityDirectory = {
  issue: Map<string, BootstrapData['issues'][number]>
  project: Map<string, BootstrapData['projects'][number]>
  team: Map<string, BootstrapData['teams'][number]>
  user: Map<string, BootstrapData['users'][number]>
  state: Map<string, BootstrapData['states'][number]>
  label: Map<string, BootstrapData['labels'][number]>
  cycle: Map<string, BootstrapData['cycles'][number]>
  initiative: Map<string, BootstrapData['initiatives'][number]>
  document: Map<string, BootstrapData['documents'][number]>
  notification: Map<string, BootstrapData['notifications'][number]>
  favorite: Map<string, BootstrapData['favorites'][number]>
  agentSession: Map<string, BootstrapData['agentSessions'][number]>
  ask: Map<string, BootstrapData['asks'][number]>
  savedView: Map<string, BootstrapData['savedViews'][number]>
}

function emptyDirectory(): EntityDirectory {
  return {
    issue: new Map(),
    project: new Map(),
    team: new Map(),
    user: new Map(),
    state: new Map(),
    label: new Map(),
    cycle: new Map(),
    initiative: new Map(),
    document: new Map(),
    notification: new Map(),
    favorite: new Map(),
    agentSession: new Map(),
    ask: new Map(),
    savedView: new Map(),
  }
}

function indexById<T extends { id: string }>(items: T[] | undefined): Map<string, T> {
  const map = new Map<string, T>()
  for (const item of items ?? []) map.set(item.id, item)
  return map
}

/** Build O(1) entity Maps from a bootstrap snapshot (LS-0718 progressive spine). */
export function buildEntityDirectory(data: BootstrapData | null | undefined): EntityDirectory {
  if (!data) return emptyDirectory()
  return {
    issue: indexById(data.issues),
    project: indexById(data.projects),
    team: indexById(data.teams),
    user: indexById(data.users),
    state: indexById(data.states),
    label: indexById(data.labels),
    cycle: indexById(data.cycles),
    initiative: indexById(data.initiatives),
    document: indexById(data.documents),
    notification: indexById(data.notifications),
    favorite: indexById(data.favorites),
    agentSession: indexById(data.agentSessions),
    ask: indexById(data.asks),
    savedView: indexById(data.savedViews),
  }
}

export function getEntityById(
  directory: EntityDirectory,
  type: string,
  id: string,
): unknown | undefined {
  const kind = normalizeEntityType(type)
  const collection = directory[kind as WorkspaceEntityType] as Map<string, unknown> | undefined
  return collection?.get(id)
}
