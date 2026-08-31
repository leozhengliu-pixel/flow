import { describe, expect, it } from 'vitest'

import { slugifyWorkspace } from './workspace-model'

describe('slugifyWorkspace', () => {
  it('preserves Unicode workspace names in URL keys', () => {
    expect(slugifyWorkspace('研发平台')).toBe('研发平台')
    expect(slugifyWorkspace('研发 Platform 二期')).toBe('研发-platform-二期')
  })

  it('normalizes separators and limits by Unicode code point', () => {
    expect(slugifyWorkspace('  产品___设计  ')).toBe('产品-设计')
    expect(Array.from(slugifyWorkspace('界'.repeat(60)))).toHaveLength(48)
  })
})
