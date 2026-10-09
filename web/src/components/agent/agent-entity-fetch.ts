import { useEffect, useMemo, useSyncExternalStore } from 'react'
import { fetchIssueRecord, listProjectRecords } from '@/lib/api'
import type { BootstrapData, Issue, Project } from '@/types/flow'
import { resolveAgentEntity, type AgentEntity, type AgentEntityTarget } from './agent-entity-refs'

/**
 * Paged workspaces (~75k issues) never hold every issue in the client, and the project list view holds no projects, so an
 * answer's chip may name a record that is not in workspace data. Those are fetched by id through the same endpoints the
 * detail pages use, once per record, and shared by every chip, hover card and list row that names them.
 */
type RecordKind = 'issue' | 'project'
type Entry = { value?: Issue | Project; missing?: boolean; loading?: boolean; at: number }

const FOUND_TTL = 2 * 60_000
const MISSING_TTL = 15_000

const records = new Map<string, Entry>()
const listeners = new Set<() => void>()
let version = 0

function recordKey(workspace: string, kind: RecordKind, id: string) {
  return `${workspace}|${kind}|${id.toLowerCase()}`
}

function publish(key: string, entry: Entry) {
  records.set(key, entry)
  version += 1
  listeners.forEach(listener => listener())
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

async function fetchRecord(kind: RecordKind, id: string, workspace: string): Promise<Issue | Project | undefined> {
  if (kind === 'issue') return fetchIssueRecord(id, undefined, workspace)
  const page = await listProjectRecords({ filter: [{ field: 'project', operator: 'is', values: [id] }], archived: 'all', limit: 1 }, undefined, workspace)
  return page.items[0]
}

/** Starts loading a record unless a fresh answer (found or missing) is cached or a request is in flight. */
export function loadAgentRecord(workspace: string, kind: RecordKind, id: string) {
  const key = recordKey(workspace, kind, id)
  const current = records.get(key)
  if (current?.loading) return
  if (current && Date.now() - current.at < (current.missing ? MISSING_TTL : FOUND_TTL)) return
  publish(key, { ...current, loading: true, at: current?.at ?? Date.now() })
  fetchRecord(kind, id, workspace).then(
    value => publish(key, value ? { value, at: Date.now() } : { missing: true, at: Date.now() }),
    () => publish(key, { missing: true, at: Date.now() }),
  )
}

/** Test hook: forget every fetched record. */
export function resetAgentRecordCache() {
  records.clear()
  version += 1
  listeners.forEach(listener => listener())
}

export type AgentEntityState =
  | { status: 'ready'; entity: AgentEntity }
  | { status: 'loading' }
  | { status: 'missing' }

/** Kinds the client may not hold in paged mode and can fetch by id. */
function fetchableKind(kind: AgentEntityTarget['kind']): kind is RecordKind {
  return kind === 'issue' || kind === 'project'
}

/**
 * Resolves an answer's entity target: from workspace data when it holds it, otherwise (paged issues / projects) from a
 * cached by-id fetch. Chips for entities that cannot be found at all report `missing` so they fall back to plain text.
 */
export function useAgentEntity(data: BootstrapData | undefined, target: Pick<AgentEntityTarget, 'kind' | 'id'> & Partial<AgentEntityTarget>): AgentEntityState {
  const { kind, id } = target
  const workspace = data?.workspace.urlKey ?? ''
  const { href, label } = target
  const held = useMemo(() => data ? resolveAgentEntity(data, { kind, id, href, label }) : undefined, [data, kind, id, href, label])
  const fetchable = Boolean(data) && !held && fetchableKind(kind) && Boolean(data?.issueCollectionPaged)
  const key = fetchable ? recordKey(workspace, kind as RecordKind, id) : ''
  useEffect(() => {
    if (fetchable) loadAgentRecord(workspace, kind as RecordKind, id)
  }, [fetchable, workspace, kind, id])
  useSyncExternalStore(subscribe, () => version, () => 0)
  if (held) return { status: 'ready', entity: held }
  if (!fetchable) return { status: 'missing' }
  const entry = records.get(key)
  if (entry?.value) return { status: 'ready', entity: kind === 'issue' ? { kind: 'issue', issue: entry.value as Issue } : { kind: 'project', project: entry.value as Project } }
  return entry?.missing ? { status: 'missing' } : { status: 'loading' }
}
