import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useServerDraft } from './use-server-draft'

describe('useServerDraft', () => {
  it('follows the server value while the draft is untouched', () => {
    const { result, rerender } = renderHook(({ server }) => useServerDraft(server), { initialProps: { server: 'cached' } })
    expect(result.current[0]).toBe('cached')
    rerender({ server: 'fresh' })
    expect(result.current[0]).toBe('fresh')
  })

  it('keeps what the user typed', () => {
    const { result, rerender } = renderHook(({ server }) => useServerDraft(server), { initialProps: { server: 'cached' } })
    act(() => result.current[1]('typed'))
    rerender({ server: 'fresh' })
    expect(result.current[0]).toBe('typed')
    // The server then catches up with the saved draft and later changes are followed again once it matches.
    rerender({ server: 'typed' })
    expect(result.current[0]).toBe('typed')
    rerender({ server: 'newer' })
    expect(result.current[0]).toBe('newer')
  })
})
