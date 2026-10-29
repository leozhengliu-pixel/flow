import { ApiError } from './api-client'
import type { Issue, IssueUpdateInput } from '@/types/flow'

/*
 * Issue writes are serialised per issue. An issue PATCH that carries
 * `expectedVersion` is a 409 when the stored version moved on, and every write
 * that changes an issue raises its version by exactly one. Without a queue, two
 * quick edits (a status then a priority, a shortcut while a picker's request is
 * in flight, a bulk edit next to a single one) both send the version the screen
 * showed, and the second is refused because of the first.
 *
 * The queue runs one write per issue at a time and remembers the run of
 * versions its own writes produced (`from` -> `to`). A queued write whose
 * expected version falls inside that run was based on a state only our own
 * writes have moved past, so it is sent with the newest version instead. Any
 * other expected version is sent as given: a change from another session still
 * answers 409.
 *
 * A write can also change other issues as a side effect (closing the last
 * sub-issue closes its parent, closing a parent closes its sub-issues). The
 * server lists them with their new versions in the response (`affectedIssues`),
 * and they are recorded the same way, as versions our own write produced, so an
 * edit of the parent made before the realtime update arrives is not a 409.
 */

type Lineage = { from: number; to: number }
type Conflict = { version: number; error: ApiError<Issue> }
type WaitingUpdate = { path: string; input: IssueUpdateInput; promise: Promise<Issue> }

const tails = new Map<string, Promise<unknown>>()
const lineages = new Map<string, Lineage>()
const conflicts = new Map<string, Conflict>()
const waiting = new Map<string, WaitingUpdate>()

function enqueue<T>(ids: string[], run: () => Promise<T>): Promise<T> {
  const previous = ids.flatMap(id => tails.get(id) ?? [])
  const task = previous.length ? Promise.all(previous).then(run) : run()
  const tail: Promise<unknown> = task.then(() => undefined, () => undefined)
  for (const id of ids) tails.set(id, tail)
  void tail.then(() => {
    for (const id of ids) {
      if (tails.get(id) !== tail) continue
      tails.delete(id)
      conflicts.delete(id)
    }
  })
  return task
}

/** The version to send for an edit based on `expected`, after this tab's own earlier writes. */
function versionToSend(id: string, expected: number | undefined) {
  if (expected === undefined) return undefined
  const lineage = lineages.get(id)
  return lineage && expected >= lineage.from && expected < lineage.to ? lineage.to : expected
}

function versionOf(value: unknown) {
  const version = (value as Partial<Issue> | null | undefined)?.version
  return typeof version === 'number' && Number.isFinite(version) ? version : undefined
}

/** Records the versions of the other issues a successful write of this tab changed. */
function recordAffected(result: unknown) {
  const written = result as Partial<Issue> | null | undefined
  const affected = written?.affectedIssues
  if (!written || !Array.isArray(affected)) return
  // Each was raised by this write, from the version just below.
  for (const issue of affected) if (issue?.id) recordWrite(issue.id, undefined, issue)
  // Only the queue reads it; the issue the caller gets is the issue.
  delete written.affectedIssues
}

/** Records the version a successful write of this tab produced. */
function recordWrite(id: string, sent: number | undefined, result: unknown) {
  recordAffected(result)
  const version = versionOf(result)
  if (version === undefined) return
  const lineage = lineages.get(id)
  if (sent !== undefined) {
    // Unchanged (a repeat of stored values keeps the version).
    if (version <= sent) return
    lineages.set(id, lineage && lineage.to === sent ? { from: lineage.from, to: version } : { from: sent, to: version })
    return
  }
  // A write without a version merges into the stored issue; one that changed
  // it moved the version from `version - 1`.
  if (lineage && version <= lineage.to) return
  lineages.set(id, lineage && lineage.to === version - 1 ? { from: lineage.from, to: version } : { from: version - 1, to: version })
}

function recordFailure(id: string, error: unknown) {
  if (!(error instanceof ApiError) || error.status !== 409) return
  lineages.delete(id)
  const version = versionOf(error.current)
  if (version !== undefined) conflicts.set(id, { version, error: error as ApiError<Issue> })
}

/** Runs one write to an issue after the writes queued before it. */
export function queueIssueWrite<T>(id: string, write: () => Promise<T>): Promise<T> {
  waiting.delete(id)
  return enqueue([id], () => write().then(
    result => { recordWrite(id, undefined, result); return result },
    error => { recordFailure(id, error); throw error },
  ))
}

/** A field write that can merge with the next one queued behind it. */
function mergeable(input: IssueUpdateInput) {
  return input.documentUpdateIds === undefined && input.expectedDocumentVersion === undefined
}

/**
 * Queues a PATCH of one issue. Consecutive field edits that are still waiting
 * for an earlier write of the same issue are sent as one request.
 */
export function queueIssueUpdate(id: string, path: string, input: IssueUpdateInput, send: (input: IssueUpdateInput) => Promise<Issue>): Promise<Issue> {
  const pending = waiting.get(id)
  if (pending && pending.path === path && mergeable(pending.input) && mergeable(input) && pending.input.expectedVersion === input.expectedVersion) {
    Object.assign(pending.input, input)
    return pending.promise
  }
  waiting.delete(id)
  // Only an edit that has to wait can take later edits in.
  const mustWait = tails.has(id)
  const body: IssueUpdateInput = { ...input }
  const promise = enqueue([id], () => {
    if (waiting.get(id)?.input === body) waiting.delete(id)
    const conflict = conflicts.get(id)
    // Queued behind a write that met another session's change: this edit was
    // based on the same outdated issue, so it shares that conflict.
    if (conflict && body.expectedVersion !== undefined && body.expectedVersion < conflict.version) return Promise.reject(conflict.error)
    const expectedVersion = versionToSend(id, body.expectedVersion)
    return send(expectedVersion === undefined ? body : { ...body, expectedVersion }).then(
      result => { recordWrite(id, expectedVersion, result); return result },
      error => { recordFailure(id, error); throw error },
    )
  })
  if (mustWait) waiting.set(id, { path, input: body, promise })
  return promise
}

/** Queues a write of several issues (a batch update) behind each issue's earlier writes. */
export function queueIssuesWrite(ids: string[], write: () => Promise<Issue[]>): Promise<Issue[]> {
  const unique = [...new Set(ids)]
  for (const id of unique) waiting.delete(id)
  return enqueue(unique, () => write().then(
    issues => { for (const issue of issues ?? []) if (issue?.id) recordWrite(issue.id, undefined, issue); return issues },
    error => { for (const id of unique) recordFailure(id, error); throw error },
  ))
}

/** Clears the queue state (tests). */
export function resetIssueMutationQueue() {
  tails.clear()
  lineages.clear()
  conflicts.clear()
  waiting.clear()
}
