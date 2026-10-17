import { describe, expect, it } from 'vitest'

import { applyLabelStateRename, applyRealtimePatch } from '@/store/apply-realtime-patch'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { RealtimeEvent } from '@/types/flow'

import { classifyRealtimeEvent } from './realtime-dispatch'

const event = (type: string, extra: Partial<RealtimeEvent> = {}): RealtimeEvent => ({ id: 'e1', type, createdAt: '2026-09-20', ...extra })
const issueA = makeIssue({ id: 'issue-a', identifier: 'TST-10' })
const issueB = makeIssue({ id: 'issue-b', identifier: 'TST-11' })

describe('classifyRealtimeEvent', () => {
  it('patches self-contained issue events, including attachments and permission projections', () => {
    for (const [type, aggregateId] of [['issue.updated', 'issue-a'], ['issue.archived', 'issue-a'], ['attachment.created', 'issue-a'], ['issue.permissions_updated', 'issue-a']]) {
      expect(classifyRealtimeEvent(event(type, { aggregateId, payload: { entity: issueA } }), true), type).toEqual({ kind: 'patch' })
    }
    expect(classifyRealtimeEvent(event('issue.deleted', { aggregateId: 'issue-a', payload: { issueIds: ['issue-a', 'child'] } }), true)).toEqual({ kind: 'patch' })
    expect(classifyRealtimeEvent(event('issue.batch_updated', { aggregateId: 'b', payload: { issueIds: ['issue-a'], issues: [issueA] } }), true)).toEqual({ kind: 'patch' })
    expect(classifyRealtimeEvent(event('issue.permission_deleted', { aggregateId: 'issue-a', payload: { issueIds: ['issue-a'], removedIssueIds: ['issue-a'] } }), true)).toEqual({ kind: 'patch' })
  })

  it('refetches only the affected issues for ids-only events', () => {
    expect(classifyRealtimeEvent(event('issue.batch_updated', { aggregateId: 'b', payload: { issueIds: ['issue-a', 'issue-b'], removedIssueIds: ['gone'] } }), true))
      .toEqual({ kind: 'refetch', issueIds: ['issue-a', 'issue-b'], removedIssueIds: ['gone'] })
    expect(classifyRealtimeEvent(event('attachment.deleted', { aggregateId: 'issue-a', payload: { issueIds: ['issue-a'] } }), true).kind).toBe('refetch')
  })

  it('reloads metadata for directory events of a paged workspace', () => {
    for (const type of ['workflow_state.deleted', 'label.merged', 'user.updated', 'workspace.updated', 'cycle.completed', 'sla_policy.updated', 'triage_responsibility.updated', 'custom_emoji.created', 'team_member.added']) {
      expect(classifyRealtimeEvent(event(type), true).kind, type).toBe('metadata')
    }
    expect(classifyRealtimeEvent(event('team_member.added'), true)).toEqual({ kind: 'metadata', visibility: true })
    expect(classifyRealtimeEvent(event('workflow_state.updated'), true)).toEqual({ kind: 'metadata', visibility: false })
    expect(classifyRealtimeEvent(event('team.updated', { payload: { name: 'New' } as never }), true)).toEqual({ kind: 'metadata', visibility: false })
    expect(classifyRealtimeEvent(event('team.updated', { payload: { private: true } as never }), true)).toEqual({ kind: 'metadata', visibility: true })
    expect(classifyRealtimeEvent(event('project.milestone_deleted'), true)).toEqual({ kind: 'metadata', visibility: false })
  })

  it('keeps full-collection workspaces on the resync path for directory events', () => {
    expect(classifyRealtimeEvent(event('label.deleted'), false)).toEqual({ kind: 'resync' })
    expect(classifyRealtimeEvent(event('project.update_created'), false)).toEqual({ kind: 'metadata', visibility: false })
  })

  it('answers label and status renames with a metadata reload, never a full bootstrap', () => {
    for (const type of ['label.updated', 'label.merged', 'workflow_state.updated']) {
      for (const paged of [false, true]) expect(classifyRealtimeEvent(event(type), paged), `${type} ${paged}`).toEqual({ kind: 'metadata', visibility: false })
    }
  })

  it('resyncs only for overflow, an expired cursor and unknown events', () => {
    for (const type of ['resync', 'workspace.resync_required', 'something.unheard_of']) {
      expect(classifyRealtimeEvent(event(type), true), type).toEqual({ kind: 'resync' })
    }
    // An issue event without an entity or ids cannot be answered locally.
    expect(classifyRealtimeEvent(event('issue.archived', { aggregateId: 'issue-a' }), true)).toEqual({ kind: 'resync' })
  })
})

