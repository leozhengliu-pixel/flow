import { afterEach, describe, expect, it, vi } from 'vitest'

describe('lazy Chinese dictionary', () => {
  afterEach(() => {
    localStorage.clear()
    vi.resetModules()
  })

  it('leaves text untranslated until the dictionary has loaded', async () => {
    vi.resetModules()
    const translate = await import('./translate')
    expect(translate.isChineseDictionaryLoaded()).toBe(false)
    expect(translate.translateToChinese('Projects')).toBe('Projects')
    await translate.loadChineseDictionary()
    expect(translate.isChineseDictionaryLoaded()).toBe(true)
    expect(translate.translateToChinese('Projects')).toBe('项目')
  })

  it('translates the team hover card\'s project and document counts', async () => {
    vi.resetModules()
    const translate = await import('./translate')
    await translate.loadChineseDictionary()
    expect(translate.translateToChinese('No projects')).toBe('没有项目')
    expect(translate.translateToChinese('No documents')).toBe('没有文档')
    expect(translate.translateToChinese('1 project')).toBe('1 个项目')
    expect(translate.translateToChinese('3 projects')).toBe('3 个项目')
    expect(translate.translateToChinese('1 document')).toBe('1 篇文档')
    expect(translate.translateToChinese('12 documents')).toBe('12 篇文档')
  })

  it('prepares the saved locale before the first render', async () => {
    vi.resetModules()
    localStorage.setItem('flow:locale', 'zh-CN')
    const translate = await import('./translate')
    const { prepareInitialLocale } = await import('./locale')
    await expect(prepareInitialLocale()).resolves.toBe('zh-CN')
    expect(translate.isChineseDictionaryLoaded()).toBe(true)
  })

  it('does not download the dictionary for English', async () => {
    vi.resetModules()
    localStorage.setItem('flow:locale', 'en-US')
    const translate = await import('./translate')
    const { prepareInitialLocale } = await import('./locale')
    await expect(prepareInitialLocale()).resolves.toBe('en-US')
    expect(translate.isChineseDictionaryLoaded()).toBe(false)
  })
})
