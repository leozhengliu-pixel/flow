import { act, renderHook } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { useLabelSelection } from './use-label-selection'

describe('persisted label selection', () => {
  it('keeps the latest selection visible while older responses arrive and serializes writes', async () => {
    let resolveFirst!: () => void, resolveSecond!: () => void
    const first = new Promise<void>(resolve => { resolveFirst = resolve })
    const second = new Promise<void>(resolve => { resolveSecond = resolve })
    const persistFirst = vi.fn(() => first)
    const persistSecond = vi.fn(() => second)
    const { result, rerender } = renderHook(({ ids }) => useLabelSelection(ids), { initialProps: { ids: [] as string[] } })
    let savingFirst!: Promise<void>, savingSecond!: Promise<void>
    act(() => { savingFirst = result.current.save(['alpha'], persistFirst) })
    act(() => { savingSecond = result.current.save(['alpha', 'beta'], persistSecond) })
    expect(result.current.selectedIds).toEqual(['alpha', 'beta'])
    expect(persistSecond).not.toHaveBeenCalled()
    await act(async () => { resolveFirst(); await savingFirst })
    rerender({ ids: ['alpha'] })
    expect(result.current.selectedIds).toEqual(['alpha', 'beta'])
    expect(persistSecond).toHaveBeenCalledTimes(1)
    rerender({ ids: ['alpha', 'beta'] })
    await act(async () => { resolveSecond(); await savingSecond })
    expect(result.current.selectedIds).toEqual(['alpha', 'beta'])
  })

  it('restores persisted selection after failure and allows the next save', async () => {
    const { result, rerender } = renderHook(({ ids }) => useLabelSelection(ids), { initialProps: { ids: ['alpha'] } })
    await act(async () => { await expect(result.current.save(['beta'], async () => { throw new Error('offline') })).rejects.toThrow('offline') })
    expect(result.current.selectedIds).toEqual(['alpha'])
    await act(async () => { await result.current.save(['beta'], async () => undefined) })
    rerender({ ids: ['beta'] })
    expect(result.current.selectedIds).toEqual(['beta'])
  })
})
