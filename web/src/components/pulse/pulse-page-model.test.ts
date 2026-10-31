import { describe, expect, it } from 'vitest'
import { emptyCopy } from './pulse-page-model'

describe('Pulse empty copy', () => {
  it('uses the desktop conjunction without changing phone copy', () => {
    const t = (source: string) => source
    expect(emptyCopy('following', true, t)).toBe('Updates from initiatives and projects that you’re a part of or subscribed to will show here.')
    expect(emptyCopy('following', true, t, true)).toBe('Updates from initiatives, projects that you’re a part of or subscribed to will show here.')
    expect(emptyCopy('following', false, t)).toBe('Updates from projects that you’re a part of or subscribed to will show here.')
  })
})
