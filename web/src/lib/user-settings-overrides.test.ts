import { afterEach, describe, expect, it, vi } from 'vitest'
import { overlayUserSettings, resetUserSettingsOverrides, trackUserSettingsWrite, USER_SETTINGS_OVERRIDES_EVENT } from './user-settings-overrides'

afterEach(() => resetUserSettingsOverrides())

describe('user settings overrides', () => {
  it('keeps a pending theme choice when a refresh brings the old server value', () => {
    trackUserSettingsWrite('ws', { interfaceTheme: 'Dark' })
    // A bootstrap fetched before the save landed still says Light.
    expect(overlayUserSettings('ws', { interfaceTheme: 'Light', fontSize: 'Default' })).toEqual({ interfaceTheme: 'Dark', fontSize: 'Default' })
    // Other workspaces are unaffected.
    expect(overlayUserSettings('other', { interfaceTheme: 'Light' })).toEqual({ interfaceTheme: 'Light' })
  })

  it('keeps winning over a stale refresh that arrives after the save succeeded, then yields to the server', () => {
    const write = trackUserSettingsWrite('ws', { interfaceTheme: 'Dark' })
    write.succeeded()
    const now = Date.now()
    expect(overlayUserSettings('ws', { interfaceTheme: 'Light' }, now + 5_000).interfaceTheme).toBe('Dark')
    // Once the server reflects the value the override is dropped …
    expect(overlayUserSettings('ws', { interfaceTheme: 'Dark' }, now + 6_000).interfaceTheme).toBe('Dark')
    // … so a later, genuine change (another device) applies.
    expect(overlayUserSettings('ws', { interfaceTheme: 'Light' }, now + 7_000).interfaceTheme).toBe('Light')
  })

  it('expires a settled override after the grace period', () => {
    trackUserSettingsWrite('ws', { fontSize: 'Large' }).succeeded()
    expect(overlayUserSettings('ws', { fontSize: 'Default' }, Date.now() + 61_000).fontSize).toBe('Default')
  })

  it('drops a failed write so the previous value re-applies, and tells the app', () => {
    const listener = vi.fn()
    window.addEventListener(USER_SETTINGS_OVERRIDES_EVENT, listener)
    const write = trackUserSettingsWrite('ws', { interfaceTheme: 'Dark' })
    write.failed()
    expect(overlayUserSettings('ws', { interfaceTheme: 'Light' }).interfaceTheme).toBe('Light')
    expect(listener).toHaveBeenCalledTimes(1)
    window.removeEventListener(USER_SETTINGS_OVERRIDES_EVENT, listener)
  })

  it('a failed earlier write does not drop a newer write of the same field', () => {
    const first = trackUserSettingsWrite('ws', { interfaceTheme: 'Dark' })
    trackUserSettingsWrite('ws', { interfaceTheme: 'System preference' })
    first.failed()
    expect(overlayUserSettings('ws', { interfaceTheme: 'Light' }).interfaceTheme).toBe('System preference')
  })
})
