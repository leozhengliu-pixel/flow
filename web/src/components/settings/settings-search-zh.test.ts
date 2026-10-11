import { describe, expect, it } from 'vitest'

import { loadChineseDictionary, translateToChinese } from '@/i18n/translate'
import { createSettingsSearchIndex, SETTINGS_SEARCH_PAGES } from './settings-search'

describe('settings search in Chinese', () => {
  it('matches page titles and settings by their displayed Chinese text as well as English', async () => {
    await loadChineseDictionary()
    const index = createSettingsSearchIndex(SETTINGS_SEARCH_PAGES, translateToChinese)
    const page = SETTINGS_SEARCH_PAGES.find(item => item.id === 'asks')!
    expect(index.search(translateToChinese(page.title)).some(result => result.page === 'asks')).toBe(true)
    expect(index.search('启用请求').some(result => result.targetTitle === 'Enable Asks')).toBe(true)
    expect(index.search('Enable Asks').some(result => result.targetTitle === 'Enable Asks')).toBe(true)
  })
})
