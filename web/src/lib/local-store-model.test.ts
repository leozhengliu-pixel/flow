import { describe, expect, it, vi } from 'vitest'
import { applyLocalIssueView, compareLocalStoreSeq, createLocalIssueView, issueFromLocalRow, LOCAL_STORE_PULSE_CURSOR, LocalStoreReset, localStoreAuthority, localStoreBuildId, localStoreEnabled, planLocalChanges, projectLocalCollection, projectLocalIssueRecord } from './local-store-model'
import type { BootstrapData, SyncChanges } from '@/types/flow'

const after = '9007199254740993'
const next = '9007199254740996'
const epoch = 'team-access-2'
const page: SyncChanges = {
  next, head: next, authzEpoch: epoch, hasMore: false, resyncRequired: false,
  transactions: [{
    seq: next, txId: 'edit', invalidations: [],
    upserts: [{ key: { kind: 'issue', id: 'a' }, completeness: 'summary', version: next, value: { id: 'a', title: 'New title', description: 'body', team: { id: 'team' } } }],
    removals: [{ key: { kind: 'document', id: 'b' }, version: next }],
  }],
}

describe('local store inputs', () => {
  it('defaults to the local store and requires the exact authority fields', () => {
    vi.stubEnv('VITE_SYNC_V2', 'false')
    expect(localStoreEnabled()).toBe(true)
    const scope = { viewerId: 'viewer', workspaceId: 'workspace', authzEpoch: epoch, head: next }
    expect(localStoreAuthority(scope)).toEqual(scope)
    expect(localStoreAuthority({ ...scope, head: 17 })).toBeUndefined()
    expect(localStoreAuthority({ ...scope, authzEpoch: '' })).toBeUndefined()
    vi.unstubAllEnvs()
  })

  it('records the entry build without the origin or query string', () => {
    const entry = document.createElement('script')
    entry.type = 'module'
    entry.src = '/assets/flow-build.js?v=1'
    document.head.append(entry)
    try { expect(localStoreBuildId()).toBe('/assets/flow-build.js') }
    finally { entry.remove() }
  })

  it('projects only active issue summaries and builds the lookup keys without retaining bodies or grants', () => {
    const data = { issues: [
      { id: 'one', team: { id: 'one-team' }, state: { id: 'open' }, parentId: 'parent', project: { id: 'project' }, assignee: { id: 'user' }, title: 'Active', description: 'editor body', permissions: ['other person'], shareToken: 'token' },
      { id: 'two', title: 'Archived', archivedAt: '2026-11-20' },
    ] } as unknown as BootstrapData
    expect([...projectLocalCollection(data, 'issue', after).values()]).toEqual([{
      id: 'one', title: 'Active', _v: null, _seq: after, parentId: 'parent', teamId: 'one-team', stateId: 'open', projectId: 'project', assigneeId: 'user',
    }])
  })

  it('accepts server-scanned gaps from unseen transactions and tracks large versions losslessly', () => {
    expect(compareLocalStoreSeq(after, next)).toBeLessThan(0)
    expect(planLocalChanges(page, after, epoch)).toEqual([
      { kind: 'issue', id: 'a', row: { id: 'a', _seq: next, _v: next, title: 'New title', teamId: 'team' } },
      { kind: 'document', id: 'b' },
    ])
    expect(planLocalChanges({ ...page, transactions: [] }, after, epoch)).toEqual([])
  })

  it('hydrates issue list fields without persisting bodies, grants or share keys', () => {
    const value = {
      id: 'a', isSummary: true, title: 'List issue', team: { id: 'team' }, state: { id: 'open' },
      hasContent: true, statusChangedAt: '2026-11-20T00:00:00Z',
      labels: [{ id: 'custom' }], subscriberIds: ['user'], shareToken: 'Share credential', permissions: [{}],
      description: 'Editor body', documentContent: { content: 'Editor body' }, attachments: [{ id: 'file' }],
    }
    const row = projectLocalIssueRecord('a', value, after, next)
    expect(row).toMatchObject({ _seq: after, _v: next, teamId: 'team', stateId: 'open', labels: [{ id: 'custom' }] })
    for (const key of ['description', 'documentContent', 'shareToken', 'permissions', 'attachments']) expect(row).not.toHaveProperty(key)
    expect(issueFromLocalRow(row)).toMatchObject({
      id: 'a', isSummary: true, description: '', hasContent: true, statusChangedAt: value.statusChangedAt,
      labels: [{ id: 'custom' }], subscriberIds: ['user'],
    })
    expect(() => projectLocalIssueRecord('a', { ...value, isSummary: false }, after, next)).toThrow(LocalStoreReset)
  })

  it('rejects invalidations, revocations, expiry and replay before any rows can commit', () => {
    for (const invalid of [
      { ...page, resyncRequired: true }, { ...page, authzEpoch: 'revoked' }, { ...page, next: after },
      { ...page, transactions: [page.transactions[0], page.transactions[0]] },
      { ...page, transactions: [{ ...page.transactions[0], invalidations: ['issues'] }] },
    ]) {
      expect(() => planLocalChanges(invalid, after, epoch)).toThrow(LocalStoreReset)
    }
  })

  it('replays issue edits alongside a Pulse delivery cursor without discarding the issue list', () => {
    const upserts = page.transactions[0].upserts.map(upsert => ({
      ...upsert,
      value: { ...upsert.value, isSummary: true, state: { id: 'open' } },
    }))
    const update = { ...page.transactions[0], upserts, removals: [], invalidations: [LOCAL_STORE_PULSE_CURSOR] }
    const delta = { ...page, transactions: [update] }
    expect(planLocalChanges(delta, after, epoch, true)).toMatchObject([
      { kind: 'issue', id: 'a', row: { id: 'a', _seq: next, _v: next, title: 'New title', teamId: 'team' } },
    ])
    expect(() => planLocalChanges(delta, after, epoch)).toThrow(LocalStoreReset)
    expect(() => planLocalChanges({ ...page, transactions: [{ ...update, invalidations: [...update.invalidations, 'settings'] }] }, after, epoch, true)).toThrow(LocalStoreReset)
  })

  it('shares only equal references and replaces rows after their batch commits', () => {
    const row = (id: string, displayName?: string) => projectLocalIssueRecord(id, {
      id, title: id, isSummary: true, team: { id: 'team' }, state: { id: 'open' }, assignee: { id: 'same-user', displayName },
    }, after, next)
    const [kept, removed, redacted] = [row('a', 'A person'), row('b', 'A person'), row('c')].map(createLocalIssueView())
    expect(kept.assignee).toBe(removed.assignee)
    expect(redacted.assignee?.displayName).toBeUndefined()
    expect(redacted.assignee).not.toBe(kept.assignee)
    expect(redacted.state).toBe(kept.state)
    const updated = applyLocalIssueView([kept, removed, redacted], [
      { kind: 'issue', id: 'b' },
      { kind: 'issue', id: 'c', row: { ...row('c'), title: 'Edited title' } },
      { kind: 'issue', id: 'd', row: row('d') },
    ])
    expect(updated[0]).toBe(kept)
    expect(updated.map(issue => [issue.id, issue.title])).toEqual([['a', 'a'], ['c', 'Edited title'], ['d', 'd']])
    expect(redacted.title).toBe('c')
  })
})
