import { describe, expect, it } from 'vitest'
import { buildPath } from '../src/http.js'

describe('path parameter parsing', () => {
  it('handles repeated unterminated placeholders without backtracking', () => {
    const path = '{{|'.repeat(30_000)
    expect(buildPath(path)).toBe(path)
    expect(buildPath('/issues/{id}', { id: 'x/y' })).toBe('/issues/x%2Fy')
  }, 2000)
})
