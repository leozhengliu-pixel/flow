import { describe, expect, it } from 'vitest'
import { translateToChinese } from './translate'

describe('agent history strings', () => {
  it('translates the empty Agent page history, its row menu and short ages', () => {
    const strings = ['Recent agent chats', 'Unread', 'Copy link', 'Open in new tab', 'Open in toolbar', 'Open past agent chat…', 'Agent chat', 'Ask Flow', 'Chat options', 'Copied to clipboard', 'Could not copy to clipboard', 'now']
    for (const source of strings) expect(translateToChinese(source), source).not.toBe(source)
    for (const template of ['{count}min', '{count}h', '{count}d', '{count}w', '{count}mo', '{count}y']) {
      const translated = translateToChinese(template)
      expect(translated, template).not.toBe(template)
      expect(translated, template).toContain('{count}')
    }
  })
})
