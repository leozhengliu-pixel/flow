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

/**
 * Resolve by UUID first, then common secondary keys (issue identifier, slugId, team key).
 * Used by hydrateModel so markdown/AI refs like ENG-12 or project-one warm the store (LS-0738/0736).
 */
export function resolveEntity(
  directory: EntityDirectory,
  type: string,
  idOrKey: string,
): unknown | undefined {
  if (!idOrKey) return undefined
  const direct = getEntityById(directory, type, idOrKey)
  if (direct) return direct

  const kind = normalizeEntityType(type)
  switch (kind) {
    case 'issue': {
      const needle = idOrKey.toUpperCase()
      for (const issue of directory.issue.values()) {
        if (issue.identifier?.toUpperCase() === needle) return issue
      }
      return undefined
    }
    case 'project': {
      for (const project of directory.project.values()) {
        if (project.slugId === idOrKey || project.id === idOrKey) return project
      }
      return undefined
    }
    case 'initiative': {
      for (const initiative of directory.initiative.values()) {
        if (
          ('slugId' in initiative && initiative.slugId === idOrKey) ||
          initiative.id === idOrKey
        ) {
          return initiative
        }
      }
      return undefined
    }
    case 'document': {
      for (const document of directory.document.values()) {
        if (
          ('slugId' in document && document.slugId === idOrKey) ||
          document.id === idOrKey
        ) {
          return document
        }
      }
      return undefined
    }
    case 'team': {
      for (const team of directory.team.values()) {
        if (team.key === idOrKey || team.id === idOrKey) return team
      }
      return undefined
    }
    case 'user': {
      for (const user of directory.user.values()) {
        if (user.id === idOrKey || user.name === idOrKey) return user
      }
      return undefined
    }
    default:
      return undefined
  }
}
