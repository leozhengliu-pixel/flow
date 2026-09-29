import type { ActivityEvent, WorkflowState } from '@/types/flow'

export type StatusStint = {
  key: string
  id?: string
  name: string
  type: string
  color?: string
  ms: number
  current: boolean
}

type StateRef = { id?: string; name: string; type?: string }

const TYPE_ORDER: Record<string, number> = { triage: 0, backlog: 1, unstarted: 2, started: 3, completed: 4, canceled: 5, duplicate: 5 }
const TRIAGE: StateRef = { id: 'triage', name: 'Triage', type: 'triage' }

/**
 * Linear's "Time in status": every status the issue has been in, with the total time spent in each, ordered like the
 * workflow. Rebuilt from the status-change activity log; time in the triage queue (a backlog state before
 * `triagedAt`) counts as Triage.
 */
export function timeInStatus({ activities, createdAt, current, states = [], triagedAt, inTriage = false, now = Date.now() }: {
  activities: ActivityEvent[]
  createdAt: string
  current: StateRef
  states?: Pick<WorkflowState, 'id' | 'name' | 'type' | 'color' | 'position'>[]
  triagedAt?: string | null
  inTriage?: boolean
  now?: number
}): StatusStint[] {
  const changes = activities
    .filter(activity => typeof activity.metadata?.state === 'string')
    .sort((left, right) => Date.parse(left.createdAt) - Date.parse(right.createdAt))
  const first = changes[0]?.metadata
  let state: StateRef = first
    ? { id: str(first.stateBeforeId), name: str(first.stateBefore) ?? current.name, type: str(first.stateBeforeType) }
    : current
  let start = Date.parse(createdAt)
  const triageEnd = inTriage ? now : triagedAt ? Date.parse(triagedAt) : undefined
  const totals = new Map<string, { ref: StateRef; ms: number }>()
  const resolve = (ref: StateRef): StateRef => {
    if (ref.type === 'triage') return ref
    const known = states.find(item => (ref.id && item.id === ref.id) || item.name === ref.name)
    return known ? { id: known.id, name: known.name, type: known.type } : ref
  }
  const add = (raw: StateRef, from: number, to: number) => {
    if (!(to > from)) return
    const ref = resolve(raw)
    const key = keyOf(ref)
    const entry = totals.get(key) ?? { ref, ms: 0 }
    entry.ms += to - from
    totals.set(key, entry)
  }
  const spend = (ref: StateRef, from: number, to: number) => {
    const backlog = (ref.type ?? typeOf(ref, states)) === 'backlog'
    if (backlog && triageEnd !== undefined && from < triageEnd) {
      add(TRIAGE, from, Math.min(to, triageEnd))
      add(ref, triageEnd, to)
    } else add(ref, from, to)
  }
  for (const change of changes) {
    const at = Date.parse(change.createdAt)
    spend(state, start, at)
    state = { id: str(change.metadata.stateId), name: str(change.metadata.state) ?? current.name, type: str(change.metadata.stateType) }
    start = at
  }
  const currentRef = inTriage ? TRIAGE : resolve({ ...current, id: current.id ?? state.id })
  spend(state, start, now)
  if (!totals.has(keyOf(currentRef))) totals.set(keyOf(currentRef), { ref: currentRef, ms: 0 })

  const rank = (ref: StateRef) => {
    const known = states.find(item => (ref.id && item.id === ref.id) || item.name === ref.name)
    const type = ref.type ?? known?.type ?? 'unstarted'
    return [TYPE_ORDER[type] ?? 2, known?.position ?? 0] as const
  }
  return [...totals.entries()]
    .map(([key, { ref, ms }]) => {
      const known = states.find(item => (ref.id && item.id === ref.id) || item.name === ref.name)
      return { key, id: known?.id ?? ref.id, name: known?.name ?? ref.name, type: ref.type ?? known?.type ?? 'unstarted', color: known?.color, ms, current: key === keyOf(currentRef) }
    })
    .sort((left, right) => {
      const [lt, lp] = rank(left), [rt, rp] = rank(right)
      return lt - rt || lp - rp
    })
}

/** Linear's compact durations: 6s, 2m, 4h, 3d, 2w, 5mo, 1y. */
export function formatStatusDuration(ms: number) {
  const seconds = Math.max(0, Math.floor(ms / 1000))
  const units: [number, string][] = [[365 * 86400, 'y'], [30 * 86400, 'mo'], [7 * 86400, 'w'], [86400, 'd'], [3600, 'h'], [60, 'm']]
  for (const [size, suffix] of units) if (seconds >= size) return `${Math.floor(seconds / size)}${suffix}`
  return `${seconds}s`
}

function keyOf(ref: StateRef) {
  return ref.type === 'triage' ? 'triage' : ref.id || `name:${ref.name}`
}

function typeOf(ref: StateRef, states: Pick<WorkflowState, 'id' | 'name' | 'type'>[]) {
  return states.find(item => (ref.id && item.id === ref.id) || item.name === ref.name)?.type
}

function str(value: unknown) {
  return typeof value === 'string' && value ? value : undefined
}
