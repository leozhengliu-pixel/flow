import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { batchUpdateIssues, toggleIssueReaction, updateIssue, updateIssueRecord } from './api'
import { resetIssueMutationQueue } from './issue-mutation-queue'
import type { Issue, IssueUpdateInput } from '@/types/flow'

type StoredIssue = { id: string; version: number; fields: Record<string, unknown> }
type Logged = { method: string; path: string; body: Record<string, unknown> }

// A server with the issue write contract: a write that changes an issue raises
// its version by one, a repeat keeps it, and a stale expectedVersion is a 409.
// It handles a request when it arrives and answers after a delay, so writes
// that are not serialised overlap the way they do over a network.
function fakeServer() {
  const issues = new Map<string, StoredIssue>()
  const log: Logged[] = []
  const view = (issue: StoredIssue) => ({ id: issue.id, version: issue.version, ...issue.fields }) as unknown as Issue
  const apply = (issue: StoredIssue, input: IssueUpdateInput) => {
    const { expectedVersion: _expected, documentUpdateIds: _updates, expectedDocumentVersion: _documentVersion, ...fields } = input
    const changed = Object.entries(fields).some(([key, value]) => JSON.stringify(issue.fields[key]) !== JSON.stringify(value))
    if (!changed) return
    issue.fields = { ...issue.fields, ...fields }
    issue.version += 1
  }
  const reply = (payload: unknown, status = 200) => new Promise<Response>(resolve => setTimeout(() => resolve({
    ok: status < 300, status, json: async () => payload, clone() { return this },
  } as unknown as Response), 5))
  const handle = (url: string, init: RequestInit = {}) => {
    const method = init.method ?? 'GET'
    const path = new URL(url, 'http://flow.local').pathname
    const body = init.body ? JSON.parse(String(init.body)) : {}
    log.push({ method, path, body })
    const single = path.match(/^\/api\/(?:issues|issue-records)\/([^/]+)$/)
    if (method === 'PATCH' && single) {
      const issue = issues.get(decodeURIComponent(single[1]))
      if (!issue) return reply({ error: 'issue not found' }, 404)
      if (body.expectedVersion !== undefined && body.expectedVersion !== issue.version) return reply({ error: 'This item changed in another session', code: 'VERSION_CONFLICT', current: view(issue) }, 409)
      apply(issue, body)
      return reply(view(issue))
    }
    if (method === 'POST' && /^\/api\/(?:issues|issue-records)\/batch$/.test(path)) {
      const updated = (body.issueIds as string[]).map(id => { const issue = issues.get(id)!; apply(issue, body.update); return view(issue) })
      return reply(updated)
    }
    const reaction = path.match(/^\/api\/issues\/([^/]+)\/reactions$/)
    if (method === 'POST' && reaction) {
      const issue = issues.get(reaction[1])!
      const reactions = (issue.fields.reactions as string[] | undefined) ?? []
      issue.fields = { ...issue.fields, reactions: [...reactions, body.emoji] }
      issue.version += 1
      return reply(view(issue))
    }
    return reply({ error: 'unexpected' }, 500)
  }
  return {
    issues,
    log,
    fetch: vi.fn(handle),
    seed(id: string, fields: Record<string, unknown> = {}) { issues.set(id, { id, version: 1, fields }) },
    /** A change made by another session. */
    foreign(id: string, fields: Record<string, unknown>) { apply(issues.get(id)!, fields) },
    patches(id: string) { return log.filter(entry => entry.method === 'PATCH' && entry.path.endsWith(`/${id}`)) },
  }
}

