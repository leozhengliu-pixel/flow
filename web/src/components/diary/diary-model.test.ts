import { describe, expect, it } from 'vitest'
import {
  diaryCreatePayload,
  groupDiaryByWeek,
  isEmptyDiaryContent,
  isDiaryDocument,
  toDiaryEntry,
  weekKey,
} from './diary-model'
import type { FlowDocument } from '@/types/flow'

function doc(partial: Partial<FlowDocument> & Pick<FlowDocument, 'id'>): FlowDocument {
  return {
    slugId: partial.id,
    title: 'Entry',
    content: '',
    creator: { id: 'u1', name: 'ada', displayName: 'Ada', email: 'a@x', active: true, emailVerified: true } as never,
    projectIds: [],
    teamIds: [],
    subscriberIds: [],
    favorite: false,
    createdAt: '2026-09-22T10:00:00.000Z',
    updatedAt: '2026-09-22T10:00:00.000Z',
    revisions: [],
    icon: 'Diary',
    contentData: { diaryDate: '2026-09-22', kind: 'diary' },
    ...partial,
  }
}

describe('diary-model', () => {
  it('recognizes diary documents for the viewer', () => {
    expect(isDiaryDocument(doc({ id: '1' }), { id: 'u1' } as never)).toBe(true)
    expect(isDiaryDocument(doc({ id: '2', icon: 'Book' }), { id: 'u1' } as never)).toBe(false)
    expect(isDiaryDocument(doc({ id: '3' }), { id: 'other' } as never)).toBe(false)
  })

  it('groups entries by ISO week', () => {
    const entries = [
      toDiaryEntry(doc({ id: 'a', contentData: { diaryDate: '2026-09-22' } })),
      toDiaryEntry(doc({ id: 'b', contentData: { diaryDate: '2026-09-21' } })),
      toDiaryEntry(doc({ id: 'c', contentData: { diaryDate: '2026-09-01' } })),
    ]
    const groups = groupDiaryByWeek(entries)
    expect(groups[0]?.entries.map(item => item.id)).toEqual(['a', 'b'])
    expect(weekKey('2026-09-22')).toMatch(/^\d{4}-W\d{2}$/)
  })

  it('builds create payload and empty detection', () => {
    const payload = diaryCreatePayload('2026-09-22')
    expect(payload.icon).toBe('Diary')
    expect(payload.contentData).toEqual({ diaryDate: '2026-09-22', kind: 'diary' })
    expect(isEmptyDiaryContent('   \n#  ')).toBe(true)
    expect(isEmptyDiaryContent('ship it')).toBe(false)
  })
})
