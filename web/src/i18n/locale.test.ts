import { afterEach, describe, expect, it, vi } from 'vitest'

function providersResponse(body: unknown) {
  return vi.fn(async () => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } }))
}

describe('initial locale', () => {
  afterEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it("uses the deployment's default language when this device has none saved", async () => {
    vi.resetModules()
    vi.spyOn(navigator, 'language', 'get').mockReturnValue('en-US')
    const fetchMock = providersResponse({ email: true, providers: [], defaultLanguage: 'zh-CN' })
    vi.stubGlobal('fetch', fetchMock)
    const { prepareInitialLocale, initialLocale } = await import('./locale')
    await expect(prepareInitialLocale()).resolves.toBe('zh-CN')
    expect(initialLocale()).toBe('zh-CN')
    expect(fetchMock).toHaveBeenCalledWith('/api/auth/providers', expect.objectContaining({ credentials: 'same-origin' }))
  })

  it('keeps the language chosen on this device without asking the server', async () => {
    vi.resetModules()
    localStorage.setItem('flow:locale', 'en-US')
    const fetchMock = providersResponse({ defaultLanguage: 'zh-CN' })
    vi.stubGlobal('fetch', fetchMock)
    const { prepareInitialLocale } = await import('./locale')
    await expect(prepareInitialLocale()).resolves.toBe('en-US')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it("follows the browser when the deployment sets no language or the request fails", async () => {
    vi.resetModules()
    vi.spyOn(navigator, 'language', 'get').mockReturnValue('zh-TW')
    vi.stubGlobal('fetch', providersResponse({ email: true, providers: [] }))
    let locale = await import('./locale')
    await expect(locale.prepareInitialLocale()).resolves.toBe('zh-CN')

    vi.resetModules()
    vi.spyOn(navigator, 'language', 'get').mockReturnValue('en-GB')
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('offline') }))
    locale = await import('./locale')
    await expect(locale.prepareInitialLocale()).resolves.toBe('en-US')

    vi.resetModules()
    vi.stubGlobal('fetch', providersResponse({ defaultLanguage: 'fr-FR' }))
    locale = await import('./locale')
    await expect(locale.prepareInitialLocale()).resolves.toBe('en-US')
  })
})
