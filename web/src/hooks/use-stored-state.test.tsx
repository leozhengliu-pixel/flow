import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ClientStorage } from '@/lib/client-storage'
import { useStoredState, useStoredStateWithTTL } from './use-stored-state'

afterEach(() => {
  localStorage.clear()
  sessionStorage.clear()
  vi.useRealTimers()
})

describe('useStoredState (LS-0771)', () => {
  it('reads and writes session storage', () => {
    const { result } = renderHook(() => useStoredState('k', 'default', 'session'))
    expect(result.current[0]).toBe('default')
    act(() => result.current[1]('saved'))
    expect(result.current[0]).toBe('saved')
    expect(ClientStorage.getSession<string>('k')).toBe('saved')
    act(() => result.current[1]('default'))
    expect(ClientStorage.getSession('k')).toBeUndefined()
  })

  it('syncs local mechanism across storage events', () => {
    const { result } = renderHook(() => useStoredState('width', 244, 'local'))
    act(() => {
      localStorage.setItem('width', JSON.stringify(300))
      window.dispatchEvent(new StorageEvent('storage', { key: 'width', newValue: JSON.stringify(300) }))
    })
    expect(result.current[0]).toBe(300)
    act(() => {
      localStorage.removeItem('width')
      window.dispatchEvent(new StorageEvent('storage', { key: 'width', newValue: null }))
    })
    expect(result.current[0]).toBe(244)
  })

  it('expires TTL session values', () => {
    vi.useFakeTimers()
    ClientStorage.setSession('ttl', { value: 'old', timestamp: Date.now() - 10 * 60 * 1000 })
    const { result } = renderHook(() => useStoredStateWithTTL('ttl', 'fresh', 5 * 60 * 1000))
    expect(result.current[0]).toBe('fresh')
    act(() => result.current[1]('live'))
    expect(ClientStorage.getSession<{ value: string }>('ttl')?.value).toBe('live')
  })
})
