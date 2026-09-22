import { afterEach, describe, expect, it } from 'vitest'
import { ClientStorage } from './client-storage'

afterEach(() => {
  localStorage.clear()
  sessionStorage.clear()
})

describe('ClientStorage (LS-0109)', () => {
  it('round-trips local get/set/remove with Map and Set encoding', () => {
    expect(ClientStorage.set('prefs', { open: true, tags: new Set(['a', 'b']), map: new Map([['k', 1]]) })).toBe(true)
    const value = ClientStorage.get<{ open: boolean; tags: Set<string>; map: Map<string, number> }>('prefs')
    expect(value?.open).toBe(true)
    expect(value?.tags).toBeInstanceOf(Set)
    expect([...value!.tags]).toEqual(['a', 'b'])
    expect(value?.map).toBeInstanceOf(Map)
    expect(value?.map.get('k')).toBe(1)
    expect(ClientStorage.remove('prefs')).toBe(true)
    expect(ClientStorage.get('prefs')).toBeUndefined()
  })

  it('namespaces persistent keys per user', () => {
    const alice = { id: 'user-a' }
    const bob = { id: 'user-b' }
    ClientStorage.setPersistent('sidebar', alice, { width: 240 })
    ClientStorage.setPersistent('sidebar', bob, { width: 280 })
    expect(ClientStorage.getPersistent<{ width: number }>('sidebar', alice)?.width).toBe(240)
    expect(ClientStorage.getPersistent<{ width: number }>('sidebar', bob)?.width).toBe(280)
    expect(localStorage.getItem('p_user-a_sidebar')).toContain('240')
    ClientStorage.removePersistent('sidebar', alice)
    expect(ClientStorage.getPersistent('sidebar', alice)).toBeUndefined()
    expect(ClientStorage.getPersistent<{ width: number }>('sidebar', bob)?.width).toBe(280)
  })

  it('supports session and ephemeral session helpers', () => {
    expect(ClientStorage.setSession('draft', { body: 'hi' })).toBe(true)
    expect(ClientStorage.getSession<{ body: string }>('draft')?.body).toBe('hi')
    expect(ClientStorage.setEphemeralSession('temp', 1)).toBe(true)
    expect(ClientStorage.getEphemeralSession<number>('temp')).toBe(1)
    expect(sessionStorage.getItem('ephemeral_temp')).toBe('1')
    ClientStorage.removeEphemeralSession('temp')
    expect(ClientStorage.getEphemeralSession('temp')).toBeUndefined()
  })

  it('clearAllNonAuthData keeps auth shell keys and drops prefs', () => {
    localStorage.setItem('clientId', '"keep"')
    localStorage.setItem('FeatureFlags', '{"x":1}')
    localStorage.setItem('flow.sidebar.width', '244')
    localStorage.setItem('flow:composer-draft:comment:1', '{"body":"x"}')
    localStorage.setItem('p_user-a_sidebar', '{"width":1}')
    sessionStorage.setItem('ApplicationStore', '{}')
    sessionStorage.setItem('scratch', '1')

    ClientStorage.clearAllNonAuthData()

    expect(localStorage.getItem('clientId')).toBe('"keep"')
    expect(localStorage.getItem('FeatureFlags')).toBe('{"x":1}')
    expect(localStorage.getItem('flow.sidebar.width')).toBeNull()
    expect(localStorage.getItem('flow:composer-draft:comment:1')).toBeNull()
    expect(localStorage.getItem('p_user-a_sidebar')).toBeNull()
    expect(sessionStorage.getItem('ApplicationStore')).toBe('{}')
    expect(sessionStorage.getItem('scratch')).toBeNull()
  })

  it('clear retains user-scoped persistent keys', () => {
    localStorage.setItem('flow.theme', '{}')
    localStorage.setItem('p_user-a_keep', '1')
    ClientStorage.clear('local')
    expect(localStorage.getItem('flow.theme')).toBeNull()
    expect(localStorage.getItem('p_user-a_keep')).toBe('1')
  })
})
