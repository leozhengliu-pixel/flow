import { describe, expect, it } from 'vitest'
import { translateToChinese } from './i18n'

describe('translateToChinese', () => {
  it('translates exact strings and preserves surrounding whitespace', () => {
    expect(translateToChinese('Projects')).toBe('项目')
    expect(translateToChinese('  Projects  ')).toBe('  项目  ')
    expect(translateToChinese('123')).toBe('123')
  })

  it('translates dynamic counts and accessibility labels', () => {
    expect(translateToChinese('3 issues')).toBe('3 个事项')
    expect(translateToChinese('2 of 5 projects completed. Click to view projects.')).toBe('已完成 2/5 个项目。点击查看项目。')
    expect(translateToChinese('1 project need an update. Click to open updates.')).toBe('1 个项目需要更新。点击打开更新。')
    expect(translateToChinese('Delete “Roadmap”?')).toBe('删除“Roadmap”吗？')
  })

  it('leaves business entity names untouched when no UI translation exists', () => {
    expect(translateToChinese('Acme Platform Migration')).toBe('Acme Platform Migration')
  })
})
