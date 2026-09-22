import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.stubGlobal('matchMedia', (query: string) => ({
  matches: false,
  media: query,
  addEventListener: () => undefined,
  removeEventListener: () => undefined,
  addListener: () => undefined,
  removeListener: () => undefined,
  dispatchEvent: () => false,
  onchange: null,
}))

describe('theme-transfer', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.resetModules()
  })

  it('round-trips export JSON', async () => {
    const {
      buildThemeTransferPayload,
      parseThemeTransfer,
      serializeThemeTransfer,
    } = await import('./theme-transfer')
    const payload = buildThemeTransferPayload(
      { interfaceTheme: 'Dark', codeTheme: 'Flow Dark', fontSize: 'Large' },
      { interfaceTheme: 'Dark' },
    )
    const again = parseThemeTransfer(serializeThemeTransfer(payload))
    expect(again.interfaceTheme).toBe('Dark')
    expect(again.codeTheme).toBe('Flow Dark')
    expect(again.fontSize).toBe('Large')
  })

  it('applies imported interface theme prefs', async () => {
    const { applyThemeTransfer } = await import('./theme-transfer')
    const applied = applyThemeTransfer({
      version: 1,
      exportedAt: '2026-09-22T00:00:00.000Z',
      interfaceTheme: 'Light',
      codeTheme: 'Flow Light',
    })
    expect(applied.interfaceTheme).toBe('Light')
    expect(applied.codeTheme).toBe('Flow Light')
  })
})
