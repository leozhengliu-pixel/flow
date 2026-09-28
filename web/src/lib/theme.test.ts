import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mediaState = { matches: true, listeners: new Set<(event: MediaQueryListEvent) => void>() }

vi.stubGlobal('matchMedia', (query: string) => ({
  matches: query.includes('dark') ? mediaState.matches : !mediaState.matches,
  media: query,
  addEventListener: (_: string, listener: (event: MediaQueryListEvent) => void) => {
    mediaState.listeners.add(listener)
  },
  removeEventListener: (_: string, listener: (event: MediaQueryListEvent) => void) => {
    mediaState.listeners.delete(listener)
  },
  addListener: () => undefined,
  removeListener: () => undefined,
  dispatchEvent: () => false,
  onchange: null,
}))

describe('theme persistence', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
    document.documentElement.removeAttribute('data-theme-preference')
    document.documentElement.style.colorScheme = ''
    mediaState.matches = true
    vi.resetModules()
  })

  afterEach(() => {
    localStorage.clear()
  })

  it('initializeTheme reads flow.theme before account settings arrive', async () => {
    localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: 'dark' }))
    const { initializeTheme, getResolvedTheme } = await import('./theme')
    initializeTheme()
    expect(getResolvedTheme()).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('applyAccountTheme keeps cached Dark when account still says System preference', async () => {
    localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: 'Dark' }))
    const { initializeTheme, applyAccountTheme, readThemeSettings, themeNeedsAccountSync } = await import('./theme')
    initializeTheme()
    const applied = applyAccountTheme({
      interfaceTheme: 'System preference',
      lightTheme: 'Light',
      darkTheme: 'Dark',
    })
    expect(applied.interfaceTheme).toBe('Dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(JSON.parse(localStorage.getItem('flow.theme') || '{}').interfaceTheme).toBe('Dark')
    expect(themeNeedsAccountSync({ interfaceTheme: 'System preference' }, applied)).toBe(true)
    expect(readThemeSettings().interfaceTheme).toBe('Dark')
  })

  it('applyAccountTheme lets explicit account Light override the cache', async () => {
    localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: 'Dark' }))
    const { initializeTheme, applyAccountTheme } = await import('./theme')
    initializeTheme()
    applyAccountTheme({ interfaceTheme: 'Light' })
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(JSON.parse(localStorage.getItem('flow.theme') || '{}').interfaceTheme).toBe('Light')
  })

  it('applyTheme from Preferences is authoritative including System preference', async () => {
    localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: 'Dark' }))
    const { initializeTheme, applyTheme } = await import('./theme')
    initializeTheme()
    applyTheme({ interfaceTheme: 'System preference' })
    expect(JSON.parse(localStorage.getItem('flow.theme') || '{}').interfaceTheme).toBe('System preference')
    expect(document.documentElement.dataset.theme).toBe('dark') // OS prefers dark in this suite
  })

  it('reconcileThemeSettings documents API vs cache precedence', async () => {
    const { reconcileThemeSettings } = await import('./theme')
    expect(
      reconcileThemeSettings(
        { interfaceTheme: 'System preference' },
        { interfaceTheme: 'light' },
      ).interfaceTheme,
    ).toBe('Light')
    expect(
      reconcileThemeSettings(
        { interfaceTheme: 'Dark' },
        { interfaceTheme: 'Light' },
      ).interfaceTheme,
    ).toBe('Dark')
  })
})

describe('theme generation wiring', () => {
  it('applyTheme writes generated CSS vars and high-contrast variant', async () => {
    const { initializeTheme, applyTheme } = await import('./theme')
    initializeTheme()
    applyTheme({
      interfaceTheme: 'Dark',
      darkTheme: 'Dark high contrast',
      lightTheme: 'Light',
    })
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(document.documentElement.dataset.themeVariant).toBe('dark-high-contrast')
    expect(document.documentElement.style.getPropertyValue('--bg-panel')).toMatch(/lch\(8%/)
  })

  it('applyTheme light high contrast sets variant and panel token', async () => {
    const { initializeTheme, applyTheme } = await import('./theme')
    initializeTheme()
    applyTheme({
      interfaceTheme: 'Light',
      lightTheme: 'Light high contrast',
      darkTheme: 'Dark',
    })
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(document.documentElement.dataset.themeVariant).toBe('light-high-contrast')
    expect(document.documentElement.style.getPropertyValue('--bg-panel')).toMatch(/lch\(98/)
  })
})
