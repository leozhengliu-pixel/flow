import { describe, expect, it } from 'vitest'

import { viewer, teammate } from '@/test/fixtures'
import type { DocumentRevision } from '@/types/flow'

import { buildHistoryEntries, dayHeadingKind, entryPreview, groupEntriesByDay } from './document-history-model'

const rev = (id: string, content: string, createdAt: string, extra: Partial<DocumentRevision> = {}): DocumentRevision =>
  ({ id, documentId: 'd', title: 'T', content, author: viewer, createdAt, ...extra })
const base = { title: 'T', content: 'now', creator: teammate, updatedAt: '2026-10-09T10:00:00' }

describe('buildHistoryEntries', () => {
  it('puts the live document first, authored by the last editor, then the revisions', () => {
    const entries = buildHistoryEntries({ ...base, revisions: [rev('a', 'older', '2026-10-09T09:00:00'), rev('b', 'oldest', '2026-10-08T09:00:00')] })
    expect(entries.map(entry => [entry.id, entry.current])).toEqual([['current', true], ['a', false], ['b', false]])
    expect(entries[0].author.id).toBe(viewer.id)
    expect(entries[0].createdAt).toBe(base.updatedAt)
  })

  it('falls back to the creator when there are no revisions', () => {
    expect(buildHistoryEntries({ ...base, revisions: [] })[0].author.id).toBe(teammate.id)
  })

  it('drops a revision identical to the live document (snapshot-after semantics)', () => {
    const entries = buildHistoryEntries({ ...base, revisions: [rev('now', 'now', '2026-10-09T10:00:00'), rev('a', 'older', '2026-10-09T09:00:00')] })
    expect(entries.map(entry => entry.id)).toEqual(['current', 'a'])
  })

  it('keeps a revision with the same content but another title', () => {
    expect(buildHistoryEntries({ ...base, revisions: [rev('a', 'now', '2026-10-09T09:00:00', { title: 'Old title' })] })).toHaveLength(2)
  })
})

describe('groupEntriesByDay', () => {
  it('groups newest-first entries by local calendar day', () => {
    const entries = buildHistoryEntries({ ...base, revisions: [rev('a', 'x', '2026-10-09T08:00:00'), rev('b', 'y', '2026-10-08T23:00:00'), rev('c', 'z', '2026-10-08T01:00:00'), rev('d', 'w', '2026-09-30T12:00:00')] })
    const days = groupEntriesByDay(entries)
    expect(days.map(day => day.entries.map(entry => entry.id))).toEqual([['current', 'a'], ['b', 'c'], ['d']])
  })

  it('names today and yesterday relative to now', () => {
    const now = new Date('2026-10-09T12:00:00')
    expect(dayHeadingKind(new Date('2026-10-09T01:00:00'), now)).toBe('today')
    expect(dayHeadingKind(new Date('2026-10-08T23:59:00'), now)).toBe('yesterday')
    expect(dayHeadingKind(new Date('2026-10-07T12:00:00'), now)).toBe('date')
  })
})

describe('entryPreview', () => {
  const doc = (text: string) => ({ type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text }] }] }) as unknown as Record<string, unknown>
  const [current, previous] = buildHistoryEntries({ ...base, contentData: doc('hello brave world'), revisions: [rev('a', 'old', '2026-10-09T09:00:00', { contentData: doc('hello world') })] })

  it('diffs against the previous version only when highlighting', () => {
    expect(entryPreview(current, previous, true).changes).toBe(1)
    expect(entryPreview(current, previous, false).changes).toBe(0)
    expect(entryPreview(current, undefined, true).changes).toBe(0)
  })
})
