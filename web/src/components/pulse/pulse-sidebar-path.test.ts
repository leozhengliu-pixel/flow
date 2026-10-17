import { beforeEach, describe, expect, it } from 'vitest'
import { parseAppRoute } from '@/lib/app-routes'
import { pulseSidebarPath, writePulseLastTab } from './pulse-unread'

describe('pulseSidebarPath', () => {
  beforeEach(() => localStorage.clear())

  it('goes to For me whenever something is unread, whatever tab was last used or whether you are on Pulse', () => {
    writePulseLastTab('acme', 'popular')
    expect(pulseSidebarPath('acme', 2)).toBe('/acme/pulse/following')
    expect(pulseSidebarPath('acme', 2, true)).toBe('/acme/pulse/following')
  })

  it('uses the last used tab, defaulting to Recent', () => {
    expect(pulseSidebarPath('acme', 0)).toBe('/acme/pulse/all')
    writePulseLastTab('acme', 'popular')
    expect(pulseSidebarPath('acme', 0)).toBe('/acme/pulse/popular')
    writePulseLastTab('acme', 'view:v1')
    expect(pulseSidebarPath('acme', 0)).toBe('/acme/pulse/view/v1')
  })

  it('is the bare /pulse path while you are on Pulse, which redirects to the last tab', () => {
    writePulseLastTab('acme', 'popular')
    expect(pulseSidebarPath('acme', 0, true)).toBe('/acme/pulse')
    expect(parseAppRoute('/acme/pulse')).toMatchObject({ kind: 'pulse', bare: true })
    expect(parseAppRoute('/acme/pulse/popular')).not.toHaveProperty('bare')
  })
})
