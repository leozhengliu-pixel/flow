import { describe, expect, it } from 'vitest'
import { avatarInitials, leadingCharacters } from './avatar-initials'

// A high surrogate without its low half, or a low surrogate without its high half: the "�" the avatars used to show.
const loneSurrogate = /[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/

describe('avatarInitials', () => {
  // Measured in the reference app: the avatars of "Skyler Anderson", "zheng liu" and the "Flow QA OAuth app" application.
  it.each([
    ['Skyler Anderson', 'SA'],
    ['zheng liu', 'ZL'],
    ['Flow QA OAuth app', 'FOA'],
  ])('matches the reference avatar of %s', (name, expected) => {
    expect(avatarInitials(name)).toBe(expected)
  })

  it.each([
    ['Hui', 'HU'],
    ['Flow', 'FL'],
    ['jiaozongben', 'JI'],
    ['A', 'A'],
    ['émile', 'ÉM'],
    ['42', '42'],
    ['42 Test', '42'],
    ['Test 42', 'TE'],
    ["O'Neil", "O'"],
    ['Jean-Luc', 'JE'],
    ['!42 #Test', '!4'],
  ])('uses the first two characters of the single word %s', (name, expected) => {
    expect(avatarInitials(name)).toBe(expected)
  })

  it.each([
    ['Skyler Anderson', 'SA'],
    [' Dev   User ', 'DU'],
    ['Jean-Luc Picard', 'JP'],
    ['Café Noir', 'CN'],
    ['[mobile] Skyler Anderson', 'SA'],
    ['--- Skyler Anderson', 'SA'],
  ])('uses the first letter of each of the two words in %s', (name, expected) => {
    expect(avatarInitials(name)).toBe(expected)
  })

  it.each([
    ['John Ronald Reuel Tolkien', 'JRT'],
    ['Jean Luc Picard', 'JLP'],
    ['  a  b   c  ', 'ABC'],
    ['[x] Ada Lovelace Byron', 'ALB'],
    ['One Two 3 Four', 'OTF'],
  ])('uses the first word and the last two words of %s', (name, expected) => {
    expect(avatarInitials(name)).toBe(expected)
  })

  it.each([
    ['jiaozongben@gmail.com', 'JI'],
    ['skyler.anderson@example.test', 'SA'],
    ['a.b.c@example.test', 'AB'],
    ['hui@example.test', 'HU'],
    ['Skyler Anderson@example.test', 'SA'],
    ['@example.test', '?'],
  ])('reads the local part of the email %s', (name, expected) => {
    expect(avatarInitials(name)).toBe(expected)
  })

  it.each([
    ['... []', '?'],
    ['...Skyler (Anderson)', '?'],
    ['', '?'],
    ['   ', '?'],
  ])('draws nothing for %s', (name, expected) => {
    expect(avatarInitials(name)).toBe(expected)
  })

  it.each([
    ['🚀 Rocket Team', 'RT'],
    ['😀 Emoji Lead', 'EL'],
    ['Rocket 🚀 Team', 'RT'],
    ['👩‍💻 Dev', '?'],
    ['👩‍💻 👨🏽‍🚀', '?'],
    ['🇺🇸 Flags', '?'],
    ['🚀', '?'],
    ['🚀A', '?'],
  ])('skips emoji and punctuation words and never shows half a surrogate pair for %s', (name, expected) => {
    const result = avatarInitials(name)
    expect(result).toBe(expected)
    expect(result).not.toMatch(loneSurrogate)
  })

  // The reference draws nothing for these (its glyph gate only accepts Latin letters and digits); Flow keeps one letter per word.
  it.each([
    ['王伟', '王'],
    ['李', '李'],
    ['张三 李四', '张李'],
    ['王 小 明', '王小'],
    ['Иван Петров', 'ИП'],
    ['Ελένη', 'Ε'],
    ['\u{20bb7}野家', '\u{20bb7}'],
    ['张三 李四', '张李'],
    ['王伟a', '王'],
  ])('keeps one character per word for the non-Latin name %s', (name, expected) => {
    const result = avatarInitials(name)
    expect(result).toBe(expected)
    expect(result).not.toMatch(loneSurrogate)
  })

  it.each([
    ['中a', '中A'],
    ['张 Li', '张L'],
    ['\u{1d5d4}lpha Beta', '\u{1d5d4}B'],
  ])('shows a Latin letter next to a non-Latin one for %s', (name, expected) => {
    const result = avatarInitials(name)
    expect(result).toBe(expected)
    expect(result).not.toMatch(loneSurrogate)
  })

  it('uses the given fallback when nothing can be drawn', () => {
    expect(avatarInitials('🚀', 'L')).toBe('L')
    expect(avatarInitials('  ', 'L')).toBe('L')
    expect(avatarInitials('', 'F')).toBe('F')
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
