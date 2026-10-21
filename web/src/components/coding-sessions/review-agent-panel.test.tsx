import { renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { request } from '@/lib/api-client'
import { makeBootstrap } from '@/test/fixtures'
import type { CodeReview } from '@/types/flow'
import { makeSession } from './coding-session-test-fixtures'
import { useReviewCodingSessionId } from './review-agent-panel'
import { resetCodingAvailabilityCache } from './use-coding-sessions'

vi.mock('@/lib/api-client', async original => ({ ...await original<typeof import('@/lib/api-client')>(), request: vi.fn() }))

const review = { id: 'review-5', issueIds: ['issue-1'], provider: 'github', number: 5, repositoryOwner: 'acme', repositoryName: 'web' } as CodeReview
const pr = (number: number, reviewId?: string) => ({ sessionId: 's', codeHost: 'github' as const, hostName: 'github.com', repository: 'acme/web', number, url: '', state: 'open' as const, draft: true, headBranch: 'b', baseBranch: 'main', reviewId, createdAt: '', updatedAt: '' })

describe('useReviewCodingSessionId', () => {
  beforeEach(() => resetCodingAvailabilityCache())
  afterEach(() => vi.mocked(request).mockReset())

  it('finds the session whose pull request is this review, through the linked issue', async () => {
    vi.mocked(request).mockImplementation(async (url: string) => {
      if (url === '/api/coding/status') return { enabled: true }
      if (url.startsWith('/api/coding/sessions?')) {
        expect(url).toContain('issueId=issue-1')
        return [
          makeSession({ id: 'other', createdAt: '2026-10-02T00:00:00Z', pullRequests: [pr(9)] }),
          makeSession({ id: 'linked', createdAt: '2026-10-01T00:00:00Z', pullRequests: [pr(5)] }),
        ]
      }
      throw new Error(`unexpected request ${url}`)
    })
    const { result } = renderHook(() => useReviewCodingSessionId(review, makeBootstrap()))
    await waitFor(() => expect(result.current).toBe('linked'))
  })

  it('finds nothing for a review without a linked issue', async () => {
    vi.mocked(request).mockResolvedValue({ enabled: true })
    const { result } = renderHook(() => useReviewCodingSessionId({ ...review, issueIds: [] }, makeBootstrap()))
    await new Promise(resolve => setTimeout(resolve, 20))
    expect(result.current).toBeUndefined()
    expect(request).not.toHaveBeenCalledWith(expect.stringContaining('/api/coding/sessions'), expect.anything())
  })
})