describe('applyRealtimePatch for server envelopes', () => {
  const paged = () => makeBootstrap({ issues: [issueA, issueB], issueCollectionPaged: true })

  it('drops a deleted issue together with its trashed sub-issues', () => {
    const result = applyRealtimePatch(paged(), event('issue.deleted', { aggregateId: 'issue-a', payload: { issueIds: ['issue-a', 'issue-b'] } }))
    expect(result.handled && result.data.issues).toEqual([])
  })

  it('merges a small batch and removes rows the batch hid', () => {
    const updated = { ...issueA, title: 'Bulk edited', version: (issueA.version ?? 0) + 1 }
    const result = applyRealtimePatch(paged(), event('issue.batch_updated', { aggregateId: 'b', payload: { issueIds: ['issue-a', 'issue-b'], removedIssueIds: ['issue-b'], issues: [updated] } }))
    expect(result.handled).toBe(true)
    if (result.handled) expect(result.data.issues.map(issue => [issue.id, issue.title])).toEqual([['issue-a', 'Bulk edited']])
  })

  it('removes an issue the viewer lost access to', () => {
    const result = applyRealtimePatch(paged(), event('issue.permissions_updated', { aggregateId: 'issue-a', payload: { issueIds: ['issue-a'], removedIssueIds: ['issue-a'] } }))
    expect(result.handled && result.data.issues.map(issue => issue.id)).toEqual(['issue-b'])
  })

  it('applies an attachment event as an issue replacement', () => {
    const next = { ...issueA, title: 'With attachment', version: (issueA.version ?? 0) + 1 }
    const result = applyRealtimePatch(paged(), event('attachment.created', { aggregateId: 'issue-a', payload: { entity: next } }))
    expect(result.handled && result.data.issues.find(issue => issue.id === 'issue-a')?.title).toBe('With attachment')
  })

  it('ignores an attachment entity that is not the aggregate issue', () => {
    expect(applyRealtimePatch(paged(), event('attachment.created', { aggregateId: 'other', payload: { entity: issueA } })).handled).toBe(false)
  })
})

describe('applyLabelStateRename', () => {
  const label = { ...makeBootstrap().labels[0]!, id: 'label-1' }
  const other = { ...label, id: 'label-2', name: 'Bug' }
  const held = () => makeBootstrap({ labels: [label, other], issues: [makeIssue({ id: 'i1', labels: [label] }), makeIssue({ id: 'i2', labels: [other] })] })

  it('rewrites the label copy on held issues from payload.label', () => {
    const next = applyLabelStateRename(held(), event('label.updated', { aggregateId: 'label-1', payload: { label: { ...label, name: 'Renamed', color: '#111111' } } as never }))
    expect(next.labels[0]).toMatchObject({ name: 'Renamed', color: '#111111' })
    expect(next.issues[0]!.labels[0]).toMatchObject({ name: 'Renamed', color: '#111111' })
    expect(next.issues[1]!.labels[0]!.name).toBe('Bug')
  })

  it('leaves data alone without an updated object (the metadata reload syncs it)', () => {
    const data = held()
    expect(applyLabelStateRename(data, event('label.updated', { aggregateId: 'label-1', payload: { name: 'x' } as never }))).toBe(data)
    expect(applyLabelStateRename(data, event('workflow_state.updated', { aggregateId: 'state-started' }))).toBe(data)
  })

  it('rewrites the state copy on held issues from payload.state', () => {
    const data = makeBootstrap({ issues: [makeIssue({ id: 'i1' })] })
    const state = { ...data.issues[0]!.state, name: 'Doing', color: '#222222' }
    const next = applyLabelStateRename(data, event('workflow_state.updated', { aggregateId: state.id, payload: { state } as never }))
    expect(next.issues[0]!.state).toMatchObject({ name: 'Doing', color: '#222222' })
    expect(next.states.find(item => item.id === state.id)?.name).toBe('Doing')
  })

  it('moves issues of merged labels to the target, without duplicates', () => {
    const data = makeBootstrap({ labels: [label, other], issues: [makeIssue({ id: 'i1', labels: [other, label] }), makeIssue({ id: 'i2', labels: [other] })] })
    const next = applyLabelStateRename(data, event('label.merged', { aggregateId: 'label-1', payload: { toLabelId: 'label-1', fromLabelIds: ['label-2'] } as never }))
    expect(next.labels.map(item => item.id)).toEqual(['label-1'])
    expect(next.issues[0]!.labels.map(item => item.id)).toEqual(['label-1'])
    expect(next.issues[1]!.labels.map(item => item.id)).toEqual(['label-1'])
  })

  it('tolerates slim issues without labels', () => {
    const slim = { ...makeIssue({ id: 's' }), labels: undefined } as never
    const data = makeBootstrap({ labels: [label], issues: [slim] })
    expect(() => applyLabelStateRename(data, event('label.updated', { payload: { label } as never }))).not.toThrow()
  })
})
