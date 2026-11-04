import { describe, expect, it } from 'vitest'
import { mentionTriggerBeforeCaret } from './mention-trigger'

describe('mentionTriggerBeforeCaret', () => {
  it.each([
    ['@', ''],
    ['hello @', ''],
    ['hello @wang', 'wang'],
    ['(@wa', 'wa'],
    ['请@', ''],
    ['请@王', '王'],
    ['，@Anna', 'Anna'],
    ['。@李', '李'],
    ['こんにちは@田中', '田中'],
    ['안녕@민', '민'],
  ])('opens the menu for %s', (text, query) => {
    expect(text.match(mentionTriggerBeforeCaret())?.[1]).toBe(query)
  })

  it.each([['a@b.com'], ['name@'], ['x@y'], ['1@'], ['hello @a b'], ['@@']])('stays closed for %s', text => {
    expect(text.match(mentionTriggerBeforeCaret())).toBeNull()
  })

  it('caps the query length when asked', () => {
    expect(`@${'a'.repeat(40)}`.match(mentionTriggerBeforeCaret(40))).not.toBeNull()
    expect(`@${'a'.repeat(41)}`.match(mentionTriggerBeforeCaret(40))).toBeNull()
  })
})
