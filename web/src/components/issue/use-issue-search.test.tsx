import { renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Issue } from '@/types/flow'

const listIssueRecords = vi.fn()
vi.mock('@/lib/api', () => ({ listIssueRecords: (...args: unknown[]) => listIssueRecords(...args) }))

const { useIssueSearch } = await import('./use-issue-search')

const issue = (id: string, identifier: string, title: string) => ({ id, identifier, title }) as Issue

describe('useIssueSearch', () => {
  beforeEach(() => listIssueRecords.mockReset())

  it('adds server matches for issues the paged workspace data has not loaded', async () => {
    const loaded = [issue('a', 'DEV-1', 'Loaded')]
    listIssueRecords.mockResolvedValue({ items: [issue('a', 'DEV-1', 'Loaded'), issue('b', 'DEV-4', 'Import your data')], hasMore: false, total: 2 })
    const { result } = renderHook(() => useIssueSearch(' import ', loaded))
    await waitFor(() => expect(result.current.map(item => item.identifier)).toEqual(['DEV-1', 'DEV-4']))
    expect(listIssueRecords).toHaveBeenCalledWith(expect.objectContaining({ q: 'import', limit: 50 }), expect.any(AbortSignal))
  })

  it('does not search while disabled', async () => {
    const { result } = renderHook(() => useIssueSearch('import', [], false))
    await new Promise(resolve => setTimeout(resolve, 200))
    expect(listIssueRecords).not.toHaveBeenCalled()
    expect(result.current).toEqual([])
  })
})
