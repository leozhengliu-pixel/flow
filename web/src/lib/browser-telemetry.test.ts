import { afterEach, describe, expect, it, vi } from 'vitest'
import { browserRoute, bundledFrames, scrubBrowserError } from './browser-telemetry-client'

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  vi.resetModules()
})

describe('browser telemetry opt-in and privacy', () => {
  it('does not register listeners or sample without the same-origin endpoint', async () => {
    const listen = vi.spyOn(window, 'addEventListener')
    const sample = vi.spyOn(Math, 'random')
    const { configureBrowserTelemetry, activeBrowserTelemetry } = await import('./browser-telemetry')
    configureBrowserTelemetry(new Response(null))
    configureBrowserTelemetry(new Response(null, { headers: { 'Flow-Telemetry': 'https://elsewhere.test/api/telemetry' } }))
    expect(activeBrowserTelemetry()).toBeUndefined()
    expect(sample).not.toHaveBeenCalled()
    expect(listen).not.toHaveBeenCalled()
  })

  it('honors a zero sample rate without loading collectors', async () => {
    const listen = vi.spyOn(window, 'addEventListener')
    const { configureBrowserTelemetry, activeBrowserTelemetry } = await import('./browser-telemetry')
    configureBrowserTelemetry(new Response(null, { headers: {
      'Flow-Telemetry': '/api/telemetry',
      'Flow-Telemetry-Sample-Rate': '0',
    } }))
    expect(activeBrowserTelemetry()).toBeUndefined()
    expect(listen).not.toHaveBeenCalled()
  })

  it('does not start on an unauthenticated session response', async () => {
    const sample = vi.spyOn(Math, 'random')
    const { configureBrowserTelemetry, activeBrowserTelemetry } = await import('./browser-telemetry')
    configureBrowserTelemetry(new Response(null, { status: 401, headers: {
      'Flow-Telemetry': '/api/telemetry',
      'Flow-Telemetry-Sample-Rate': '1',
    } }))
    expect(activeBrowserTelemetry()).toBeUndefined()
    expect(sample).not.toHaveBeenCalled()
  })

  it.each([404, 401])('stops periodic and page-hide delivery after a %s intake response', async status => {
    vi.useFakeTimers()
    const observe = vi.fn()
    const disconnect = vi.fn()
    vi.stubGlobal('PerformanceObserver', class {
      static supportedEntryTypes = ['longtask']
      observe = observe
      disconnect = disconnect
    })
    const send = vi.fn(async () => new Response(null, { status }))
    vi.stubGlobal('fetch', send)
    const cleanup = vi.fn()
    const { configureBrowserTelemetry, activeBrowserTelemetry, onBrowserTelemetryStart } = await import('./browser-telemetry')
    onBrowserTelemetryStart(() => cleanup)
    configureBrowserTelemetry(new Response(null, { headers: {
      'Flow-Telemetry': '/api/telemetry',
      'Flow-Telemetry-Sample-Rate': '1',
    } }))
    await vi.dynamicImportSettled()
    const client = activeBrowserTelemetry()
    expect(client).toBeDefined()
    expect(observe).toHaveBeenCalledWith({ type: 'longtask', buffered: true })
    client?.record({ kind: 'memory', value: 12 })
    await vi.advanceTimersByTimeAsync(10_000)
    expect(send).toHaveBeenCalledExactlyOnceWith('/api/telemetry', expect.objectContaining({
      credentials: 'same-origin', referrerPolicy: 'no-referrer', keepalive: true,
    }))
    expect(activeBrowserTelemetry()).toBeUndefined()
    expect(cleanup).toHaveBeenCalledOnce()
    expect(disconnect).toHaveBeenCalledOnce()
    client?.record({ kind: 'memory', value: 15 })
    window.dispatchEvent(new Event('pagehide'))
    await vi.advanceTimersByTimeAsync(30_000)
    expect(send).toHaveBeenCalledTimes(1)
  })

  it('uses page families and bundled asset names without copying private URLs or message values', () => {
    expect(browserRoute('/tenant-secret/issue/secret-issue/hidden-title')).toBe('/{workspace}/issue/{id}')
    expect(browserRoute('/tenant-secret/does-not-exist')).toBe('unmatched')
    expect(bundledFrames([
      'fixture-private-title',
      '    at privateName (https://flow.test/assets/index-00001234.js:46:7)',
      '    at https://flow.test/assets/index-00001234.js?token=fixture-secret:52:7',
      '    at https://elsewhere.test/assets/index-00001234.js:42:5',
      '    at https://flow.test/src/user-data.ts:111:8',
    ].join('\n'), 'https://flow.test')).toEqual(['index-00001234.js:46'])
    expect(scrubBrowserError('Bad input "private-title" alice@example.test https://flow.test/p?q=secret token=sample 3680eb59-f9d4-4cb7-bbea-b095792b4d47'))
      .toBe('Bad input <value> <email> <url> <secret> <id>')
  })
})
