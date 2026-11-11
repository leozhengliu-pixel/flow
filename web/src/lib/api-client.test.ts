import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError, request, requestRetryingBusy } from './api-client'

const busy = (retryAfter?: string) => new Response(JSON.stringify({ error: 'busy' }), { status: 503, headers: retryAfter ? { 'Retry-After': retryAfter } : {} })
const ok = (value: unknown) => new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } })

describe('requestRetryingBusy', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.spyOn(Math, 'random').mockReturnValue(0.5)
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('waits the Retry-After the server names and tries again', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(busy('2')).mockResolvedValueOnce(ok({ workspace: 'acme' }))
    const result = requestRetryingBusy<{ workspace: string }>('/api/issue-records/bootstrap')
    await vi.advanceTimersByTimeAsync(1999)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    await expect(result).resolves.toEqual({ workspace: 'acme' })
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('does not retry a 503 without Retry-After', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(busy())
    await expect(requestRetryingBusy('/api/issue-records/bootstrap')).rejects.toMatchObject({ status: 503 })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('gives up after the attempt limit with the last error', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => busy('1'))
    const result = requestRetryingBusy('/api/issue-records/bootstrap', {}, 3)
    const assertion = expect(result).rejects.toBeInstanceOf(ApiError)
    await vi.advanceTimersByTimeAsync(5000)
    await assertion
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })

  it('stops waiting when the request is aborted', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(busy('10'))
    const controller = new AbortController()
    const result = requestRetryingBusy('/api/issue-records/bootstrap', { signal: controller.signal })
    const assertion = expect(result).rejects.toThrow()
    await vi.advanceTimersByTimeAsync(100)
    controller.abort()
    await assertion
    await vi.advanceTimersByTimeAsync(20000)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('retries a busy resource read (a project, document or initiative) but never a write', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(busy('2')).mockResolvedValueOnce(ok({ id: 'project-1' }))
    const read = request<{ id: string }>('/api/projects/project-1')
    await vi.advanceTimersByTimeAsync(3000)
    await expect(read).resolves.toEqual({ id: 'project-1' })
    expect(fetchMock).toHaveBeenCalledTimes(2)
    fetchMock.mockReset().mockResolvedValue(busy('2'))
    await expect(request('/api/projects/project-1', { method: 'PATCH', body: '{}' })).rejects.toMatchObject({ status: 503 })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    // Bounded: a read the server keeps turning away fails after three tries.
    fetchMock.mockReset().mockResolvedValue(busy('1'))
    const stuck = request('/api/documents/doc-1')
    const assertion = expect(stuck).rejects.toMatchObject({ status: 503 })
    await vi.advanceTimersByTimeAsync(5000)
    await assertion
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })
})
