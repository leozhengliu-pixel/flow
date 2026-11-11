import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError, requestRetryingBusy } from './api-client'

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
})
