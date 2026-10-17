import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { registerServiceWorker, serviceWorkerEnabled } from './service-worker'

describe('service worker registration guard', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs() })

  it('only runs in production builds with service worker support', () => {
    expect(serviceWorkerEnabled({ PROD: false }, { serviceWorker: {} })).toBe(false)
    expect(serviceWorkerEnabled({ PROD: true }, {})).toBe(false)
    expect(serviceWorkerEnabled({ PROD: true }, { serviceWorker: {} })).toBe(true)
  })

  it('can be disabled by build flag or per browser', () => {
    expect(serviceWorkerEnabled({ PROD: true, VITE_DISABLE_SW: '1' }, { serviceWorker: {} })).toBe(false)
    localStorage.setItem('flow:disable-sw', '1')
    expect(serviceWorkerEnabled({ PROD: true }, { serviceWorker: {} })).toBe(false)
  })

  it('removes a worker without a Cache API instead of throwing', async () => {
    vi.stubGlobal('caches', undefined)
    const unregister = vi.fn()
    vi.stubGlobal('navigator', { serviceWorker: { register: vi.fn(), getRegistrations: vi.fn().mockResolvedValue([{ unregister }]) } })
    localStorage.setItem('flow:disable-sw', '1')
    vi.stubEnv('PROD', true)
    registerServiceWorker()
    await vi.waitFor(() => expect(unregister).toHaveBeenCalled())
    vi.unstubAllEnvs()
  })

  it('does nothing outside production (the test environment)', () => {
    const register = vi.fn()
    vi.stubGlobal('navigator', { serviceWorker: { register, getRegistrations: vi.fn() } })
    registerServiceWorker()
    expect(register).not.toHaveBeenCalled()
  })
})
