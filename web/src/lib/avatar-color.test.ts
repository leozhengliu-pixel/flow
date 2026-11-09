import { describe, expect, it } from 'vitest'
import { avatarColor, avatarTone, workspaceAvatarColor } from './avatar-color'

describe('avatarColor', () => {
  it('maps an id to one of 36 hues at lightness 40, 55 or 70, chroma 60', () => {
    const seen = new Set<string>()
    for (let index = 0; index < 5000; index++) {
      const { hue, lightness } = avatarTone(`usr_${index}`)
      expect(hue % 10).toBe(0)
      expect(hue).toBeGreaterThanOrEqual(0)
      expect(hue).toBeLessThan(360)
      expect([40, 55, 70]).toContain(lightness)
      seen.add(avatarColor(`usr_${index}`))
    }
    expect(seen.size).toBe(108)
  })

  it('hashes the id from its last character to its first', () => {
    expect(avatarColor('usr_local')).toBe('lch(40% 60 150)')
    expect(avatarColor('rdm')).toBe('lch(55% 60 260)')
  })

  it('uses the shared missing-id key when there is no id', () => {
    expect(avatarColor(undefined)).toBe(avatarColor('rdm'))
    expect(avatarColor('')).toBe(avatarColor('rdm'))
  })

  it('keys by id, so people with the same name keep their own colour', () => {
    expect(avatarColor('usr_a')).toBe(avatarColor('usr_a'))
    expect(new Set(['usr_a', 'usr_b', 'usr_c', 'usr_d'].map(avatarColor)).size).toBeGreaterThan(1)
  })
})

describe('workspaceAvatarColor', () => {
  it('uses a colour set through the API, otherwise the colour of the workspace id', () => {
    expect(workspaceAvatarColor({ id: 'workspace_a', color: 'lch(70 60 90)' })).toBe('lch(70 60 90)')
    expect(workspaceAvatarColor({ id: 'workspace_a' })).toBe(avatarColor('workspace_a'))
  })
})