describe('issue writes from one tab', () => {
  let server: ReturnType<typeof fakeServer>

  beforeEach(() => {
    resetIssueMutationQueue()
    server = fakeServer()
    vi.stubGlobal('fetch', server.fetch)
    window.history.replaceState({}, '', '/workspace/issues/all')
  })
  afterEach(() => {
    vi.unstubAllEnvs()
    vi.unstubAllGlobals()
  })

  for (const paged of [false, true]) {
    const route = paged ? '/api/issue-records' : '/api/issues'
    it(`rapid edits of one issue on ${route} chain their versions and never conflict`, async () => {
      if (paged) vi.stubEnv('VITE_PAGED_ISSUES', 'true')
      server.seed('issue-1', { stateId: 'todo', priority: 0 })
      // Status, then priority, then labels, each from the screen's version 1,
      // all before the first answer.
      const results = await Promise.allSettled([
        updateIssue('issue-1', { stateId: 'started', expectedVersion: 1 }),
        updateIssue('issue-1', { priority: 2, expectedVersion: 1 }),
        updateIssue('issue-1', { labelIds: ['bug'], expectedVersion: 1 }),
      ])
      expect(results.map(result => result.status)).toEqual(['fulfilled', 'fulfilled', 'fulfilled'])
      const patches = server.patches('issue-1')
      expect(patches.every(entry => entry.path === `${route}/issue-1`)).toBe(true)
      // The two edits that waited for the first went as one request, based on
      // the version the first returned.
      expect(patches.map(entry => entry.body)).toEqual([
        { stateId: 'started', expectedVersion: 1 },
        { priority: 2, labelIds: ['bug'], expectedVersion: 2 },
      ])
      expect(server.issues.get('issue-1')).toEqual({ id: 'issue-1', version: 3, fields: { stateId: 'started', priority: 2, labelIds: ['bug'] } })
      const last = (results[2] as PromiseFulfilledResult<Issue>).value
      expect(last.version).toBe(3)
    })
  }

  it('keeps chaining edits sent one after another while earlier ones are in flight', async () => {
    server.seed('issue-1', { priority: 0 })
    const first = updateIssue('issue-1', { priority: 1, expectedVersion: 1 })
    const second = updateIssue('issue-1', { priority: 2, expectedVersion: 1 })
    await first
    // A shortcut pressed after the first answer, from a screen that still shows version 1.
    const third = updateIssueRecord('issue-1', { priority: 3, expectedVersion: 1 })
    await expect(Promise.all([second, third])).resolves.toHaveLength(2)
    expect(server.patches('issue-1').map(entry => entry.body.expectedVersion)).toEqual([1, 2, 3])
    expect(server.log.some(entry => entry.path.startsWith('/api/issue-records/'))).toBe(true)
    expect(server.issues.get('issue-1')?.version).toBe(4)
    expect(server.issues.get('issue-1')?.fields.priority).toBe(3)
  })

  it('sends a description save on its own, chained after a field edit', async () => {
    server.seed('issue-1', { priority: 0, description: '' })
    const results = await Promise.allSettled([
      updateIssue('issue-1', { priority: 1, expectedVersion: 1 }),
      updateIssue('issue-1', { description: 'Body', documentUpdateIds: ['u1'], expectedDocumentVersion: 0, expectedVersion: 1 }),
      updateIssue('issue-1', { priority: 2, expectedVersion: 1 }),
    ])
    expect(results.every(result => result.status === 'fulfilled')).toBe(true)
    expect(server.patches('issue-1').map(entry => [entry.body.expectedVersion, Object.keys(entry.body).sort().join(',')])).toEqual([
      [1, 'expectedVersion,priority'],
      [2, 'description,documentUpdateIds,expectedDocumentVersion,expectedVersion'],
      [3, 'expectedVersion,priority'],
    ])
    expect(server.issues.get('issue-1')).toMatchObject({ version: 4, fields: { priority: 2, description: 'Body' } })
  })

  it('surfaces a change from another session once and accepts the next edit from the refreshed issue', async () => {
    server.seed('issue-1', { title: 'Original', priority: 0 })
    server.foreign('issue-1', { title: 'Renamed elsewhere' })
    const first = updateIssue('issue-1', { priority: 1, expectedVersion: 1 })
    const queued = updateIssue('issue-1', { stateId: 'done', expectedVersion: 1 })
    const [a, b] = await Promise.allSettled([first, queued])
    expect(a).toMatchObject({ status: 'rejected', reason: { status: 409, code: 'VERSION_CONFLICT', current: { version: 2, title: 'Renamed elsewhere' } } })
    // The edit queued behind it was based on the same outdated issue: it
    // shares the conflict instead of making a second request.
    expect(b).toMatchObject({ status: 'rejected' })
    expect((b as PromiseRejectedResult).reason).toBe((a as PromiseRejectedResult).reason)
    expect(server.log.filter(entry => entry.method === 'PATCH')).toHaveLength(1)
    await expect(updateIssue('issue-1', { priority: 1, expectedVersion: 2 })).resolves.toMatchObject({ version: 3, priority: 1, title: 'Renamed elsewhere' })
  })

  it('still refuses an edit based on a version our own writes did not produce', async () => {
    server.seed('issue-1', { priority: 0 })
    await updateIssue('issue-1', { priority: 1, expectedVersion: 1 })
    server.foreign('issue-1', { title: 'Elsewhere' })
    // From a screen that missed both changes.
    await expect(updateIssue('issue-1', { priority: 2, expectedVersion: 1 })).rejects.toMatchObject({ status: 409 })
    expect(server.patches('issue-1').map(entry => entry.body.expectedVersion)).toEqual([1, 2])
    expect(server.issues.get('issue-1')?.fields.priority).toBe(1)
  })

  it('orders a bulk edit between single edits of the same issues', async () => {
    server.seed('issue-1', { priority: 0 })
    server.seed('issue-2', { priority: 0 })
    const single = updateIssue('issue-1', { stateId: 'started', expectedVersion: 1 })
    const bulk = batchUpdateIssues(['issue-1', 'issue-2'], { priority: 1 })
    const after = updateIssue('issue-1', { priority: 3, expectedVersion: 1 })
    const other = updateIssue('issue-2', { stateId: 'done', expectedVersion: 1 })
    const results = await Promise.allSettled([single, bulk, after, other])
    expect(results.map(result => result.status)).toEqual(['fulfilled', 'fulfilled', 'fulfilled', 'fulfilled'])
    // The bulk edit waited for the single one, and the later edits for it.
    expect(server.log.map(entry => `${entry.method} ${entry.path} ${entry.body.expectedVersion ?? ''}`.trim())).toEqual([
      'PATCH /api/issues/issue-1 1',
      'POST /api/issues/batch',
      'PATCH /api/issues/issue-1 3',
      'PATCH /api/issues/issue-2 2',
    ])
    expect(server.issues.get('issue-1')).toMatchObject({ version: 4, fields: { stateId: 'started', priority: 3 } })
    expect(server.issues.get('issue-2')).toMatchObject({ version: 3, fields: { stateId: 'done', priority: 1 } })
  })

  it('lets a single edit follow a bulk edit that is still in flight', async () => {
    server.seed('issue-1', { priority: 0 })
    const bulk = batchUpdateIssues(['issue-1'], { priority: 4 })
    const single = updateIssue('issue-1', { labelIds: ['bug'], expectedVersion: 1 })
    await expect(Promise.all([bulk, single])).resolves.toHaveLength(2)
    expect(server.patches('issue-1').map(entry => entry.body.expectedVersion)).toEqual([2])
    expect(server.issues.get('issue-1')).toMatchObject({ version: 3, fields: { priority: 4, labelIds: ['bug'] } })
  })

  it('chains an edit after a reaction to the same issue', async () => {
    server.seed('issue-1', { priority: 0 })
    const reaction = toggleIssueReaction('issue-1', '👍')
    const edit = updateIssue('issue-1', { priority: 2, expectedVersion: 1 })
    await expect(Promise.all([reaction, edit])).resolves.toHaveLength(2)
    expect(server.patches('issue-1').map(entry => entry.body.expectedVersion)).toEqual([2])
  })

  it('keeps the version after a repeat of stored values', async () => {
    server.seed('issue-1', { priority: 2 })
    const repeat = updateIssue('issue-1', { priority: 2, expectedVersion: 1 })
    const edit = updateIssue('issue-1', { stateId: 'started', expectedVersion: 1 })
    await expect(Promise.all([repeat, edit])).resolves.toHaveLength(2)
    expect(server.patches('issue-1').map(entry => entry.body.expectedVersion)).toEqual([1, 1])
    expect(server.issues.get('issue-1')?.version).toBe(2)
  })
})
