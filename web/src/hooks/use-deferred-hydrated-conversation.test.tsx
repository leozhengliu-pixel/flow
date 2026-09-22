import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import {
  asPersistedConversation,
  useDeferredHydratedConversation,
} from './use-deferred-hydrated-conversation'

describe('useDeferredHydratedConversation (LS-0727)', () => {
  it('returns the input immediately when not persisted', () => {
    const conversation = { id: 'c1', title: 'Hi' }
    const { result } = renderHook(() => useDeferredHydratedConversation(conversation))
    expect(result.current).toEqual(conversation)
  })

  it('hydrates after mount and aborts when unmounted', async () => {
    let resolve!: (value: { id: string; title: string; messages: string[] }) => void
    const hydrate = vi.fn(
      () =>
        new Promise<{ id: string; title: string; messages: string[] }>((res) => {
          resolve = res
        }),
    )
    const summary = asPersistedConversation({ id: 'c1', title: 'Hi', messages: [] as string[] }, hydrate)!
    const { result, unmount } = renderHook(() => useDeferredHydratedConversation(summary))
    expect(result.current?.title).toBe('Hi')
    expect(hydrate).toHaveBeenCalledTimes(1)
    unmount()
    await act(async () => {
      resolve({ id: 'c1', title: 'Hi', messages: ['done'] })
    })
    // Unmounted — no throw; hydrate still settled.
    expect(hydrate).toHaveBeenCalledTimes(1)
  })

  it('replaces summary with hydrated conversation when ids match', async () => {
    const hydrate = vi.fn(async () => ({ id: 'c1', title: 'Hi', messages: ['a'] }))
    const summary = asPersistedConversation({ id: 'c1', title: 'Hi', messages: [] as string[] }, hydrate)!
    const { result } = renderHook(() => useDeferredHydratedConversation(summary))
    await waitFor(() => expect(result.current?.messages).toEqual(['a']))
  })
})
