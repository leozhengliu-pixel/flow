import { describe, expect, it } from 'vitest'
import { viewer } from '@/test/fixtures'
import type { AuditLogEntry } from '@/types/flow'
import { coalesceHistoryEntries, describeHistoryEntry, historyChanges } from './entity-history-model'

const agent = { type: 'agent', name: 'Flow', onBehalfOfId: viewer.id }
const entry = (id: string, seconds: number, action: string, metadata: Record<string, unknown> = {}, extra: Partial<AuditLogEntry> = {}): AuditLogEntry => ({
  id, action, metadata, actor: viewer, botActor: agent, resourceType: 'project', resourceId: 'p1', createdAt: new Date(Date.UTC(2026, 9, 12, 9, 0, seconds)).toISOString(), ...extra,
})

describe('project and initiative history rows', () => {
  it('describes property changes, creation and milestones without the actor', () => {
    expect(describeHistoryEntry(entry('a', 0, 'updated', { changes: [{ field: 'priority', from: '', to: 'High' }, { field: 'targetDate', from: '', to: '2026-10-26' }, { field: 'status', from: 'Backlog', to: 'In Progress' }] }), 'project'))
      .toBe('set priority to High, set the target date to Oct 26 and changed the status to In Progress')
    expect(describeHistoryEntry(entry('b', 0, 'created'), 'initiative')).toBe('created the initiative')
    expect(describeHistoryEntry(entry('c', 0, 'milestone_created', { milestoneId: 'm1', milestoneName: 'Beta' }), 'project')).toBe('added milestone Beta')
    expect(describeHistoryEntry(entry('d', 0, 'updated', { changes: [{ field: 'status', from: 'active', to: 'planned' }, { field: 'description', from: '', to: '' }] }), 'initiative')).toBe('changed the status to Planned and updated the description')
    expect(describeHistoryEntry(entry('e', 0, 'relation_created'), 'project')).toBeNull()
  })

  it('folds consecutive changes by the same actor and attribution', () => {
    const rows = coalesceHistoryEntries([
      entry('a', 0, 'updated', { changes: [{ field: 'priority', from: '', to: 'High' }] }),
      entry('b', 5, 'updated', { changes: [{ field: 'lead', from: '', to: 'Dev User' }] }),
      entry('c', 6, 'updated', { changes: [{ field: 'lead', from: 'Dev User', to: '' }] }),
      entry('d', 7, 'updated', { changes: [{ field: 'name', from: 'A', to: 'B' }] }, { botActor: undefined }),
    ])
    expect(rows.map(row => row.id)).toEqual(['a', 'c', 'd'])
    expect(historyChanges(rows[0]).map(change => change.field)).toEqual(['priority', 'lead'])
  })
})
