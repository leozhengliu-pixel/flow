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

  it('translates every fiscal month and nested settings option', () => {
    expect([
      'January', 'February', 'March', 'April', 'May', 'June',
      'July', 'August', 'September', 'October', 'November', 'December',
    ].map(translateToChinese)).toEqual([
      '一月', '二月', '三月', '四月', '五月', '六月',
      '七月', '八月', '九月', '十月', '十一月', '十二月',
    ])
    expect(translateToChinese('Squash and merge')).toBe('压缩合并')
    expect(translateToChinese('Pending invites')).toBe('待处理邀请')
    expect(translateToChinese('Suspended')).toBe('已停用')
    expect(translateToChinese('Exponential')).toBe('指数')
    expect(translateToChinese('Fibonacci')).toBe('斐波那契')
    expect(translateToChinese('Assign to issue creator')).toBe('分配给事项创建者')
    expect(translateToChinese('Assign to team owner')).toBe('分配给团队负责人')
    expect(translateToChinese('Use responsibility')).toBe('使用责任人规则')
  })
})
