import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { createRealtimeCoalescer } from './realtime-bootstrap-coalescer'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

describe('createRealtimeCoalescer', () => {
  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })

  it('spreads the first reload of a burst and shares one trailing reload among later events', async () => {
    const loads: ReturnType<typeof deferred<string>>[] = []
    const load = vi.fn(() => { const next = deferred<string>(); loads.push(next); return next.promise })
    const coalesced = createRealtimeCoalescer(load, 1000, () => 0.25)

    const first = coalesced('acme')
    await vi.advanceTimersByTimeAsync(249)
    expect(load).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(load).toHaveBeenCalledTimes(1)

    // Three events while the first reload runs: one reload after it, shared.
    const second = coalesced('acme'), third = coalesced('acme'), fourth = coalesced('acme')
    expect(second).toBe(third)
    expect(third).toBe(fourth)
    loads[0].resolve('v1')
    await expect(first).resolves.toBe('v1')
    await vi.advanceTimersByTimeAsync(0)
    expect(load).toHaveBeenCalledTimes(2)
    loads[1].resolve('v2')
    await expect(second).resolves.toBe('v2')
    expect(load).toHaveBeenCalledTimes(2)
  })

  it('keeps workspaces apart and starts fresh after a burst', async () => {
    const load = vi.fn(async (key: string) => key)
    const coalesced = createRealtimeCoalescer(load, 0)
    await expect(coalesced('acme')).resolves.toBe('acme')
    await expect(coalesced('other')).resolves.toBe('other')
    await expect(coalesced('acme')).resolves.toBe('acme')
    expect(load).toHaveBeenCalledTimes(3)
  })

  it('still runs the trailing reload when the running one fails', async () => {
    const failing = deferred<string>()
    const load = vi.fn().mockReturnValueOnce(failing.promise).mockResolvedValueOnce('recovered')
    const coalesced = createRealtimeCoalescer(load, 0)
    const first = coalesced('acme')
    const second = coalesced('acme')
    failing.reject(new Error('busy'))
    await expect(first).rejects.toThrow('busy')
    await expect(second).resolves.toBe('recovered')
  })
})
