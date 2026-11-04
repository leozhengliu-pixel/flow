import { describe, expect, it } from 'vitest'
import { avatarInitials, leadingCharacters } from './avatar-initials'

// A high surrogate without its low half, or a low surrogate without its high half: the "�" the avatars used to show.
const loneSurrogate = /[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/

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

  it.each([
    ['🚀 Rocket Team', 'RT'],
    ['👩‍💻 Dev', 'D'],
    ['👩‍💻 👨🏽‍🚀', '?'],
    ['😀 Emoji Lead', 'EL'],
    ['🇺🇸 Flags', 'F'],
    ['Café Noir', 'CN'],
    ['émile', 'É'],
    ['中a', '中'],
    ['王伟', '王'],
    ['李', '李'],
    ['\u{20bb7}野家', '\u{20bb7}'],
    ['\u{1d5d4}lpha Beta', '\u{1d5d4}B'],
  ])('keeps whole characters, never a lone surrogate, for %s', (name, expected) => {
    const result = avatarInitials(name)
    expect(result).toBe(expected)
    expect(result).not.toMatch(loneSurrogate)
  })

  it('uses the given fallback when no word has a letter or digit', () => {
    expect(avatarInitials('🚀', 'L')).toBe('L')
    expect(avatarInitials('  ', 'L')).toBe('L')
  })
})

describe('leadingCharacters', () => {
  it.each([
    ['🚀 Rocket Team', 1, '🚀'],
    ['👩‍💻 Dev', 1, '👩‍💻'],
    ['👨🏽‍🚀x', 2, '👨🏽‍🚀x'],
    ['王伟', 1, '王'],
    ['王伟', 2, '王伟'],
    ['李', 2, '李'],
    ['', 1, ''],
  ])('takes whole graphemes from %s', (value, count, expected) => {
    const result = leadingCharacters(value, count)
    expect(result).toBe(expected)
    expect(result).not.toMatch(loneSurrogate)
  })
})
