import { describe, expect, it } from 'vitest'
import { viewer, teammate, makeBootstrap, project } from '@/test/fixtures'
import type { FlowDocument } from '@/types/flow'
import {
  documentFilterBlocks,
  groupedDocumentFilterBlocks,
  matchDocumentFilters,
  parseDocumentIndexFilters,
} from './document-filter-blocks'

const doc = (overrides: Partial<FlowDocument> = {}): FlowDocument => ({
  id: 'document-1', slugId: 'document-one', title: 'Notes', content: '', creator: viewer,
  projectIds: [project.id], teamIds: ['team-1'], subscriberIds: [], favorite: false,
  createdAt: '2026-09-20T02:00:00.000Z', updatedAt: '2026-09-20T04:00:00.000Z', revisions: [],
  ...overrides,
} as FlowDocument)

describe('DocumentFilterBlocks', () => {
  it('exposes Creator + Project + Dates first', () => {
    expect(documentFilterBlocks.slice(0, 3).map(block => block.name)).toEqual(['Creator', 'Project', 'Dates'])
    expect(groupedDocumentFilterBlocks.map(group => group.id)).toEqual(['people', 'relations', 'dates'])
  })

  it('parses creator/project/dates from the route search', () => {
    expect(parseDocumentIndexFilters('?creatorId=user-2&projectId=project-1&dates=week&q=x')).toEqual({
      creatorId: 'user-2', projectId: 'project-1', dates: 'week',
    })
  })

  it('matches documents against Creator + Project filters', () => {
    const notes = doc({ creator: teammate, projectIds: [project.id] })
    expect(matchDocumentFilters(notes, { creatorId: teammate.id, projectId: project.id, dates: '' })).toBe(true)
    expect(matchDocumentFilters(notes, { creatorId: viewer.id, projectId: '', dates: '' })).toBe(false)
    expect(matchDocumentFilters(notes, { creatorId: '', projectId: 'other', dates: '' })).toBe(false)
  })

  it('builds option lists from bootstrap documents', () => {
    const data = makeBootstrap({ documents: [doc({ creator: teammate })] as FlowDocument[] })
    expect(data.documents[0].creator.id).toBe(teammate.id)
  })
})
