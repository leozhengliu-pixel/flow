import { teamHierarchy } from '@/lib/team-hierarchy'
import type { BootstrapData, WorkflowState } from '@/types/flow'

type StatusRef = { id: string; name: string; type: string; color?: string }
type StatusGroupData = Pick<BootstrapData, 'states' | 'teams'> & Partial<Pick<BootstrapData, 'teamSettings'>>

const TYPE_ORDER: Record<string, number> = { backlog: 0, unstarted: 1, started: 2, completed: 3, canceled: 4 }

/**
 * The insight group a status belongs to. Teams with their own workflows each have their own status
 * IDs, so across a team and its sub-teams "Done" would otherwise show up once per workflow. Statuses
 * merge when they share a type and a name; names compare trimmed and case-insensitively because
 * that is how people read them ("Done" and "done " are the same column), while the type is part of
 * the key so a completed "Done" never merges with a canceled one.
 */
export function statusGroupId(state: Pick<StatusRef, 'name' | 'type'>) {
  return `status:${state.type}:${state.name.trim().toLocaleLowerCase()}`
}

export type StatusGroupIndex = ReturnType<typeof buildStatusGroupIndex>

const cache = new WeakMap<object, { data: StatusGroupData; index: StatusGroupIndex }>()
/** Resolves statuses to their merged insight group and ranks them for display and ordering (memoised per workflow data). */
export function statusGroupIndex(data: StatusGroupData): StatusGroupIndex {
  const cached = cache.get(data.states)
  if (cached && cached.data.teams === data.teams && cached.data.teamSettings === data.teamSettings) return cached.index
  const index = buildStatusGroupIndex(data)
  cache.set(data.states, { data: { states: data.states, teams: data.teams, teamSettings: data.teamSettings }, index })
  return index
}

function buildStatusGroupIndex(data: StatusGroupData) {
  const states = new Map(data.states.map((state, index) => [state.id, { state, index }]))
  const hierarchy = teamHierarchy(data.teams, data.teamSettings ?? {})
  const members = new Map<string, WorkflowState[]>()
  for (const state of data.states) { const id = statusGroupId(state); members.set(id, [...members.get(id) ?? [], state]) }
  // Workspace statuses first, then the shallowest team (the parent in a team-and-sub-teams view), its workflow position, then the workspace order.
  const rank = (id: string): [number, number, number] => {
    const entry = states.get(id)
    if (!entry) return [Number.POSITIVE_INFINITY, Number.POSITIVE_INFINITY, Number.POSITIVE_INFINITY]
    const depth = entry.state.teamId ? hierarchy.ancestors.get(entry.state.teamId)?.length ?? 0 : -1
    return [depth, entry.state.position ?? 0, entry.index]
  }
  const compare = (left: string, right: string) => {
    const a = rank(left), b = rank(right)
    return a[0] - b[0] || a[1] - b[1] || a[2] - b[2] || left.localeCompare(right)
  }
  /** Every workspace status ID in a group, best representative first. */
  const stateIds = (groupId: string) => (members.get(groupId) ?? []).map(state => state.id).sort(compare)
  return {
    /** The group of a status ID, or of a status the workspace does not list (Triage); undefined for an unknown ID. */
    groupOf(state: string | StatusRef) {
      if (typeof state !== 'string') return statusGroupId(states.get(state.id)?.state ?? state)
      const known = states.get(state)?.state
      return known ? statusGroupId(known) : undefined
    },
    stateIds,
    /** The workspace status that names and colours a group (see `rank`). */
    representative(groupId: string): WorkflowState | undefined { const [id] = stateIds(groupId); return id ? states.get(id)?.state : undefined },
    /** Sorts status IDs best representative first. */
    compare,
    /** Workflow order: Triage, then type, then the representative's position. */
    order(state: StatusRef): [number, number] {
      if (state.id === 'triage') return [-1, 0]
      return [TYPE_ORDER[state.type] ?? 99, states.get(state.id)?.state.position ?? Number.POSITIVE_INFINITY]
    },
  }
}
