import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useIssueAutosave } from './use-issue-autosave'

afterEach(() => vi.useRealTimers())

describe('issue autosave state machine', () => {
  it('merges queued edits, flushes once, and returns to idle', async () => {
    vi.useFakeTimers()
    const save = vi.fn().mockResolvedValue(undefined)
    const { result } = renderHook(() => useIssueAutosave(save, 500))
    act(() => {
      result.current.schedule({ title: 'Title' })
      result.current.schedule({ description: 'Body' })
    })
    expect(result.current.state).toBe('saving')
    await act(() => result.current.flush())
    expect(save).toHaveBeenCalledOnce()
    expect(save).toHaveBeenCalledWith({ title: 'Title', description: 'Body' })
    expect(result.current.state).toBe('saved')
    act(() => vi.advanceTimersByTime(900))
    expect(result.current.state).toBe('idle')
  })

  it('retains failed edits and retries them with newer changes', async () => {
    const save = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(undefined)
    const { result } = renderHook(() => useIssueAutosave(save, 500))
    act(() => result.current.schedule({ title: 'First' }))
    await act(async () => { expect(await result.current.flush()).toBe(false) })
    expect(result.current.state).toBe('error')
    act(() => result.current.schedule({ description: 'Newer body' }))
    await act(async () => { expect(await result.current.retry()).toBe(true) })
    expect(save).toHaveBeenLastCalledWith({ title: 'First', description: 'Newer body' })
    expect(result.current.state).toBe('saved')
  })
})
