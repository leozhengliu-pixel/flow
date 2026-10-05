import { renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { updateUserSettings } from '@/lib/api'
import { useSaveBrowserTimeZone } from './use-browser-timezone'

vi.mock('@/lib/api', () => ({ updateUserSettings: vi.fn(async () => ({})) }))

beforeEach(() => {
  sessionStorage.clear()
  vi.mocked(updateUserSettings).mockClear()
  vi.spyOn(Intl, 'DateTimeFormat').mockReturnValue({ resolvedOptions: () => ({ timeZone: 'Asia/Shanghai' }) } as unknown as Intl.DateTimeFormat)
})
afterEach(() => { vi.restoreAllMocks() })

describe('useSaveBrowserTimeZone', () => {
  it('saves the browser zone for a user without a settings record (no timezone known)', () => {
    renderHook(() => useSaveBrowserTimeZone('acme', 'user-1', undefined))
    expect(updateUserSettings).toHaveBeenCalledWith({ timezone: 'Asia/Shanghai' }, 'acme')
  })

  it('saves when the stored zone differs from the browser zone, once per session', () => {
    const first = renderHook(() => useSaveBrowserTimeZone('acme', 'user-1', 'Europe/Berlin'))
    expect(updateUserSettings).toHaveBeenCalledTimes(1)
    first.unmount()
    renderHook(() => useSaveBrowserTimeZone('acme', 'user-1', 'Europe/Berlin'))
    expect(updateUserSettings).toHaveBeenCalledTimes(1)
    // Another user in the same browser session is saved separately.
    renderHook(() => useSaveBrowserTimeZone('acme', 'user-2', 'Europe/Berlin'))
    expect(updateUserSettings).toHaveBeenCalledTimes(2)
  })

  it('does nothing when the stored zone already matches', () => {
    renderHook(() => useSaveBrowserTimeZone('acme', 'user-1', 'Asia/Shanghai'))
    expect(updateUserSettings).not.toHaveBeenCalled()
  })
})
