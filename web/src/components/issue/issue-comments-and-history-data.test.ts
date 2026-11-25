import { describe, expect, it, vi } from 'vitest'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { fetchIssueHistory } from '@/lib/api'
import { hydrateIssueCommentsAndHistory } from './issue-comments-and-history-data'

vi.mock('@/lib/api', () => ({ fetchIssueHistory: vi.fn() }))

describe('history relation hydration', () => {
  it('keeps the labels and project removed from the current issue', async () => {
    const data = makeBootstrap()
    const issue = { ...data.issues[0], labels: [], project: undefined }
    vi.mocked(fetchIssueHistory).mockResolvedValue({ comments: [], activities: [{ id: 'old', type: 'issue.updated', actor: viewer, createdAt: '2026-11-01T12:00:00Z', metadata: { project: '', projectBefore: data.projects[0].id, removedLabelIds: JSON.stringify([data.labels[0].id]) } }] })
    const result = await hydrateIssueCommentsAndHistory({ issue, data })
    expect(result.relations.projects).toEqual([data.projects[0]])
    expect(result.relations.labels).toEqual([data.labels[0]])
  })
})
