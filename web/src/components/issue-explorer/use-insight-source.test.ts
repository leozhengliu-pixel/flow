import { describe, expect, it, vi } from 'vitest'
import { listIssueRecords } from '@/lib/api'
import { makeIssue } from '@/test/fixtures'
import { readInsightPages } from './use-insight-source'

vi.mock('@/lib/api', () => ({ listIssueRecords: vi.fn() }))
describe('complete insight source', () => {
  it('reads beyond the visible page with a stable filter and workspace', async () => {
    vi.mocked(listIssueRecords).mockReset()
    for (let page = 0; page < 20; page++) vi.mocked(listIssueRecords).mockResolvedValueOnce({
      items: Array.from({ length: 500 }, (_, i) => makeIssue({ id: `issue-${page * 500 + i}` })), total: -1,
      hasMore: page < 19, nextCursor: `page-${page+1}`,
    })
    const abort = new AbortController()
    const query = { filter: { field: 'assignee', values: ['viewer'] }, archived: 'all' as const }
    const result = await readInsightPages(query, abort.signal, 'workspace')
    expect(result.items).toHaveLength(10000)
    expect(listIssueRecords).toHaveBeenCalledTimes(20)
    expect(listIssueRecords).toHaveBeenLastCalledWith(expect.objectContaining({ ...query, cursor: 'page-19', limit: 500, includeTotal: false }), abort.signal, 'workspace')
  })
  it('rejects broken pagination instead of silently showing partial statistics', async () => {
    vi.mocked(listIssueRecords).mockReset().mockResolvedValue({ items: [], total: -1, hasMore: true })
    await expect(readInsightPages({}, new AbortController().signal, 'workspace')).rejects.toThrow('Invalid insights cursor')
  })
  it('stops a superseded query before publishing stale data', async () => {
    const abort = new AbortController()
    vi.mocked(listIssueRecords).mockReset().mockImplementationOnce(async () => {
      abort.abort()
      return { items: [makeIssue()], hasMore: true, nextCursor: 'next', total: -1 }
    })
    await expect(readInsightPages({}, abort.signal, 'workspace')).rejects.toThrow()
    expect(listIssueRecords).toHaveBeenCalledTimes(1)
  })
})
