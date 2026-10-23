import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api-client'
import { reloadIssueAfterRevocation } from './issue-access-reload'
import type { Issue } from '@/types/flow'

describe('reloadIssueAfterRevocation', () => {
  it('refreshes an issue the viewer can still open', async () => {
    const issue = { id: 'issue-1', title: 'Still visible' } as Issue
    const onIssueUpdated = vi.fn()
    const onIssueAccessLost = vi.fn()
    await reloadIssueAfterRevocation('issue-1', { onIssueUpdated, onIssueAccessLost, fetchIssue: async () => issue })
    expect(onIssueUpdated).toHaveBeenCalledWith(issue)
    expect(onIssueAccessLost).not.toHaveBeenCalled()
  })

  it.each([403, 404])('reports the issue as lost on %i', async status => {
    const onIssueUpdated = vi.fn()
    const onIssueAccessLost = vi.fn()
    await reloadIssueAfterRevocation('issue-1', { onIssueUpdated, onIssueAccessLost, fetchIssue: async () => { throw new ApiError('No access', status) } })
    expect(onIssueAccessLost).toHaveBeenCalledWith('issue-1')
    expect(onIssueUpdated).not.toHaveBeenCalled()
  })

  it('keeps the issue on other failures (offline, server errors)', async () => {
    const onIssueAccessLost = vi.fn()
    await reloadIssueAfterRevocation('issue-1', { onIssueAccessLost, fetchIssue: async () => { throw new ApiError('Unavailable', 503) } })
    await reloadIssueAfterRevocation('issue-1', { onIssueAccessLost, fetchIssue: async () => { throw new TypeError('Failed to fetch') } })
    expect(onIssueAccessLost).not.toHaveBeenCalled()
  })
})
