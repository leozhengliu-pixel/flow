import { describe, expect, it } from 'vitest'
import { partialMenuHeight } from './menu-height'

const rows = [32, 12, 32, 12, ...Array<number>(10).fill(32), 12, ...Array<number>(4).fill(32), 12, ...Array<number>(6).fill(32)]

describe('partialMenuHeight', () => {
  it('keeps a partial row or separator visible at short viewport heights', () => {
    expect(partialMenuHeight(rows, 43.5, 6, 597.75)).toBe(578)
    expect(partialMenuHeight(rows, 43.5, 6, 617.75)).toBe(599)
  })
  it('lets a complete or searched menu use its natural height', () => {
    expect(partialMenuHeight(rows, 43.5, 6, 850)).toBeUndefined()
    expect(partialMenuHeight([32], 43.5, 6, 597.75)).toBeUndefined()
    expect(partialMenuHeight([], 43.5, 6, 597.75)).toBeUndefined()
  })
  it('respects a viewport too short to expose the first row', () => {
    expect(partialMenuHeight([32], 43.5, 6, 50)).toBe(50)
  })
})
