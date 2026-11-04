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

  it('never mixes languages in catch-all phrases', async () => {
    vi.resetModules()
    const { translateToChinese, loadChineseDictionary } = await import('./translate')
    await loadChineseDictionary()
    // A verb with a noun that has no entry stays whole English; it is not "更改foo bar".
    for (const phrase of ['Change frobnicate', 'Open sub-issues thing', 'Add widgets of doom', 'Create quux', 'New zzyzx', 'Search flarp', 'No flibbertigibbet', 'Remove blorp', 'Add to flarp']) {
      expect(translateToChinese(phrase)).toBe(phrase)
    }
    // A known noun is translated, with the ellipsis normalised.
    expect(translateToChinese('Change status...')).toBe('更改状态…')
    expect(translateToChinese('Search projects…')).toBe('搜索项目…')
    expect(translateToChinese('No labels')).toBe('没有标签')
    expect(translateToChinese('Open RVI-1')).toBe('打开 RVI-1')
  })

  it('translates sentences that end in "issue views" whole, never as a name plus a Chinese noun', async () => {
    vi.resetModules()
    const { translateToChinese, loadChineseDictionary } = await import('./translate')
    await loadChineseDictionary()
    expect(translateToChinese('Track service level agreements across issue views')).toBe('跨事项视图追踪服务级别协议')
    expect(translateToChinese('Custom issue views')).toBe('自定义事项视图')
    expect(translateToChinese('{name} issue views')).toBe('{name} 事项视图')
    // A phrase with no entry stays whole English.
    expect(translateToChinese('Frobnicate across issue views')).toBe('Frobnicate across issue views')
  })

  it('translates the loop template cards', async () => {
    vi.resetModules()
    const { translateToChinese, loadChineseDictionary } = await import('./translate')
    await loadChineseDictionary()
    for (const text of ['Autofix bugs', 'Triage agent', 'Slack Q&A assistant', 'Weekly wrap', 'Feature request report', 'Security alerts', 'Start a coding session', 'Route the issue to an owner', 'Reply in the Slack thread', 'Share the highlights', 'Post a weekly report', 'Alert the Slack channel', 'Cycle options']) {
      expect(translateToChinese(text)).not.toBe(text)
    }
  })

  it('translates property picker phrases as whole sentences', async () => {
    vi.resetModules()
    const { translateToChinese, loadChineseDictionary } = await import('./translate')
    await loadChineseDictionary()
    const latin = (text: string) => /[A-Za-z]{3,}/.test(text)
    for (const phrase of [
      'Change or add labels...', 'Change or add labels. Bug selected', 'Change Status. Current value is Backlog', 'Change priority. Urgent is selected',
      'Open sub-issues of RVI-1', 'Open initiative updates', 'Open details (⌘I)', 'Select issue Urgent Priority RVI-23 Fix it', 'Select issue No Priority RVI-2 Fix it',
      'Group ordering: ascending', 'Ordering direction: descending', 'Created Nov 2, 5:56:12 PM', '3 issues will be moved to selected team', 'Canceled 2 issues, 1 failed',
      '0% of', 'Backlog 0% of 3', 'Project actions for Mobile app',
    ]) {
      const translated = translateToChinese(phrase)
      expect(translated, phrase).not.toBe(phrase)
      expect(latin(translated.replace(/RVI-\d+|Fix it|Bug|Mobile app|Backlog/g, '')), `${phrase} → ${translated}`).toBe(false)
    }
    expect(translateToChinese('Select issue Urgent Priority RVI-23 Fix it')).toBe('选择事项：紧急优先级 RVI-23 Fix it')
    expect(translateToChinese('Created Nov 2, 5:56:12 PM')).toBe('创建于 11月2日 下午5:56:12')
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
