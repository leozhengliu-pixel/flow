import { afterEach, describe, expect, it, vi } from 'vitest'
import { ClientStorage } from './client-storage'
import {
  NORDVPN_TPP_DISMISSED_KEY,
  detectAllSecurityExtensions,
  detectNordVPNThreatProtection,
  dismissNordVpnWarning,
  isNordVpnDismissed,
  observeNordVPNThreatProtection,
  resetSecurityExtensionDetectorCache,
} from './security-extension-detector'

vi.mock('sonner', () => ({
  toast: { warning: vi.fn() },
}))

afterEach(() => {
  resetSecurityExtensionDetectorCache()
  localStorage.clear()
})

describe('SecurityExtensionDetector (LS-0544)', () => {
  it('detects NordVPN via long same-origin resource paths', () => {
    const origin = 'https://flow.example'
    const long = `${origin}/${'a'.repeat(60)}`
    expect(detectNordVPNThreatProtection([{ name: long }], origin)).toBe(true)
    expect(detectNordVPNThreatProtection([{ name: `${origin}/api/health` }], origin)).toBe(false)
  })

  it('aggregates probe names', () => {
    const result = detectAllSecurityExtensions([
      { name: 'Seraphic', detect: () => true },
      { name: 'NordVPN Threat Protection Pro', detect: () => false },
    ])
    expect(result).toEqual({ detected: true, extensionNames: ['Seraphic'] })
  })

  it('respects ClientStorage dismiss TTL', () => {
    const now = 1_000_000
    expect(isNordVpnDismissed(now)).toBe(false)
    dismissNordVpnWarning(now)
    expect(ClientStorage.get<number>(NORDVPN_TPP_DISMISSED_KEY)).toBe(now)
    expect(isNordVpnDismissed(now + 1000)).toBe(true)
  })

  it('skips observer when dismissed', () => {
    dismissNordVpnWarning()
    const createObserver = vi.fn()
    observeNordVPNThreatProtection({ createObserver })
    expect(createObserver).not.toHaveBeenCalled()
  })
})
