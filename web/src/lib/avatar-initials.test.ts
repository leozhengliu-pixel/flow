import { describe, expect, it } from 'vitest'
import { avatarInitials } from './avatar-initials'

describe('avatarInitials', () => {
  it.each([
    ['[mobile] Skyler Anderson', 'MS'],
    ['...Skyler (Anderson)', 'SA'],
    ['--- Skyler Anderson', 'SA'],
    ['!42 #Test', '4T'],
    ['... []', '?'],
  ])('uses alphanumeric initials for %s', (name, expected) => {
    expect(avatarInitials(name)).toBe(expected)
  })

  it.each([
    ['Skyler Anderson', 'SA'],
    [' Dev   User ', 'DU'],
    ['Flow', 'F'],
    ['Jean-Luc Picard', 'JP'],
    ['skyler.anderson@example.test', 'SE'],
    ['\u5f20\u4e09 \u674e\u56db', '\u5f20\u674e'],
    ['', '?'],
  ])('preserves existing unprefixed initials for %s', (name, expected) => {
    expect(avatarInitials(name)).toBe(expected)
  })
})
