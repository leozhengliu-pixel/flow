import { useEffect, useMemo, useSyncExternalStore } from 'react'
import { fetchIssueRecord, listProjectRecords } from '@/lib/api'
import type { BootstrapData, Issue, Project } from '@/types/flow'
import { isAgentIdentifier, resolveAgentEntity, type AgentEntity, type AgentEntityTarget } from './agent-entity-refs'

/**
 * An answer's chip may name a record that is not in workspace data: paged workspaces (~75k issues) never hold every
 * issue in the client, the project list view holds no projects, and the agent can create records (a new issue, a new
 * project) after the page loaded. Those are fetched by id / identifier through the same endpoints the detail pages use,
 * once per record, and shared by every chip, hover card and list row that names them.
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
    value => {
      // A record named by identifier / slug is also answered when it is named by id (and the other way round).
      if (value) for (const alias of [value.id, kind === 'issue' ? (value as Issue).identifier : (value as Project).slugId]) if (alias) records.set(recordKey(workspace, kind, alias), { value, at: Date.now() })
      publish(key, value ? { value, at: Date.now() } : { missing: true, at: Date.now() })
    },
    () => publish(key, { missing: true, at: Date.now() }),
  )
}

/** The cached issue / project for a key, when a fetch already found it. */
export function peekAgentRecord(workspace: string, kind: RecordKind, id: string): Issue | Project | undefined {
  return records.get(recordKey(workspace, kind, id))?.value
}

/** Loads the records the targets name and resolves once each one is found or known to be missing. */
export function settleAgentRecords(workspace: string, targets: Array<Pick<AgentEntityTarget, 'kind' | 'id'>>): Promise<void> {
  const wanted = targets.filter((target): target is { kind: RecordKind; id: string } => target.kind === 'issue' || target.kind === 'project')
  wanted.forEach(target => loadAgentRecord(workspace, target.kind, target.id))
  return new Promise(resolve => {
    const done = () => wanted.every(target => {
      const entry = records.get(recordKey(workspace, target.kind, target.id))
      return Boolean(entry && !entry.loading && (entry.value || entry.missing))
    })
    if (done()) { resolve(); return }
    const unsubscribe = subscribe(() => { if (done()) { unsubscribe(); resolve() } })
  })
}

/** Re-renders when any record loads while `active` (a surface that waits on records): lets it convert its references again once they arrive. */
export function useAgentRecordVersion(active = true) {
  return useSyncExternalStore(subscribe, () => active ? version : 0, () => 0)
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

const ISSUE_IDENTIFIER = /^[A-Za-z][A-Za-z0-9]*-\d+$/

/**
 * Kinds the client may not hold and can fetch by id. An issue named by identifier is only worth a request when its
 * prefix is a real team key ("UTF-8" or "DEV-404" in a workspace without DEV stay plain text without a round trip).
 */
function fetchableTarget(data: BootstrapData, kind: AgentEntityTarget['kind'], id: string): kind is RecordKind {
  if (kind === 'project') return true
  if (kind !== 'issue') return false
  return !ISSUE_IDENTIFIER.test(id) || isAgentIdentifier(data, id)
}

/**
 * Resolves an answer's entity target: from workspace data when it holds it, otherwise (issues / projects the client does
 * not hold: paged workspaces, records created this session) from a cached by-id fetch. Chips for entities that cannot be
 * found at all report `missing` so they fall back to plain text.
 */
export function useAgentEntity(data: BootstrapData | undefined, target: Pick<AgentEntityTarget, 'kind' | 'id'> & Partial<AgentEntityTarget>): AgentEntityState {
  const { kind, id } = target
  const workspace = data?.workspace.urlKey ?? ''
  const { href, label } = target
  const held = useMemo(() => data ? resolveAgentEntity(data, { kind, id, href, label }) : undefined, [data, kind, id, href, label])
  const fetchable = Boolean(data) && !held && fetchableTarget(data as BootstrapData, kind, id)
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
