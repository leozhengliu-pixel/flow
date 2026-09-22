import { describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { useSecuritySetting } from './use-security-setting'
import type { WorkspaceSettings } from '@/types/flow'

const baseSettings = {
  apiKeyPermission: 'members',
  invitePermission: 'admins',
  teamCreatePermission: 'members',
  labelPermission: 'members',
  templatePermission: 'members',
} as WorkspaceSettings

describe('useSecuritySetting', () => {
  it('exposes the four-role options and saves non-apiKey changes', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    const { result } = renderHook(() => useSecuritySetting('invitePermission', baseSettings, save))
    expect(result.current.options.map((item) => item.label)).toEqual([
      'All members',
      'Only owners and admins',
      'Only owners',
      'Only admins',
    ])
    await act(async () => {
      result.current.onValueChange('owners')
    })
    await waitFor(() => expect(save).toHaveBeenCalledWith({ ...baseSettings, invitePermission: 'owners' }))
  })

  it('opens confirm when restricting API keys', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    const { result } = renderHook(() => useSecuritySetting('apiKeyPermission', baseSettings, save))
    act(() => {
      result.current.onValueChange('owners')
    })
    expect(result.current.confirmOpen).toBe(true)
    expect(save).not.toHaveBeenCalled()
    await act(async () => {
      await result.current.confirmRestrict()
    })
    expect(save).toHaveBeenCalledWith({ ...baseSettings, apiKeyPermission: 'owners' })
  })
})
